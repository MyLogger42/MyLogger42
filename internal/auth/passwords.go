package auth

import "golang.org/x/crypto/bcrypt"

// bcryptCost is fixed at 12 — slow enough to deter brute force, fast
// enough for the rare admin login (~250ms on commodity hardware). Treat
// as a security parameter; do not lower without explicit review.
const bcryptCost = 12

// HashPassword bcrypts the given plaintext at the canonical cost. The
// returned string is suitable for storage in users.password_hash.
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// VerifyPassword reports whether `plain` matches `hash`. Returns false on
// any error (including malformed hash) — callers don't need to
// differentiate "wrong password" from "corrupt hash" on the auth path.
func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
