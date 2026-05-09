# Component / Layer Map

## Go Project Layout

```
mylogger42/
├── cmd/
│   └── server/
│       └── main.go              # Entry point: parse config, wire dependencies, start HTTP server
│
├── internal/
│   ├── api/
│   │   ├── ingest.go            # POST /api/v1/logs handler
│   │   ├── query.go             # GET /api/v1/logs, GET /api/v1/logs/:id, GET /api/v1/sources
│   │   ├── middleware.go        # API key auth middleware
│   │   └── router.go            # Route registration (stdlib mux or chi)
│   │
│   ├── domain/
│   │   ├── log_entry.go         # LogEntry struct, LogLevel type + constants
│   │   └── filters.go           # QueryFilter, LiveTailFilter value objects
│   │
│   ├── store/
│   │   ├── store.go             # LogStore interface
│   │   ├── sqlite.go            # SQLite implementation (mattn/go-sqlite3 or modernc)
│   │   ├── postgres.go          # Postgres implementation (pgx/v5)
│   │   └── migrations/
│   │       ├── sqlite/
│   │       │   └── 001_init.sql
│   │       └── postgres/
│   │           └── 001_init.sql
│   │
│   ├── retention/
│   │   └── worker.go            # Goroutine that runs purge on a ticker
│   │
│   └── ws/
│       ├── hub.go               # Hub: register/unregister clients, fan-out Publish
│       └── client.go            # Client: wraps WebSocket conn, holds LiveTailFilter
│
├── web/                         # React + Vite project
│   ├── src/
│   │   ├── App.tsx
│   │   ├── pages/
│   │   │   ├── LogsPage.tsx     # Search + filter view
│   │   │   └── LiveTailPage.tsx # WebSocket live tail view
│   │   ├── components/
│   │   │   ├── LogList.tsx
│   │   │   ├── LogDetail.tsx
│   │   │   ├── FilterBar.tsx
│   │   │   └── LevelBadge.tsx
│   │   └── hooks/
│   │       ├── useLogs.ts       # Fetch + paginate logs
│   │       └── useLiveTail.ts   # WebSocket connection + filter
│   ├── package.json
│   └── vite.config.ts
│
├── docs/                        # This documentation
│
├── Makefile                     # build, run, docker-build targets
├── Dockerfile
├── docker-compose.yml
└── .env.example
```

## Layer Responsibilities

| Layer | Package | Responsibility |
|-------|---------|----------------|
| **Entry point** | `cmd/server` | Load config, instantiate store, hub, retention worker; start HTTP server |
| **API** | `internal/api` | HTTP routing, request parsing, response serialisation, auth middleware |
| **Domain** | `internal/domain` | Pure value types — no infrastructure, no I/O |
| **Store** | `internal/store` | Persistence interface + driver implementations + migrations |
| **WebSocket Hub** | `internal/ws` | In-process pub/sub — routes published events to live tail clients |
| **Retention** | `internal/retention` | Background goroutine, reads from store, deletes expired entries |
| **Web UI** | `web/` | React SPA, served as embedded static assets from the Go binary |

## Dependency Flow

```
cmd/server
    └── internal/api     → internal/domain
    └── internal/store   → internal/domain
    └── internal/ws      → internal/domain
    └── internal/retention → internal/store
    └── web/dist (embedded)
```

`internal/domain` has no dependencies on any other internal package — it is the stable core.

## Configuration (Environment Variables)

| Variable | Default | Description |
|----------|---------|-------------|
| `INGEST_API_KEY` | (required) | Secret key clients send in `X-API-Key` header. Single static key; rotate by updating `.env` and restarting. |
| `DB_DRIVER` | `sqlite` | `sqlite` or `postgres` |
| `DB_DSN` | `./data/mylogger42.db` | SQLite file path or Postgres connection string |
| `RETENTION_DAYS` | `30` | Delete log entries older than N days. Set to `0` to disable retention. |
| `HTTP_PORT` | `8080` | Port the server listens on |
| `LOG_LEVEL` | `info` | Server's own log verbosity (not client log levels) |

**Rate limiting:** Not implemented in v1. The ingest endpoint is trusted; the API key is the sole guard. Add `golang.org/x/time/rate` middleware if floods become an issue in practice.

**Reverse proxy / TLS:** Not bundled. Run MyLogger42 behind Nginx or Caddy for TLS termination and optional IP allowlisting on the UI port. See the project README for setup guidance.
