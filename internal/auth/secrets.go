// Package auth owns crypto-touching helpers: project-key generation and
// verification, password hashing, session JWT signing.
//
// No network or DB code lives here — this package is pure compute, which
// makes it cheap to test and easy to audit.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// GenerateSecret returns a cryptographically random hex string of size
// 2*nBytes characters. Use 32 (→ 64 hex chars) for KEY_PEPPER and
// SESSION_SECRET; smaller values are reserved for tests.
//
// Returns an error if nBytes is below the 16-byte security floor — that
// guards against accidentally seeding production secrets at toy sizes.
func GenerateSecret(nBytes int) (string, error) {
	if nBytes < 16 {
		return "", errors.New("secret must be ≥ 16 bytes")
	}
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
