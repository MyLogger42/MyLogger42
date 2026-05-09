# ADR-006: Atomic Batch Ingest (All-or-Nothing)

## Context

The `POST /api/v1/logs/batch` endpoint accepts multiple log entries in a single request. A clear failure contract is needed: when some entries in the batch are invalid (missing fields, unknown level), should the server write the valid ones and reject the rest, or treat the batch as a unit?

## Options Considered

| Option | Behaviour on partial failure | Client retry complexity |
|--------|------------------------------|------------------------|
| **Atomic (all-or-nothing)** | Validate all first; if any entry is invalid, reject the entire batch with `400 Bad Request` and per-entry error detail. Nothing is written. | Low — retry the whole batch after fixing errors. |
| Best-effort (207 Multi-Status) | Accept valid entries, persist them, return per-index errors for invalid ones. | High — client must track which indices succeeded and which failed; partial retries risk duplicates. |

## Decision

**Atomic.** Validate all entries before writing any. If any entry fails validation, return `400 Bad Request` with an array of per-entry errors (index + reason). No entries are written.

## Consequences

- `POST /api/v1/logs/batch` returns only `201 Created` (all valid, all written) or `400 Bad Request` (at least one invalid, none written)
- Server runs a validation pass over the full array before opening a storage transaction
- Storage write is a single transaction — either all rows commit or none do
- Client retry logic is straightforward: fix the invalid entry at the reported index and resubmit the full batch
- No `207 Multi-Status` response ever used on this endpoint
