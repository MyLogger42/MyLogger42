package store

import (
	"context"
	"testing"
	"time"

	"github.com/mylogger42/mylogger42/internal/domain"
)

// TestSQLiteLogStoreConformance runs the shared LogStore contract against
// the SQLite backend. SQLite supports every capability; the VictoriaLogs
// runner (build tag `vlogs`) declares its divergences instead.
func TestSQLiteLogStoreConformance(t *testing.T) {
	runLogStoreConformance(t, func(t *testing.T) LogStore {
		return openTestStores(t).Logs
	}, storeCaps{
		substringSearch:  true,
		deletes:          true,
		sourceTimestamps: true,
	})
}

func TestSQLiteSearchEscapesLikeWildcards(t *testing.T) {
	ctx := context.Background()
	stores := openTestStores(t)
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	if err := stores.Projects.Create(ctx, domain.Project{
		ID:        "proj-like",
		Slug:      "proj-like",
		Name:      "Like Test",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}

	entries := []domain.LogEntry{
		{ID: "e-pct", ProjectID: "proj-like", Service: "svc", Level: domain.LevelInfo, Message: "100% done", Timestamp: now, ReceivedAt: now},
		{ID: "e-nopct", ProjectID: "proj-like", Service: "svc", Level: domain.LevelInfo, Message: "1000 done", Timestamp: now.Add(time.Second), ReceivedAt: now},
		{ID: "e-under", ProjectID: "proj-like", Service: "svc", Level: domain.LevelInfo, Message: "foo_bar", Timestamp: now.Add(2 * time.Second), ReceivedAt: now},
		{ID: "e-nounder", ProjectID: "proj-like", Service: "svc", Level: domain.LevelInfo, Message: "fooxbar", Timestamp: now.Add(3 * time.Second), ReceivedAt: now},
		{ID: "e-slash", ProjectID: "proj-like", Service: "svc", Level: domain.LevelInfo, Message: `a\b`, Timestamp: now.Add(4 * time.Second), ReceivedAt: now},
		{ID: "e-noslash", ProjectID: "proj-like", Service: "svc", Level: domain.LevelInfo, Message: "axb", Timestamp: now.Add(5 * time.Second), ReceivedAt: now},
	}
	if err := stores.Logs.InsertBatch(ctx, entries); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	cases := []struct {
		search string
		wantID string
	}{
		{"100%", "e-pct"},
		{"%", "e-pct"},
		{"foo_bar", "e-under"},
		{"_", "e-under"},
		{`a\b`, "e-slash"},
	}
	for _, tc := range cases {
		got, _, err := stores.Logs.Query(ctx, "proj-like", domain.QueryFilter{Search: tc.search, Limit: 50})
		if err != nil {
			t.Fatalf("Query(%q): %v", tc.search, err)
		}
		if len(got) != 1 || got[0].ID != tc.wantID {
			ids := make([]string, len(got))
			for i, e := range got {
				ids[i] = e.ID
			}
			t.Fatalf("Query(%q) returned IDs %v, want [%s]", tc.search, ids, tc.wantID)
		}
	}
}
