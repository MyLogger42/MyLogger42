package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mylogger42/mylogger42/internal/domain"
)

// The retention delete loops in retentionChunk-sized batches. The shared
// conformance suite only ever deletes a couple of rows, so it exercises a
// single pass — it would not notice a loop that double-counted rows,
// stopped after the first chunk, or spun forever. This test crosses the
// chunk boundary several times.
func TestSQLiteDeleteOlderThanChunking(t *testing.T) {
	ctx := context.Background()
	stores := openTestStores(t)
	logs := stores.Logs

	const (
		oldRows = retentionChunk*2 + 137 // deliberately not a chunk multiple
		newRows = 500
	)
	base := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Second)
	cutoff := base.Add(24 * time.Hour)

	seedRetention(t, logs, "proj_keep_me", base, cutoff, oldRows, newRows)
	// A second project must be untouched by the sweep.
	seedRetention(t, logs, "proj_other", base, cutoff, 10, 10)

	deleted, err := logs.DeleteOlderThanForProject(ctx, "proj_keep_me", cutoff)
	if err != nil {
		t.Fatalf("DeleteOlderThanForProject: %v", err)
	}
	if deleted != oldRows {
		t.Errorf("deleted = %d, want %d (chunk loop miscounted or exited early)", deleted, oldRows)
	}

	_, remaining, err := logs.Query(ctx, "proj_keep_me", domain.QueryFilter{Limit: 1})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if remaining != newRows {
		t.Errorf("%d rows left, want %d", remaining, newRows)
	}

	// Idempotent: nothing left older than the cutoff.
	again, err := logs.DeleteOlderThanForProject(ctx, "proj_keep_me", cutoff)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if again != 0 {
		t.Errorf("second sweep deleted = %d, want 0", again)
	}

	// The other project keeps everything, old rows included.
	_, otherTotal, err := logs.Query(ctx, "proj_other", domain.QueryFilter{Limit: 1})
	if err != nil {
		t.Fatalf("Query other: %v", err)
	}
	if otherTotal != 20 {
		t.Errorf("other project has %d rows, want 20 — sweep crossed the project boundary", otherTotal)
	}
}

// seedRetention writes `old` entries before cutoff and `fresh` entries
// after it, for one project.
func seedRetention(t *testing.T, logs LogStore, projectID string, base, cutoff time.Time, old, fresh int) {
	t.Helper()
	ctx := context.Background()

	batch := make([]domain.LogEntry, 0, 500)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := logs.InsertBatch(ctx, batch); err != nil {
			t.Fatalf("seed: %v", err)
		}
		batch = batch[:0]
	}
	add := func(id string, at time.Time) {
		batch = append(batch, domain.LogEntry{
			ID: id, ProjectID: projectID, Service: "api", Level: domain.LevelInfo,
			Message: "seed", Timestamp: at, ReceivedAt: at, Metadata: map[string]any{},
		})
		if len(batch) == cap(batch) {
			flush()
		}
	}

	for i := 0; i < old; i++ {
		add(fmt.Sprintf("%s_old_%d", projectID, i), base.Add(time.Duration(i)*time.Millisecond))
	}
	for i := 0; i < fresh; i++ {
		add(fmt.Sprintf("%s_new_%d", projectID, i), cutoff.Add(time.Duration(i+1)*time.Second))
	}
	flush()
}
