package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestGenerateKeyShape(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k.Value, "mlk_") {
		t.Fatalf("missing mlk_ prefix: %q", k.Value)
	}
	if len(k.Prefix) != 12 {
		t.Fatalf("Prefix len = %d, want 12", len(k.Prefix))
	}
	if len(k.Secret) != 32 {
		t.Fatalf("Secret len = %d, want 32", len(k.Secret))
	}
	want := "mlk_" + k.Prefix + "_" + k.Secret
	if k.Value != want {
		t.Fatalf("Value = %q, want %q", k.Value, want)
	}
}

func TestGenerateKeyCharset(t *testing.T) {
	// Every produced char must be base32-lowercase: a-z + 2-7 (RFC 4648).
	// We accept the slightly looser [a-z0-9] in tests since post-encoding
	// lowercasing yields some 0-9 too (no — RFC4648 base32 is A-Z + 2-7,
	// so we test for that explicitly).
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	check := func(name, s string) {
		for _, c := range s {
			if !((c >= 'a' && c <= 'z') || (c >= '2' && c <= '7')) {
				t.Errorf("%s contains invalid base32-lowercase char %q", name, c)
			}
		}
	}
	check("Prefix", k.Prefix)
	check("Secret", k.Secret)
}

func TestGenerateKeyUnique(t *testing.T) {
	a, _ := GenerateKey()
	b, _ := GenerateKey()
	if a.Value == b.Value {
		t.Fatal("two consecutive keys collided")
	}
}

func TestParseKey(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	prefix, secret, err := ParseKey(k.Value)
	if err != nil {
		t.Fatal(err)
	}
	if prefix != k.Prefix || secret != k.Secret {
		t.Fatalf("ParseKey roundtrip mismatch: %q/%q", prefix, secret)
	}
}

func TestParseKeyRejectsMalformed(t *testing.T) {
	bad := []string{
		"",
		"garbage",
		"mlk_short_secret",
		"mlk_abcdefghijkl_short",
		"wrong_abcdefghijkl_zyxwvutsrqponmlkjihgfedcba234567",
		"mlk_ABCDEFGHIJKL_zyxwvutsrqponmlkjihgfedcba234567", // uppercase prefix
		"mlk__zyxwvutsrqponmlkjihgfedcba234567",             // empty prefix
	}
	for _, b := range bad {
		if _, _, err := ParseKey(b); err == nil {
			t.Errorf("ParseKey(%q) should have failed", b)
		}
	}
}

func TestComputeHMAC(t *testing.T) {
	pepper := []byte("pepper")
	got := ComputeHMAC(pepper, "secret")
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte("secret"))
	want := hex.EncodeToString(mac.Sum(nil))
	if got != want {
		t.Fatalf("ComputeHMAC mismatch: got %q want %q", got, want)
	}
}

func TestVerifyKey(t *testing.T) {
	pepper := []byte("server-pepper")
	stored := ComputeHMAC(pepper, "the-real-secret")
	if !VerifyKey(pepper, "the-real-secret", stored) {
		t.Fatal("matching secret should verify")
	}
	if VerifyKey(pepper, "wrong-secret", stored) {
		t.Fatal("wrong secret should not verify")
	}
	if VerifyKey([]byte("different-pepper"), "the-real-secret", stored) {
		t.Fatal("wrong pepper should not verify")
	}
	// Garbage hex must not panic and must not verify.
	if VerifyKey(pepper, "x", "not-hex") {
		t.Fatal("garbage stored hmac should not verify")
	}
}

func TestFakeVerifyForTimingParityNoOp(t *testing.T) {
	// Just confirms it doesn't panic; the value is intentionally
	// discarded — its only purpose is to consume CPU equivalent to a real
	// VerifyKey when the prefix lookup misses.
	FakeVerifyForTimingParity([]byte("pepper"))
}

func TestReadTokenRoundTripAndIsolation(t *testing.T) {
	tok, err := GenerateReadToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok.Value, "mlr_") {
		t.Fatalf("read token %q lacks mlr_ tag", tok.Value)
	}
	prefix, secret, err := ParseReadToken(tok.Value)
	if err != nil || prefix != tok.Prefix || secret != tok.Secret {
		t.Fatalf("ParseReadToken(%q) = %q, %q, %v", tok.Value, prefix, secret, err)
	}
	// Tags must not cross: an ingest key never reads, a read token never ingests.
	if _, _, err := ParseKey(tok.Value); err == nil {
		t.Fatal("ParseKey accepted a read token")
	}
	ingest, _ := GenerateKey()
	if _, _, err := ParseReadToken(ingest.Value); err == nil {
		t.Fatal("ParseReadToken accepted an ingest key")
	}
}
