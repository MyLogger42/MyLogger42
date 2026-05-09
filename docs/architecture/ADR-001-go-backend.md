# ADR-001: Go for the Backend

## Context

MyLogger42 needs a backend that handles concurrent HTTP ingest from multiple clients, maintains persistent WebSocket connections for live tail, and runs efficiently in a small Docker container on a VPS. The developer is comfortable with Go; this is a solo project with no team language constraint.

## Options Considered

| Option | Pros | Cons |
|--------|------|------|
| **Go** | Compiles to a single static binary; tiny Docker image (scratch or distroless); excellent net/http stdlib; goroutines make WebSocket fan-out trivial; zero runtime dependencies | Slightly more verbose than scripting languages |
| Node.js / TypeScript | Familiar if JS is primary language; large ecosystem; good WebSocket libs | Requires Node runtime in image (~180 MB); GC pauses at higher concurrency |
| Python (FastAPI) | Fast to write; good async WebSocket support (Starlette) | Runtime overhead; slower at high concurrency; heavier image |
| Rust | Maximum performance; tiny binary | Steep learning curve; slower development velocity for a solo tool |

## Decision

**Go.**

Single binary + scratch-based Docker image keeps the deployment footprint minimal. The stdlib `net/http` package handles HTTP and WebSocket upgrades cleanly. Goroutines are the natural model for a WebSocket fan-out hub. Development velocity is acceptable for a solo project.

## Consequences

- Backend ships as a single binary embedded in a `FROM scratch` or `FROM gcr.io/distroless/static` Docker image
- No separate process manager or runtime required in the container
- React UI build artefacts embedded via `go:embed` — one image serves everything
- Standard Go project layout (`cmd/`, `internal/`) applies
