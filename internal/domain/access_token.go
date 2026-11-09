package domain

import "time"

// AccessToken is a read-only credential for the MCP endpoint. Unlike a
// ProjectKey it is not bound to a project: an agent holding one can read
// every project, but can write nothing. Like ProjectKey, the secret is
// stored only as an HMAC and revoking keeps the row for the audit trail.
type AccessToken struct {
	ID        string
	Prefix    string // 12 chars base32-lowercase; UNIQUE in DB
	Label     string // human description, e.g. "claude-code laptop"
	CreatedAt time.Time
	RevokedAt *time.Time
}
