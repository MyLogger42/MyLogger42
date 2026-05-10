package domain

import (
	"bytes"
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
//
// ProjectID is server-stamped from the authenticating ingest key in the
// API layer; it is never read from the client payload (see
// DecodeIngestPayloadStrict, which rejects payloads that try).
type LogEntry struct {
	ID         string         `json:"id"`
	ProjectID  string         `json:"projectId"`
	Service    string         `json:"service"`
	Level      LogLevel       `json:"level"`
	Message    string         `json:"message"`
	Timestamp  time.Time      `json:"timestamp"`
	Metadata   map[string]any `json:"metadata"`
	ReceivedAt time.Time      `json:"receivedAt"`
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
// defaults: id, receivedAt, and timestamp/metadata fallbacks. The
// projectID is stamped by the caller (resolved from the authenticating
// ingest key) — never trusted from the payload itself. Caller must have
// already invoked Validate.
func (p IngestPayload) ToEntry(id, projectID string, receivedAt time.Time) (LogEntry, error) {
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
		ProjectID:  projectID,
		Service:    p.Service,
		Level:      level,
		Message:    p.Message,
		Timestamp:  ts,
		Metadata:   meta,
		ReceivedAt: receivedAt.UTC(),
	}, nil
}

// DecodeIngestPayloadStrict parses one IngestPayload from raw JSON and
// rejects payloads containing unknown fields — most importantly a
// top-level "project" field, which would otherwise let clients claim
// project membership the auth layer didn't grant. Project membership is
// always derived from the authenticating ingest key.
//
// This is the entry point handlers should use instead of plain
// json.NewDecoder + Decode, because the strict decoder is the only place
// the "no project field" rule is enforced.
func DecodeIngestPayloadStrict(raw []byte) (IngestPayload, error) {
	var p IngestPayload
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return IngestPayload{}, err
	}
	return p, nil
}

// DecodeIngestBatchStrict is the array-shaped sibling of
// DecodeIngestPayloadStrict. It enforces the "no unknown fields" rule
// per-element so a single batch entry trying to carry a `project` field
// rejects the whole batch — consistent with the v2 spec's atomicity
// rule (one bad entry fails the whole batch).
func DecodeIngestBatchStrict(raw []byte) ([]IngestPayload, error) {
	var ps []IngestPayload
	// Outer parse is non-strict (we want a clean "must be array" error
	// rather than an unknown-fields error). DisallowUnknownFields here
	// applies to the slice element struct because Go's json package
	// propagates the flag into the slice elements.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ps); err != nil {
		return nil, err
	}
	return ps, nil
}

// Source is a read-only projection over the distinct services. It is not
// stored as its own table — it's computed via GROUP BY on log_entries.
type Source struct {
	Name         string    `json:"name"`
	FirstSeenAt  time.Time `json:"firstSeenAt"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
	TotalEntries int64     `json:"totalEntries"`
}
