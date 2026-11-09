package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mylogger42/mylogger42/internal/domain"
	"github.com/mylogger42/mylogger42/internal/store"
)

// connect seeds a SQLite store with one project and three entries, then
// returns an MCP client session wired to the server over in-memory
// transports — the same tool code the HTTP handler runs.
func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	stores, err := store.OpenSQLiteStores(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stores.Logs.Close() })

	p := domain.Project{ID: uuid.NewString(), Slug: "shop", Name: "Shop", CreatedAt: time.Now().UTC()}
	if err := stores.Projects.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var entries []domain.LogEntry
	for i, lvl := range []domain.LogLevel{"Info", "Error", "Error"} {
		entries = append(entries, domain.LogEntry{
			ID: uuid.NewString(), ProjectID: p.ID, Service: "checkout", Level: lvl,
			Message: "payment failed", Timestamp: now.Add(time.Duration(i) * time.Second),
			ReceivedAt: now, Metadata: map[string]any{"n": i},
		})
	}
	if err := stores.Logs.InsertBatch(ctx, entries); err != nil {
		t.Fatal(err)
	}

	srvT, cliT := mcp.NewInMemoryTransports()
	if _, err := NewServer(stores.Logs, stores.Projects, nil).Connect(ctx, srvT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, cliT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// call runs a tool and decodes its JSON text content into out (may be nil).
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	if out != nil && !res.IsError {
		text := res.Content[0].(*mcp.TextContent).Text
		if err := json.Unmarshal([]byte(text), out); err != nil {
			t.Fatalf("%s: decode %q: %v", name, text, err)
		}
	}
	return res
}

func TestTools(t *testing.T) {
	cs := connect(t)

	var projects struct{ Projects []struct{ Slug string } }
	call(t, cs, "list_projects", nil, &projects)
	if len(projects.Projects) != 1 || projects.Projects[0].Slug != "shop" {
		t.Fatalf("list_projects = %+v", projects)
	}

	var services struct{ Services []domain.Source }
	call(t, cs, "list_services", map[string]any{"project": "shop"}, &services)
	if len(services.Services) != 1 || services.Services[0].Name != "checkout" {
		t.Fatalf("list_services = %+v", services)
	}

	var search struct {
		Items []domain.LogEntry
		Total int
	}
	call(t, cs, "search_logs", map[string]any{"project": "shop", "level": "error", "limit": 1}, &search)
	if search.Total != 2 || len(search.Items) != 1 || search.Items[0].Level != "Error" {
		t.Fatalf("search_logs = %+v", search)
	}

	var entry domain.LogEntry
	call(t, cs, "get_log", map[string]any{"project": "shop", "id": search.Items[0].ID}, &entry)
	if entry.ID != search.Items[0].ID {
		t.Fatalf("get_log = %+v", entry)
	}

	var stats domain.LogStats
	call(t, cs, "log_stats", map[string]any{"project": "shop", "bucket": 60}, &stats)
	if stats.Totals["Error"] != 2 || stats.Totals["Info"] != 1 || len(stats.TopMessages) == 0 {
		t.Fatalf("log_stats = %+v", stats)
	}
}

// Bad input must come back as a tool error the agent can read and fix,
// never a protocol error.
func TestToolErrors(t *testing.T) {
	cs := connect(t)
	for name, tc := range map[string]struct {
		tool string
		args map[string]any
		want string
	}{
		"unknown project": {"search_logs", map[string]any{"project": "nope"}, "not found"},
		"bad level":       {"search_logs", map[string]any{"project": "shop", "level": "loud"}, "unknown log level"},
		"bad time":        {"log_stats", map[string]any{"project": "shop", "from": "yesterday"}, "RFC3339"},
		"limit too big":   {"search_logs", map[string]any{"project": "shop", "limit": 500}, "<= 200"},
		"bad bucket":      {"log_stats", map[string]any{"project": "shop", "bucket": 7}, "bucket"},
		"missing entry":   {"get_log", map[string]any{"project": "shop", "id": "x"}, "not found"},
	} {
		res := call(t, cs, tc.tool, tc.args, nil)
		text := res.Content[0].(*mcp.TextContent).Text
		if !res.IsError || !strings.Contains(text, tc.want) {
			t.Errorf("%s: IsError=%v text=%q, want error containing %q", name, res.IsError, text, tc.want)
		}
	}
}
