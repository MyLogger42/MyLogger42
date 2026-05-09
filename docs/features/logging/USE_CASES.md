# Use Cases

## UC-01: Client Sends a Log Event

**Actor:** Any HTTP client (Android app, Node.js service, Python script, curl, etc.)

**Preconditions:**
- Client has a valid `INGEST_API_KEY`
- MyLogger42 server is running and reachable

**Main Flow:**
1. Client constructs a JSON payload with `service`, `level`, `message`, and optional `timestamp` + `metadata`
2. Client sends `POST /api/v1/logs` with `X-API-Key` header
3. Server validates the API key
4. Server validates the payload (required fields, valid log level)
5. Server assigns a UUID and `receivedAt` timestamp
6. Server persists the entry to storage
7. Server publishes the entry to the WebSocket hub
8. Server returns `201 Created` with the assigned `id`

**Alternative Flows:**
- *3a. Invalid API key:* server returns `401 Unauthorized`. Client should check key configuration.
- *4a. Missing required field (`service`, `level`, or `message`):* server returns `400 Bad Request` with field-level error detail.
- *4b. Unknown log level value:* server returns `400 Bad Request` — accepted values listed in response body.
- *6a. Storage write fails:* server returns `500 Internal Server Error`; event is not published to hub.

**Postconditions:**
- LogEntry exists in storage with assigned `id` and `receivedAt`
- All connected live tail clients with matching filters receive the entry

---

## UC-02: Developer Queries Logs in Web UI

**Actor:** Developer (browser)

**Preconditions:**
- MyLogger42 server is running
- At least some log entries exist in storage

**Main Flow:**
1. Developer opens the Logs page in the browser
2. UI fetches `GET /api/v1/sources` to populate the service filter dropdown
3. Developer optionally applies filters: service, level(s), time range, free-text search
4. UI sends `GET /api/v1/logs` with filter query params
5. Server queries storage and returns a paginated list of `LogEntry` objects
6. UI renders the list; developer clicks an entry for the detail view
7. UI fetches `GET /api/v1/logs/:id` for the full entry including metadata

**Alternative Flows:**
- *4a. No entries match filters:* server returns `200 OK` with empty `items` array and `total: 0`.
- *4b. Invalid time range (from > to):* server returns `400 Bad Request`.
- *7a. Entry not found (deleted by retention):* server returns `404 Not Found`; UI shows "entry no longer available".

**Postconditions:**
- Developer can see the log entries matching their query

---

## UC-03: Developer Live-Tails Logs

**Actor:** Developer (browser)

**Preconditions:**
- MyLogger42 server is running
- Developer has the Live Tail page open

**Main Flow:**
1. Developer opens the Live Tail page
2. UI opens a WebSocket connection to `GET /api/v1/logs/stream`
3. UI optionally sends a filter message (service, level) over the WebSocket
4. As new log events are ingested by any client, the server pushes matching entries to the UI
5. Developer sees entries appear in real-time, newest at the top (or bottom — configurable)
6. Developer changes the filter; UI sends a new filter message; hub updates the client's filter
7. Developer navigates away; browser closes the WebSocket; hub deregisters the client

**Alternative Flows:**
- *2a. WebSocket upgrade fails (server unavailable):* UI shows a connection error banner and retries with exponential backoff.
- *3a. No filter set:* all incoming entries are pushed regardless of service or level.
- *4a. No events arrive for >30s:* server sends a WebSocket ping frame; client responds with pong.

**Postconditions:**
- Developer sees real-time log events during an active session

---

## UC-04: Retention Worker Purges Old Entries

**Actor:** System (automated background process)

**Preconditions:**
- `RETENTION_DAYS` env var is set to a positive integer
- Server has been running for at least `RETENTION_DAYS` days (or entries are older than the TTL)

**Main Flow:**
1. Worker goroutine starts at server boot
2. Worker waits for the configured tick interval (every 1 hour)
3. Worker calculates the cutoff: `now - RETENTION_DAYS`
4. Worker calls `store.DeleteWhere(receivedAt < cutoff)`
5. Storage deletes matching entries and returns the count deleted
6. Worker logs the count at Info level
7. Worker sleeps until the next tick

**Alternative Flows:**
- *4a. Storage delete fails:* worker logs the error at Error level and retries on the next tick.
- *`RETENTION_DAYS=0`:* retention is disabled; worker skips the delete step.

**Postconditions:**
- All log entries with `receivedAt` older than the TTL are removed from storage
