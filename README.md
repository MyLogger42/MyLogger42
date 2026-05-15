# MyLogger42

A small, self-hosted logging server. Your apps `POST` JSON events to it,
and you read them back in a browser — search, filter by service or
level, or watch them stream in live.

Runs as a single binary. Single SQLite file by default; Postgres if you
want. The web UI is bundled into the binary, so there's nothing to
serve separately.

```
┌──────────────┐    POST  /api/v1/logs    ┌─────────────────┐
│  your app    │ ───────────────────────▶ │   mylogger42    │
│  (any lang)  │     X-API-Key: mlk_…     │  (Go binary)    │
└──────────────┘                          │  + embedded UI  │
                                          └────────┬────────┘
                                                   │
                                                   ▼
        ┌──────────────────────────────────────────────────┐
        │   Browser at http://localhost:8080/              │
        │   admin login → search · filter · live tail      │
        └──────────────────────────────────────────────────┘
```

---

## Screenshots

| Login | Logs | Settings |
|-------|------|----------|
| ![Login screen](screenshots/screenshot1.png) | ![Logs view with expanded entry](screenshots/screenshot3.png) | ![Project settings and ingest keys](screenshots/screenshot2.png) |

---

## Concepts in 60 seconds

You need to know three things to use MyLogger42 productively.

- **Project.** A logical bucket of log entries — typically one per app
  or service group. Each project has its own ingest keys and its own
  retention window.
- **Ingest key.** A long random string of the form
  `mlk_<prefix>_<secret>` that an HTTP client sends in the
  `X-API-Key` header. Each key belongs to exactly one project. You
  can have several per project (one per deploy / CI job / dev
  machine), and revoke any of them without affecting the others.
- **Admin user.** A single human login that has access to the web UI
  and can create / archive projects and issue / revoke keys. Read
  endpoints all require this login. Ingest endpoints do not — they
  only need a valid key.

---

## Get started

### 1. Build

```sh
make build
```

This builds the React UI and the Go binary, embeds the UI into the
binary, and writes the result to `bin/mylogger42`.

> Prerequisites: Go 1.22+, Node 20+, npm. SQLite is built in (no
> system library needed).

### 2. Bootstrap (one command)

```sh
./bin/mylogger42 admin init
```

Run this directly — `admin init` generates everything for you.

This walks you through everything in one go:

- Generates `KEY_PEPPER` and `SESSION_SECRET` (the two crypto secrets
  the server needs to start).
- Asks for the admin username and password (no echo on the password).
- Optionally creates a first project (default: skip — you can create
  projects from the web UI after logging in).
- Writes everything to `./.env` (mode `0600`, owner-only readable).

You'll see something like:

```
init: generated KEY_PEPPER
init: generated SESSION_SECRET
admin username: admin
admin password: ********
confirm:        ********
init: admin user configured
init: wrote .env
Create a first project now? [y/N] n

Next steps:
  1. Source the env and start the server:
       set -a; source .env; set +a
       ./bin/mylogger42
  2. Open http://localhost:8080/ and log in.
  3. Create a project + key when ready:
       ./bin/mylogger42 admin create-project <slug> <name>
     or use the Projects page in the UI.
```

### 3. Start the server

```sh
set -a; source .env; set +a
./bin/mylogger42
```

The binary serves the API and the UI on the same port (8080 by
default).

### 4. Log in

Open `http://localhost:8080/` in your browser, sign in with the admin
user you just created. If no projects exist yet you'll see an empty
state with a **+ New project** button — create one there, copy the
ingest key from the modal, and you're ready to send events.

### 5. Send your first log entry

Use the ingest key issued when you created the project. From any terminal:

```sh
curl -X POST http://localhost:8080/api/v1/logs \
  -H "X-API-Key: mlk_ch4kz63ldwhi_gpkm7u6d3iiuln7owtmbiawycp5qldxk" \
  -H "Content-Type: application/json" \
  -d '{
    "service": "signup",
    "level":   "Info",
    "message": "user registered",
    "metadata": {"userId": "u_42", "country": "DK"}
  }'
```

Response:

```json
{"id": "ce384bf2-6de1-4b72-9e9f-232884dfd19a"}
```

Reload the project's **Logs** page in the UI — your entry is there.
Open **Live tail** in another tab and POST again — you'll see it
arrive in real time.

That's the whole loop.

---

## Sending events from your app

The wire format is one JSON document per request, or an array for
batch.

### Required fields

| Field     | Type   | Notes                                                                                  |
|-----------|--------|----------------------------------------------------------------------------------------|
| `service` | string | The component within the project that emitted the event (e.g. `"signup"`, `"worker"`). |
| `level`   | string | One of `Verbose`, `Debug`, `Info`, `Event`, `Warning`, `Error`, `Fatal`.               |
| `message` | string | Short human-readable line. Free text.                                                  |

### Optional fields

| Field        | Type    | Default                  | Notes                                                                                                                                                  |
|--------------|---------|--------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------|
| `timestamp`  | RFC3339 | server's receive time    | Use this if you're shipping batched / delayed events and want them to keep their original timestamp.                                                   |
| `metadata`   | object  | `{}`                     | Any JSON object — request IDs, user IDs, structured fields you'd want to filter on later. Stored as JSON; v2 doesn't index inside it but does show it. |

### Forbidden field

`project` — never include it. The project is determined by which
ingest key you use. A payload that includes a top-level `project`
field is rejected with `400 Bad Request` (this prevents apps from
accidentally writing into the wrong project).

### Single

```sh
curl -X POST http://localhost:8080/api/v1/logs \
  -H "X-API-Key: mlk_…" \
  -H "Content-Type: application/json" \
  -d '{"service":"signup","level":"Error","message":"db unreachable","metadata":{"err":"timeout"}}'
```

Returns `201 Created` with `{"id": "<uuid>"}`.

### Batch (up to 500 entries)

```sh
curl -X POST http://localhost:8080/api/v1/logs/batch \
  -H "X-API-Key: mlk_…" \
  -H "Content-Type: application/json" \
  -d '[
    {"service":"signup","level":"Info","message":"a"},
    {"service":"signup","level":"Info","message":"b"}
  ]'
```

Returns `201 Created` with `{"accepted": N}`. **Atomic**: if any
single entry is invalid, the entire batch is rejected with `400` and
none of the entries are written. The response body lists the failing
indices.

### Quick recipes

<details>
<summary><strong>JavaScript / Node fetch</strong></summary>

```js
await fetch('http://localhost:8080/api/v1/logs', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    'X-API-Key': process.env.MYLOGGER42_KEY,
  },
  body: JSON.stringify({
    service: 'web',
    level:   'Error',
    message: err.message,
    metadata: { stack: err.stack, requestId: req.id },
  }),
});
```

</details>

<details>
<summary><strong>Python (requests)</strong></summary>

```py
import os, requests

requests.post(
    "http://localhost:8080/api/v1/logs",
    headers={"X-API-Key": os.environ["MYLOGGER42_KEY"]},
    json={
        "service": "ingest-worker",
        "level":   "Warning",
        "message": "retry queue backing up",
        "metadata": {"depth": 421},
    },
    timeout=2,
)
```

</details>

<details>
<summary><strong>Go (net/http)</strong></summary>

```go
body, _ := json.Marshal(map[string]any{
    "service": "billing",
    "level":   "Info",
    "message": "invoice issued",
    "metadata": map[string]any{"invoiceId": id},
})
req, _ := http.NewRequest("POST", "http://localhost:8080/api/v1/logs", bytes.NewReader(body))
req.Header.Set("X-API-Key", os.Getenv("MYLOGGER42_KEY"))
req.Header.Set("Content-Type", "application/json")
http.DefaultClient.Do(req)
```

</details>

---

## Using the UI

After logging in at `http://localhost:8080/` you land on **Projects**.
Pick one and you get three tabs:

### Logs (history)

Search and filter past entries. Filters:

- **Service** — dropdown populated from services seen in this project.
- **Level** — single level on the history page; the live-tail page
  has multi-select.
- **From / To** — datetime range.
- **Search** — substring match on `message`.

Click any row to expand it — you'll see the full metadata JSON, the
exact RFC3339 timestamp, and the entry ID. Use Previous / Next to
page through results (50 per page).

### Live tail

Opens a WebSocket and streams matching entries as they arrive. You
can:

- Filter by service and any combination of levels.
- **Connect / Disconnect** to pause and resume the stream.
- **Auto-scroll** off if you're reading older entries while new ones
  flow in.
- **Clear** to drop the buffer.

### Settings

Per-project settings:

- **Project details** — rename, change retention. Slug is permanent
  (it's in URLs and on every existing key).
- **Ingest keys** — table of all keys for this project. **Issue new
  key** opens a label prompt and shows the new key value once. Each
  row has a **Revoke** button; revoked keys stop authenticating
  immediately.
- **Danger zone** — archive (stops new ingest, keeps existing
  entries) or unarchive. Hard delete is CLI-only.

The project switcher in the top bar lets you jump between projects
while staying on whichever tab you were looking at.

---

## Day-to-day operations

### Issue another ingest key

For a separate deploy, CI job, or dev machine you want to revoke
independently:

In the UI: **Settings → Ingest keys → Issue new key**.

Or from the CLI:

```sh
./bin/mylogger42 admin issue-key auth-service --label=ci-runner
```

The plaintext `mlk_…` value is printed to stdout once — pipe it where
it needs to go.

### Revoke a key

In the UI, click **Revoke** on the row. Or:

```sh
./bin/mylogger42 admin revoke-key <key-id>
```

(`<key-id>` is the UUID shown in the keys table — not the prefix.)

Revocation is immediate and idempotent. Anything still using the key
will start getting `401 Unauthorized` on the next request.

### Create another project

In the UI: **Projects → + New project**. The first key is shown once
in a modal — copy it.

Or from the CLI:

```sh
./bin/mylogger42 admin create-project billing "Billing" --retention=30
```

Prints the first key.

### Change retention for one project

UI: **Settings → Project details → Retention (days)**. Save.

- Blank → inherit `RETENTION_DAYS` from `.env`.
- `0` → keep entries forever for this project.
- Any positive integer → keep that many days; a background worker
  deletes anything older once an hour.

### Archive a project

UI: **Settings → Danger zone → Archive**.

Archived projects stop accepting ingest (clients get `403 Forbidden`)
but their existing entries stay readable until retention deletes them.
Reversible — **Unarchive** brings it back.

If you want an archived project's entries kept indefinitely, set its
retention to `0` **before** archiving. (Archive doesn't pause the
retention worker.)

### Rotate the admin password

**Local:**
```sh
./bin/mylogger42 admin set-password <username>
```

**Docker:**
```sh
docker exec -it mylogger42 /app/mylogger42 admin set-password <username>
```

Note: changing the password does **not** invalidate sessions issued
under the old password — JWTs are signed with `SESSION_SECRET`, not
linked to the password hash. If you're rotating because of a
compromise, also rotate `SESSION_SECRET` (see below). That kicks
everyone out.

### Force everyone out

**Local:** replace `SESSION_SECRET` in `.env` with a fresh value:

```sh
./bin/mylogger42 admin generate-secrets
# copy just the SESSION_SECRET= line into .env, then restart
```

**Docker:** edit the secrets file on the data volume:

```sh
docker exec -it mylogger42 sh
/app/mylogger42 admin generate-secrets   # copy the SESSION_SECRET= line
vi /app/data/.secrets                    # replace the line, save
exit
docker compose restart mylogger42
```

All previously-issued session cookies stop working immediately after
restart.

---

## Running with Docker

The recommended way to self-host. No Go or Node toolchain needed —
the image builds everything internally.

### 1. Set your admin credentials

Create a local `.env` file in the project root (it is gitignored, so
it never gets committed):

```
ADMIN_USERNAME=admin
ADMIN_PASSWORD=your-strong-password
```

Docker Compose reads this file automatically. Alternatively, export
the variables in your shell before running:

```sh
export ADMIN_USERNAME=admin
export ADMIN_PASSWORD=your-strong-password
```

> These variables are only used on **first boot** to seed the admin
> user. Once the database has a user record they are ignored on every
> subsequent start — the database is the source of truth.

### 2. Start

```sh
docker compose up -d
```

On first boot the container automatically:

1. Generates `KEY_PEPPER` and `SESSION_SECRET` and writes them to
   `/app/data/.secrets` inside the named volume. They persist across
   restarts and re-creates — rotating them invalidates all ingest keys
   and logs out all active sessions.
2. Hashes `ADMIN_PASSWORD` and seeds the admin user in the database.
3. Applies any pending schema migrations.
4. Starts serving on port `8080`.

### 3. Follow the logs

```sh
docker compose logs -f mylogger42
```

On a clean first boot you should see lines like:

```
mylogger42: first boot — generating secrets
mylogger42 init: starting mylogger42 (driver=sqlite, port=8080, retention=30d)
mylogger42 init: bootstrapped user "admin" from env vars
mylogger42 init: http listening on :8080
```

### Admin CLI via Docker

All `admin` subcommands work through `docker exec`. The container
already has `KEY_PEPPER` and `SESSION_SECRET` in its environment, so
DB-touching commands work without any extra setup:

```sh
# Create a project and receive the first ingest key
docker exec mylogger42 /app/mylogger42 admin create-project auth-service "Auth Service"

# Issue an additional ingest key for an existing project
docker exec mylogger42 /app/mylogger42 admin issue-key auth-service --label=ci-runner

# Revoke a key by its UUID (shown in the UI Settings tab)
docker exec mylogger42 /app/mylogger42 admin revoke-key <key-id>

# Rotate the admin password (interactive, prompts for new password)
docker exec -it mylogger42 /app/mylogger42 admin set-password admin

# List all projects
docker exec mylogger42 /app/mylogger42 admin list-projects
```

### Force everyone out (rotate SESSION_SECRET)

Replacing `SESSION_SECRET` invalidates all outstanding session
cookies. Edit the secrets file that lives on the data volume:

```sh
# Open a shell inside the running container
docker exec -it mylogger42 sh

# Generate a fresh secret pair (copy only the SESSION_SECRET= line)
/app/mylogger42 admin generate-secrets

# Edit the file — replace the SESSION_SECRET line with the new value
vi /app/data/.secrets

# Exit the shell, then restart
docker compose restart mylogger42
```

### Data persistence

The SQLite database and the generated secrets file are stored on the
`mylogger42-data` named volume. The volume survives `docker compose
down` — only `docker compose down -v` removes it (taking your data
with it).

### Switching to Postgres

Add `DB_DRIVER` and `DB_DSN` to your local `.env`:

```
DB_DRIVER=postgres
DB_DSN=postgres://user:pass@host:5432/mylogger42?sslmode=disable
```

No migration step needed — the binary applies migrations automatically
on startup.

---

## Running behind a TLS reverse proxy

**Required for internet-facing deployments.** Put Caddy / Nginx /
Traefik in front so traffic reaches the server over HTTPS.

`TRUST_FORWARDED_PROTO` is already set to `true` in
`docker-compose.yml`. The server honors `X-Forwarded-Proto: https`
when deciding whether to mark the session cookie `Secure`.

The server has a built-in login rate limiter (5 failed attempts per IP
per 15 minutes). When running behind a proxy, it reads the real client
IP from `X-Forwarded-For` automatically.

Caddy minimal example (handles TLS automatically):

```
logs.example.com {
  reverse_proxy localhost:8080
}
```

If you are not using Docker, set `TRUST_FORWARDED_PROTO=true` in your
`.env` and ensure your firewall only allows traffic to port 8080 from
the proxy.

---

## Configuration reference (`.env`)

| Variable                | Required          | Default                  | Notes                                                                                                      |
|-------------------------|-------------------|--------------------------|------------------------------------------------------------------------------------------------------------|
| `KEY_PEPPER`            | yes               | —                        | 32+ random bytes, hex-encoded. Fingerprints ingest keys. Auto-generated by Docker; `admin init` for local. |
| `SESSION_SECRET`        | yes               | —                        | 32+ random bytes, hex-encoded. Signs admin session JWTs. Auto-generated by Docker.                         |
| `ADMIN_USERNAME`        | first start only  | —                        | Bootstraps the admin user when the `users` table is empty.                                                 |
| `ADMIN_PASSWORD`        | first start only  | —                        | **Docker only.** Plaintext password — the entrypoint hashes it before the server starts.                   |
| `ADMIN_PASSWORD_HASH`   | first start only  | —                        | **Local dev only.** Pre-computed bcrypt hash. Generate with `admin hash-password`.                         |
| `DB_DRIVER`             | no                | `sqlite`                 | `sqlite` or `postgres`.                                                                                    |
| `DB_DSN`                | no                | `./data/mylogger42.db`   | Filesystem path or libpq URL.                                                                              |
| `RETENTION_DAYS`        | no                | `30`                     | Default retention. Per-project values override. `0` = retain forever.                                      |
| `HTTP_PORT`             | no                | `8080`                   |                                                                                                            |
| `LOG_LEVEL`             | no                | `info`                   | Server's own access log verbosity.                                                                         |
| `TRUST_FORWARDED_PROTO` | no                | `false`                  | Set to `true` behind a TLS-terminating reverse proxy. Already `true` in `docker-compose.yml`.              |

---

## Troubleshooting

**`KEY_PEPPER is required` on startup.** You haven't sourced `.env`,
or you ran the binary in a different shell from the one where you
sourced it. Try `set -a; source .env; set +a` again, then start. With
Docker this should never occur — secrets are generated automatically
on first boot and loaded from the data volume on every subsequent
start.

**Login screen gives "JSON" / "Unexpected end" errors.** You're on
an old build. Rebuild (`make build`), restart, and hard-refresh the
browser (Cmd-Shift-R / Ctrl-Shift-R).

**"No admin configured" message in the UI.** The `users` table is
empty and no admin was seeded on first boot. With Docker this means
`ADMIN_USERNAME` / `ADMIN_PASSWORD` were not set when the container
first started. Fix it by running:

```sh
docker exec -it mylogger42 /app/mylogger42 admin create-user
```

For local dev: `./bin/mylogger42 admin create-user`.

**Ingest returns `403 Forbidden`.** The project the key belongs to is
archived. Unarchive it from the UI or accept that ingest is paused.

**Ingest returns `401 Unauthorized`.** The key is wrong, malformed
(must be exactly `mlk_<12 chars>_<32 chars>`), or revoked. Issue a
fresh one.

**Ingest returns `400 Bad Request: unknown field "project"`.** Your
payload includes a top-level `project` field. Remove it — the
project comes from the auth header, not the body.

**Live tail says "Connection error" / closes immediately with code
1007.** The first WebSocket frame must include at least one project
slug. The bundled UI always sends one; if you're connecting from a
custom client, send `{"projects":["<slug>"]}` as the first message.

**Ingest works fine but the UI shows nothing.** You're looking at
the wrong project. Use the project switcher in the top bar.

---

## Admin CLI reference

```
mylogger42 admin init [--force] [--print] [--no-project] [--env=PATH]
                                          # one-shot bootstrap

mylogger42 admin generate-secrets         # prints fresh KEY_PEPPER + SESSION_SECRET
mylogger42 admin hash-password            # stdin → bcrypt hash on stdout

mylogger42 admin create-user              # interactive
mylogger42 admin set-password <username>  # interactive

mylogger42 admin create-project <slug> <name> [--retention=N]
mylogger42 admin list-projects
mylogger42 admin issue-key <slug> [--label=...]
mylogger42 admin revoke-key <key-id>
mylogger42 admin delete-project <slug> --force   # hard delete (CLI only)
```

`init`, `generate-secrets`, and `hash-password` need no environment.
The rest open the same database the server uses, so they need
`DB_DRIVER`, `DB_DSN`, `KEY_PEPPER`, and `SESSION_SECRET` in the
shell — sourcing `.env` covers all four.

---

## Frontend dev loop

```sh
# terminal 1: backend
make run

# terminal 2: SPA dev server with HMR (proxies /api → :8080)
cd web && npm install && npm run dev
```

Open `http://localhost:5173`. The Vite dev server proxies API and
WebSocket calls to `:8080`, so login / cookies / WS upgrades all
work the same as in production.

---

## Project layout

```
cmd/server/         program entry point + admin subcommand dispatch
internal/admin/     CLI subcommands (init, create-project, …)
internal/api/       HTTP routing, handlers, middleware (auth, admin)
internal/auth/      key generation/verification, bcrypt, session JWTs
internal/domain/    LogEntry, Project, ProjectKey, User, filters
internal/store/     LogStore + ProjectStore + KeyStore + UserStore
internal/retention/ per-project purge worker
internal/ws/        WebSocket hub + per-client goroutines
web/                React + Vite SPA, embedded into the binary
docs/               architecture decisions, contracts, sequences
```

Architecture decisions and the v2 multi-project design are written
up in [`docs/architecture/`](./docs/architecture).
