package reports_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Yeti47/github-mirror/internal/reports"
)

func newTestRepo(t *testing.T, retention int) *reports.Repository {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	repo, err := reports.NewRepository(dbPath, retention)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func newTestFactory(t *testing.T, retention int) (*reports.Repository, reports.Factory) {
	t.Helper()
	repo := newTestRepo(t, retention)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return repo, reports.NewSQLiteFactory(repo, logger)
}

func TestRepository_InsertAndLatest(t *testing.T) {
	repo := newTestRepo(t, 0)
	ctx := context.Background()

	started := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	next := started.Add(time.Hour)

	p := &reports.SyncProtocol{StartedAt: started, NextRunAt: next}
	if err := repo.Insert(ctx, p); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("expected ID after Insert")
	}
	if !p.LastActivityAt.Equal(started) {
		t.Errorf("LastActivityAt = %v, want %v", p.LastActivityAt, started)
	}

	got, err := repo.Latest(ctx)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got == nil || got.EndedAt != nil {
		t.Fatalf("expected in-progress protocol, got %+v", got)
	}
	if !got.LastActivityAt.Equal(started) {
		t.Errorf("LastActivityAt = %v, want %v", got.LastActivityAt, started)
	}
}

func TestRepository_RecordResultBumpsLastActivity(t *testing.T) {
	repo := newTestRepo(t, 0)
	ctx := context.Background()

	started := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	p := &reports.SyncProtocol{StartedAt: started, NextRunAt: started.Add(time.Hour)}
	if err := repo.Insert(ctx, p); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	bump := started.Add(45 * time.Second)
	err := repo.RecordResult(ctx, p.ID, reports.RepoResult{
		RepoFullName: "Yeti47/a", Operation: "clone", Duration: 5 * time.Second, Success: true,
	}, bump)
	if err != nil {
		t.Fatalf("RecordResult: %v", err)
	}

	got, err := repo.Latest(ctx)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !got.LastActivityAt.Equal(bump) {
		t.Errorf("LastActivityAt = %v, want %v", got.LastActivityAt, bump)
	}

	n, err := repo.CountResults(ctx, p.ID)
	if err != nil {
		t.Fatalf("CountResults: %v", err)
	}
	if n != 1 {
		t.Errorf("CountResults = %d, want 1", n)
	}
}

func TestRepository_Complete_RequiresInsertAndEndedAt(t *testing.T) {
	repo := newTestRepo(t, 0)
	now := time.Now().UTC()

	if err := repo.Complete(context.Background(), &reports.SyncProtocol{EndedAt: &now}); err == nil {
		t.Error("expected error when ID is zero")
	}
	if err := repo.Complete(context.Background(), &reports.SyncProtocol{ID: 1}); err == nil {
		t.Error("expected error when EndedAt is nil")
	}
}

func TestRepository_Latest_EmptyDatabase(t *testing.T) {
	repo := newTestRepo(t, 0)
	got, err := repo.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest on empty DB: %v", err)
	}
	if got != nil {
		t.Errorf("Latest on empty DB = %+v, want nil", got)
	}
}

func TestRepository_Insert_PrunesOldProtocols(t *testing.T) {
	repo := newTestRepo(t, 3)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		p := &reports.SyncProtocol{
			StartedAt: time.Now().UTC(),
			NextRunAt: time.Now().UTC().Add(time.Hour),
		}
		if err := repo.Insert(ctx, p); err != nil {
			t.Fatalf("Insert #%d: %v", i, err)
		}
	}

	n, err := repo.CountProtocols(ctx)
	if err != nil {
		t.Fatalf("CountProtocols: %v", err)
	}
	if n != 3 {
		t.Errorf("CountProtocols = %d, want 3 (retention)", n)
	}
}

func TestSQLiteFactory_CreatesInProgressRow(t *testing.T) {
	repo, factory := newTestFactory(t, 0)
	ctx := context.Background()

	w, err := factory.CreateProtocolWriter(ctx, time.Now().UTC(), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateProtocol: %v", err)
	}
	if w == nil {
		t.Fatal("expected a writer")
	}

	got, err := repo.Latest(ctx)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got == nil || got.EndedAt != nil {
		t.Fatalf("expected an in-progress protocol after CreateProtocol, got %+v", got)
	}
}

func TestSQLiteWriter_RecordsImmediatelyAndCompletes(t *testing.T) {
	repo, factory := newTestFactory(t, 0)
	ctx := context.Background()

	started := time.Now().UTC()
	w, err := factory.CreateProtocolWriter(ctx, started, started.Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateProtocol: %v", err)
	}

	w.RecordSuccess(ctx, "Yeti47/a", "clone", 5*time.Second)

	// Result should be visible in the database BEFORE Complete is called.
	mid, err := repo.Latest(ctx)
	if err != nil {
		t.Fatalf("Latest mid-cycle: %v", err)
	}
	if mid == nil {
		t.Fatal("Latest returned nil mid-cycle")
	}
	if n, _ := repo.CountResults(ctx, mid.ID); n != 1 {
		t.Errorf("CountResults mid-cycle = %d, want 1", n)
	}
	firstActivity := mid.LastActivityAt

	w.RecordFailure(ctx, "Yeti47/b", "update", 1*time.Second, errors.New("network down"))

	mid2, _ := repo.Latest(ctx)
	if !mid2.LastActivityAt.After(firstActivity) && !mid2.LastActivityAt.Equal(firstActivity) {
		t.Errorf("LastActivityAt should not regress: was %v, now %v", firstActivity, mid2.LastActivityAt)
	}

	if err := w.Complete(ctx); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := w.Complete(ctx); err == nil {
		t.Error("expected error on second Complete")
	}

	got, err := repo.Latest(ctx)
	if err != nil {
		t.Fatalf("Latest after Complete: %v", err)
	}
	if got == nil || got.EndedAt == nil {
		t.Fatalf("expected completed protocol, got %+v", got)
	}
	if got.TotalRepos != 2 || got.SucceededRepos != 1 || got.FailedRepos != 1 {
		t.Errorf("totals = (%d,%d,%d), want (2,1,1)",
			got.TotalRepos, got.SucceededRepos, got.FailedRepos)
	}
	if n, _ := repo.CountProtocols(ctx); n != 1 {
		t.Errorf("CountProtocols = %d, want 1 (Insert+Complete should not duplicate)", n)
	}
}

// fakeSource lets us drive the HealthChecker without a real DB.
type fakeSource struct {
	p   *reports.SyncProtocol
	err error
}

func (f *fakeSource) Latest(ctx context.Context) (*reports.SyncProtocol, error) {
	return f.p, f.err
}

func TestHealthChecker_NoProtocolIsUnhealthy(t *testing.T) {
	hc := reports.NewHealthCheckerWithClock(&fakeSource{}, time.Hour, fixedNow(time.Now().UTC()))
	st := hc.Check(context.Background())
	if st.Healthy {
		t.Errorf("expected unhealthy, got: %s", st.Message)
	}
}

func TestHealthChecker_RecentActivityIsHealthy(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	src := &fakeSource{p: &reports.SyncProtocol{
		StartedAt:      now.Add(-30 * time.Minute),
		LastActivityAt: now.Add(-1 * time.Minute),
		// EndedAt nil = in progress
	}}
	hc := reports.NewHealthCheckerWithClock(src, 30*time.Minute, fixedNow(now))
	st := hc.Check(context.Background())
	if !st.Healthy {
		t.Errorf("expected healthy, got: %s", st.Message)
	}
}

func TestHealthChecker_StaleInProgressIsUnhealthy(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	src := &fakeSource{p: &reports.SyncProtocol{
		StartedAt:      now.Add(-3 * time.Hour),
		LastActivityAt: now.Add(-3 * time.Hour),
	}}
	hc := reports.NewHealthCheckerWithClock(src, 30*time.Minute, fixedNow(now))
	st := hc.Check(context.Background())
	if st.Healthy {
		t.Errorf("expected unhealthy for stuck cycle, got: %s", st.Message)
	}
}

func TestHealthChecker_RecentlyCompletedIsHealthy(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	ended := now.Add(-10 * time.Minute)
	src := &fakeSource{p: &reports.SyncProtocol{
		StartedAt:      ended.Add(-2 * time.Minute),
		EndedAt:        &ended,
		LastActivityAt: ended,
		TotalRepos:     5,
		SucceededRepos: 5,
	}}
	hc := reports.NewHealthCheckerWithClock(src, time.Hour, fixedNow(now))
	st := hc.Check(context.Background())
	if !st.Healthy {
		t.Errorf("expected healthy, got: %s", st.Message)
	}
}

func TestHealthChecker_StaleCompletedIsUnhealthy(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	ended := now.Add(-3 * time.Hour)
	src := &fakeSource{p: &reports.SyncProtocol{
		StartedAt:      ended.Add(-2 * time.Minute),
		EndedAt:        &ended,
		LastActivityAt: ended,
	}}
	hc := reports.NewHealthCheckerWithClock(src, time.Hour, fixedNow(now))
	st := hc.Check(context.Background())
	if st.Healthy {
		t.Errorf("expected unhealthy, got: %s", st.Message)
	}
}

func TestHealthChecker_DBErrorIsUnhealthy(t *testing.T) {
	hc := reports.NewHealthCheckerWithClock(
		&fakeSource{err: errors.New("disk i/o error")},
		time.Hour,
		fixedNow(time.Now().UTC()),
	)
	st := hc.Check(context.Background())
	if st.Healthy {
		t.Errorf("expected unhealthy on DB error, got: %s", st.Message)
	}
}

func fixedNow(t time.Time) func() time.Time {
	return func() time.Time { return t }
}
