# Test Plan

## Ingest — POST /api/v1/logs

| Scenario | Input | Expected |
|----------|-------|----------|
| Happy path | Valid payload + correct API key | 201 Created, `id` in response body |
| Missing API key | No `X-API-Key` header | 401 Unauthorized |
| Wrong API key | `X-API-Key: wrong-key` | 401 Unauthorized |
| Missing `service` | `{"level":"Info","message":"hi"}` | 400 Bad Request, error names `service` |
| Missing `message` | `{"service":"x","level":"Info"}` | 400 Bad Request, error names `message` |
| Unknown level | `{"service":"x","level":"Trace","message":"hi"}` | 400 Bad Request, lists valid levels |
| Missing `level` | `{"service":"x","message":"hi"}` | 400 Bad Request |
| No `timestamp` | Omit `timestamp` field | 201 Created; stored entry has `timestamp == receivedAt` |
| No `metadata` | Omit `metadata` field | 201 Created; stored entry has `metadata: {}` |
| Invalid JSON body | `not-json` | 400 Bad Request |
| Extra unknown fields | `{"service":"x","level":"Info","message":"hi","foo":"bar"}` | 201 Created; unknown fields silently ignored |

## Ingest — POST /api/v1/logs/batch

Batch writes are atomic: all entries valid → all written; any entry invalid → none written.

| Scenario | Input | Expected |
|----------|-------|----------|
| All valid | 3 valid entries | 201 Created, `{ "accepted": 3 }` |
| One invalid entry | 2 valid + 1 unknown level | 400 Bad Request, `errors[0].index == 2`; verify 0 entries written in storage |
| First entry invalid | Bad `level` at index 0, valid entries at 1–2 | 400 Bad Request; 0 entries written |
| All invalid | 3 entries with missing `service` | 400 Bad Request, 3 errors returned; 0 entries written |
| Over limit | 501 entries | 400 Bad Request, "batch exceeds maximum of 500 entries" |
| Empty array | `[]` | 400 Bad Request |
| Missing API key | Valid batch, no `X-API-Key` | 401 Unauthorized |

## Query — GET /api/v1/logs

| Scenario | Params | Expected |
|----------|--------|----------|
| No filters | (none) | 200 OK, returns all entries (newest first), correct `total` |
| Filter by service | `service=api-server` | Only entries where `service == "api-server"` |
| Filter by level | `level=Error` | Only entries where `level == "Error"` |
| Filter by time range | `from=...&to=...` | Only entries in range (inclusive) |
| Free-text search | `search=timeout` | Only entries where `message` contains "timeout" (case-insensitive) |
| Pagination | `limit=10&offset=20` | 10 entries starting from offset 20, `total` reflects full result count |
| Sort ascending | `sort=asc` | Oldest entry first |
| Empty result | `service=nonexistent` | 200 OK, `items: [], total: 0` |
| Invalid time range | `from=2026-05-10&to=2026-05-09` | 400 Bad Request |
| Invalid limit | `limit=0` or `limit=501` | 400 Bad Request |
| Unknown level filter | `level=Trace` | 400 Bad Request |

## Single Entry — GET /api/v1/logs/:id

| Scenario | Input | Expected |
|----------|-------|----------|
| Valid ID | Existing UUID | 200 OK, full LogEntry |
| Non-existent ID | Random UUID | 404 Not Found |
| Invalid UUID format | `not-a-uuid` | 400 Bad Request |

## Sources — GET /api/v1/sources

| Scenario | State | Expected |
|----------|-------|----------|
| Services exist | Entries from 2 services | 200 OK, both services listed with correct counts |
| No entries yet | Empty DB | 200 OK, `sources: []` |
| Count accuracy | Insert 5 entries for "worker" | `totalEntries: 5` for "worker" |

## Live Tail — WebSocket /api/v1/logs/stream

| Scenario | Steps | Expected |
|----------|-------|----------|
| Receive all events | Connect, no filter | All ingested events arrive in real-time |
| Filter by service | Connect, send filter `{"services":["api"]}` | Only entries with `service == "api"` arrive |
| Filter by level | Connect, send filter `{"levels":["Error","Fatal"]}` | Only Error/Fatal entries arrive |
| Update filter | Connect, send filter A, then filter B | Only events matching B arrive after second filter |
| Non-matching event | Send event from filtered-out service | Nothing arrives on the WebSocket |
| Clean disconnect | Close tab / navigate away | Hub deregisters client, no goroutine leak |
| Reconnect | Disconnect and reconnect | New session works; no duplicate events |
| Keep-alive | Idle for >30s | Server sends ping; connection stays open on pong |

## Retention Worker

| Scenario | Setup | Expected |
|----------|-------|----------|
| Purges old entries | Insert entry with `receivedAt = now - 31 days`, RETENTION_DAYS=30 | Entry deleted after worker runs |
| Keeps recent entries | Insert entry with `receivedAt = now - 29 days`, RETENTION_DAYS=30 | Entry still present after worker runs |
| Disabled retention | RETENTION_DAYS=0 | Worker skips delete; all entries retained |
| Storage failure | Simulate DB error on delete | Worker logs error at Error level; no panic; retries next tick |

## Storage — Both Drivers

All of the above scenarios should pass against both the SQLite and Postgres implementations.

| Area | SQLite-specific | Postgres-specific |
|------|----------------|-------------------|
| Concurrent writes | WAL mode enabled; test 10 concurrent inserts | Connection pool; test 50 concurrent inserts |
| Migrations | Applied at startup; re-runs are no-op | Same |
| DSN config | File path (relative + absolute) | Connection string with sslmode |
