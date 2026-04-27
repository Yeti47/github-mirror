// Package mirror orchestrates the periodic mirroring of GitHub repositories.
// It fans out sync work across a configurable worker pool and isolates
// per-repository errors so a single failure never aborts the full cycle.
package mirror

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	stdSync "sync"
	"time"

	"github.com/Yeti47/github-mirror/internal/git"
	"github.com/Yeti47/github-mirror/internal/reports"
)

// repoLister lists the repositories that should be mirrored.
// Defined here, at the consumer, so the mirror package owns the abstraction
// and concrete implementations (github.Client) satisfy it implicitly.
type repoLister interface {
	ListOwnedRepos(ctx context.Context) ([]git.Repo, error)
}

// gitRunner performs git mirror operations on a local bare clone.
type gitRunner interface {
	CloneMirror(ctx context.Context, cloneURL, targetDir string) error
	RemoteUpdate(ctx context.Context, dir string) error
	FetchLFS(ctx context.Context, dir string) error
}

// Config holds the Engine's operational parameters.
type Config struct {
	MirrorDir    string
	Concurrency  int
	IncludeLFS   bool
	GitTimeout   time.Duration
	SyncInterval time.Duration
}

// Engine orchestrates mirror sync cycles using a worker pool.
type Engine struct {
	lister  repoLister
	runner  gitRunner
	factory reports.Factory
	cfg     Config
	logger  *slog.Logger
}

// NewEngine constructs an Engine with the provided dependencies and config.
func NewEngine(lister repoLister, runner gitRunner, factory reports.Factory, cfg Config, logger *slog.Logger) *Engine {
	return &Engine{
		lister:  lister,
		runner:  runner,
		factory: factory,
		cfg:     cfg,
		logger:  logger,
	}
}

// Sync performs one full synchronization pass over all owned repositories.
//
// A new protocol is created via the factory at the very start of the cycle
// (so the health check immediately sees an in-progress row). Per-repo
// outcomes are recorded as they happen; the protocol is finalised by
// Complete before Sync returns.
//
// Repositories are dispatched to a bounded worker pool. Each worker runs its
// git operation in a fresh context with a per-repo deadline derived from
// context.Background() (not from ctx), so in-flight operations complete
// gracefully even after ctx is cancelled — new repos simply stop being
// dispatched. Sync returns after all in-flight workers finish.
//
// Errors from individual repositories are accumulated and returned as a joined
// error; they do not affect other repositories in the cycle.
func (e *Engine) Sync(ctx context.Context) error {
	start := time.Now()
	e.logger.Info("sync cycle started")

	startedAt := start.UTC()
	nextRunAt := startedAt.Add(e.cfg.SyncInterval)
	writer, err := e.factory.CreateProtocolWriter(ctx, startedAt, nextRunAt)
	if err != nil {
		return fmt.Errorf("create sync protocol: %w", err)
	}

	repos, err := e.lister.ListOwnedRepos(ctx)
	if err != nil {
		// Persist completion so the health check still reflects that a cycle
		// was attempted recently.
		e.completeProtocol(writer)
		return fmt.Errorf("list repos: %w", err)
	}
	e.logger.Info("repos discovered", "count", len(repos))

	sem := make(chan struct{}, e.cfg.Concurrency)
	var wg stdSync.WaitGroup
	var mu stdSync.Mutex
	var errs []error

	skipped := 0
outer:
	for i, repo := range repos {
		// Before acquiring a slot, check if the caller has signalled shutdown.
		// Already-running workers are allowed to finish (see opCtx below).
		select {
		case <-ctx.Done():
			skipped = len(repos) - i
			e.logger.Info("sync interrupted: shutdown signalled, skipping remaining repos",
				"skipped", skipped,
				"dispatched", i,
			)
			break outer
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(r git.Repo) {
			defer wg.Done()
			defer func() { <-sem }()

			// Use a fresh context so this op is not cancelled by a shutdown signal.
			// The per-repo timeout is the only bound on how long it can run.
			opCtx, cancel := context.WithTimeout(context.Background(), e.cfg.GitTimeout)
			defer cancel()

			op, dur, syncErr := e.syncRepo(opCtx, r)
			if syncErr != nil {
				writer.RecordFailure(opCtx, r.FullName, op, dur, syncErr)
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", r.FullName, syncErr))
				mu.Unlock()
				return
			}
			writer.RecordSuccess(opCtx, r.FullName, op, dur)
		}(repo)
	}

	wg.Wait()
	e.completeProtocol(writer)

	elapsed := time.Since(start)
	if len(errs) > 0 {
		e.logger.Warn("sync cycle completed with errors",
			"duration", elapsed,
			"total", len(repos),
			"failed", len(errs),
			"skipped", skipped,
		)
		return errors.Join(errs...)
	}

	e.logger.Info("sync cycle completed",
		"duration", elapsed,
		"total", len(repos),
		"skipped", skipped,
	)
	return nil
}

// completeProtocol persists the protocol's completion using a fresh context
// with a short deadline, so persistence still succeeds during graceful
// shutdown. Failures are logged but never surface to the caller.
func (e *Engine) completeProtocol(writer reports.Writer) {
	persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := writer.Complete(persistCtx); err != nil {
		e.logger.Error("failed to persist sync protocol completion", "err", err)
	}
}

// syncRepo mirrors a single repository: clones it if it does not yet exist
// locally, or fetches updates if it does. LFS objects are fetched when
// IncludeLFS is enabled. The returned op and duration describe the main git
// operation (not LFS) and are suitable for inclusion in a sync protocol.
func (e *Engine) syncRepo(ctx context.Context, repo git.Repo) (op string, duration time.Duration, err error) {
	log := e.logger.With("repo", repo.FullName, "archived", repo.Archived)
	start := time.Now()

	targetDir := e.repoDir(repo.FullName)

	var gitErr error

	// Presence of HEAD indicates an existing mirror clone.
	if _, statErr := os.Stat(filepath.Join(targetDir, "HEAD")); os.IsNotExist(statErr) {
		op = "clone"
		if mkErr := os.MkdirAll(filepath.Dir(targetDir), 0o755); mkErr != nil {
			return op, time.Since(start), fmt.Errorf("create parent dir: %w", mkErr)
		}
		gitErr = e.runner.CloneMirror(ctx, repo.CloneURL, targetDir)
	} else {
		op = "update"
		gitErr = e.runner.RemoteUpdate(ctx, targetDir)
	}

	if gitErr != nil {
		duration = time.Since(start)
		log.Error("git operation failed",
			"op", op,
			"duration", duration,
			"err", gitErr,
		)
		return op, duration, gitErr
	}

	if e.cfg.IncludeLFS {
		if lfsErr := e.runner.FetchLFS(ctx, targetDir); lfsErr != nil {
			// Non-fatal: repos without LFS will typically return quickly.
			log.Warn("lfs fetch failed (non-fatal)",
				"op", "lfs-fetch",
				"duration", time.Since(start),
				"err", lfsErr,
			)
		}
	}

	duration = time.Since(start)
	log.Info("repo synced",
		"op", op,
		"duration", duration,
	)
	return op, duration, nil
}

// repoDir returns the local bare mirror path for a given "owner/repo" name.
// The ".git" suffix is conventional for bare repositories.
func (e *Engine) repoDir(fullName string) string {
	return filepath.Join(e.cfg.MirrorDir, fullName+".git")
}
