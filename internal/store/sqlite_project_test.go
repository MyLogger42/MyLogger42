package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/silverbaq/mylogger42/internal/domain"
)

// mkProject is a helper that builds a domain.Project with a fresh UUID
// and sane defaults. Tests use this so each row is a value, not a
// reference, and to keep their bodies short.
func mkProject(slug string) domain.Project {
	return domain.Project{
		ID:        uuid.NewString(),
		Slug:      slug,
		Name:      "test " + slug,
		CreatedAt: time.Now().UTC(),
	}
}

func TestSQLiteProjectCreateAndGet(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)

	p := mkProject("auth-service")
	if err := s.Projects.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.Projects.GetBySlug(ctx, "auth-service")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if got.ID != p.ID || got.Slug != p.Slug || got.Name != p.Name {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, p)
	}

	gotByID, err := s.Projects.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if gotByID.Slug != p.Slug {
		t.Fatalf("GetByID slug = %q, want %q", gotByID.Slug, p.Slug)
	}
}

func TestSQLiteProjectCreateDuplicateSlug(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)

	p := mkProject("dup")
	if err := s.Projects.Create(ctx, p); err != nil {
		t.Fatal(err)
	}

	q := mkProject("dup") // same slug, different id
	err := s.Projects.Create(ctx, q)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestSQLiteProjectGetSlugNotFound(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)

	_, err := s.Projects.GetBySlug(ctx, "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	_, err = s.Projects.GetByID(ctx, "no-such-uuid")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for id, got %v", err)
	}
}

func TestSQLiteProjectListAndArchive(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)

	for _, slug := range []string{"a", "b", "c"} {
		if err := s.Projects.Create(ctx, mkProject(slug)); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.Projects.Archive(ctx, "b"); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	active, err := s.Projects.List(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 {
		t.Fatalf("active=%d, want 2", len(active))
	}
	for _, p := range active {
		if p.Slug == "b" {
			t.Fatal("archived project leaked into active list")
		}
		if p.ArchivedAt != nil {
			t.Fatalf("active project %q has ArchivedAt set", p.Slug)
		}
	}

	all, err := s.Projects.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("all=%d, want 3", len(all))
	}

	// Unarchive restores it to the active set.
	if _, err := s.Projects.Unarchive(ctx, "b"); err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
	active2, _ := s.Projects.List(ctx, false)
	if len(active2) != 3 {
		t.Fatalf("after unarchive, active=%d, want 3", len(active2))
	}
}

func TestSQLiteProjectUpdateRetention(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)

	if err := s.Projects.Create(ctx, mkProject("svc")); err != nil {
		t.Fatal(err)
	}

	d := 7
	got, err := s.Projects.Update(ctx, "svc", "Service Y", &d)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Service Y" || got.RetentionDays == nil || *got.RetentionDays != 7 {
		t.Fatalf("update mismatch: %+v", got)
	}

	// nil retention clears the override.
	got, err = s.Projects.Update(ctx, "svc", "Service Y", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.RetentionDays != nil {
		t.Fatalf("expected nil retention after clearing, got %v", *got.RetentionDays)
	}

	// Updating an unknown slug is ErrNotFound.
	if _, err := s.Projects.Update(ctx, "nope", "x", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSQLiteProjectCount(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)

	n, err := s.Projects.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("Count = %d, want 0", n)
	}
	if err := s.Projects.Create(ctx, mkProject("only")); err != nil {
		t.Fatal(err)
	}
	n, _ = s.Projects.Count(ctx)
	if n != 1 {
		t.Fatalf("Count after create = %d, want 1", n)
	}
}

func TestSQLiteProjectDelete(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)
	if err := s.Projects.Create(ctx, mkProject("doomed")); err != nil {
		t.Fatal(err)
	}
	if err := s.Projects.Delete(ctx, "doomed"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Projects.GetBySlug(ctx, "doomed"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Delete: expected ErrNotFound, got %v", err)
	}
	if err := s.Projects.Delete(ctx, "doomed"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete on missing: expected ErrNotFound, got %v", err)
	}
}
