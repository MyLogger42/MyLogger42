# Operations guide

Day-to-day administration, troubleshooting, and the full admin CLI
reference. For install and first-run setup, see the [README](../README.md).

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

Revocation is idempotent. Revoking **in the UI** takes effect at once:
anything still using the key gets `401 Unauthorized` on its next
request. Revoking with the **CLI** writes to the database behind the
running server's ingest-auth cache, so the key can keep being accepted
for up to 60 seconds (the cache TTL) before the `401` starts. Use the UI
when a key is compromised and every second counts — or restart the
server, which empties the cache.

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

Archiving takes effect on the client's next request — it is an API-only
operation, and the server drops its ingest-auth cache as part of it.

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

### Force everyone out (rotate SESSION_SECRET)

Replacing `SESSION_SECRET` invalidates all outstanding session
cookies.

**Local:** replace `SESSION_SECRET` in `.env` with a fresh value:

```sh
./bin/mylogger42 admin generate-secrets
# copy just the SESSION_SECRET= line into .env, then restart
```

**Docker:** edit the secrets file on the data volume:

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

All previously-issued session cookies stop working immediately after
restart.

---

## Admin CLI via Docker

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

mylogger42 admin issue-token [--label=...]       # read-only MCP token (mlr_…), printed once
mylogger42 admin list-tokens                     # id, prefix, label, state, created
mylogger42 admin revoke-token <token-id>
```

`init`, `generate-secrets`, and `hash-password` need no environment.
The rest open the same database the server uses, so they need
`DB_DRIVER`, `DB_DSN`, `KEY_PEPPER`, and `SESSION_SECRET` in the
shell — sourcing `.env` covers all four.
