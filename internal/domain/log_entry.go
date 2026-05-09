package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// LogLevel is the severity of a log entry. Persisted as the string value
// (e.g. "Error") rather than the numeric ordinal so the database remains
// human-readable.
type LogLevel string

const (
	LevelVerbose LogLevel = "Verbose"
	LevelDebug   LogLevel = "Debug"
	LevelInfo    LogLevel = "Info"
	LevelEvent   LogLevel = "Event"
	LevelWarning LogLevel = "Warning"
	LevelError   LogLevel = "Error"
	LevelFatal   LogLevel = "Fatal"
)

// allLevels is the canonical ordered list. The index is the documented
// numeric ordinal (Verbose=0 ... Fatal=6).
var allLevels = []LogLevel{
	LevelVerbose,
	LevelDebug,
	LevelInfo,
	LevelEvent,
	LevelWarning,
	LevelError,
	LevelFatal,
}

// AllLevels returns the canonical level list in ordinal order.
func AllLevels() []LogLevel {
	out := make([]LogLevel, len(allLevels))
	copy(out, allLevels)
	return out
}

// Ordinal returns the documented numeric ordinal for the level. Returns -1
// for unknown levels.
func (l LogLevel) Ordinal() int {
	for i, v := range allLevels {
		if v == l {
			return i
		}
	}
	return -1
}

// Valid reports whether the level is one of the known constants.
func (l LogLevel) Valid() bool {
	return l.Ordinal() >= 0
}

// ParseLogLevel parses a level string (case-insensitive). Unknown levels
// produce an error per the spec — they are rejected, not coerced.
func ParseLogLevel(s string) (LogLevel, error) {
	if s == "" {
		return "", errors.New("level is required")
	}
	for _, v := range allLevels {
		if strings.EqualFold(s, string(v)) {
			return v, nil
		}
	}
	return "", fmt.Errorf("unknown log level %q", s)
}

// LogEntry is the canonical log record both at the API boundary and in
// storage. Timestamps are RFC3339 on the wire, time.Time internally.
type LogEntry struct {
	ID         string                 `json:"id"`
	Service    string                 `json:"service"`
	Level      LogLevel               `json:"level"`
	Message    string                 `json:"message"`
	Timestamp  time.Time              `json:"timestamp"`
	Metadata   map[string]any         `json:"metadata"`
	ReceivedAt time.Time              `json:"receivedAt"`
}

// IngestPayload is the client-supplied shape for a single log POST. Fields
// the client may omit (timestamp, metadata) are pointers / nilable so we
// can distinguish "not sent" from "sent as zero value".
type IngestPayload struct {
	Service   string          `json:"service"`
	Level     string          `json:"level"`
	Message   string          `json:"message"`
	Timestamp *time.Time      `json:"timestamp,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

// Validate checks required fields. It does NOT mutate the payload.
func (p IngestPayload) Validate() error {
	if strings.TrimSpace(p.Service) == "" {
		return errors.New("service is required")
	}
	if strings.TrimSpace(p.Message) == "" {
		return errors.New("message is required")
	}
	if _, err := ParseLogLevel(p.Level); err != nil {
		return err
	}
	if len(p.Metadata) > 0 {
		// Confirm metadata is a JSON object (or null). Per spec we default
		// missing/null metadata to {}, but if the client sent a non-object
		// (e.g. an array, string, number) we reject.
		var probe any
		if err := json.Unmarshal(p.Metadata, &probe); err != nil {
			return fmt.Errorf("metadata: %w", err)
		}
		if probe != nil {
			if _, ok := probe.(map[string]any); !ok {
				return errors.New("metadata must be a JSON object")
			}
		}
	}
	return nil
}

// ToEntry converts a validated payload into a LogEntry, applying server
// defaults: id, receivedAt, and timestamp/metadata fallbacks. Caller must
// have already invoked Validate.
func (p IngestPayload) ToEntry(id string, receivedAt time.Time) (LogEntry, error) {
	level, err := ParseLogLevel(p.Level)
	if err != nil {
		return LogEntry{}, err
	}

	ts := receivedAt
	if p.Timestamp != nil && !p.Timestamp.IsZero() {
		ts = p.Timestamp.UTC()
	}

	meta := map[string]any{}
	if len(p.Metadata) > 0 {
		var decoded map[string]any
		if err := json.Unmarshal(p.Metadata, &decoded); err != nil {
			return LogEntry{}, fmt.Errorf("metadata: %w", err)
		}
		if decoded != nil {
			meta = decoded
		}
	}

	return LogEntry{
		ID:         id,
		Service:    p.Service,
		Level:      level,
		Message:    p.Message,
		Timestamp:  ts,
		Metadata:   meta,
		ReceivedAt: receivedAt.UTC(),
	}, nil
}

// Source is a read-only projection over the distinct services. It is not
// stored as its own table — it's computed via GROUP BY on log_entries.
type Source struct {
	Name         string    `json:"name"`
	FirstSeenAt  time.Time `json:"firstSeenAt"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
	TotalEntries int64     `json:"totalEntries"`
}
