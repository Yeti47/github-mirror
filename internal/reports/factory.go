package reports

import (
	"context"
	"log/slog"
	"time"
)

// Factory creates a fresh Writer for each sync cycle. Creating the writer
// also creates the in-progress protocol row in the database, so by the time
// the caller has a Writer back, a concurrent health probe can already see
// that a cycle has started.
type Factory interface {
	CreateProtocolWriter(ctx context.Context, startedAt, nextRunAt time.Time) (Writer, error)
}

// SQLiteFactory builds Writers backed by a Repository.
type SQLiteFactory struct {
	repo   *Repository
	logger *slog.Logger
}

// NewSQLiteFactory constructs a Factory that persists protocols through the
// supplied Repository. The logger is forwarded to each Writer for reporting
// non-fatal per-result persistence errors.
func NewSQLiteFactory(repo *Repository, logger *slog.Logger) *SQLiteFactory {
	return &SQLiteFactory{repo: repo, logger: logger}
}

// CreateProtocolWriter inserts a new in-progress protocol row and returns a Writer
// bound to it.
func (f *SQLiteFactory) CreateProtocolWriter(ctx context.Context, startedAt, nextRunAt time.Time) (Writer, error) {
	p := &SyncProtocol{
		StartedAt: startedAt.UTC(),
		NextRunAt: nextRunAt.UTC(),
	}
	if err := f.repo.Insert(ctx, p); err != nil {
		return nil, err
	}
	return &sqliteWriter{
		repo:      f.repo,
		logger:    f.logger,
		id:        p.ID,
		startedAt: p.StartedAt,
	}, nil
}
