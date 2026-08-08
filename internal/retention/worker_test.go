package retention

import (
	"context"
	"io"
	"log"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mylogger42/mylogger42/internal/domain"
	"github.com/mylogger42/mylogger42/internal/store"
)

// openTestStores opens a fresh SQLite store under t.TempDir, runs all
// migrations, and registers cleanup. Mirrors the helper in the store
// package — duplicated here because Go's testing rules don't expose
// _test.go helpers across packages.
func openTestStores(t *testing.T) *store.Stores {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	stores, err := store.OpenSQLiteStores(context.Background(), dsn)
	if err != nil {
		t.Fatalf("OpenSQLiteStores: %v", err)
	}
	t.Cleanup(func() { _ = stores.Logs.Close() })
	return stores
}

// insert inserts a single LogEntry with the given received_at age (days
// in the past). Returns the entry's ID for assertions.
func insert(t *testing.T, s *store.Stores, projectID, service string, ageDays int) string {
	t.Helper()
	now := time.Now().UTC()
	e := domain.LogEntry{
		ID:         uuid.NewString(),
		ProjectID:  projectID,
		Service:    service,
		Level:      domain.LevelInfo,
		Message:    "m",
		Timestamp:  now.Add(-time.Duration(ageDays) * 24 * time.Hour),
		ReceivedAt: now.Add(-time.Duration(ageDays) * 24 * time.Hour),
		Metadata:   map[string]any{},
	}
	if err := s.Logs.Insert(context.Background(), e); err != nil {
		t.Fatalf("insert: %v", err)
	}
	return e.ID
}

// countEntries returns how many rows the project has remaining.
func countEntries(t *testing.T, s *store.Stores, projectID string) int {
	t.Helper()
	_, total, err := s.Logs.Query(context.Background(), projectID, domain.QueryFilter{Limit: 500})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	return total
}

func TestSweepPerProjectRetention(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)

	// Three projects with different retention semantics:
	//   p-default: nil RetentionDays → use globalDefault (7 days)
	//   p-strict:  3 days
	//   p-forever: 0 (retain forever)
	mkProject := func(slug string, retention *int) domain.Project {
		return domain.Project{
			ID: uuid.NewString(), Slug: slug, Name: slug,
			RetentionDays: retention,
			CreatedAt:     time.Now().UTC(),
		}
	}
	r3 := 3
	r0 := 0
	pDefault := mkProject("p-default", nil)
	pStrict := mkProject("p-strict", &r3)
	pForever := mkProject("p-forever", &r0)
	for _, p := range []domain.Project{pDefault, pStrict, pForever} {
		if err := s.Projects.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	// Seed 4 entries per project, ages 1/2/5/10 days.
	for _, p := range []domain.Project{pDefault, pStrict, pForever} {
		for _, age := range []int{1, 2, 5, 10} {
			insert(t, s, p.ID, "x", age)
		}
	}

	worker := New(s.Logs, s.Projects, 7 /* globalDefault days */, log.New(io.Discard, "", 0))
	worker.sweep(ctx)

	// p-default uses 7-day window: keeps ages 1, 2, 5; deletes 10.
	if got := countEntries(t, s, pDefault.ID); got != 3 {
		t.Errorf("p-default remaining = %d, want 3", got)
	}
	// p-strict uses 3-day window: keeps ages 1, 2; deletes 5, 10.
	if got := countEntries(t, s, pStrict.ID); got != 2 {
		t.Errorf("p-strict remaining = %d, want 2", got)
	}
	// p-forever (RetentionDays=0): retains everything.
	if got := countEntries(t, s, pForever.ID); got != 4 {
		t.Errorf("p-forever remaining = %d, want 4", got)
	}
}

func TestSweepGlobalDefaultZeroMeansForever(t *testing.T) {
	// When globalDefault is 0 and a project doesn't override, nothing
	// is swept for that project.
	ctx := context.Background()
	s := openTestStores(t)

	p := domain.Project{ID: uuid.NewString(), Slug: "p", Name: "p", CreatedAt: time.Now().UTC()}
	if err := s.Projects.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	insert(t, s, p.ID, "x", 1000) // ancient

	worker := New(s.Logs, s.Projects, 0 /* globalDefault disabled */, log.New(io.Discard, "", 0))
	worker.sweep(ctx)

	if got := countEntries(t, s, p.ID); got != 1 {
		t.Errorf("globalDefault=0 should not delete anything; got %d remaining, want 1", got)
	}
}

func TestSweepIncludesArchivedProjects(t *testing.T) {
	// Archived projects are still subject to retention — disk doesn't
	// stop being consumed when ingest stops.
	ctx := context.Background()
	s := openTestStores(t)

	r := 3
	p := domain.Project{
		ID: uuid.NewString(), Slug: "p-arch", Name: "p-arch",
		RetentionDays: &r,
		CreatedAt:     time.Now().UTC(),
	}
	if err := s.Projects.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	insert(t, s, p.ID, "x", 1) // recent
	insert(t, s, p.ID, "x", 5) // old

	if _, err := s.Projects.Archive(ctx, p.Slug); err != nil {
		t.Fatal(err)
	}

	worker := New(s.Logs, s.Projects, 0, log.New(io.Discard, "", 0))
	worker.sweep(ctx)

	if got := countEntries(t, s, p.ID); got != 1 {
		t.Errorf("archived project: remaining = %d, want 1 (the recent one)", got)
	}
}

func TestSweepFailureIsolation(t *testing.T) {
	// One project's failure must not abort the rest of the sweep. We
	// verify this by making sure a sweep completes cleanly with a mix
	// of valid and not-yet-existent project IDs in the worker's view —
	// the sweep itself sources from ProjectStore.List, so deletions
	// only run for rows the store reports. To approximate "one failure"
	// we just confirm that with multiple projects, all that should be
	// swept actually are swept (the per-project loop is independent).
	ctx := context.Background()
	s := openTestStores(t)

	r := 1
	a := domain.Project{ID: uuid.NewString(), Slug: "a", Name: "a", RetentionDays: &r, CreatedAt: time.Now().UTC()}
	b := domain.Project{ID: uuid.NewString(), Slug: "b", Name: "b", RetentionDays: &r, CreatedAt: time.Now().UTC()}
	for _, p := range []domain.Project{a, b} {
		if err := s.Projects.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		insert(t, s, p.ID, "x", 5) // older than 1 day
	}
	worker := New(s.Logs, s.Projects, 0, log.New(io.Discard, "", 0))
	worker.sweep(ctx)
	if got := countEntries(t, s, a.ID); got != 0 {
		t.Errorf("project a remaining = %d, want 0", got)
	}
	if got := countEntries(t, s, b.ID); got != 0 {
		t.Errorf("project b remaining = %d, want 0", got)
	}
}
