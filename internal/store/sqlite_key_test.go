package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mylogger42/mylogger42/internal/domain"
)

// mkKey is a helper that creates and inserts a ProjectKey under the
// given project. Returns the inserted struct (with the helper's
// generated UUID) so tests can assert against it.
func mkKey(t *testing.T, s *Stores, projectID, prefix, label string) domain.ProjectKey {
	t.Helper()
	k := domain.ProjectKey{
		ID:        uuid.NewString(),
		ProjectID: projectID,
		Prefix:    prefix,
		Label:     label,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.Keys.Create(context.Background(), k, "fakehmac"); err != nil {
		t.Fatalf("Keys.Create: %v", err)
	}
	return k
}

func TestSQLiteKeyCreateAndLookup(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)
	p := mkProject("svc")
	if err := s.Projects.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	mkKey(t, s, p.ID, "abcdefghijkl", "deploy")

	got, hmac, err := s.Keys.GetActiveByPrefix(ctx, "abcdefghijkl")
	if err != nil {
		t.Fatalf("GetActiveByPrefix: %v", err)
	}
	if got.ProjectID != p.ID || got.Label != "deploy" || hmac != "fakehmac" {
		t.Fatalf("mismatch: %+v hmac=%q", got, hmac)
	}
}

func TestSQLiteKeyDuplicatePrefix(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)
	p := mkProject("svc")
	if err := s.Projects.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	mkKey(t, s, p.ID, "samesame1234", "first")

	dup := domain.ProjectKey{
		ID:        uuid.NewString(),
		ProjectID: p.ID,
		Prefix:    "samesame1234", // collides
		Label:     "second",
		CreatedAt: time.Now().UTC(),
	}
	err := s.Keys.Create(ctx, dup, "anotherfakehmac")
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestSQLiteKeyRevokeHidesFromActiveLookup(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)
	p := mkProject("svc")
	if err := s.Projects.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	k := mkKey(t, s, p.ID, "prefix111111", "ci")
	if err := s.Keys.Revoke(ctx, k.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	_, _, err := s.Keys.GetActiveByPrefix(ctx, "prefix111111")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for revoked key, got %v", err)
	}

	// includeRevoked=true still surfaces it.
	all, err := s.Keys.ListByProject(ctx, p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("ListByProject(includeRevoked=true) = %d, want 1", len(all))
	}
	if all[0].RevokedAt == nil {
		t.Fatal("expected RevokedAt to be set")
	}

	active, err := s.Keys.ListByProject(ctx, p.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("ListByProject(includeRevoked=false) = %d, want 0", len(active))
	}

	// Revoke is idempotent: a second call on the same key returns nil.
	if err := s.Keys.Revoke(ctx, k.ID); err != nil {
		t.Fatalf("idempotent revoke: %v", err)
	}
	// Revoke on a non-existent key returns ErrNotFound.
	if err := s.Keys.Revoke(ctx, "no-such-id"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Revoke(nonexistent) = %v, want ErrNotFound", err)
	}
}

func TestSQLiteKeyNoteUsage(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)
	p := mkProject("svc")
	if err := s.Projects.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	k := mkKey(t, s, p.ID, "prefixusage1", "ci")

	when := time.Now().UTC().Truncate(time.Second)
	if err := s.Keys.NoteUsage(ctx, k.ID, when); err != nil {
		t.Fatal(err)
	}

	got, err := s.Keys.GetByID(ctx, k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(when) {
		t.Fatalf("LastUsedAt = %v, want %v", got.LastUsedAt, when)
	}
}

func TestSQLiteKeyListByProjectScoped(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)

	p1 := mkProject("p1")
	p2 := mkProject("p2")
	if err := s.Projects.Create(ctx, p1); err != nil {
		t.Fatal(err)
	}
	if err := s.Projects.Create(ctx, p2); err != nil {
		t.Fatal(err)
	}

	mkKey(t, s, p1.ID, "p1key1111111", "first")
	mkKey(t, s, p1.ID, "p1key2222222", "second")
	mkKey(t, s, p2.ID, "p2key1111111", "other-project")

	p1Keys, err := s.Keys.ListByProject(ctx, p1.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p1Keys) != 2 {
		t.Fatalf("p1 keys = %d, want 2", len(p1Keys))
	}
	for _, k := range p1Keys {
		if k.ProjectID != p1.ID {
			t.Fatalf("leaked key from project %s", k.ProjectID)
		}
	}
}
