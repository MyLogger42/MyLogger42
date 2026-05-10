package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
)

// IssuedKey is the result of GenerateKey. The Value is what the admin
// sees once at issuance time; the server stores only Prefix and the
// HMAC of Secret. Callers must not persist Value or Secret beyond the
// admin-side reveal.
type IssuedKey struct {
	Prefix string
	Secret string
	Value  string
}

var (
	// b32 is RFC 4648 base32 with no padding. We lowercase the result so
	// keys are URL-safe, copy-paste-safe across all clipboards/terminals,
	// and double-click-selectable (no slashes or equals signs).
	b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

	// keyPattern is the canonical "mlk_<12>_<32>" shape. Both halves are
	// constrained to base32-lowercase (a-z + 2-7).
	keyPattern = regexp.MustCompile(`^mlk_([a-z2-7]{12})_([a-z2-7]{32})$`)
)

// GenerateKey produces a fresh ingest key. Format is
// "mlk_<prefix-12>_<secret-32>", 51 chars total. Random bytes come from
// crypto/rand.
//
// Entropy: 8 bytes → 13 base32 chars (truncated to 12, ~60 bits) for the
// prefix, 20 bytes → 32 base32 chars (~160 bits) for the secret.
func GenerateKey() (*IssuedKey, error) {
	prefixBytes := make([]byte, 8)
	secretBytes := make([]byte, 20)
	if _, err := rand.Read(prefixBytes); err != nil {
		return nil, err
	}
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, err
	}
	prefix := strings.ToLower(b32.EncodeToString(prefixBytes))[:12]
	secret := strings.ToLower(b32.EncodeToString(secretBytes))[:32]
	return &IssuedKey{
		Prefix: prefix,
		Secret: secret,
		Value:  "mlk_" + prefix + "_" + secret,
	}, nil
}

// ParseKey splits a key value into its (prefix, secret) parts. It rejects
// any key that doesn't match the canonical mlk_<12>_<32> lowercase shape.
//
// The middleware path uses ParseKey first (cheap reject for malformed
// input) and only proceeds to the DB lookup on a successful parse.
func ParseKey(value string) (prefix, secret string, err error) {
	m := keyPattern.FindStringSubmatch(value)
	if m == nil {
		return "", "", errors.New("malformed key")
	}
	return m[1], m[2], nil
}

// ComputeHMAC returns hex(HMAC-SHA256(pepper, secret)). This is the exact
// value stored in the project_keys.hmac column.
func ComputeHMAC(pepper []byte, secret string) string {
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte(secret))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyKey reports whether `secret` matches the stored hex HMAC for the
// given pepper. Comparison is constant-time via crypto/hmac.Equal —
// safe against timing oracles. Returns false on any decode error
// (including malformed hex) so the caller doesn't have to differentiate.
func VerifyKey(pepper []byte, secret, storedHexHMAC string) bool {
	stored, err := hex.DecodeString(storedHexHMAC)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte(secret))
	return hmac.Equal(mac.Sum(nil), stored)
}

// FakeVerifyForTimingParity does HMAC work over a dummy secret so the
// auth middleware can keep timing constant on prefix-lookup misses. The
// result is intentionally discarded — only the CPU consumption matters.
//
// Without this, an attacker could distinguish "no such prefix" (fast) from
// "wrong secret" (HMAC + compare) by timing alone.
func FakeVerifyForTimingParity(pepper []byte) {
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte("0000000000000000000000000000000000000000"))
	_ = mac.Sum(nil)
}
