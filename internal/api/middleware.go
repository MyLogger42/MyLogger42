package api

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"time"
)

// requireAPIKey wraps a handler so it only runs when the request carries
// the correct X-API-Key header. Used on POST ingest routes.
func requireAPIKey(key string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-API-Key")
		if got == "" || got != key {
			writeError(w, http.StatusUnauthorized, "invalid api key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withCORS adds permissive CORS headers — the UI is served from the same
// origin in production but during dev the React app runs on a different
// port, so allow-all is fine for v1 (no UI auth, ingest is key-protected).
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withLogging emits a one-line access log per request. We deliberately
// don't pull in a structured logger — the server's own observability is
// minimal in v1.
func withLogging(logger interface {
	Printf(format string, v ...any)
}, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rr := &recordingWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rr, r)
		logger.Printf("http %s %s -> %d (%s)", r.Method, r.URL.Path, rr.status, time.Since(start))
	})
}

// recordingWriter captures the response status for the access log. It
// transparently passes through Hijack so the WebSocket upgrade on
// /api/v1/logs/stream can take over the underlying TCP connection — the
// stdlib's default ResponseWriter is a Hijacker but interface-wrapping
// hides it, so we forward explicitly.
type recordingWriter struct {
	http.ResponseWriter
	status int
}

func (r *recordingWriter) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *recordingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	// A successful hijack means we never see a WriteHeader call, so the
	// access log will report the default 200. That's fine — the WS
	// upgrade itself responds 101 to the client; only our visibility is
	// approximate.
	return hj.Hijack()
}
