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
	e, err := p.ToEntry("id-1", "proj-test", now)
	if err != nil {
		t.Fatal(err)
	}
	if e.ProjectID != "proj-test" {
		t.Fatalf("ProjectID = %q, want %q", e.ProjectID, "proj-test")
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

func TestDecodeIngestBatchStrictRejectsProjectField(t *testing.T) {
	// One bad entry in the batch must reject the whole array — the
	// caller's atomicity rule means one rejection takes the whole batch.
	bad := []byte(`[{"service":"a","level":"Info","message":"m"},` +
		`{"service":"b","level":"Info","message":"m","project":"x"}]`)
	if _, err := DecodeIngestBatchStrict(bad); err == nil {
		t.Fatal("expected error rejecting batch entry with 'project' field")
	}

	good := []byte(`[{"service":"a","level":"Info","message":"m"}]`)
	got, err := DecodeIngestBatchStrict(good)
	if err != nil {
		t.Fatalf("good batch rejected: %v", err)
	}
	if len(got) != 1 || got[0].Service != "a" {
		t.Fatalf("unexpected batch: %+v", got)
	}

	// Non-array body — must error.
	if _, err := DecodeIngestBatchStrict([]byte(`{"service":"a"}`)); err == nil {
		t.Fatal("expected error on non-array batch body")
	}
}

func TestDecodeIngestPayloadStrictRejectsProjectField(t *testing.T) {
	// Per v2 spec: project is determined by the authenticating key, not by
	// the payload. A top-level "project" field must be rejected.
	bad := []byte(`{"service":"s","level":"Info","message":"m","project":"foo"}`)
	if _, err := DecodeIngestPayloadStrict(bad); err == nil {
		t.Fatal("expected error rejecting payload with 'project' field")
	}
	good := []byte(`{"service":"s","level":"Info","message":"m"}`)
	if _, err := DecodeIngestPayloadStrict(good); err != nil {
		t.Fatalf("good payload rejected: %v", err)
	}
}

func TestLiveTailFilterMatches(t *testing.T) {
	entry := LogEntry{ProjectID: "p1", Service: "auth", Level: LevelError}

	// Empty filter — no projects specified — matches nothing per v2 spec.
	if (LiveTailFilter{}).Matches(entry) {
		t.Fatal("empty filter should not match (projects required)")
	}

	// Project-only match.
	if !(LiveTailFilter{Projects: []string{"p1"}}).Matches(entry) {
		t.Fatal("project match should pass")
	}

	// Wrong project.
	if (LiveTailFilter{Projects: []string{"other"}}).Matches(entry) {
		t.Fatal("wrong project should not match")
	}

	// Project + service mismatch.
	if (LiveTailFilter{
		Projects: []string{"p1"},
		Services: []string{"billing"},
	}).Matches(entry) {
		t.Fatal("service mismatch should fail")
	}

	// Project + level match.
	if !(LiveTailFilter{
		Projects: []string{"p1"},
		Levels:   []LogLevel{LevelError, LevelFatal},
	}).Matches(entry) {
		t.Fatal("project + level match should pass")
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
