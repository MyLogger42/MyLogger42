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

// QueryFilter captures the parameters supported by GET /api/v1/logs and
// GET /api/v1/stats. All fields are optional; zero values mean "no
// constraint". Limit/Offset have defaults applied at the API layer
// (limit=50, offset=0, sort=desc).
type QueryFilter struct {
	Service  string
	Level    LogLevel
	Levels   []LogLevel
	MinLevel LogLevel
	From     *time.Time
	To       *time.Time
	Search   string
	Limit    int
	Offset   int
	Sort     SortOrder
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
	for _, l := range f.Levels {
		if !l.Valid() {
			return errors.New("unknown log level")
		}
	}
	if f.MinLevel != "" && !f.MinLevel.Valid() {
		return errors.New("unknown log level")
	}
	if f.From != nil && f.To != nil && f.To.Before(*f.From) {
		return errors.New("to must be >= from")
	}
	return nil
}

// EffectiveLevels resolves Level, Levels, and MinLevel into a single
// deduplicated slice of matching levels. Returns nil when no level
// constraint is set; returns a non-nil empty slice when combined
// constraints exclude all levels.
func (f QueryFilter) EffectiveLevels() []LogLevel {
	hasExplicit := f.Level != "" || len(f.Levels) > 0
	if !hasExplicit && f.MinLevel == "" {
		return nil
	}

	if !hasExplicit {
		minOrd := f.MinLevel.Ordinal()
		var out []LogLevel
		for _, l := range allLevels {
			if l.Ordinal() >= minOrd {
				out = append(out, l)
			}
		}
		return out
	}

	seen := make(map[LogLevel]bool, len(f.Levels)+1)
	out := make([]LogLevel, 0, len(f.Levels)+1)
	minOrd := -1
	if f.MinLevel != "" {
		minOrd = f.MinLevel.Ordinal()
	}
	add := func(l LogLevel) {
		if l == "" || seen[l] {
			return
		}
		if minOrd >= 0 && l.Ordinal() < minOrd {
			return
		}
		seen[l] = true
		out = append(out, l)
	}
	add(f.Level)
	for _, l := range f.Levels {
		add(l)
	}
	return out
}

// LiveTailFilter is the per-WS-client subscription filter. The Projects
// slice carries project IDs (resolved from slugs at WS-connect time) and
// is REQUIRED non-empty — broadcasting across all projects is an explicit
// admin decision made out-of-band, not the WS default. Services and
// Levels remain optional; empty means "any" for that dimension.
type LiveTailFilter struct {
	Projects []string   `json:"projects,omitempty"`
	Services []string   `json:"services,omitempty"`
	Levels   []LogLevel `json:"levels,omitempty"`
}

// Matches reports whether a published entry should be delivered to a
// client with this filter. A filter with no projects matches nothing —
// callers must opt into at least one project explicitly.
func (f LiveTailFilter) Matches(e LogEntry) bool {
	if len(f.Projects) == 0 {
		return false
	}
	if !contains(f.Projects, e.ProjectID) {
		return false
	}
	if len(f.Services) > 0 && !contains(f.Services, e.Service) {
		return false
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

// contains reports whether s is in the haystack. Used by Matches to keep
// the per-dimension checks tight.
func contains(haystack []string, s string) bool {
	for _, h := range haystack {
		if h == s {
			return true
		}
	}
	return false
}
