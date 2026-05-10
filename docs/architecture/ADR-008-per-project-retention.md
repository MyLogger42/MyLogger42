# ADR-008: Per-project retention with global default

> **Status:** Accepted (v2).

## Context

v1's retention worker was global: `RETENTION_DAYS=N` deleted any row
older than N days, full stop. With per-project isolation in v2, an
operator running multiple projects on one server may want different
windows per project — short-lived dev projects (3 days) alongside
production audit-style logs (90 days).

## Options Considered

| Option | Pros | Cons |
|---|---|---|
| Stay global | Simple; current code | Either too aggressive for "important" projects or too lax for noisy ones |
| **Per-project override + global default** | Precise control; minimal config when defaults are good enough | Worker now iterates projects; one more column on `projects` |
| Per-project required, no global | Forces explicit decisions | Friction at project creation for the common case |
| Per-table partitioning | Scales to billions of rows | Out of scope for SQLite; v2 is solo-dev scale |

## Decision

**Per-project override stored on the project, with global
`RETENTION_DAYS` env as the inheritance default.**

`projects.retention_days` is `INTEGER NULL`:

- `NULL` → inherit `RETENTION_DAYS` from env (the v1 default carries
  forward unchanged for projects that don't customize).
- `0` → retain forever (per-project opt-out from any sweep).
- `N > 0` → keep entries with `received_at >= now - N days`.

Same semantics for the global `RETENTION_DAYS=0` — it means "no global
sweep," but per-project overrides still apply.

The worker:

1. Lists all projects (including archived — disk doesn't stop being
   consumed when ingest stops, and operators can pre-emptively set
   `retention_days = 0` before archiving if they want indefinite
   read access to historical entries).
2. For each, computes `effectiveRetention(p) = p.RetentionDays ?? globalDefault`
   and skips when `<= 0`.
3. Calls `LogStore.DeleteOlderThanForProject(p.ID, cutoff)`.
4. A failure on one project is logged and the worker continues to
   the next project (failure isolation).

Tick interval is 1 hour (unchanged from v1).

## Consequences

- LogStore's old `DeleteOlderThan(cutoff)` is removed and replaced by
  `DeleteOlderThanForProject(projectID, cutoff)`. The interface change
  forces every caller to think about scope.
- The v2 spec calls out the gotcha: archived projects are still swept;
  to retain forever after archive, set `retention_days = 0` BEFORE
  archiving.
- Migration 002 adds `retention_days INTEGER` to `projects`. NULL is
  the natural "inherit" sentinel — no special-case value needed.
- Future scaling to billions of rows would want partitioning by
  `(project_id, date)`, but the current composite index
  `(project_id, timestamp DESC)` is enough for v2's target scale.
