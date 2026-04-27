// Package reports persists and retrieves SyncProtocol records that describe
// each sync cycle, and exposes a HealthChecker that uses them to determine
// application health.
package reports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS sync_protocols (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at       TIMESTAMP NOT NULL,
    ended_at         TIMESTAMP,
    next_run_at      TIMESTAMP NOT NULL,
    last_activity_at TIMESTAMP NOT NULL,
    total_repos      INTEGER NOT NULL DEFAULT 0,
    succeeded_repos  INTEGER NOT NULL DEFAULT 0,
    failed_repos     INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_protocols_last_activity_at ON sync_protocols(last_activity_at);

CREATE TABLE IF NOT EXISTS repo_results (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    protocol_id    INTEGER NOT NULL,
    repo_full_name TEXT NOT NULL,
    operation      TEXT NOT NULL,
    duration_ms    INTEGER NOT NULL,
    success        INTEGER NOT NULL,
    error          TEXT,
    recorded_at    TIMESTAMP NOT NULL,
    FOREIGN KEY (protocol_id) REFERENCES sync_protocols(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_repo_results_protocol_id ON repo_results(protocol_id);
`

// Repository persists SyncProtocol records in a SQLite database.
type Repository struct {
	db        *sql.DB
	retention int
}

// NewRepository opens (and if needed creates) the SQLite database at dbPath
// and applies the schema. The parent directory is created automatically.
// retention controls how many of the most recent SyncProtocol rows are kept;
// older rows are deleted whenever a new one is inserted. A retention value of
// zero disables pruning.
func NewRepository(dbPath string, retention int) (*Repository, error) {
	if dbPath == "" {
		return nil, errors.New("dbPath is required")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite is single-writer; serialise all access through one connection.
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("apply %q: %w", p, err)
		}
	}

	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	return &Repository{db: db, retention: retention}, nil
}

// Close releases the underlying database handle.
func (r *Repository) Close() error {
	return r.db.Close()
}

// Insert creates a new in-progress SyncProtocol row (ended_at = NULL,
// last_activity_at = StartedAt) and assigns the generated id back onto p.
// Insert also prunes rows beyond the configured retention so the table never
// grows unbounded.
func (r *Repository) Insert(ctx context.Context, p *SyncProtocol) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO sync_protocols (started_at, ended_at, next_run_at, last_activity_at)
		VALUES (?, NULL, ?, ?)
	`, p.StartedAt, p.NextRunAt, p.StartedAt)
	if err != nil {
		return fmt.Errorf("insert protocol: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("last insert id: %w", err)
	}
	p.ID = id
	p.LastActivityAt = p.StartedAt

	if err := pruneOldProtocols(ctx, tx, r.retention); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// RecordResult inserts a single per-repo result and bumps the protocol's
// last_activity_at. Both writes happen in one transaction so a health check
// will never see a row whose activity timestamp is older than its newest
// result.
func (r *Repository) RecordResult(ctx context.Context, protocolID int64, result RepoResult, recordedAt time.Time) error {
	if protocolID == 0 {
		return errors.New("record result: protocol id is required")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO repo_results
			(protocol_id, repo_full_name, operation, duration_ms, success, error, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, protocolID, result.RepoFullName, result.Operation,
		result.Duration.Milliseconds(), boolToInt(result.Success),
		result.Error, recordedAt,
	); err != nil {
		return fmt.Errorf("insert result for %s: %w", result.RepoFullName, err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE sync_protocols SET last_activity_at = ? WHERE id = ?
	`, recordedAt, protocolID); err != nil {
		return fmt.Errorf("update last_activity_at: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Complete finalises a previously-Inserted SyncProtocol: it updates the
// row's ended_at, last_activity_at, and totals. The protocol must have a
// non-zero ID and a non-nil EndedAt.
func (r *Repository) Complete(ctx context.Context, p *SyncProtocol) error {
	if p.ID == 0 {
		return errors.New("complete: protocol has no ID")
	}
	if p.EndedAt == nil {
		return errors.New("complete: EndedAt must be set")
	}

	if _, err := r.db.ExecContext(ctx, `
		UPDATE sync_protocols
		SET ended_at = ?, last_activity_at = ?,
		    total_repos = ?, succeeded_repos = ?, failed_repos = ?
		WHERE id = ?
	`, *p.EndedAt, *p.EndedAt, p.TotalRepos, p.SucceededRepos, p.FailedRepos, p.ID); err != nil {
		return fmt.Errorf("update protocol on complete: %w", err)
	}
	return nil
}

// pruneOldProtocols deletes protocols beyond the retention window. Cascade
// delete on repo_results.protocol_id removes their results too.
func pruneOldProtocols(ctx context.Context, tx *sql.Tx, retention int) error {
	if retention <= 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM sync_protocols
		WHERE id NOT IN (
			SELECT id FROM sync_protocols ORDER BY id DESC LIMIT ?
		)
	`, retention); err != nil {
		return fmt.Errorf("prune old protocols: %w", err)
	}
	return nil
}

// Latest returns the most recently inserted SyncProtocol (header only —
// Results is left nil), or (nil, nil) if no protocols have been recorded
// yet. EndedAt is nil when the latest protocol is still in progress.
func (r *Repository) Latest(ctx context.Context) (*SyncProtocol, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, started_at, ended_at, next_run_at, last_activity_at,
		       total_repos, succeeded_repos, failed_repos
		FROM sync_protocols
		ORDER BY id DESC
		LIMIT 1
	`)
	var (
		p       SyncProtocol
		endedAt sql.NullTime
	)
	err := row.Scan(&p.ID, &p.StartedAt, &endedAt, &p.NextRunAt, &p.LastActivityAt,
		&p.TotalRepos, &p.SucceededRepos, &p.FailedRepos)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query latest: %w", err)
	}
	// SQLite returns timestamps as UTC naïvely; normalise.
	p.StartedAt = p.StartedAt.UTC()
	p.NextRunAt = p.NextRunAt.UTC()
	p.LastActivityAt = p.LastActivityAt.UTC()
	if endedAt.Valid {
		t := endedAt.Time.UTC()
		p.EndedAt = &t
	}
	return &p, nil
}

// CountResults returns the number of repo_results rows for a given protocol id.
func (r *Repository) CountResults(ctx context.Context, protocolID int64) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM repo_results WHERE protocol_id = ?`, protocolID,
	).Scan(&n)
	return n, err
}

// CountProtocols returns the total number of stored sync_protocols rows.
func (r *Repository) CountProtocols(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_protocols`).Scan(&n)
	return n, err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
