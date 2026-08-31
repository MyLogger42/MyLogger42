//go:build vlogs

// Runs the shared LogStore conformance suite against a real VictoriaLogs
// server, so the two backends are held to one contract instead of one
// contract each.
//
// Requires a running server:
//
//	docker compose --profile victorialogs up -d vlogs
//	VLOGS_TEST_URL=http://localhost:9428 go test -tags=vlogs ./internal/store
//
// It is behind a build tag because it needs that server; the unit tests
// in victorialogs_test.go cover the query building and parsing with no
// network at all.

package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mylogger42/mylogger42/internal/domain"
)

// visibilityTimeout bounds how long we wait for an ingested entry to
// become queryable. VictoriaLogs buffers in memory and flushes on an
// interval, so writes are not read-your-writes consistent.
const visibilityTimeout = 15 * time.Second

func TestVictoriaLogsConformance(t *testing.T) {
	baseURL := os.Getenv("VLOGS_TEST_URL")
	if baseURL == "" {
		t.Skip("VLOGS_TEST_URL not set; start VictoriaLogs to run this suite")
	}

	newStore := func(t *testing.T) LogStore {
		s, err := OpenVictoriaLogs(baseURL)
		if err != nil {
			t.Fatalf("OpenVictoriaLogs: %v", err)
		}
		if err := s.Ping(context.Background()); err != nil {
			t.Fatalf("VictoriaLogs unreachable at %s: %v", baseURL, err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return &awaitVisibleStore{LogStore: s, t: t}
	}

	runLogStoreConformance(t, newStore, storeCaps{
		// Phrase matching, not substring — the documented divergence.
		substringSearch: false,
		// No delete API; retention is the server's -retentionPeriod.
		deletes: false,
		// min()/max() over _time in the stats pipe.
		sourceTimestamps: true,
	})
}

// awaitVisibleStore makes writes synchronous from the test's point of
// view by blocking until the last written entry can be read back. Without
// it every assertion races the server's flush interval.
type awaitVisibleStore struct {
	LogStore
	t *testing.T
}

func (a *awaitVisibleStore) Insert(ctx context.Context, e domain.LogEntry) error {
	if err := a.LogStore.Insert(ctx, e); err != nil {
		return err
	}
	a.awaitVisible(ctx, e)
	return nil
}

func (a *awaitVisibleStore) InsertBatch(ctx context.Context, entries []domain.LogEntry) error {
	if err := a.LogStore.InsertBatch(ctx, entries); err != nil {
		return err
	}
	if len(entries) > 0 {
		// Entries in one batch land together, so the last one becoming
		// visible implies the rest are too.
		a.awaitVisible(ctx, entries[len(entries)-1])
	}
	return nil
}

func (a *awaitVisibleStore) awaitVisible(ctx context.Context, e domain.LogEntry) {
	a.t.Helper()
	deadline := time.Now().Add(visibilityTimeout)
	for {
		if _, err := a.LogStore.Get(ctx, e.ProjectID, e.ID); err == nil {
			return
		}
		if time.Now().After(deadline) {
			a.t.Fatalf("entry %s never became queryable within %s", e.ID, visibilityTimeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
