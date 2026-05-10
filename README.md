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

This walks you through everything in one go:

- Generates `KEY_PEPPER` and `SESSION_SECRET` (the two crypto secrets
  the server needs to start).
- Asks for the admin username and password (no echo on the password).
- Optionally creates your first project and prints the first ingest
  key.
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
Create a first project now? [Y/n] y
project slug (e.g. auth-service): auth-service
project name: Auth Service
retention days (blank = inherit global default): 14

Your initial ingest key — copy it now, it won't be shown again:

mlk_3xkza82bvwn4_8cqf2nrtmvpz9wljg7bdxkh4yvs

Next steps:
  1. Source the env and start the server
  ...
```

**Copy the `mlk_…` value somewhere safe** — the server only stores a
hash of it, so we genuinely cannot show it again later. If you lose
it, just issue a new key (instructions below).

### 3. Start the server

```sh
set -a; source .env; set +a
./bin/mylogger42
```

The binary serves the API and the UI on the same port (8080 by
default).

### 4. Log in

Open `http://localhost:8080/` in your browser, sign in with the admin
user you just created, and you'll land on the projects page.

### 5. Send your first log entry

Use the ingest key from step 2. From any terminal:

```sh
curl -X POST http://localhost:8080/api/v1/logs \
  -H "X-API-Key: mlk_3xkza82bvwn4_8cqf2nrtmvpz9wljg7bdxkh4yvs" \
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

```sh
./bin/mylogger42 admin set-password <username>
```

Note: changing the password does **not** invalidate sessions issued
under the old password — JWTs are signed with `SESSION_SECRET`, not
linked to the password hash. If you're rotating because of a
compromise, also rotate `SESSION_SECRET` (regenerate it, paste the
new value into `.env`, restart the server). That kicks everyone out.

### Force everyone out

Replace `SESSION_SECRET` in `.env` with a fresh value:

```sh
./bin/mylogger42 admin generate-secrets
```

…and copy just the `SESSION_SECRET=` line over. Restart the server.
All previously-issued session cookies stop working.

---

## Running with Docker

```sh
cp .env.example .env
docker compose up -d --build
```

Edit `.env` first — you need real values for `KEY_PEPPER` and
`SESSION_SECRET`, plus optionally `ADMIN_USERNAME` /
`ADMIN_PASSWORD_HASH` to seed the admin user on first start.

If you'd rather use the interactive bootstrapper inside the
container:

```sh
docker compose run --rm --service-ports mylogger42 admin init
docker compose up -d
```

Or just run `admin init` on the host first to write `.env`, then
`docker compose up -d`.

The SQLite database is on a named volume (`mylogger42-data`) so it
survives container restarts. Switch to Postgres by changing
`DB_DRIVER`/`DB_DSN` in `.env`; no schema migration step needed, the
binary applies migrations on start.

---

## Running behind a TLS reverse proxy

Put Caddy / Nginx / Traefik in front for HTTPS. Two notes:

1. Set `TRUST_FORWARDED_PROTO=true` in `.env`. The server then
   honors `X-Forwarded-Proto: https` when deciding whether to mark
   the session cookie `Secure` (so cookies aren't sent over plain
   HTTP).
2. There's no built-in rate limiter for login attempts. Point
   fail2ban (or your proxy's equivalent) at the access log and ban
   repeat `401`s on `POST /api/v1/auth/login`.

Caddy minimal example:

```
logs.example.com {
  reverse_proxy localhost:8080
}
```

Combined with `TRUST_FORWARDED_PROTO=true`, Caddy's automatic
`X-Forwarded-Proto: https` is enough.

---

## Configuration reference (`.env`)

| Variable                | Required          | Default                  | Notes                                                                                |
|-------------------------|-------------------|--------------------------|--------------------------------------------------------------------------------------|
| `KEY_PEPPER`            | yes               | —                        | 32+ random bytes, hex-encoded. Used to fingerprint ingest keys. `admin init` sets it. |
| `SESSION_SECRET`        | yes               | —                        | 32+ random bytes, hex-encoded. Signs admin session JWTs.                              |
| `ADMIN_USERNAME`        | first start only  | —                        | Bootstraps the admin user when the `users` table is empty.                            |
| `ADMIN_PASSWORD_HASH`   | first start only  | —                        | bcrypt hash. Generate with `admin hash-password`.                                     |
| `DB_DRIVER`             | no                | `sqlite`                 | `sqlite` or `postgres`.                                                               |
| `DB_DSN`                | no                | `./data/mylogger42.db`   | Filesystem path or libpq URL.                                                         |
| `RETENTION_DAYS`        | no                | `30`                     | Default retention. Per-project values override. `0` = retain forever.                 |
| `HTTP_PORT`             | no                | `8080`                   |                                                                                       |
| `LOG_LEVEL`             | no                | `info`                   | Server's own access log verbosity.                                                    |
| `TRUST_FORWARDED_PROTO` | no                | `false`                  | Set to `true` behind a TLS-terminating reverse proxy.                                 |

---

## Troubleshooting

**`KEY_PEPPER is required` on startup.** You haven't sourced `.env`,
or you ran the binary in a different shell from the one where you
sourced it. Try `set -a; source .env; set +a` again, then start.

**Login screen gives "JSON" / "Unexpected end" errors.** You're on
an old build. Rebuild (`make build`), restart, and hard-refresh the
browser (Cmd-Shift-R / Ctrl-Shift-R).

**"No admin configured" message in the UI.** Either `users` is empty
and `ADMIN_USERNAME` / `ADMIN_PASSWORD_HASH` weren't set on first
start, or someone deleted the user row. Run
`./bin/mylogger42 admin create-user` to fix.

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
