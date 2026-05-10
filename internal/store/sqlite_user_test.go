package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/silverbaq/mylogger42/internal/domain"
)

func mkUser(username, hash string) domain.User {
	return domain.User{
		ID:           uuid.NewString(),
		Username:     username,
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC(),
	}
}

func TestSQLiteUserCreateAndGet(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)
	u := mkUser("admin", "$2a$12$abc")
	if err := s.Users.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	got, err := s.Users.GetByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != u.ID || got.PasswordHash != u.PasswordHash {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestSQLiteUserDuplicateUsername(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)
	if err := s.Users.Create(ctx, mkUser("dup", "h1")); err != nil {
		t.Fatal(err)
	}
	err := s.Users.Create(ctx, mkUser("dup", "h2"))
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestSQLiteUserCount(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)
	n, err := s.Users.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("Count = %d, want 0", n)
	}
	if err := s.Users.Create(ctx, mkUser("a", "h")); err != nil {
		t.Fatal(err)
	}
	n, _ = s.Users.Count(ctx)
	if n != 1 {
		t.Fatalf("Count after create = %d, want 1", n)
	}
}

func TestSQLiteUserUpdatePassword(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)
	if err := s.Users.Create(ctx, mkUser("x", "old")); err != nil {
		t.Fatal(err)
	}
	if err := s.Users.UpdatePassword(ctx, "x", "new"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Users.GetByUsername(ctx, "x")
	if err != nil {
		t.Fatal(err)
	}
	if got.PasswordHash != "new" {
		t.Fatalf("PasswordHash = %q, want %q", got.PasswordHash, "new")
	}

	if err := s.Users.UpdatePassword(ctx, "missing", "anything"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdatePassword(missing) = %v, want ErrNotFound", err)
	}
}

func TestSQLiteUserGetUnknown(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)
	_, err := s.Users.GetByUsername(ctx, "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSQLiteUserNoteLogin(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)
	u := mkUser("a", "h")
	if err := s.Users.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Truncate(time.Second)
	if err := s.Users.NoteLogin(ctx, u.ID, when); err != nil {
		t.Fatal(err)
	}
	got, err := s.Users.GetByUsername(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if got.LastLoginAt == nil || !got.LastLoginAt.Equal(when) {
		t.Fatalf("LastLoginAt = %v, want %v", got.LastLoginAt, when)
	}
}
