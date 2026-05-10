package domain

import "time"

// User is a UI/admin login. v2 has exactly one row (the admin); the
// table shape is forward-compatible with v3 multi-user.
//
// PasswordHash lives in the DB but is never JSON-marshalled. Keeping it
// on a domain type lets the store layer return the full record while the
// API layer stays disciplined about what it serialises out.
type User struct {
	ID           string
	Username     string
	PasswordHash string // bcrypt; never serialized
	CreatedAt    time.Time
	LastLoginAt  *time.Time
}
