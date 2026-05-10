# MyLogger42

Self-hosted event logging system. HTTP clients POST structured log events;
a React UI (admin-login-gated) searches, filters, and live-tails them.

In v2 every entry belongs to a **project**. Each project has its own
ingest keys (1..N) and its own retention window. The admin UI manages
projects + keys; clients POST with `X-API-Key: mlk_<prefix>_<secret>`
and the server stamps the project from the key.

Architecture and contracts are in [`docs/`](./docs). This README is the
operator quickstart.

## Quickstart (local)

```sh
make build
./bin/mylogger42 admin init
```

`init` is the friendly bootstrapper. It walks you through:

1. Generating `KEY_PEPPER` + `SESSION_SECRET` (random, 32 bytes each).
2. Setting your admin username + password (no-echo prompt).
3. Optionally creating your first project + ingest key (key value
   printed once on stdout — copy it before it scrolls away).
4. Writing it all to `./.env` with mode 0600.

Then start the server:

```sh
set -a; source .env; set +a
./bin/mylogger42
```

The binary serves both the API and the embedded SPA on `HTTP_PORT`
(8080 by default). Visit `http://localhost:8080/` and log in as the
admin user.

`admin init` is idempotent: re-running it preserves existing values
(`KEY_PEPPER`, `SESSION_SECRET`, etc.) unless you pass `--force`. If
you'd rather assemble `.env` by hand, the underlying primitives are
still there (`admin generate-secrets`, `admin hash-password`,
`admin create-project`); see the CLI reference below.

## Quickstart (Docker)

```sh
cp .env.example .env
# Edit .env: paste real KEY_PEPPER, SESSION_SECRET, and (optionally)
# ADMIN_USERNAME + ADMIN_PASSWORD_HASH. The first two are required.
docker compose up -d --build
```

If you didn't seed `ADMIN_USERNAME`/`ADMIN_PASSWORD_HASH`, exec into
the container once after first start and run:

```sh
docker compose exec mylogger42 ./mylogger42 admin create-user
```

## Frontend dev loop

```sh
# terminal 1: backend
make run

# terminal 2: SPA dev server with HMR (proxies /api → :8080)
cd web && npm install && npm run dev
```

Open `http://localhost:5173`.

## Smoke test the ingest endpoint

```sh
curl -X POST http://localhost:8080/api/v1/logs \
  -H "X-API-Key: mlk_<prefix>_<secret>" \
  -H "Content-Type: application/json" \
  -d '{"service":"demo","level":"Info","message":"hello world"}'
```

The key value is what `admin create-project` (or `admin issue-key`)
printed. The project is determined by the key — payloads must NOT
include a top-level `project` field (rejected with 400).

## Reverse-proxy + TLS

Run behind Caddy / Nginx / Traefik for TLS termination. Set
`TRUST_FORWARDED_PROTO=true` so the session cookie picks up the
`Secure` flag from `X-Forwarded-Proto: https`.

For login brute-force protection, point fail2ban (or similar) at the
proxy's access log and ban repeat 401s on `POST /api/v1/auth/login`.
v2 has no in-process rate limiter by design.

## Admin CLI

```
mylogger42 admin init [--force] [--print] [--no-project] [--env=PATH]
                                                 # one-shot bootstrap: writes .env,
                                                 # prompts for admin user, optional
                                                 # first project + key
mylogger42 admin generate-secrets               # prints KEY_PEPPER + SESSION_SECRET
mylogger42 admin hash-password                   # stdin → bcrypt hash on stdout
mylogger42 admin create-user                     # interactive
mylogger42 admin set-password <username>         # interactive
mylogger42 admin create-project <slug> <name> [--retention=N]
mylogger42 admin list-projects
mylogger42 admin issue-key <slug> [--label=...]
mylogger42 admin revoke-key <key-id>
mylogger42 admin delete-project <slug> --force
```

`init`, `generate-secrets`, and `hash-password` need no env (init is
the bootstrap path itself). The rest open the same DB the server
uses, so they require `KEY_PEPPER`/`SESSION_SECRET` in the env block
— in practice you'll have the real ones in `.env` after running init.

## Layout

```
cmd/server/         program entry point
internal/admin/     CLI subcommands (generate-secrets, create-project, …)
internal/api/       HTTP routing, handlers, middleware (auth, admin)
internal/auth/      key gen/verify, bcrypt, session JWTs
internal/domain/    LogEntry, Project, ProjectKey, User, filters
internal/store/     LogStore + ProjectStore + KeyStore + UserStore
internal/retention/ per-project purge worker
internal/ws/        WebSocket hub + per-client goroutines
web/                React + Vite SPA, embedded into the binary
```

## Configuration

All config via env vars; see `.env.example`. Required: `KEY_PEPPER`,
`SESSION_SECRET`. The v1 `INGEST_API_KEY` is gone — ingest auth is
per-project in v2.
