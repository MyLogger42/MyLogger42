# Data Contracts

## LogEntry (canonical shape)

Full representation of a stored log entry, as returned by the query API.

```json
{
  "id": "01934b2c-7a1d-7f3e-a8b1-2c4e6f890123",
  "service": "api-server",
  "level": "Error",
  "message": "Failed to connect to payment gateway: timeout after 5s",
  "timestamp": "2026-05-09T14:32:01.123Z",
  "receivedAt": "2026-05-09T14:32:01.456Z",
  "metadata": {
    "requestId": "req-abc123",
    "userId": "usr-999",
    "gateway": "stripe",
    "attemptNumber": 3
  }
}
```

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `id` | UUID string | server-assigned | v4 UUID generated on receipt |
| `service` | string | yes | Non-empty; identifies the emitting service |
| `level` | string | yes | One of: `Verbose`, `Debug`, `Info`, `Event`, `Warning`, `Error`, `Fatal` |
| `message` | string | yes | Human-readable event description; non-empty |
| `timestamp` | RFC3339 string | client-optional | Client-supplied event time; defaults to `receivedAt` if absent |
| `receivedAt` | RFC3339 string | server-assigned | Server receipt time; used for retention and ordering |
| `metadata` | JSON object | no | Arbitrary key-value pairs; defaults to `{}` |

---

## Ingest Request Payload

Body of `POST /api/v1/logs`. Fields marked optional may be omitted.

```json
{
  "service": "android-app",
  "level": "Info",
  "message": "User completed onboarding flow",
  "timestamp": "2026-05-09T14:30:00.000Z",
  "metadata": {
    "userId": "usr-42",
    "screen": "OnboardingComplete",
    "durationMs": 4200
  }
}
```

Minimal valid payload (no timestamp, no metadata):

```json
{
  "service": "worker",
  "level": "Warning",
  "message": "Queue depth exceeded threshold"
}
```

---

## Batch Ingest Request Payload

`POST /api/v1/logs/batch` — ingest multiple entries in a single request.

```json
[
  {
    "service": "api-server",
    "level": "Debug",
    "message": "Request received",
    "metadata": { "path": "/health" }
  },
  {
    "service": "api-server",
    "level": "Info",
    "message": "Health check passed"
  }
]
```

Max 500 entries per batch. Behaviour is atomic: the server validates all entries before writing any. On any validation failure the entire batch is rejected with `400 Bad Request` — no entries are written. On success `201 Created` is returned.

---

## Query Response Shape

Response to `GET /api/v1/logs`.

```json
{
  "items": [
    {
      "id": "01934b2c-7a1d-7f3e-a8b1-2c4e6f890123",
      "service": "api-server",
      "level": "Error",
      "message": "Failed to connect to payment gateway",
      "timestamp": "2026-05-09T14:32:01.123Z",
      "receivedAt": "2026-05-09T14:32:01.456Z",
      "metadata": { "requestId": "req-abc123" }
    }
  ],
  "total": 1842,
  "limit": 50,
  "offset": 0
}
```

---

## Sources Response Shape

Response to `GET /api/v1/sources`.

```json
{
  "sources": [
    {
      "name": "api-server",
      "firstSeenAt": "2026-04-01T08:00:00.000Z",
      "lastSeenAt": "2026-05-09T14:32:01.456Z",
      "totalEntries": 12450
    },
    {
      "name": "android-app",
      "firstSeenAt": "2026-04-15T12:00:00.000Z",
      "lastSeenAt": "2026-05-09T13:00:00.000Z",
      "totalEntries": 340
    }
  ]
}
```

---

## WebSocket Messages

### Client → Server: Filter

Sent after connection to narrow the live tail stream. Send an empty filter to receive all events.

```json
{
  "services": ["api-server", "worker"],
  "levels": ["Warning", "Error", "Fatal"]
}
```

### Server → Client: Log Event

Same shape as a single `LogEntry` (see above). Pushed whenever a new entry matches the client's active filter.

```json
{
  "id": "01934b2c-7a1d-7f3e-a8b1-2c4e6f890123",
  "service": "api-server",
  "level": "Error",
  "message": "Failed to connect to payment gateway",
  "timestamp": "2026-05-09T14:32:01.123Z",
  "receivedAt": "2026-05-09T14:32:01.456Z",
  "metadata": {}
}
```
