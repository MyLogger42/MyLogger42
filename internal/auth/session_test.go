package auth

import (
	"strings"
	"testing"
	"time"
)

func TestSessionRoundTrip(t *testing.T) {
	signer := NewSessionSigner([]byte("secret"))
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	tok, err := signer.Issue("user-id", "admin", now)
	if err != nil {
		t.Fatal(err)
	}
	if tok == "" || !strings.Contains(tok, ".") {
		t.Fatalf("token doesn't look like a JWT: %q", tok)
	}

	claims, err := signer.Verify(tok, now)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "user-id" || claims.Username != "admin" {
		t.Fatalf("claims mismatch: %+v", claims)
	}
	// IssuedAt should round-trip with second precision (JWT uses Unix
	// seconds).
	if !claims.IssuedAt.Equal(now) {
		t.Fatalf("IssuedAt = %v, want %v", claims.IssuedAt, now)
	}
	expectedExp := now.Add(7 * 24 * time.Hour)
	if !claims.Expires.Equal(expectedExp) {
		t.Fatalf("Expires = %v, want %v", claims.Expires, expectedExp)
	}
}

func TestSessionExpired(t *testing.T) {
	signer := NewSessionSigner([]byte("secret"))
	issued := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	tok, _ := signer.Issue("u", "a", issued)
	// 8 days later — past 7-day exp.
	if _, err := signer.Verify(tok, issued.Add(8*24*time.Hour)); err == nil {
		t.Fatal("expected expiry error")
	}
}

func TestSessionTamperResistant(t *testing.T) {
	a := NewSessionSigner([]byte("secret-a"))
	b := NewSessionSigner([]byte("secret-b"))
	tok, _ := a.Issue("u", "a", time.Now())
	if _, err := b.Verify(tok, time.Now()); err == nil {
		t.Fatal("token signed under one secret must not verify under another")
	}
}

func TestSessionRejectsGarbage(t *testing.T) {
	signer := NewSessionSigner([]byte("secret"))
	bad := []string{
		"",
		"garbage",
		"not.a.jwt",
		"a.b.c", // looks like a JWT but isn't valid
	}
	for _, b := range bad {
		if _, err := signer.Verify(b, time.Now()); err == nil {
			t.Errorf("Verify(%q) should have failed", b)
		}
	}
}

func TestShouldRenewWithinWindow(t *testing.T) {
	signer := NewSessionSigner([]byte("s"))
	issued := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	if signer.ShouldRenew(issued, issued.Add(1*time.Hour)) {
		t.Fatal("recent token should not renew")
	}
	if signer.ShouldRenew(issued, issued.Add(23*time.Hour)) {
		t.Fatal("token under 24h should not renew")
	}
	if !signer.ShouldRenew(issued, issued.Add(25*time.Hour)) {
		t.Fatal("token older than 24h should renew")
	}
}
