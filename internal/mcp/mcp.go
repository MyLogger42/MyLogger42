// Package mcp exposes MyLogger42's read surface as a Model Context
// Protocol server, so AI agents can browse projects, search logs and read
// activity stats. It is strictly read-only: every tool maps onto a
// LogStore / ProjectStore read method. Authentication happens in front of
// the handler (api.requireReadToken); this package trusts its caller.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mylogger42/mylogger42/internal/domain"
	"github.com/mylogger42/mylogger42/internal/store"
)

// maxSearchLimit is lower than the HTTP API's 500: every row lands in the
// agent's context window, metadata included.
const maxSearchLimit = 200

const instructions = `MyLogger42 is a self-hosted log server. Logs are grouped into projects (identified by slug); each project has services (the component that emitted the log).

Suggested workflow to understand what is going on:
1. list_projects — find the project slug.
2. list_services — see which services log and when they were last active.
3. log_stats — level-over-time histogram, per-level totals and the most repeated messages. Use it first to spot error spikes and noisy messages before reading rows.
4. search_logs — read the actual entries, narrowed by service, level, time range or message substring.
5. get_log — fetch one entry by id.

Levels, lowest to highest: Verbose, Debug, Info, Event, Warning, Error, Fatal. The level filter matches one level exactly. Timestamps are RFC3339 (e.g. 2026-01-02T15:04:05Z).`

// NewServer builds the MCP server with all tools registered. Exposed for
// tests, which connect to it over in-memory transports.
func NewServer(logs store.LogStore, projects store.ProjectStore, logger *log.Logger) *mcp.Server {
	if logger == nil {
		logger = log.Default()
	}
	t := tools{logs: logs, projects: projects, logger: logger}
	s := mcp.NewServer(&mcp.Implementation{Name: "mylogger42", Version: "1"},
		&mcp.ServerOptions{Instructions: instructions})

	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(s, &mcp.Tool{Name: "list_projects", Annotations: readOnly,
		Description: "List log projects (slug, name, retention, archived state)."}, t.listProjects)
	mcp.AddTool(s, &mcp.Tool{Name: "list_services", Annotations: readOnly,
		Description: "List the services that have logged to a project, with first/last seen time and entry count."}, t.listServices)
	mcp.AddTool(s, &mcp.Tool{Name: "search_logs", Annotations: readOnly,
		Description: fmt.Sprintf("Search log entries in a project, newest first by default. Returns items plus the total match count for paging. limit defaults to 50, max %d.", maxSearchLimit)}, t.searchLogs)
	mcp.AddTool(s, &mcp.Tool{Name: "get_log", Annotations: readOnly,
		Description: "Fetch one log entry by id, including its full metadata."}, t.getLog)
	mcp.AddTool(s, &mcp.Tool{Name: "log_stats", Annotations: readOnly,
		Description: "Aggregate stats for the filtered logs: per-level counts over time (histogram), per-level totals, and the top repeated (service, level, message) groups. Best first look at a project."}, t.logStats)
	return s
}

// NewHandler returns the Streamable HTTP handler for /mcp.
//
//   - Stateless: no sessions to track; every POST is self-contained.
//   - JSONResponse: plain application/json instead of SSE. The access-log
//     wrapper in internal/api doesn't forward Flush, and a read-only
//     server has nothing to stream anyway.
//   - DisableLocalhostProtection: the DNS-rebinding guard 403s any
//     deployment behind a same-host reverse proxy (Host: logs.example.com
//     arriving from 127.0.0.1). The bearer token in front of this handler
//     is what actually protects it.
func NewHandler(logs store.LogStore, projects store.ProjectStore, logger *log.Logger) http.Handler {
	s := NewServer(logs, projects, logger)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{
			Stateless:                  true,
			JSONResponse:               true,
			DisableLocalhostProtection: true,
		})
}

type tools struct {
	logs     store.LogStore
	projects store.ProjectStore
	logger   *log.Logger
}

// --- inputs -------------------------------------------------------------

type listProjectsIn struct {
	IncludeArchived bool `json:"include_archived,omitempty" jsonschema:"also list archived projects"`
}

type projectIn struct {
	Project string `json:"project" jsonschema:"project slug, from list_projects"`
}

type filterIn struct {
	Project string `json:"project" jsonschema:"project slug, from list_projects"`
	Service string `json:"service,omitempty" jsonschema:"only this service (exact name)"`
	Level   string `json:"level,omitempty" jsonschema:"only this level: Verbose, Debug, Info, Event, Warning, Error or Fatal"`
	From    string `json:"from,omitempty" jsonschema:"inclusive start time, RFC3339"`
	To      string `json:"to,omitempty" jsonschema:"inclusive end time, RFC3339"`
	Search  string `json:"search,omitempty" jsonschema:"substring match on the message"`
}

type searchIn struct {
	filterIn
	Limit  int    `json:"limit,omitempty" jsonschema:"max rows to return (default 50, max 200)"`
	Offset int    `json:"offset,omitempty" jsonschema:"rows to skip, for paging"`
	Sort   string `json:"sort,omitempty" jsonschema:"desc (newest first, default) or asc"`
}

type getLogIn struct {
	Project string `json:"project" jsonschema:"project slug"`
	ID      string `json:"id" jsonschema:"log entry id"`
}

type statsIn struct {
	filterIn
	Bucket int `json:"bucket,omitempty" jsonschema:"histogram bucket size in seconds: 60, 300, 3600 (default) or 86400"`
}

// --- outputs ------------------------------------------------------------
//
// Handlers return `any` as the typed output so the SDK skips output-schema
// inference over domain types; the value is still sent as JSON content.

type projectOut struct {
	Slug          string     `json:"slug"`
	Name          string     `json:"name"`
	RetentionDays *int       `json:"retentionDays"`
	CreatedAt     time.Time  `json:"createdAt"`
	ArchivedAt    *time.Time `json:"archivedAt,omitempty"`
}

// --- handlers -----------------------------------------------------------

func (t tools) listProjects(ctx context.Context, _ *mcp.CallToolRequest, in listProjectsIn) (*mcp.CallToolResult, any, error) {
	ps, err := t.projects.List(ctx, in.IncludeArchived)
	if err != nil {
		return nil, nil, t.storageErr(err)
	}
	out := make([]projectOut, 0, len(ps))
	for _, p := range ps {
		out = append(out, projectOut{p.Slug, p.Name, p.RetentionDays, p.CreatedAt, p.ArchivedAt})
	}
	return nil, map[string]any{"projects": out}, nil
}

func (t tools) listServices(ctx context.Context, _ *mcp.CallToolRequest, in projectIn) (*mcp.CallToolResult, any, error) {
	p, err := t.resolveProject(ctx, in.Project)
	if err != nil {
		return nil, nil, err
	}
	srcs, err := t.logs.Sources(ctx, p.ID)
	if err != nil {
		return nil, nil, t.storageErr(err)
	}
	if srcs == nil {
		srcs = []domain.Source{}
	}
	return nil, map[string]any{"services": srcs}, nil
}

func (t tools) searchLogs(ctx context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, any, error) {
	p, err := t.resolveProject(ctx, in.Project)
	if err != nil {
		return nil, nil, err
	}
	if in.Limit > maxSearchLimit {
		return nil, nil, fmt.Errorf("limit must be <= %d", maxSearchLimit)
	}
	f, err := buildFilter(in.filterIn)
	if err != nil {
		return nil, nil, err
	}
	f.Limit, f.Offset = in.Limit, in.Offset
	if in.Limit == 0 {
		f.Limit = 50
	}
	if in.Sort != "" {
		f.Sort = domain.SortOrder(strings.ToLower(in.Sort))
	}
	if err := f.Validate(); err != nil {
		return nil, nil, err
	}
	items, total, err := t.logs.Query(ctx, p.ID, f)
	if err != nil {
		return nil, nil, t.storageErr(err)
	}
	if items == nil {
		items = []domain.LogEntry{}
	}
	return nil, map[string]any{"items": items, "total": total, "limit": f.Limit, "offset": f.Offset}, nil
}

func (t tools) getLog(ctx context.Context, _ *mcp.CallToolRequest, in getLogIn) (*mcp.CallToolResult, any, error) {
	p, err := t.resolveProject(ctx, in.Project)
	if err != nil {
		return nil, nil, err
	}
	if in.ID == "" {
		return nil, nil, errors.New("id is required")
	}
	e, err := t.logs.Get(ctx, p.ID, in.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, errors.New("log entry not found")
	}
	if err != nil {
		return nil, nil, t.storageErr(err)
	}
	return nil, e, nil
}

func (t tools) logStats(ctx context.Context, _ *mcp.CallToolRequest, in statsIn) (*mcp.CallToolResult, any, error) {
	p, err := t.resolveProject(ctx, in.Project)
	if err != nil {
		return nil, nil, err
	}
	f, err := buildFilter(in.filterIn)
	if err != nil {
		return nil, nil, err
	}
	if err := f.Validate(); err != nil {
		return nil, nil, err
	}
	if err := domain.ValidateBucketSeconds(in.Bucket); err != nil {
		return nil, nil, err
	}
	stats, err := t.logs.Stats(ctx, p.ID, f, in.Bucket)
	if err != nil {
		return nil, nil, t.storageErr(err)
	}
	return nil, stats, nil
}

// --- helpers ------------------------------------------------------------

// resolveProject mirrors api.resolveProjectFromQuery: malformed slug and
// unknown project are distinct, agent-readable errors.
func (t tools) resolveProject(ctx context.Context, slug string) (*domain.Project, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return nil, errors.New("project is required (a slug from list_projects)")
	}
	if err := domain.ValidateSlug(slug); err != nil {
		return nil, err
	}
	p, err := t.projects.GetBySlug(ctx, slug)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("project %q not found; call list_projects", slug)
	}
	if err != nil {
		return nil, t.storageErr(err)
	}
	return p, nil
}

// buildFilter is the MCP twin of api.parseQueryFilter: same fields, same
// parsing rules, typed input instead of a query string. Callers run
// QueryFilter.Validate after setting paging fields.
func buildFilter(in filterIn) (domain.QueryFilter, error) {
	f := domain.QueryFilter{
		Service: strings.TrimSpace(in.Service),
		Search:  strings.TrimSpace(in.Search),
		Sort:    domain.SortDesc,
	}
	if in.Level != "" {
		lvl, err := domain.ParseLogLevel(in.Level)
		if err != nil {
			return f, err
		}
		f.Level = lvl
	}
	for _, tc := range []struct {
		name, raw string
		dst       **time.Time
	}{{"from", in.From, &f.From}, {"to", in.To, &f.To}} {
		if tc.raw == "" {
			continue
		}
		ts, err := time.Parse(time.RFC3339, tc.raw)
		if err != nil {
			return f, fmt.Errorf("%s: want RFC3339 like 2026-01-02T15:04:05Z: %w", tc.name, err)
		}
		*tc.dst = &ts
	}
	return f, nil
}

// storageErr logs the real cause and hands the agent a generic error, same
// posture as the HTTP API's "storage error" 500s.
func (t tools) storageErr(err error) error {
	t.logger.Printf("mcp: storage: %v", err)
	return errors.New("storage error")
}
