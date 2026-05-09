---
name: ResponseWriter wrappers must forward Hijack for WebSocket
description: Any custom http.ResponseWriter wrapper in the api/middleware chain must implement http.Hijacker, or /api/v1/logs/stream upgrades will 500
type: feedback
---

The WebSocket handler (`/api/v1/logs/stream`) calls `gorilla/websocket.Upgrader.Upgrade`, which calls `ResponseWriter.Hijack()`. Any middleware that wraps the ResponseWriter (currently `recordingWriter` in `internal/api/middleware.go`, which captures the status code for the access log) MUST forward `Hijack()` explicitly via a method like:

```go
func (r *recordingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
    hj, ok := r.ResponseWriter.(http.Hijacker)
    if !ok { return nil, nil, errors.New("...") }
    return hj.Hijack()
}
```

Without this, the upgrade fails with status 500 — Gorilla writes the error to the ResponseWriter and closes.

**Why:** Go interface composition does NOT auto-forward methods. Embedding `http.ResponseWriter` only gives you the `ResponseWriter` interface; `Hijacker` is a separate interface that must be implemented (or forwarded) explicitly on the wrapper type.

**How to apply:** If you add another middleware in `internal/api/middleware.go` that wraps the ResponseWriter (e.g. for compression, request-ID injection, response capture), it must also implement `Hijack()` — otherwise the WS route breaks. The same applies to `Flush()` if you add SSE later.
