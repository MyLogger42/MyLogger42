package domain

import (
	"errors"
	"regexp"
	"time"
)

// Project is a logical bucket of log entries owned by one or more ingest
// keys. Project membership of a LogEntry is server-stamped from the
// authenticating ingest key — never trusted from the payload.
type Project struct {
	ID            string
	Slug          string
	Name          string
	RetentionDays *int // nil = inherit global default; 0 = forever
	CreatedAt     time.Time
	ArchivedAt    *time.Time
}

// slugPattern is the canonical project-slug regex. Slug must start with a
// lowercase letter, then [a-z0-9-], total length 2-63. The "starts with a
// letter" rule keeps slugs human-pronounceable and avoids ambiguity with
// numeric IDs in URL paths.
var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

// ValidateSlug enforces the slug rules. It is the single source of truth
// for what counts as a valid slug across admin handlers, the CLI, and the
// store. Callers that need to validate a user-supplied slug should call
// this rather than rolling their own regex.
func ValidateSlug(s string) error {
	if s == "" {
		return errors.New("slug is required")
	}
	if !slugPattern.MatchString(s) {
		return errors.New("slug must match [a-z][a-z0-9-]{1,62}")
	}
	return nil
}
