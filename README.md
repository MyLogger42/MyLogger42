# MyLogger42

Self-hosted event logging system. HTTP clients POST structured log events;
a React UI searches, filters, and live-tails them.

Architecture and contracts are documented in [`docs/`](./docs). This README
is just the operator quickstart.

## Quickstart (local)

```sh
cp .env.example .env
# edit .env and set INGEST_API_KEY at minimum
make build
./bin/mylogger42
```

The binary serves both the API and the embedded SPA on `HTTP_PORT` (8080
by default).

## Quickstart (Docker)

```sh
cp .env.example .env
docker compose up -d --build
```

## Frontend dev loop

```sh
# terminal 1: backend
make run

# terminal 2: SPA dev server with HMR (proxies /api → :8080)
cd web && npm install && npm run dev
```

Open http://localhost:5173.

## Smoke test the ingest endpoint

```sh
curl -X POST http://localhost:8080/api/v1/logs \
  -H "X-API-Key: $INGEST_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"service":"demo","level":"Info","message":"hello world"}'
```

## Layout

```
cmd/server/        program entry point
internal/api/      HTTP routing, handlers, middleware
internal/domain/   LogEntry, LogLevel, filters
internal/store/    LogStore interface + sqlite + postgres
internal/retention/ background purge worker
internal/ws/       WebSocket hub + per-client goroutines
web/               React + Vite SPA, embedded into the binary
```

## Configuration

All config is via env vars; see `.env.example` for the full list.
