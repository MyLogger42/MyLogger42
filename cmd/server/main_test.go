package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLogLevel(t *testing.T) {
	cases := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{in: "", want: slog.LevelInfo},
		{in: "info", want: slog.LevelInfo},
		{in: "DEBUG", want: slog.LevelDebug},
		{in: " warn ", want: slog.LevelWarn},
		{in: "warning", want: slog.LevelWarn},
		{in: "error", want: slog.LevelError},
		{in: "trace", wantErr: true},
	}

	for _, tc := range cases {
		got, err := parseLogLevel(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseLogLevel(%q) = %v, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseLogLevel(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseLogLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestNewLogger(t *testing.T) {
	t.Run("json format and level filtering", func(t *testing.T) {
		var buf bytes.Buffer
		logger, err := newLogger("warn", "json", &buf)
		if err != nil {
			t.Fatalf("newLogger error: %v", err)
		}

		logger.Debug("ignored debug")
		logger.Info("ignored info")
		logger.Warn("kept warn", "component", "test")

		lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
		if len(lines) != 1 {
			t.Fatalf("got %d log lines, want 1: %q", len(lines), buf.String())
		}

		var rec map[string]any
		if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
			t.Fatalf("invalid json log: %v (%s)", err, lines[0])
		}
		if rec["level"] != "WARN" {
			t.Errorf("level = %v, want WARN", rec["level"])
		}
		if rec["msg"] != "kept warn" {
			t.Errorf("msg = %v, want 'kept warn'", rec["msg"])
		}
		if rec["component"] != "test" {
			t.Errorf("component = %v, want 'test'", rec["component"])
		}
	})

	t.Run("text format default", func(t *testing.T) {
		var buf bytes.Buffer
		logger, err := newLogger("info", "text", &buf)
		if err != nil {
			t.Fatalf("newLogger error: %v", err)
		}
		logger.Info("hello", "k", "v")
		out := buf.String()
		if !strings.Contains(out, "level=INFO") || !strings.Contains(out, "msg=hello") || !strings.Contains(out, "k=v") {
			t.Errorf("unexpected text log output: %q", out)
		}
	})

	t.Run("invalid format", func(t *testing.T) {
		var buf bytes.Buffer
		if _, err := newLogger("info", "yaml", &buf); err == nil {
			t.Fatal("expected error for unsupported LOG_FORMAT")
		}
	})
}
