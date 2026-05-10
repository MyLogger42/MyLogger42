package domain

import "time"

// ProjectKey is one ingest credential for a Project. A project may have
// many keys; revoking sets RevokedAt rather than deleting the row, which
// preserves an audit trail.
//
// Plaintext key material lives only inside the issuance flow — once
// written to the DB the secret is HMAC-only and unrecoverable. The
// IssuedKey type in internal/auth carries the plaintext at creation time.
type ProjectKey struct {
	ID         string
	ProjectID  string
	Prefix     string // 12 chars base32-lowercase; UNIQUE in DB
	Label      string // human description, e.g. "prod-deploy"
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// IsActive reports whether the key still authorizes ingest.
func (k ProjectKey) IsActive() bool { return k.RevokedAt == nil }
