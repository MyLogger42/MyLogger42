---
name: modernc.org/sqlite — DATETIME aggregates lose type
description: When using modernc.org/sqlite, MIN/MAX/aggregate over DATETIME columns must be scanned as string and parsed manually, not as time.Time
type: feedback
---

When scanning aggregate values (MIN, MAX, etc.) of a `DATETIME` column from `modernc.org/sqlite`, do NOT scan into `*time.Time` — it fails with "unsupported Scan, storing driver.Value type string into type *time.Time". Scan into `string` and parse manually.

The driver writes `time.Time` parameters via `time.Time.String()`, so aggregates come back in the format `"2006-01-02 15:04:05.999999999 -0700 MST"` (with `MST`-style zone abbreviation). RFC3339 layouts will NOT match. The helper `parseSQLiteTime` in `internal/store/sqlite.go` accepts that layout plus several fallbacks.

**Why:** Direct column reads of `time.Time` work because the driver knows the column type from schema metadata. Aggregates lose that type binding — the column-type info doesn't propagate through `MIN()`/`MAX()`.

**How to apply:** Any new query in `internal/store/sqlite.go` that uses a SQL aggregate on a DATETIME column (timestamp, received_at) must scan that aggregate into `string` and route through `parseSQLiteTime`. The Postgres backend (`pgx`) is unaffected — it preserves types through aggregates.
