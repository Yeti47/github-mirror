// Command github-mirror is a daemon that periodically clones and updates all
// of a user's GitHub repositories (including private ones) into a local set
// of bare mirror clones.
//
// Run without flags it executes sync cycles forever; run with --health it
// inspects the persisted sync history and exits 0 (healthy) or 1 (unhealthy)
// — this mode is what the Docker HEALTHCHECK invokes in a separate process.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Yeti47/github-mirror/internal/config"
	"github.com/Yeti47/github-mirror/internal/git"
	"github.com/Yeti47/github-mirror/internal/github"
	"github.com/Yeti47/github-mirror/internal/mirror"
	"github.com/Yeti47/github-mirror/internal/reports"
)

func main() {
	healthFlag := flag.Bool("health", false,
		"check application health based on the most recent sync protocol and exit (0 healthy, 1 unhealthy)")
	onceFlag := flag.Bool("once", false,
		"run a single sync cycle and exit; useful for scripting and end-to-end tests")
	flag.Parse()

	if err := run(*healthFlag, *onceFlag); err != nil {
		// errUnhealthy already printed its own status line; don't double-print.
		if err != errUnhealthy {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

func run(healthMode, onceMode bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := newLogger(cfg.LogLevel)

	// The sync-protocol DB is shared between the long-running daemon and the
	// short-lived health probe. SQLite + WAL handles the concurrency.
	repo, err := reports.NewRepository(cfg.DBPath, 100)
	if err != nil {
		return fmt.Errorf("open sync protocol db: %w", err)
	}
	defer func() { _ = repo.Close() }()

	if healthMode {
		return runHealthCheck(cfg, repo)
	}

	return runDaemon(cfg, repo, logger, onceMode)
}

// errUnhealthy is returned by runHealthCheck when the app is unhealthy.
// main() converts it to exit code 1.
var errUnhealthy = fmt.Errorf("unhealthy")

// runHealthCheck implements --health: print a one-line status and return
// errUnhealthy on failure. Designed to be invoked by Docker HEALTHCHECK.
func runHealthCheck(cfg *config.Config, repo *reports.Repository) error {
	// The application is unhealthy if a full sync interval plus one git
	// timeout has elapsed without a completed cycle. That gives an in-flight
	// cycle one extra slot to finish before being considered stuck.
	tolerance := cfg.SyncInterval + cfg.GitTimeout
	checker := reports.NewHealthChecker(repo, tolerance)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status := checker.Check(ctx)
	fmt.Println(status.Message)
	if !status.Healthy {
		return errUnhealthy
	}
	return nil
}

// runDaemon wires the composition root and runs the periodic sync loop until
// SIGINT/SIGTERM is received. When once is true it performs a single cycle
// and exits immediately.
func runDaemon(cfg *config.Config, repo *reports.Repository, logger *slog.Logger, once bool) error {
	pat := config.NewPersonalAccessToken(cfg.GitHubToken)

	ghClient := github.NewClient(pat)
	gitRunner := git.NewRunner(pat)

	factory := reports.NewSQLiteFactory(repo, logger)

	engine := mirror.NewEngine(ghClient, gitRunner, factory, mirror.Config{
		MirrorDir:    cfg.MirrorDir,
		Concurrency:  cfg.Concurrency,
		IncludeLFS:   cfg.IncludeLFS,
		GitTimeout:   cfg.GitTimeout,
		SyncInterval: cfg.SyncInterval,
	}, logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info("github-mirror starting",
		"mirror_dir", cfg.MirrorDir,
		"db_path", cfg.DBPath,
		"sync_interval", cfg.SyncInterval,
		"concurrency", cfg.Concurrency,
		"include_lfs", cfg.IncludeLFS,
		"git_timeout", cfg.GitTimeout,
		"once", once,
	)

	runSync(ctx, engine, logger)

	if once {
		return nil
	}

	ticker := time.NewTicker(cfg.SyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutdown signal received, exiting")
			return nil
		case <-ticker.C:
			runSync(ctx, engine, logger)
		}
	}
}

// runSync performs a single sync cycle. The engine creates and finalises
// the SyncProtocol internally via its injected factory.
func runSync(ctx context.Context, engine *mirror.Engine, logger *slog.Logger) {
	if err := engine.Sync(ctx); err != nil {
		logger.Warn("sync cycle returned error(s)", "err", err)
	}
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	return slog.New(handler)
}
