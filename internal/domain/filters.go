package domain

import (
	"errors"
	"time"
)

// SortOrder controls ascending/descending result ordering.
type SortOrder string

const (
	SortAsc  SortOrder = "asc"
	SortDesc SortOrder = "desc"
)

// QueryFilter captures the parameters supported by GET /api/v1/logs. All
// fields are optional; zero values mean "no constraint". Limit/Offset have
// defaults applied at the API layer (limit=50, offset=0, sort=desc).
type QueryFilter struct {
	Service string
	Level   LogLevel
	From    *time.Time
	To      *time.Time
	Search  string
	Limit   int
	Offset  int
	Sort    SortOrder
}

// Validate enforces invariants that are cheap to check before hitting the
// store.
func (f QueryFilter) Validate() error {
	if f.Limit < 0 {
		return errors.New("limit must be >= 0")
	}
	if f.Limit > 500 {
		return errors.New("limit must be <= 500")
	}
	if f.Offset < 0 {
		return errors.New("offset must be >= 0")
	}
	if f.Sort != "" && f.Sort != SortAsc && f.Sort != SortDesc {
		return errors.New("sort must be asc or desc")
	}
	if f.Level != "" && !f.Level.Valid() {
		return errors.New("unknown log level")
	}
	if f.From != nil && f.To != nil && f.To.Before(*f.From) {
		return errors.New("to must be >= from")
	}
	return nil
}

// LiveTailFilter is the per-WS-client subscription filter. Empty slices
// mean "match anything" for that dimension.
type LiveTailFilter struct {
	Services []string   `json:"services,omitempty"`
	Levels   []LogLevel `json:"levels,omitempty"`
}

// Matches reports whether a published entry should be delivered to a
// client with this filter.
func (f LiveTailFilter) Matches(e LogEntry) bool {
	if len(f.Services) > 0 {
		hit := false
		for _, s := range f.Services {
			if s == e.Service {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if len(f.Levels) > 0 {
		hit := false
		for _, l := range f.Levels {
			if l == e.Level {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}
