# ADR-002: Pluggable Storage — SQLite or Postgres

## Context

MyLogger42 needs durable log storage. Different deployment scenarios have different needs: a local dev machine wants zero-ops simplicity; a long-running VPS may want better concurrent write performance or an existing Postgres instance. A single hardcoded choice would force trade-offs on users.

## Options Considered

| Option | Pros | Cons |
|--------|------|------|
| **SQLite only** | Zero ops; embedded; no extra container; file-based backup | Single-writer lock limits concurrent ingest bursts; no network access from external tools |
| **Postgres only** | Concurrent writes; rich SQL; external tooling (pgAdmin, psql) | Requires a second container; ops overhead; overkill for low volume |
| **Pluggable (SQLite or Postgres)** | User picks the right tool for their context; single codebase | Slightly more code — a `LogStore` interface + two implementations |
| **Embedded TSDB (e.g. DuckDB)** | Analytical query performance | Immature Go drivers; operational unfamiliarity |

## Decision

**Pluggable storage via a `LogStore` interface, with SQLite and Postgres implementations.**

The backend defines a `LogStore` interface. The concrete implementation is selected at startup via `DB_DRIVER=sqlite|postgres` environment variable. SQLite is the default — zero extra containers, instant start. Postgres is available when the user needs it.

## Consequences

- `internal/store/` contains: `store.go` (interface), `sqlite.go`, `postgres.go`
- `DB_DRIVER`, `DB_DSN` env vars control selection
- SQLite uses WAL mode to improve concurrent read performance
- Postgres connection uses `pgx` driver
- Migrations run at startup via embedded SQL files (both drivers)
- `docker-compose.yml` includes an optional `postgres` service, commented out by default
- Backups: SQLite → copy the `.db` file; Postgres → `pg_dump`
