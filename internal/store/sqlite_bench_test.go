package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mylogger42/mylogger42/internal/domain"
)

// Benchmarks backing the Phase 0 throughput work. The point of these is
// the before/after delta, not the absolute number — run them on the same
// machine, same power state, before and after a change:
//
//	go test -bench=. -benchmem -run='^$' ./internal/store
//
// BenchmarkInsertParallel is the headline: it is the one that the single
// shared connection (SetMaxOpenConns(1)) throttles, and the one a read
// pool is meant to unblock.

const benchSeedRows = 200_000

var benchSeq atomic.Int64

// benchStore opens a fresh SQLite store in a temp dir.
func benchStore(b *testing.B) *sqliteStore {
	b.Helper()
	dsn := filepath.Join(b.TempDir(), "bench.db")
	s, err := OpenSQLite(context.Background(), dsn)
	if err != nil {
		b.Fatalf("OpenSQLite: %v", err)
	}
	b.Cleanup(func() { _ = s.Close() })
	return s.(*sqliteStore)
}

// benchEntry builds a unique entry. Messages vary so search benchmarks
// aren't measuring a degenerate all-rows-match scan.
func benchEntry(projectID string) domain.LogEntry {
	n := benchSeq.Add(1)
	svc := benchServices[n%int64(len(benchServices))]
	msg := benchMessages[n%int64(len(benchMessages))]
	ts := time.Unix(1767000000+n, 0).UTC()
	return domain.LogEntry{
		ID:         fmt.Sprintf("bench_%d", n),
		ProjectID:  projectID,
		Service:    svc,
		Level:      benchLevels[n%int64(len(benchLevels))],
		Message:    fmt.Sprintf("%s seq=%d", msg, n),
		Timestamp:  ts,
		ReceivedAt: ts,
		Metadata:   map[string]any{"seq": n, "host": "web-1"},
	}
}

var (
	benchServices = []string{"api", "worker", "scheduler", "web", "auth"}
	benchLevels   = []domain.LogLevel{domain.LevelInfo, domain.LevelError, domain.LevelWarning, domain.LevelDebug}
	benchMessages = []string{
		"request served in 12ms",
		"db timeout on pool acquire",
		"queue drained cleanly",
		"heartbeat ok",
		"cache miss for user session",
	}
)

// seedBench fills a store with rows for the query benchmarks.
func seedBench(b *testing.B, s *sqliteStore, projectID string, rows int) {
	b.Helper()
	ctx := context.Background()
	const batch = 500
	buf := make([]domain.LogEntry, 0, batch)
	for i := 0; i < rows; i++ {
		buf = append(buf, benchEntry(projectID))
		if len(buf) == batch {
			if err := s.InsertBatch(ctx, buf); err != nil {
				b.Fatalf("seed: %v", err)
			}
			buf = buf[:0]
		}
	}
	if len(buf) > 0 {
		if err := s.InsertBatch(ctx, buf); err != nil {
			b.Fatalf("seed: %v", err)
		}
	}
}

func BenchmarkInsert(b *testing.B) {
	ctx := context.Background()
	s := benchStore(b)
	pid := "bench_proj"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Insert(ctx, benchEntry(pid)); err != nil {
			b.Fatalf("Insert: %v", err)
		}
	}
}

// BenchmarkInsertParallel simulates concurrent HTTP ingest workers. This
// is the benchmark SetMaxOpenConns(1) serialises.
func BenchmarkInsertParallel(b *testing.B) {
	ctx := context.Background()
	s := benchStore(b)
	pid := "bench_proj"

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := s.Insert(ctx, benchEntry(pid)); err != nil {
				b.Fatalf("Insert: %v", err)
			}
		}
	})
}

// BenchmarkInsertBatch measures the /logs/batch path at its API cap of
// 500 entries per call. ns/op is per batch, so divide by 500 for per-log.
func BenchmarkInsertBatch(b *testing.B) {
	ctx := context.Background()
	s := benchStore(b)
	pid := "bench_proj"

	batches := make([][]domain.LogEntry, b.N)
	for i := range batches {
		batch := make([]domain.LogEntry, maxBenchBatch)
		for j := range batch {
			batch[j] = benchEntry(pid)
		}
		batches[i] = batch
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.InsertBatch(ctx, batches[i]); err != nil {
			b.Fatalf("InsertBatch: %v", err)
		}
	}
}

const maxBenchBatch = 500

// benchQueryStore seeds once and hands the same store to every iteration.
func benchQueryStore(b *testing.B) (*sqliteStore, string) {
	b.Helper()
	s := benchStore(b)
	pid := "bench_proj"
	seedBench(b, s, pid, benchSeedRows)
	return s, pid
}

// BenchmarkQuerySearch is the unindexed leading-wildcard LIKE scan.
func BenchmarkQuerySearch(b *testing.B) {
	ctx := context.Background()
	s, pid := benchQueryStore(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := s.Query(ctx, pid, domain.QueryFilter{Search: "timeout", Limit: 50}); err != nil {
			b.Fatalf("Query: %v", err)
		}
	}
}

// BenchmarkQueryPage is the common dashboard read: first page, no search.
func BenchmarkQueryPage(b *testing.B) {
	ctx := context.Background()
	s, pid := benchQueryStore(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := s.Query(ctx, pid, domain.QueryFilter{Limit: 50}); err != nil {
			b.Fatalf("Query: %v", err)
		}
	}
}

func BenchmarkSources(b *testing.B) {
	ctx := context.Background()
	s, pid := benchQueryStore(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Sources(ctx, pid); err != nil {
			b.Fatalf("Sources: %v", err)
		}
	}
}

// BenchmarkIngestUnderSearchLoad measures insert throughput while a
// searcher hammers the same database. With one shared connection the
// scan blocks ingest outright; with a read pool the two should barely
// interact. This is the benchmark that justifies Phase 0.2.
func BenchmarkIngestUnderSearchLoad(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := benchStore(b)
	pid := "bench_proj"
	seedBench(b, s, pid, benchSeedRows)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			// Errors are expected once ctx is cancelled at the end.
			_, _, _ = s.Query(ctx, pid, domain.QueryFilter{Search: "timeout", Limit: 50})
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Insert(ctx, benchEntry(pid)); err != nil {
			b.Fatalf("Insert: %v", err)
		}
	}
	b.StopTimer()
	cancel()
	<-done
}

// BenchmarkRetentionSweep measures a purge of roughly half the dataset.
// Each iteration reseeds, so ns/op includes seeding — compare only
// against other runs of this same benchmark.
func BenchmarkRetentionSweep(b *testing.B) {
	ctx := context.Background()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		s := benchStore(b)
		pid := "bench_proj"
		seedBench(b, s, pid, benchSeedRows/4)
		// benchEntry timestamps advance one second per row, so the
		// midpoint of the seeded range is a ~50% cutoff.
		var cutoff time.Time
		items, _, err := s.Query(ctx, pid, domain.QueryFilter{Limit: 1, Sort: domain.SortAsc})
		if err != nil || len(items) == 0 {
			b.Fatalf("probe oldest: %v", err)
		}
		cutoff = items[0].ReceivedAt.Add(time.Duration(benchSeedRows/8) * time.Second)
		b.StartTimer()

		if _, err := s.DeleteOlderThanForProject(ctx, pid, cutoff); err != nil {
			b.Fatalf("DeleteOlderThanForProject: %v", err)
		}
	}
}
