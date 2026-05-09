# API Contracts

Base path: `/api/v1`

Auth: Ingest endpoints require `X-API-Key: <INGEST_API_KEY>` header. Read endpoints are open.

---

## POST /api/v1/logs

Ingest a single log entry.

**Auth:** Required (`X-API-Key`)

**Request:**
```
POST /api/v1/logs
Content-Type: application/json
X-API-Key: my-secret-key

{
  "service": "api-server",
  "level": "Error",
  "message": "Database connection failed",
  "timestamp": "2026-05-09T14:00:00.000Z",
  "metadata": { "db": "postgres", "attemptNumber": 1 }
}
```

**Response 201 Created:**
```json
{ "id": "01934b2c-7a1d-7f3e-a8b1-2c4e6f890123" }
```

**Error Responses:**
| Status | Condition |
|--------|-----------|
| 400 | Missing required field or unknown log level |
| 401 | Missing or invalid `X-API-Key` |
| 500 | Storage write failure |

---

## POST /api/v1/logs/batch

Ingest up to 500 log entries in a single request.

**Auth:** Required (`X-API-Key`)

**Request:**
```
POST /api/v1/logs/batch
Content-Type: application/json
X-API-Key: my-secret-key

[
  { "service": "worker", "level": "Info", "message": "Job started" },
  { "service": "worker", "level": "Info", "message": "Job completed" }
]
```

**Response 201 Created** (all entries valid and written):
```json
{ "accepted": 2 }
```

**Response 400 Bad Request** (any entry invalid — nothing written):
```json
{
  "errors": [
    { "index": 1, "error": "level 'Trace' is not a valid log level" },
    { "index": 3, "error": "field 'message' is required" }
  ]
}
```

Behaviour is atomic: server validates all entries before writing any. If any entry fails, the entire batch is rejected and zero entries are persisted. Fix the entries at the reported indices and resubmit the full batch.

---

## GET /api/v1/logs

Query log entries with optional filters and pagination.

**Auth:** None

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `service` | string | Filter by service name (exact match) |
| `level` | string | Filter by level (exact match, e.g. `Error`) |
| `from` | RFC3339 | Entries with `timestamp >= from` |
| `to` | RFC3339 | Entries with `timestamp <= to` |
| `search` | string | Full-text search on `message` field |
| `limit` | int | Max entries to return (default: 50, max: 500) |
| `offset` | int | Pagination offset (default: 0) |
| `sort` | string | `asc` or `desc` by `timestamp` (default: `desc`) |

**Example Request:**
```
GET /api/v1/logs?service=api-server&level=Error&from=2026-05-09T00:00:00Z&limit=20
```

**Response 200 OK:**
```json
{
  "items": [ /* array of LogEntry */ ],
  "total": 84,
  "limit": 20,
  "offset": 0
}
```

**Error Responses:**
| Status | Condition |
|--------|-----------|
| 400 | Invalid query param (e.g. `from` > `to`, unknown level) |

---

## GET /api/v1/logs/:id

Fetch a single log entry by ID.

**Auth:** None

**Example Request:**
```
GET /api/v1/logs/01934b2c-7a1d-7f3e-a8b1-2c4e6f890123
```

**Response 200 OK:** Full `LogEntry` object (see Data Contracts)

**Error Responses:**
| Status | Condition |
|--------|-----------|
| 404 | Entry not found (never existed or purged by retention) |

---

## GET /api/v1/sources

List all services that have sent at least one log entry.

**Auth:** None

**Example Request:**
```
GET /api/v1/sources
```

**Response 200 OK:**
```json
{
  "sources": [
    {
      "name": "api-server",
      "firstSeenAt": "2026-04-01T08:00:00.000Z",
      "lastSeenAt": "2026-05-09T14:32:01.456Z",
      "totalEntries": 12450
    }
  ]
}
```

---

## GET /api/v1/logs/stream (WebSocket)

Open a live tail WebSocket connection.

**Auth:** None

**Upgrade:**
```
GET /api/v1/logs/stream HTTP/1.1
Upgrade: websocket
Connection: Upgrade
Sec-WebSocket-Key: ...
```

**After connect — client sends filter (optional):**
```json
{
  "services": ["api-server"],
  "levels": ["Error", "Fatal"]
}
```
Empty arrays = receive all. Filter can be re-sent at any time to update.

**Server pushes matching entries as they arrive:**
```json
{
  "id": "...",
  "service": "api-server",
  "level": "Error",
  "message": "...",
  "timestamp": "...",
  "receivedAt": "...",
  "metadata": {}
}
```

**Keep-alive:** Server sends a WebSocket ping every 30 seconds. Client must respond with pong or the connection is closed.

---

## GET /api/v1/health

Health check endpoint for reverse proxy / Docker health check.

**Auth:** None

**Response 200 OK:**
```json
{ "status": "ok", "storage": "sqlite", "uptime": "4h32m" }
```

**Response 503 Service Unavailable:** Storage is unreachable.
