package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	// bcrypt hashes start with $2a$ / $2b$ / $2y$.
	if !strings.HasPrefix(hash, "$2") {
		t.Fatalf("hash %q doesn't look like bcrypt", hash)
	}
	if !VerifyPassword(hash, "hunter2") {
		t.Fatal("correct password rejected")
	}
	if VerifyPassword(hash, "wrong") {
		t.Fatal("wrong password accepted")
	}
}

func TestHashPasswordUnique(t *testing.T) {
	// bcrypt salts internally — same plaintext should produce different
	// hashes on each call.
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Fatal("two hashes of the same plaintext collided — bcrypt salt missing?")
	}
}

func TestVerifyPasswordOnGarbageHash(t *testing.T) {
	if VerifyPassword("not-a-bcrypt-hash", "anything") {
		t.Fatal("garbage hash must never verify")
	}
	if VerifyPassword("", "anything") {
		t.Fatal("empty hash must never verify")
	}
}
