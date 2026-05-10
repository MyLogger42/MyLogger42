# ADR-007: Per-project ingest keys + session-cookie admin auth

> **Status:** Accepted (v2). Supersedes [ADR-005](./ADR-005-api-key-auth.md).

## Context

v1 used a single `INGEST_API_KEY` shared across all clients and an
open (no-auth) UI. The product evolved to support multiple isolated
projects in one server, which required two coupled changes:

1. **Ingest auth needs to identify which project an entry belongs to.**
   A single static key can't do that without trusting client-supplied
   metadata, which the spec explicitly forbids.
2. **Reads can no longer be open** if projects need any meaningful
   isolation — even single-admin v2 needs the UI behind a login so the
   "v3 multi-user" path doesn't require a second auth-system rewrite.

## Options Considered

### For ingest auth

| Option | Pros | Cons |
|---|---|---|
| Single key (v1) | Trivial | Can't identify the project; no rotation without downtime |
| **Per-project keys, prefix-and-HMAC stored** | Multi-key per project; rotate without downtime; key never reconstructible from DB | One indexed lookup + HMAC compare per request; adds an HMAC pepper to env |
| Per-project keys, bcrypt-stored | Same security profile as PHC password storage | ~10ms per ingest at cost=12; too slow for the hot path |
| JWT-issued per project | Per-token expiry, audience claims | Issuance flow needed; revocation requires denylist; overkill |

### For UI auth

| Option | Pros | Cons |
|---|---|---|
| Stay open (v1) | No additional surface | Inconsistent with project-as-trust-boundary; closes the door on multi-user |
| **Single admin user, session-cookie auth** | Forward-compatible with multi-user; standard cookie+JWT pattern | Adds a `users` table and session-management logic |
| Multi-user from the start | Future-proof | 2-3x the implementation cost; not justified by current scale |

## Decision

**Ingest:** keys are issued per project with the wire format
`mlk_<prefix-12>_<secret-32>` (base32-lowercase). The server stores
only `(prefix, hex(HMAC-SHA256(KEY_PEPPER, secret)))`. The middleware:

1. Splits the presented `X-API-Key` into prefix + secret.
2. Looks up the active key by prefix (UNIQUE-indexed; partial index
   on `revoked_at IS NULL`).
3. On miss, runs a dummy HMAC computation so timing doesn't reveal
   "no such prefix" vs "wrong secret."
4. `crypto/hmac.Equal` compare the HMAC of the presented secret
   against the stored value.
5. Resolves the project; archived projects respond 403.
6. Stamps the project on the request context; ingest handlers read it
   from there and never trust the payload's `project` field
   (`DecodeIngestPayloadStrict` rejects extras).

**UI auth:** a single admin user lives in a `users` table. Login
mints an HS256 JWT (claims: sub, usr, iat, exp = iat + 7d) and sets
it as a `session=` cookie (HttpOnly, SameSite=Lax, Secure when
running over TLS). All read endpoints, the WebSocket upgrade, and
admin endpoints require the cookie. The session signer slides the
cookie forward when iat > 24h ago.

**Rotation:**

- Per-key: `mylogger42 admin revoke-key <id>` (idempotent).
- Per-project: archive (soft) or `delete-project --force` (hard).
- All sessions: rotate `SESSION_SECRET` and restart.
- Compromised admin password: rotate via `set-password` AND rotate
  `SESSION_SECRET` in the same operation (the JWT has no link back to
  the bcrypt hash, so password change alone doesn't kill old cookies).

**Pepper:** `KEY_PEPPER` is required (≥32 hex bytes after decode).
HMAC-SHA256 with the pepper means a DB-only leak doesn't yield usable
keys; both DB and `.env` would have to leak together.

## Consequences

- The auth surface adds an `internal/auth` package (key gen/verify,
  bcrypt wrappers, session signer) and an `internal/admin` CLI for
  issuance + bootstrap.
- The store layer splits into LogStore + ProjectStore + KeyStore +
  UserStore. The four share one DB connection but expose narrow ports.
- `INGEST_API_KEY` is removed; the server fails to start without
  `KEY_PEPPER` and `SESSION_SECRET`.
- v3 multi-user can layer per-user-per-project membership on top
  without changing the ingest path; the JWT already carries `sub`.
- **Brute-force protection** is delegated to the reverse proxy
  (fail2ban on the access log). v2 adds no in-process rate limit.
