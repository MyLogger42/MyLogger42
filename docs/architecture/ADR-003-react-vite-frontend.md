# ADR-003: React + Vite for the Web UI

## Context

The web UI needs: a filterable log list, real-time live tail via WebSocket, and a log detail view. It is served from the Go binary via `go:embed`. The developer is an Android engineer with general frontend exposure — tooling should be standard enough to extend without deep frontend expertise.

## Options Considered

| Option | Pros | Cons |
|--------|------|------|
| **React + Vite** | Industry standard; excellent WebSocket hook ecosystem; Vite builds fast; large community; easy to embed as static assets | Adds a JS build step; heavier than server-rendered approaches |
| HTMX + Go templates | No JS build step; served directly from Go; minimal deps | Live tail via SSE is workable but less ergonomic than WebSocket; filtering UI becomes awkward without JS |
| Vue + Vite | Similar to React; slightly lighter | Smaller ecosystem; less familiar to most developers |
| Svelte + Vite | Smallest bundle; reactive by default | Smaller community; fewer ready-made component libraries |

## Decision

**React + Vite.**

WebSocket live tail is a first-class requirement. React's `useEffect` + `WebSocket` pattern and the available hook libraries make this straightforward. Vite produces a static build that embeds cleanly into Go via `go:embed`. The tooling is widely understood.

## Consequences

- `web/` directory contains the Vite + React project
- `npm run build` produces `web/dist/` which is embedded into the Go binary at compile time
- Go serves the SPA from `GET /*` with fallback to `index.html`
- API calls from the UI hit the same origin (no CORS complexity in typical deployment)
- A `Makefile` target handles `npm run build && go build` in the correct order
