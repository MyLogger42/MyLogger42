package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseLogLevelKnown(t *testing.T) {
	cases := []struct {
		in   string
		want LogLevel
	}{
		{"Verbose", LevelVerbose},
		{"debug", LevelDebug},
		{"INFO", LevelInfo},
		{"Event", LevelEvent},
		{"warning", LevelWarning},
		{"Error", LevelError},
		{"FATAL", LevelFatal},
	}
	for _, tc := range cases {
		got, err := ParseLogLevel(tc.in)
		if err != nil {
			t.Fatalf("ParseLogLevel(%q) error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseLogLevel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseLogLevelUnknown(t *testing.T) {
	if _, err := ParseLogLevel("Trace"); err == nil {
		t.Fatal("expected error for unknown level")
	}
	if _, err := ParseLogLevel(""); err == nil {
		t.Fatal("expected error for empty level")
	}
}

func TestPayloadValidate(t *testing.T) {
	bad := []IngestPayload{
		{Service: "", Level: "Info", Message: "x"},
		{Service: "s", Level: "", Message: "x"},
		{Service: "s", Level: "Trace", Message: "x"},
		{Service: "s", Level: "Info", Message: ""},
	}
	for i, p := range bad {
		if err := p.Validate(); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
	good := IngestPayload{Service: "svc", Level: "Info", Message: "hi"}
	if err := good.Validate(); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestPayloadValidateMetadata(t *testing.T) {
	p := IngestPayload{
		Service:  "s",
		Level:    "Info",
		Message:  "m",
		Metadata: json.RawMessage(`[1,2,3]`),
	}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for non-object metadata")
	}
	p.Metadata = json.RawMessage(`{"a":1}`)
	if err := p.Validate(); err != nil {
		t.Fatalf("expected valid object metadata, got %v", err)
	}
}

func TestToEntryDefaults(t *testing.T) {
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	p := IngestPayload{Service: "s", Level: "Info", Message: "m"}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	e, err := p.ToEntry("id-1", now)
	if err != nil {
		t.Fatal(err)
	}
	// Per spec: missing timestamp defaults to receivedAt.
	if !e.Timestamp.Equal(now) {
		t.Fatalf("Timestamp = %v, want %v", e.Timestamp, now)
	}
	if !e.ReceivedAt.Equal(now) {
		t.Fatalf("ReceivedAt = %v, want %v", e.ReceivedAt, now)
	}
	// Per spec: missing metadata defaults to {}.
	if e.Metadata == nil || len(e.Metadata) != 0 {
		t.Fatalf("Metadata = %v, want empty map", e.Metadata)
	}
}

func TestLiveTailFilterMatches(t *testing.T) {
	entry := LogEntry{Service: "auth", Level: LevelError}

	// Empty filter matches everything.
	if !((LiveTailFilter{}).Matches(entry)) {
		t.Fatal("empty filter should match")
	}

	// Service mismatch.
	if (LiveTailFilter{Services: []string{"billing"}}).Matches(entry) {
		t.Fatal("service mismatch should not match")
	}

	// Level mismatch.
	if (LiveTailFilter{Levels: []LogLevel{LevelInfo}}).Matches(entry) {
		t.Fatal("level mismatch should not match")
	}

	// Both match.
	if !(LiveTailFilter{
		Services: []string{"auth"},
		Levels:   []LogLevel{LevelError, LevelFatal},
	}).Matches(entry) {
		t.Fatal("both should match")
	}
}

func TestQueryFilterValidate(t *testing.T) {
	bad := []QueryFilter{
		{Limit: -1},
		{Limit: 1000},
		{Offset: -1},
		{Sort: "sideways"},
		{Level: "Trace"},
	}
	for i, f := range bad {
		if err := f.Validate(); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
	from := time.Now()
	to := from.Add(-time.Hour)
	if err := (QueryFilter{From: &from, To: &to}).Validate(); err == nil {
		t.Fatal("expected error when to < from")
	}
}
