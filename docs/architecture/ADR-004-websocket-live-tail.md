# ADR-004: WebSocket for Live Tail

## Context

The web UI needs to display log events in real-time as they arrive at the ingest endpoint — "live tail". The push mechanism must be compatible with the Go backend and the React frontend, and must support optional filtering (e.g. tail only ERROR from service X).

## Options Considered

| Option | Pros | Cons |
|--------|------|------|
| **WebSocket** | Full-duplex; client can send filter params after connect; well-supported in Go (`gorilla/websocket` or stdlib) and React | Slightly more complex than SSE; requires upgrade handshake |
| Server-Sent Events (SSE) | Simpler protocol; auto-reconnect built in; pure HTTP | One-way only — client cannot send filter params over the same connection; no binary framing |
| Long polling | Works everywhere | High latency; heavy on server connections; obsolete for this use case |

## Decision

**WebSocket.**

The live tail needs client-to-server filter negotiation (service name, log level). WebSocket supports this naturally — the client sends a filter message after connect, and the server applies it before fanning out events. SSE would require filter params baked into query strings at connect time with no way to update them without reconnecting.

## Consequences

- `internal/ws/` contains a `Hub` struct that manages connected clients
- Each ingest event is published to the hub after storage write
- The hub filters events per-client based on the filter the client registered at connect
- WebSocket endpoint: `GET /api/v1/logs/stream` (upgraded connection)
- Filter message format (client → server, JSON):
  ```json
  { "services": ["api-server"], "levels": ["Error", "Fatal"] }
  ```
  Empty arrays mean "no filter — receive all".
- Events pushed from server → client use the same `LogEntry` JSON shape as the REST query response
- Disconnected clients are cleaned up immediately via `defer hub.Unregister(client)`
