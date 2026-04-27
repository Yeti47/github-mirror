package reports

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Writer accumulates per-repo results during a sync cycle and persists each
// result immediately as it is recorded. Complete writes the cycle's
// completion-relevant fields (ended_at, totals).
//
// Implementations must be safe for concurrent use by the worker pool. Errors
// from per-repo writes are logged on the writer's logger rather than
// returned, so the caller (the worker pool) does not need to thread them
// through every git op.
//
// The protocol row is created by Factory.CreateProtocol; by the time a
// caller has a Writer, an in-progress row already exists in the database
// and the health check can observe it.
type Writer interface {
	RecordSuccess(ctx context.Context, repoFullName, operation string, duration time.Duration)
	RecordFailure(ctx context.Context, repoFullName, operation string, duration time.Duration, err error)
	Complete(ctx context.Context) error
}

// sqliteWriter is the Repository-backed implementation of Writer.
type sqliteWriter struct {
	repo      *Repository
	logger    *slog.Logger
	id        int64
	startedAt time.Time

	mu        sync.Mutex
	succeeded int
	failed    int
	completed bool
}

func (w *sqliteWriter) RecordSuccess(ctx context.Context, repoFullName, operation string, duration time.Duration) {
	w.recordResult(ctx, RepoResult{
		RepoFullName: repoFullName,
		Operation:    operation,
		Duration:     duration,
		Success:      true,
	})
}

func (w *sqliteWriter) RecordFailure(ctx context.Context, repoFullName, operation string, duration time.Duration, err error) {
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	w.recordResult(ctx, RepoResult{
		RepoFullName: repoFullName,
		Operation:    operation,
		Duration:     duration,
		Success:      false,
		Error:        errMsg,
	})
}

func (w *sqliteWriter) recordResult(ctx context.Context, result RepoResult) {
	now := time.Now().UTC()
	if err := w.repo.RecordResult(ctx, w.id, result, now); err != nil {
		w.logger.Error("failed to persist repo result",
			"protocol_id", w.id,
			"repo", result.RepoFullName,
			"err", err,
		)
		// Still bump the in-memory counters so Complete reports totals even
		// if a single insert was lost.
	}
	w.mu.Lock()
	if result.Success {
		w.succeeded++
	} else {
		w.failed++
	}
	w.mu.Unlock()
}

// Complete writes the cycle's completion-relevant fields (ended_at, totals)
// to the existing protocol row. Per-repo results have already been written
// by RecordSuccess / RecordFailure as they happened.
func (w *sqliteWriter) Complete(ctx context.Context) error {
	w.mu.Lock()
	if w.completed {
		w.mu.Unlock()
		return errors.New("sync protocol already completed")
	}
	w.completed = true
	succeeded := w.succeeded
	failed := w.failed
	w.mu.Unlock()

	endedAt := time.Now().UTC()
	p := &SyncProtocol{
		ID:             w.id,
		StartedAt:      w.startedAt,
		EndedAt:        &endedAt,
		TotalRepos:     succeeded + failed,
		SucceededRepos: succeeded,
		FailedRepos:    failed,
	}
	return w.repo.Complete(ctx, p)
}
