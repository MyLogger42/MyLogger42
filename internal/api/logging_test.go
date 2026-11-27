package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWithRequestID(t *testing.T) {
	t.Run("generates request id when missing", func(t *testing.T) {
		var ctxID string
		h := withRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctxID = RequestIDFromContext(r.Context())
			w.WriteHeader(http.StatusNoContent)
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		hdrID := rec.Header().Get(requestIDHeader)
		if hdrID == "" {
			t.Fatal("expected X-Request-Id response header to be set")
		}
		if ctxID != hdrID {
			t.Errorf("context request id = %q, header = %q", ctxID, hdrID)
		}
	})

	t.Run("propagates valid incoming request id", func(t *testing.T) {
		var ctxID string
		h := withRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctxID = RequestIDFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.Header.Set(requestIDHeader, "req-abc-123")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if got := rec.Header().Get(requestIDHeader); got != "req-abc-123" {
			t.Errorf("header = %q, want req-abc-123", got)
		}
		if ctxID != "req-abc-123" {
			t.Errorf("context = %q, want req-abc-123", ctxID)
		}
	})

	t.Run("replaces unsafe incoming request id", func(t *testing.T) {
		h := withRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.Header.Set(requestIDHeader, "bad\nid")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		got := rec.Header().Get(requestIDHeader)
		if got == "" || got == "bad\nid" || strings.Contains(got, "\n") {
			t.Errorf("expected sanitized replacement request id, got %q", got)
		}
	})
}

func TestWithLoggingStructuredFieldsAndLevelFilter(t *testing.T) {
	t.Run("emits structured access log fields", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

		h := withRequestID(withLogging(logger, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("hello world"))
		})))

		req := httptest.NewRequest(http.MethodPost, "/api/v1/logs", nil)
		req.RemoteAddr = "10.0.0.5:54321"
		req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.5")
		req.Header.Set(requestIDHeader, "trace-42")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		var entry map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
			t.Fatalf("invalid json log: %v (%s)", err, buf.String())
		}

		checks := map[string]any{
			"level":         "INFO",
			"msg":           "http request",
			"method":        "POST",
			"path":          "/api/v1/logs",
			"status":        float64(http.StatusCreated),
			"bytes_written": float64(11),
			"remote_ip":     "203.0.113.9",
			"request_id":    "trace-42",
		}
		for k, want := range checks {
			if got := entry[k]; got != want {
				t.Errorf("field %s = %v (%T), want %v (%T)", k, got, got, want, want)
			}
		}
		if dur, ok := entry["duration_ms"].(float64); !ok || dur < 0 {
			t.Errorf("duration_ms = %v, want non-negative number", entry["duration_ms"])
		}
	})

	t.Run("warn level suppresses info access logs while keeping request id", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

		h := withRequestID(withLogging(logger, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if buf.Len() != 0 {
			t.Errorf("expected no access log at warn level, got %q", buf.String())
		}
		if rec.Header().Get(requestIDHeader) == "" {
			t.Error("expected X-Request-Id header even when access logs are filtered")
		}
	})

	t.Run("recordingWriter implements Hijacker and Unwrap", func(t *testing.T) {
		rec := httptest.NewRecorder()
		rw := &recordingWriter{ResponseWriter: rec, status: http.StatusOK}
		if rw.Unwrap() != rec {
			t.Error("Unwrap() did not return underlying ResponseWriter")
		}
		var _ http.Hijacker = rw
	})
}
