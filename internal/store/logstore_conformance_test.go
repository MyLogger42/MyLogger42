package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mylogger42/mylogger42/internal/domain"
)

// This file is the shared LogStore conformance suite. Every backend must
// pass it, so behaviour differences between SQLite and VictoriaLogs stay
// deliberate and documented rather than accidental.
//
// Backends declare their divergences via storeCaps instead of forking the
// suite — a backend that can't do something says so in one place, and the
// rest of the contract is still enforced.

// storeCaps describes the documented behavioural divergences between
// backends. Anything NOT listed here is contract every backend must meet.
type storeCaps struct {
	// substringSearch is true when Search matches a bare substring
	// (SQLite's LIKE '%x%'). VictoriaLogs uses phrase matching, which
	// only matches on word boundaries.
	substringSearch bool
	// deletes is true when DeleteOlderThanForProject actually removes
	// entries. VictoriaLogs has no delete API — retention there is the
	// server's global -retentionPeriod.
	deletes bool
	// sourceTimestamps is true when Sources populates FirstSeenAt /
	// LastSeenAt. Neither field is rendered by the SPA, so a backend may
	// legitimately return zero values.
	sourceTimestamps bool
}

// projectSeq keeps project IDs unique across subtests. A backend like
// VictoriaLogs can't be truncated between tests, so isolation comes from
// each subtest owning a fresh project ID rather than a fresh database.
var projectSeq atomic.Int64

func nextProjectID(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("proj_%d_%d", time.Now().UnixNano(), projectSeq.Add(1))
}

// baseTime is the anchor for every fixture timestamp. Whole seconds, UTC —
// backends serialise sub-second precision differently and that is not what
// this suite is testing.
//
// It is RELATIVE TO NOW, not a fixed date, and that is load-bearing:
// VictoriaLogs silently drops entries older than its -retentionPeriod
// (default 7d) while still answering the ingest request with HTTP 200.
// A hardcoded date makes every VictoriaLogs assertion fail in a way that
// looks like a query bug. Fixtures span baseTime .. baseTime+1h, so keep
// the offset comfortably inside any sane retention window.
var baseTime = time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)

// mkEntry builds a fixture entry offset minutes from baseTime.
func mkEntry(projectID, id, service string, level domain.LogLevel, msg string, offsetMin int) domain.LogEntry {
	ts := baseTime.Add(time.Duration(offsetMin) * time.Minute)
	return domain.LogEntry{
		ID:         id,
		ProjectID:  projectID,
		Service:    service,
		Level:      level,
		Message:    msg,
		Timestamp:  ts,
		ReceivedAt: ts,
		Metadata:   map[string]any{},
	}
}

// runLogStoreConformance runs the full contract against one backend.
// newStore must hand back a store that is ready to accept writes; it may
// return the same instance every call.
func runLogStoreConformance(t *testing.T, newStore func(t *testing.T) LogStore, caps storeCaps) {
	t.Helper()

	t.Run("InsertAndGetRoundTrip", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		pid := nextProjectID(t)

		want := mkEntry(pid, "log_1", "api", domain.LevelError, "db timeout on pool", 0)
		want.Metadata = map[string]any{"attempt": float64(3), "nested": map[string]any{"host": "web-1"}}

		if err := s.Insert(ctx, want); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		got, err := s.Get(ctx, pid, "log_1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		assertEntryEqual(t, want, *got)
	})

	t.Run("GetMissingReturnsErrNotFound", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		pid := nextProjectID(t)

		if _, err := s.Get(ctx, pid, "nope"); err != ErrNotFound {
			t.Fatalf("Get missing: want ErrNotFound, got %v", err)
		}
	})

	t.Run("GetIsProjectScoped", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		mine, theirs := nextProjectID(t), nextProjectID(t)

		if err := s.Insert(ctx, mkEntry(theirs, "log_theirs", "api", domain.LevelInfo, "secret", 0)); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		// Reading another project's entry by ID must 404, not leak it.
		if _, err := s.Get(ctx, mine, "log_theirs"); err != ErrNotFound {
			t.Fatalf("cross-project Get: want ErrNotFound, got %v", err)
		}
	})

	t.Run("InsertBatchEmptyIsNoop", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		if err := s.InsertBatch(ctx, nil); err != nil {
			t.Fatalf("InsertBatch(nil): %v", err)
		}
	})

	t.Run("QueryEmptyReturnsEmptySliceNotNil", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		pid := nextProjectID(t)

		items, total, err := s.Query(ctx, pid, domain.QueryFilter{Limit: 50})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		// handleQuery has no nil guard — a nil slice serialises as
		// `items: null` and breaks the SPA's items.map().
		if items == nil {
			t.Fatal("Query returned a nil slice; must be an empty non-nil slice")
		}
		if len(items) != 0 || total != 0 {
			t.Fatalf("Query on empty project: got %d items, total %d", len(items), total)
		}
	})

	t.Run("QueryIsProjectScoped", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		mine, theirs := nextProjectID(t), nextProjectID(t)

		seed(t, s, []domain.LogEntry{
			mkEntry(mine, "m1", "api", domain.LevelInfo, "mine", 0),
			mkEntry(theirs, "t1", "api", domain.LevelInfo, "theirs", 1),
			mkEntry(theirs, "t2", "api", domain.LevelInfo, "theirs", 2),
		})

		items, total, err := s.Query(ctx, mine, domain.QueryFilter{Limit: 50})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if total != 1 || len(items) != 1 || items[0].ID != "m1" {
			t.Fatalf("project isolation broken: total=%d items=%+v", total, ids(items))
		}
	})

	t.Run("QueryFilters", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		pid := nextProjectID(t)

		seed(t, s, []domain.LogEntry{
			mkEntry(pid, "e1", "api", domain.LevelInfo, "request served", 0),
			mkEntry(pid, "e2", "api", domain.LevelError, "db timeout on pool", 10),
			mkEntry(pid, "e3", "worker", domain.LevelError, "queue drained", 20),
			mkEntry(pid, "e4", "worker", domain.LevelInfo, "heartbeat", 30),
		})

		from := baseTime.Add(5 * time.Minute)
		to := baseTime.Add(25 * time.Minute)

		cases := []struct {
			name   string
			filter domain.QueryFilter
			want   []string
		}{
			{"Service", domain.QueryFilter{Service: "worker"}, []string{"e4", "e3"}},
			{"Level", domain.QueryFilter{Level: domain.LevelError}, []string{"e3", "e2"}},
			{"ServiceAndLevel", domain.QueryFilter{Service: "api", Level: domain.LevelError}, []string{"e2"}},
			{"From", domain.QueryFilter{From: &from}, []string{"e4", "e3", "e2"}},
			{"To", domain.QueryFilter{To: &to}, []string{"e3", "e2", "e1"}},
			{"FromAndTo", domain.QueryFilter{From: &from, To: &to}, []string{"e3", "e2"}},
			{"Search", domain.QueryFilter{Search: "timeout"}, []string{"e2"}},
			{"SortAsc", domain.QueryFilter{Sort: domain.SortAsc}, []string{"e1", "e2", "e3", "e4"}},
			{"SortDesc", domain.QueryFilter{Sort: domain.SortDesc}, []string{"e4", "e3", "e2", "e1"}},
			{"NoMatch", domain.QueryFilter{Service: "ghost"}, nil},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				f := tc.filter
				f.Limit = 50
				items, total, err := s.Query(ctx, pid, f)
				if err != nil {
					t.Fatalf("Query: %v", err)
				}
				if total != len(tc.want) {
					t.Errorf("total = %d, want %d", total, len(tc.want))
				}
				assertIDs(t, tc.want, items)
			})
		}
	})

	t.Run("QuerySearchIsCaseInsensitive", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		pid := nextProjectID(t)

		seed(t, s, []domain.LogEntry{
			mkEntry(pid, "c1", "api", domain.LevelError, "DB Timeout on pool", 0),
		})
		items, _, err := s.Query(ctx, pid, domain.QueryFilter{Search: "timeout", Limit: 50})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		assertIDs(t, []string{"c1"}, items)
	})

	t.Run("QuerySearchSubstring", func(t *testing.T) {
		if !caps.substringSearch {
			t.Skip("backend uses phrase matching, not substring — documented divergence")
		}
		ctx := context.Background()
		s := newStore(t)
		pid := nextProjectID(t)

		seed(t, s, []domain.LogEntry{
			mkEntry(pid, "s1", "api", domain.LevelError, "db timeout on pool", 0),
		})
		items, _, err := s.Query(ctx, pid, domain.QueryFilter{Search: "imeou", Limit: 50})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		assertIDs(t, []string{"s1"}, items)
	})

	t.Run("QueryPaginationTotalIsFullMatchCount", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		pid := nextProjectID(t)

		var entries []domain.LogEntry
		for i := 0; i < 5; i++ {
			entries = append(entries, mkEntry(pid, fmt.Sprintf("p%d", i), "api", domain.LevelInfo, "page me", i))
		}
		seed(t, s, entries)

		// Newest first: p4, p3, p2, p1, p0.
		wantPages := [][]string{
			{"p4", "p3"},
			{"p2", "p1"},
			{"p0"},
		}
		for page, want := range wantPages {
			items, total, err := s.Query(ctx, pid, domain.QueryFilter{Limit: 2, Offset: page * 2})
			if err != nil {
				t.Fatalf("page %d: %v", page, err)
			}
			// The SPA renders "{total} entries match" and "Page X of Y",
			// so total must be the full match count on every page.
			if total != 5 {
				t.Errorf("page %d: total = %d, want 5", page, total)
			}
			assertIDs(t, want, items)
		}

		// Offset past the end is empty, not an error, and still non-nil.
		items, total, err := s.Query(ctx, pid, domain.QueryFilter{Limit: 2, Offset: 100})
		if err != nil {
			t.Fatalf("offset past end: %v", err)
		}
		if items == nil {
			t.Error("offset past end returned nil slice; want empty non-nil")
		}
		if len(items) != 0 || total != 5 {
			t.Errorf("offset past end: %d items, total %d, want 0 items and total 5", len(items), total)
		}
	})

	t.Run("MetadataRoundTrip", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		pid := nextProjectID(t)

		nested := mkEntry(pid, "m_nested", "api", domain.LevelInfo, "nested meta", 0)
		nested.Metadata = map[string]any{
			"str":    "value",
			"num":    float64(42),
			"bool":   true,
			"nested": map[string]any{"deep": map[string]any{"deeper": "yes"}},
			"list":   []any{"a", float64(1)},
		}
		empty := mkEntry(pid, "m_empty", "api", domain.LevelInfo, "empty meta", 1)
		empty.Metadata = map[string]any{}

		seed(t, s, []domain.LogEntry{nested, empty})

		for _, want := range []domain.LogEntry{nested, empty} {
			got, err := s.Get(ctx, pid, want.ID)
			if err != nil {
				t.Fatalf("Get %s: %v", want.ID, err)
			}
			// Metadata must always be a JSON object, never nil — the SPA
			// JSON.stringify's it directly.
			if got.Metadata == nil {
				t.Fatalf("%s: Metadata is nil; want an object", want.ID)
			}
			assertMetadataEqual(t, want.ID, want.Metadata, got.Metadata)
		}
	})

	t.Run("Sources", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		pid, other := nextProjectID(t), nextProjectID(t)

		seed(t, s, []domain.LogEntry{
			mkEntry(pid, "src1", "worker", domain.LevelInfo, "a", 0),
			mkEntry(pid, "src2", "api", domain.LevelInfo, "b", 10),
			mkEntry(pid, "src3", "api", domain.LevelError, "c", 20),
			mkEntry(other, "src4", "ghost", domain.LevelInfo, "d", 0),
		})

		got, err := s.Sources(ctx, pid)
		if err != nil {
			t.Fatalf("Sources: %v", err)
		}
		// Sorted by service name; the other project's service must not leak.
		if len(got) != 2 {
			t.Fatalf("Sources: got %d sources %+v, want 2", len(got), got)
		}
		if got[0].Name != "api" || got[1].Name != "worker" {
			t.Fatalf("Sources not ordered by name: %+v", got)
		}
		// totalEntries is rendered in the SPA's service dropdown.
		if got[0].TotalEntries != 2 {
			t.Errorf("api totalEntries = %d, want 2", got[0].TotalEntries)
		}
		if got[1].TotalEntries != 1 {
			t.Errorf("worker totalEntries = %d, want 1", got[1].TotalEntries)
		}
		if caps.sourceTimestamps {
			wantFirst := baseTime.Add(10 * time.Minute)
			wantLast := baseTime.Add(20 * time.Minute)
			if !got[0].FirstSeenAt.Equal(wantFirst) {
				t.Errorf("api FirstSeenAt = %s, want %s", got[0].FirstSeenAt, wantFirst)
			}
			if !got[0].LastSeenAt.Equal(wantLast) {
				t.Errorf("api LastSeenAt = %s, want %s", got[0].LastSeenAt, wantLast)
			}
		}
	})

	t.Run("SourcesEmptyProject", func(t *testing.T) {
		s := newStore(t)
		got, err := s.Sources(context.Background(), nextProjectID(t))
		if err != nil {
			t.Fatalf("Sources: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("Sources on empty project: got %+v", got)
		}
	})

	t.Run("DeleteOlderThanForProject", func(t *testing.T) {
		if !caps.deletes {
			t.Skip("backend delegates retention to the engine — documented divergence")
		}
		ctx := context.Background()
		s := newStore(t)
		pid, other := nextProjectID(t), nextProjectID(t)

		seed(t, s, []domain.LogEntry{
			mkEntry(pid, "old1", "api", domain.LevelInfo, "old", 0),
			mkEntry(pid, "old2", "api", domain.LevelInfo, "old", 10),
			mkEntry(pid, "new1", "api", domain.LevelInfo, "new", 60),
			mkEntry(other, "otherold", "api", domain.LevelInfo, "old", 0),
		})

		cutoff := baseTime.Add(30 * time.Minute)
		deleted, err := s.DeleteOlderThanForProject(ctx, pid, cutoff)
		if err != nil {
			t.Fatalf("DeleteOlderThanForProject: %v", err)
		}
		if deleted != 2 {
			t.Errorf("deleted = %d, want 2", deleted)
		}
		items, _, err := s.Query(ctx, pid, domain.QueryFilter{Limit: 50})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		assertIDs(t, []string{"new1"}, items)

		// Retention is per-project: the other project is untouched.
		if _, err := s.Get(ctx, other, "otherold"); err != nil {
			t.Errorf("delete crossed project boundary: %v", err)
		}

		// A second sweep with nothing to do reports zero, not an error.
		again, err := s.DeleteOlderThanForProject(ctx, pid, cutoff)
		if err != nil {
			t.Fatalf("second sweep: %v", err)
		}
		if again != 0 {
			t.Errorf("second sweep deleted = %d, want 0", again)
		}
	})

	t.Run("Ping", func(t *testing.T) {
		s := newStore(t)
		if err := s.Ping(context.Background()); err != nil {
			t.Fatalf("Ping: %v", err)
		}
	})
}

// --- helpers ----------------------------------------------------------

func seed(t *testing.T, s LogStore, entries []domain.LogEntry) {
	t.Helper()
	if err := s.InsertBatch(context.Background(), entries); err != nil {
		t.Fatalf("seed InsertBatch: %v", err)
	}
}

func ids(entries []domain.LogEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

func assertIDs(t *testing.T, want []string, got []domain.LogEntry) {
	t.Helper()
	gotIDs := ids(got)
	if len(want) != len(gotIDs) {
		t.Fatalf("ids = %v, want %v", gotIDs, want)
	}
	for i := range want {
		if want[i] != gotIDs[i] {
			t.Fatalf("ids = %v, want %v", gotIDs, want)
		}
	}
}

func assertEntryEqual(t *testing.T, want, got domain.LogEntry) {
	t.Helper()
	if got.ID != want.ID {
		t.Errorf("ID = %q, want %q", got.ID, want.ID)
	}
	if got.ProjectID != want.ProjectID {
		t.Errorf("ProjectID = %q, want %q", got.ProjectID, want.ProjectID)
	}
	if got.Service != want.Service {
		t.Errorf("Service = %q, want %q", got.Service, want.Service)
	}
	if got.Level != want.Level {
		t.Errorf("Level = %q, want %q", got.Level, want.Level)
	}
	if got.Message != want.Message {
		t.Errorf("Message = %q, want %q", got.Message, want.Message)
	}
	if !got.Timestamp.Equal(want.Timestamp) {
		t.Errorf("Timestamp = %s, want %s", got.Timestamp, want.Timestamp)
	}
	// receivedAt is rendered raw in LogDetail — a zero value is a visible bug.
	if !got.ReceivedAt.Equal(want.ReceivedAt) {
		t.Errorf("ReceivedAt = %s, want %s", got.ReceivedAt, want.ReceivedAt)
	}
	assertMetadataEqual(t, want.ID, want.Metadata, got.Metadata)
}

// assertMetadataEqual compares metadata via their JSON encodings, which
// sidesteps map ordering and the float64-vs-int decoding of any values.
func assertMetadataEqual(t *testing.T, id string, want, got map[string]any) {
	t.Helper()
	wantJSON := mustJSON(t, want)
	gotJSON := mustJSON(t, got)
	if wantJSON != gotJSON {
		t.Errorf("%s: metadata = %s, want %s", id, gotJSON, wantJSON)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
