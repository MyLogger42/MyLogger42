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

# 1. Bootstrap secrets and the admin password hash. Append both
#    KEY_PEPPER + SESSION_SECRET lines into your .env, plus
#    ADMIN_USERNAME and ADMIN_PASSWORD_HASH.
cp .env.example .env
./bin/mylogger42 admin generate-secrets >> .env
echo "ADMIN_USERNAME=admin" >> .env
printf "ADMIN_PASSWORD_HASH=" >> .env
./bin/mylogger42 admin hash-password >> .env
# (paste your password, press enter, ctrl-D)

# 2. Edit .env to remove the placeholder KEY_PEPPER/SESSION_SECRET
#    lines (the appended ones from generate-secrets are the real ones).

# 3. Start the server.
set -a; source .env; set +a
./bin/mylogger42

# 4. Create your first project + ingest key.
./bin/mylogger42 admin create-project demo "Demo" --retention=14
# → mlk_<prefix>_<secret>   (copy this; you won't see it again)
```

The binary serves both the API and the embedded SPA on `HTTP_PORT`
(8080 by default). Visit `http://localhost:8080/` and log in as the
admin user.

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

`generate-secrets` and `hash-password` need no env. The rest open the
same DB the server uses, so they require `KEY_PEPPER`/`SESSION_SECRET`
in the env block (any value of valid shape works for non-ingest
operations, but in practice you'll have the real ones in `.env`).

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
