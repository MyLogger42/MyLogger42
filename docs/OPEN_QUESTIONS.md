# Open Questions / Risks

All questions from the initial design phase have been resolved. Decisions are recorded below and in the relevant ADRs.

---

## OQ-01: API Key Management — RESOLVED

**Decision:** Single static `INGEST_API_KEY` in `.env`. Rotation = update key + `docker compose restart`. Acceptable downtime for a solo dev tool.

**Rationale:** Zero additional complexity. Multi-key rotation can be added when (if) team or production use requires it.

---

## OQ-02: Source / Service Registration — RESOLVED

**Decision:** Auto-create on first log event. No pre-registration required. The `sources` list is derived from distinct `service` values seen in stored entries.

**Rationale:** Minimises friction for adding new services. The API key is the trust boundary; any client with the key can log under any service name.

---

## OQ-03: Rate Limiting on Ingest — RESOLVED

**Decision:** No rate limiting in v1. Trust the client.

**Rationale:** Solo dev tool with a single known API key. If a client misbehaves, restart it. Rate limiting can be added via `golang.org/x/time/rate` middleware once real load patterns are observed.

---

## OQ-04: Batch Ingest Atomicity — RESOLVED

**Decision:** Atomic (all-or-nothing). Validate all entries before writing any. If any entry fails validation, the entire batch is rejected with `400 Bad Request` and per-entry error detail. No entries are written on failure.

**Rationale:** Simpler client retry logic — either all entries landed or none did. Clients don't need to track which indices succeeded. See `ADR-006-batch-atomicity.md`.

---

## OQ-05: Web UI Reverse Proxy Configuration — RESOLVED

**Decision:** No example config files in the repo. Setup guidance (Caddy or Nginx + TLS) documented in the project README.

**Rationale:** Keeps the repo clean; most VPS operators have an existing reverse proxy preference. A README section covers the essentials without maintaining example configs that can go stale.

---

## OQ-06: Log Level Ordering in UI — RESOLVED

**Decision:** Multi-select checkboxes. Users pick any combination of levels (e.g. Warning + Fatal only).

**Rationale:** Consistent with the WebSocket live tail filter model which already supports arbitrary level arrays. More precise than a minimum-level dropdown.
