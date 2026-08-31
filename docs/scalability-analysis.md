# MyLogger42: Performance, Bottlenecks & Scalability

This document records what actually limited MyLogger42's throughput, what
was changed, and what the numbers were before and after. Every figure
below is a measurement from `internal/store/sqlite_bench_test.go` on this
codebase, not an estimate.

Reproduce with:

```bash
go test -bench=. -benchmem -run='^$' ./internal/store
```

---

## 1. Executive Summary

The original version of this document argued that SQLite was
architecturally unsuited to log workloads and that a purpose-built engine
was required to get past ~150–400 logs/sec. Benchmarking showed that
diagnosis was wrong in an instructive way.

- **The ceiling was configuration, not the engine.** `sqlite.go` opened a
  single connection (`SetMaxOpenConns(1)`) shared by readers and writers.
  One dashboard search therefore blocked all ingest. Measured: **41.4 ms
  per insert** while a search ran concurrently, versus **69 µs** idle — a
  603× collapse to roughly 24 logs/sec. That, not B-tree write
  amplification, is where "150–400 logs/sec" came from.
- **Splitting reads onto their own pool removed it.** WAL already gave
  readers a non-blocking consistent snapshot; nothing but the connection
  cap was preventing it. Ingest under concurrent search went to **82 µs**
  (~12,200 logs/sec), a **506× improvement**, with no new dependency.
- **The Postgres backend was never a real alternative.** Its
  `ProjectStore`, `KeyStore` and `UserStore` returned `errNotImplemented`
  for every method, so `DB_DRIVER=postgres` could not serve projects,
  keys, or login. It had no tests and no CI coverage. It has been removed
  (440 lines) rather than compared against.
- **VictoriaLogs replaces it as the scale-out option**, in a hybrid
  arrangement: logs to VictoriaLogs, metadata still in SQLite. This is
  now a deliberate trade rather than a rescue — SQLite handles far more
  than previously believed.

---

## 2. Measured Results

200,000 rows seeded for the query benchmarks. Apple M3 Max, 14 cores,
`modernc.org/sqlite v1.34.1`, darwin/arm64.

| Benchmark | Before | After | Change |
| :--- | ---: | ---: | ---: |
| `IngestUnderSearchLoad` | 41,414,062 ns | **81,778 ns** | **506× faster** |
| `QuerySearch` (`LIKE '%x%'`, 200k rows) | 81,342,331 ns | 50,583,400 ns | 1.6× faster |
| `Sources` (`GROUP BY service`) | 154,866,367 ns | 118,670,827 ns | 1.3× faster |
| `InsertBatch` (500 entries) | 5,964,066 ns | 4,797,709 ns | 1.24× faster |
| `Insert` (single) | 68,665 ns | 63,303 ns | 1.08× faster |
| `InsertParallel` | 69,072 ns | 59,692 ns | 1.16× faster |
| `QueryPage` (first page) | 10,896,771 ns | 10,614,290 ns | ~unchanged |

Reading these:

- **`IngestUnderSearchLoad` is the headline.** It is the only benchmark
  that models what a real deployment does — write logs while somebody has
  the dashboard open. It was the actual production ceiling.
- **`InsertParallel` matching `Insert` before the change** is the direct
  evidence: on a 14-core machine, concurrent inserts were exactly as fast
  as serial ones, because they were serial.
- **Write throughput barely moved, and that is correct.** SQLite permits
  one writer; the write path was never the problem. The modest gain comes
  from the index diet below.
- **`QueryPage` is unchanged and still ~10 ms.** It is dominated by the
  exact `COUNT(*)` the UI needs for "Page X of Y". At 200k rows that is
  acceptable; it grows linearly, and is the next thing to address if
  datasets get much larger.

---

## 3. What Was Changed

### 3.1 Read connection pool

`sqliteStore` now holds two handles: a one-connection write handle, and a
four-connection read pool carrying `query_only`. Reads (`Query`, `Get`,
`Sources`) use the pool; writes keep the single connection, because
SQLite allows exactly one writer and a larger pool would only manufacture
`SQLITE_BUSY`.

In-memory DSNs skip the pool — a second handle to `:memory:` opens a
different, empty database.

### 3.2 Index diet

Every insert previously wrote four index entries. `idx_service`,
`idx_level` and `idx_received_at` were unusable, because every query path
leads with `project_id` and the composite index already serves them.
`003_perf.sql` drops those three and adds
`(project_id, received_at)` — which the retention sweep needs and which
did not previously exist. Two index writes per insert instead of four.

### 3.3 Chunked retention deletes

Retention issued one unbounded `DELETE` per project, holding the single
write connection for the whole purge. It now deletes in 5,000-row chunks,
returning the connection between statements and honouring context
cancellation.

### 3.4 Ingest auth cache

Each ingested log cost two queries (resolve key by prefix, resolve its
project). Both are now cached in-process for 60 s. Three properties the
cache deliberately keeps:

- **Misses are cached too.** A hit-only cache makes a known prefix
  measurably faster than an unknown one, which is a prefix-existence
  timing oracle — exactly what `auth.FakeVerifyForTimingParity` exists to
  prevent.
- **It caches facts, not verdicts.** The HMAC compare and the
  archived-project check still run on every request.
- **Correctness does not depend on the TTL.** Admin mutations invalidate
  explicitly, so a revoked key is rejected on the very next request. The
  TTL only bounds staleness from changes made by another process, such
  as the `admin` CLI.

---

## 4. Storage Engine Comparison

| Characteristic | SQLite (default) | VictoriaLogs (optional) |
| :--- | :--- | :--- |
| Architecture | In-process, single file | Log-native time-series engine, separate container |
| Write model | B-tree, WAL | Append-only compressed block segments |
| Disk compression | None | High |
| Text search | `LIKE '%x%'`, full scan — 50 ms at 200k rows, grows linearly | Indexed phrase match |
| Retention | Per-project, chunked `DELETE` | **Global only** — no delete API |
| Containers | 1 | 2 (`app` + `vlogs`) |
| Operational cost | None | A second service to run and monitor |

Compression and search figures for VictoriaLogs are its documented
characteristics; this project has not benchmarked them at scale. The
behavioural differences below have been verified against a running
instance by the shared conformance suite.

### When to switch

Stay on SQLite unless you have a measured reason to move. It is the
single-binary, zero-dependency mode and it is considerably faster than
this document previously claimed. Switch when search latency on your
actual dataset becomes unacceptable, or disk footprint matters.

### Verified behavioural differences

Both backends run the same conformance suite
(`internal/store/logstore_conformance_test.go`), which encodes exactly
two permitted divergences:

1. **Search semantics.** SQLite does substring matching; VictoriaLogs
   does case-insensitive phrase matching, so `imeou` no longer finds
   `timeout`. This is what makes search hit an index instead of scanning.
2. **Per-project retention.** VictoriaLogs has no delete API and a single
   global `-retentionPeriod`. `retentionDays` is stored but not enforced;
   `/api/v1/health` reports `capabilities.perProjectRetention: false` and
   the UI flags the field.

And one hazard that is not a divergence so much as a trap:

3. **VictoriaLogs silently discards entries older than its retention
   window**, while still answering the ingest request `200`. There is no
   per-request rejection count to inspect, so MyLogger42 returns `201` to
   the client and the log is gone. Size `RETENTION_DAYS` to cover the
   oldest timestamp you ingest.

---

## 5. What's Next

In rough order of value, none of it urgent:

1. **`COUNT(*)` on every page.** ~10 ms at 200k rows and linear. Options:
   cache the count per filter for a paging session, or switch the UI to a
   `hasMore` boolean.
2. **`Sources()` full `GROUP BY`.** ~119 ms at 200k rows, run on every
   dashboard load. A periodically refreshed summary table would fix it.
3. **FTS5 for SQLite search.** Confirmed available in the pinned pure-Go
   driver (`-DSQLITE_ENABLE_FTS5`, all target platforms). Deferred
   deliberately: the read pool removed the pain that made search urgent.
   It would also converge SQLite's search semantics with VictoriaLogs'.
4. **Live-tail fan-out.** `Hub.Publish` still drops frames on a saturated
   1,024-entry buffer. Not yet measured under load.
