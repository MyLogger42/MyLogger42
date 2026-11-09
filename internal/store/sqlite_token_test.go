package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mylogger42/mylogger42/internal/domain"
)

func TestSQLiteTokenLifecycle(t *testing.T) {
	ctx := context.Background()
	s := openTestStores(t)
	tok := domain.AccessToken{
		ID:        uuid.NewString(),
		Prefix:    "abcdefghijkl",
		Label:     "claude",
		CreatedAt: time.Now().UTC(),
	}
	if err := s.Tokens.Create(ctx, tok, "fakehmac"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Tokens.Create(ctx, tok, "fakehmac"); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate Create = %v, want ErrAlreadyExists", err)
	}

	got, hmac, err := s.Tokens.GetActiveByPrefix(ctx, tok.Prefix)
	if err != nil || got.ID != tok.ID || got.Label != "claude" || hmac != "fakehmac" {
		t.Fatalf("GetActiveByPrefix = %+v, %q, %v", got, hmac, err)
	}

	if err := s.Tokens.Revoke(ctx, tok.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := s.Tokens.Revoke(ctx, tok.ID); err != nil {
		t.Fatalf("second Revoke should be idempotent, got %v", err)
	}
	if err := s.Tokens.Revoke(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Revoke unknown = %v, want ErrNotFound", err)
	}
	if _, _, err := s.Tokens.GetActiveByPrefix(ctx, tok.Prefix); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked lookup = %v, want ErrNotFound", err)
	}

	active, _ := s.Tokens.List(ctx, false)
	all, _ := s.Tokens.List(ctx, true)
	if len(active) != 0 || len(all) != 1 || all[0].RevokedAt == nil {
		t.Fatalf("List active=%d all=%+v", len(active), all)
	}
}
