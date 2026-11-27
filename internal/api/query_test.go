package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mylogger42/mylogger42/internal/domain"
	"github.com/mylogger42/mylogger42/internal/store"
)

// queryTestServer builds a minimal Server wired with the fake stores
// from middleware_test.go. We bypass requireSession by calling the
// handler directly — these tests exercise resolveProjectFromQuery and
// the per-handler scoping, not the cookie auth (covered separately).
func queryTestServer(t *testing.T, projects map[string]*domain.Project) *Server {
	t.Helper()
	bySlug := make(map[string]*domain.Project, len(projects))
	byID := make(map[string]*domain.Project, len(projects))
	for slug, p := range projects {
		p.Slug = slug
		bySlug[slug] = p
		byID[p.ID] = p
	}
	return &Server{
		store:    &fakeLogStore{},
		projects: &fakeProjectStoreWithSlugs{byID: byID, bySlug: bySlug},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// fakeLogStore returns a no-op LogStore so the query handlers can
// reach the storage layer without crashing. Returning empty results is
// fine — these tests assert on status codes and the project-scoping
// path, not on the items themselves.
type fakeLogStore struct{}

func (f *fakeLogStore) Insert(context.Context, domain.LogEntry) error        { return nil }
func (f *fakeLogStore) InsertBatch(context.Context, []domain.LogEntry) error { return nil }
func (f *fakeLogStore) Get(context.Context, string, string) (*domain.LogEntry, error) {
	return nil, store.ErrNotFound
}
func (f *fakeLogStore) Query(context.Context, string, domain.QueryFilter) ([]domain.LogEntry, int, error) {
	return []domain.LogEntry{}, 0, nil
}
func (f *fakeLogStore) Sources(context.Context, string) ([]domain.Source, error) {
	return []domain.Source{}, nil
}
func (f *fakeLogStore) Stats(context.Context, string, domain.QueryFilter, int) (*domain.LogStats, error) {
	return &domain.LogStats{
		Series:      []domain.LevelBucket{},
		Totals:      map[domain.LogLevel]int64{},
		TopMessages: []domain.MessageGroup{},
	}, nil
}
func (f *fakeLogStore) DeleteOlderThanForProject(context.Context, string, time.Time) (int64, error) {
	return 0, nil
}
func (f *fakeLogStore) Ping(context.Context) error { return nil }
func (f *fakeLogStore) Close() error               { return nil }

// fakeProjectStoreWithSlugs extends fakeProjectStore (in middleware_test.go)
// with slug lookup, used by resolveProjectFromQuery.
type fakeProjectStoreWithSlugs struct {
	byID   map[string]*domain.Project
	bySlug map[string]*domain.Project
}

func (f *fakeProjectStoreWithSlugs) Create(context.Context, domain.Project) error {
	return store.ErrNotFound
}
func (f *fakeProjectStoreWithSlugs) GetBySlug(_ context.Context, slug string) (*domain.Project, error) {
	p, ok := f.bySlug[slug]
	if !ok {
		return nil, store.ErrNotFound
	}
	return p, nil
}
func (f *fakeProjectStoreWithSlugs) GetByID(_ context.Context, id string) (*domain.Project, error) {
	p, ok := f.byID[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return p, nil
}
func (f *fakeProjectStoreWithSlugs) List(context.Context, bool) ([]domain.Project, error) {
	return nil, nil
}
func (f *fakeProjectStoreWithSlugs) Update(context.Context, string, string, *int) (*domain.Project, error) {
	return nil, store.ErrNotFound
}
func (f *fakeProjectStoreWithSlugs) Archive(context.Context, string) (*domain.Project, error) {
	return nil, store.ErrNotFound
}
func (f *fakeProjectStoreWithSlugs) Unarchive(context.Context, string) (*domain.Project, error) {
	return nil, store.ErrNotFound
}
func (f *fakeProjectStoreWithSlugs) Delete(context.Context, string) error { return nil }
func (f *fakeProjectStoreWithSlugs) Count(context.Context) (int, error)   { return 0, nil }

// --- tests ---

func TestQueryRejectsMissingProjectParam(t *testing.T) {
	s := queryTestServer(t, map[string]*domain.Project{
		"auth-svc": {ID: "p1"},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs", nil)
	rr := httptest.NewRecorder()
	s.handleQuery(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestQueryRejectsBadSlugShape(t *testing.T) {
	s := queryTestServer(t, map[string]*domain.Project{
		"auth-svc": {ID: "p1"},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs?project=Bad-Slug", nil)
	rr := httptest.NewRecorder()
	s.handleQuery(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for uppercase slug; body=%s", rr.Code, rr.Body.String())
	}
}

func TestQueryReturns404ForUnknownProject(t *testing.T) {
	s := queryTestServer(t, map[string]*domain.Project{
		"auth-svc": {ID: "p1"},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs?project=missing", nil)
	rr := httptest.NewRecorder()
	s.handleQuery(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestQueryHappyPath(t *testing.T) {
	s := queryTestServer(t, map[string]*domain.Project{
		"auth-svc": {ID: "p1"},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs?project=auth-svc", nil)
	rr := httptest.NewRecorder()
	s.handleQuery(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestSourcesRequiresProjectParam(t *testing.T) {
	s := queryTestServer(t, map[string]*domain.Project{
		"auth-svc": {ID: "p1"},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sources", nil)
	rr := httptest.NewRecorder()
	s.handleSources(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for missing ?project=", rr.Code)
	}
}

func TestGetByIDRequiresProjectParam(t *testing.T) {
	s := queryTestServer(t, map[string]*domain.Project{
		"auth-svc": {ID: "p1"},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs/some-id", nil)
	rr := httptest.NewRecorder()
	s.handleGetByID(rr, req, "some-id")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for missing ?project=", rr.Code)
	}
}

// TestStatsBucketAllowlist covers the one input /stats adds over /logs.
// An arbitrary bucket reaches a GROUP BY divisor and a `_time:` pipe, so
// it is validated at the boundary rather than clamped downstream.
func TestStatsBucketAllowlist(t *testing.T) {
	s := queryTestServer(t, map[string]*domain.Project{"auth-svc": {ID: "p1"}})
	cases := []struct {
		query string
		want  int
	}{
		{"?project=auth-svc", http.StatusOK},            // no bucket = default
		{"?project=auth-svc&bucket=300", http.StatusOK}, // allowlisted
		{"?project=auth-svc&bucket=7", http.StatusBadRequest},
		{"?project=auth-svc&bucket=abc", http.StatusBadRequest},
		{"?bucket=300", http.StatusBadRequest}, // project still required
	}
	for _, tc := range cases {
		rr := httptest.NewRecorder()
		s.handleStats(rr, httptest.NewRequest(http.MethodGet, "/api/v1/stats"+tc.query, nil))
		if rr.Code != tc.want {
			t.Errorf("%s: status = %d, want %d; body=%s", tc.query, rr.Code, tc.want, rr.Body.String())
		}
	}
}

type exportCapturingStore struct {
	fakeLogStore
	entries []domain.LogEntry
	queries []domain.QueryFilter
}

func (f *exportCapturingStore) Query(_ context.Context, _ string, filter domain.QueryFilter) ([]domain.LogEntry, int, error) {
	f.queries = append(f.queries, filter)
	total := len(f.entries)
	start := filter.Offset
	if start >= total {
		return []domain.LogEntry{}, total, nil
	}
	end := start + filter.Limit
	if filter.Limit <= 0 || end > total {
		end = total
	}
	return f.entries[start:end], total, nil
}

func TestExportValidation(t *testing.T) {
	s := queryTestServer(t, map[string]*domain.Project{"auth-svc": {ID: "p1"}})
	cases := []struct {
		query string
		want  int
	}{
		{"", http.StatusBadRequest},
		{"?project=missing", http.StatusNotFound},
		{"?project=auth-svc&format=xml", http.StatusBadRequest},
		{"?project=auth-svc&level=bogus", http.StatusBadRequest},
		{"?project=auth-svc&format=ndjson", http.StatusOK},
		{"?project=auth-svc&format=csv", http.StatusOK},
		{"?project=auth-svc", http.StatusOK},
	}
	for _, tc := range cases {
		rr := httptest.NewRecorder()
		s.handleExport(rr, httptest.NewRequest(http.MethodGet, "/api/v1/logs/export"+tc.query, nil))
		if rr.Code != tc.want {
			t.Errorf("%s: status = %d, want %d; body=%s", tc.query, rr.Code, tc.want, rr.Body.String())
		}
	}
}

func TestExportNDJSONAndCSV(t *testing.T) {
	ts := time.Date(2026, 11, 27, 1, 0, 0, 0, time.UTC)
	st := &exportCapturingStore{
		entries: []domain.LogEntry{
			{
				ID:         "e1",
				ProjectID:  "p1",
				Service:    "api",
				Level:      domain.LevelError,
				Message:    `boom, "quoted"`,
				Timestamp:  ts,
				ReceivedAt: ts,
				Metadata:   map[string]any{"code": float64(500)},
			},
			{
				ID:         "e2",
				ProjectID:  "p1",
				Service:    "worker",
				Level:      domain.LevelInfo,
				Message:    "started",
				Timestamp:  ts.Add(time.Second),
				ReceivedAt: ts.Add(time.Second),
				Metadata:   map[string]any{},
			},
		},
	}
	s := &Server{
		store:    st,
		projects: &fakeProjectStoreWithSlugs{bySlug: map[string]*domain.Project{"auth-svc": {ID: "p1", Slug: "auth-svc"}}},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	// NDJSON
	rr := httptest.NewRecorder()
	s.handleExport(rr, httptest.NewRequest(http.MethodGet, "/api/v1/logs/export?project=auth-svc&format=ndjson&service=api", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("ndjson status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/x-ndjson") {
		t.Errorf("ndjson Content-Type = %q", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); cd != `attachment; filename="auth-svc-logs.ndjson"` {
		t.Errorf("ndjson Content-Disposition = %q", cd)
	}
	lines := strings.Split(strings.TrimSpace(rr.Body.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("ndjson lines = %d, want 2", len(lines))
	}
	var got domain.LogEntry
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("ndjson unmarshal: %v", err)
	}
	if got.ID != "e1" || got.Message != `boom, "quoted"` {
		t.Errorf("ndjson first entry = %+v", got)
	}
	if len(st.queries) == 0 || st.queries[0].Service != "api" {
		t.Errorf("filter not forwarded to store: %+v", st.queries)
	}

	// CSV
	rr = httptest.NewRecorder()
	s.handleExport(rr, httptest.NewRequest(http.MethodGet, "/api/v1/logs/export?project=auth-svc&format=csv", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("csv status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("csv Content-Type = %q", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); cd != `attachment; filename="auth-svc-logs.csv"` {
		t.Errorf("csv Content-Disposition = %q", cd)
	}
	records, err := csv.NewReader(strings.NewReader(rr.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("csv rows = %d, want 3 (header + 2)", len(records))
	}
	if records[1][0] != "e1" || records[1][5] != `boom, "quoted"` || records[1][6] != `{"code":500}` {
		t.Errorf("csv row 1 = %v", records[1])
	}
	if records[2][6] != "{}" {
		t.Errorf("csv row 2 metadata = %q, want {}", records[2][6])
	}
}

func TestExportCapsAtMaxRows(t *testing.T) {
	ts := time.Date(2026, 11, 27, 1, 0, 0, 0, time.UTC)
	entries := make([]domain.LogEntry, exportMaxRows+25)
	for i := range entries {
		entries[i] = domain.LogEntry{
			ID:         "id",
			ProjectID:  "p1",
			Service:    "api",
			Level:      domain.LevelInfo,
			Message:    "msg",
			Timestamp:  ts,
			ReceivedAt: ts,
		}
	}
	st := &exportCapturingStore{entries: entries}
	s := &Server{
		store:    st,
		projects: &fakeProjectStoreWithSlugs{bySlug: map[string]*domain.Project{"auth-svc": {ID: "p1", Slug: "auth-svc"}}},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	rr := httptest.NewRecorder()
	s.handleExport(rr, httptest.NewRequest(http.MethodGet, "/api/v1/logs/export?project=auth-svc&format=ndjson", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if rr.Header().Get("X-Export-Truncated") != "true" {
		t.Errorf("expected X-Export-Truncated=true, got %q", rr.Header().Get("X-Export-Truncated"))
	}
	lines := strings.Split(strings.TrimSpace(rr.Body.String()), "\n")
	if len(lines) != exportMaxRows {
		t.Errorf("exported %d lines, want %d", len(lines), exportMaxRows)
	}
}

type capturingLogStore struct {
	fakeLogStore
	lastQueryFilter domain.QueryFilter
	lastStatsFilter domain.QueryFilter
}

func (c *capturingLogStore) Query(_ context.Context, _ string, f domain.QueryFilter) ([]domain.LogEntry, int, error) {
	c.lastQueryFilter = f
	return []domain.LogEntry{}, 0, nil
}

func (c *capturingLogStore) Stats(_ context.Context, _ string, f domain.QueryFilter, _ int) (*domain.LogStats, error) {
	c.lastStatsFilter = f
	return &domain.LogStats{
		Series:      []domain.LevelBucket{},
		Totals:      map[domain.LogLevel]int64{},
		TopMessages: []domain.MessageGroup{},
	}, nil
}

func TestQueryAndStatsMultiLevelFilters(t *testing.T) {
	store := &capturingLogStore{}
	s := &Server{
		store: store,
		projects: &fakeProjectStoreWithSlugs{
			byID:   map[string]*domain.Project{"p1": {ID: "p1", Slug: "auth-svc"}},
			bySlug: map[string]*domain.Project{"auth-svc": {ID: "p1", Slug: "auth-svc"}},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	cases := []struct {
		name       string
		query      string
		wantStatus int
		wantLevels []domain.LogLevel
	}{
		{
			name:       "LegacySingleLevel",
			query:      "?project=auth-svc&level=Error",
			wantStatus: http.StatusOK,
			wantLevels: []domain.LogLevel{domain.LevelError},
		},
		{
			name:       "CommaSeparatedLevels",
			query:      "?project=auth-svc&levels=warning,Error,FATAL",
			wantStatus: http.StatusOK,
			wantLevels: []domain.LogLevel{domain.LevelWarning, domain.LevelError, domain.LevelFatal},
		},
		{
			name:       "RepeatedLevelsParam",
			query:      "?project=auth-svc&levels=Info&levels=Warning,Error",
			wantStatus: http.StatusOK,
			wantLevels: []domain.LogLevel{domain.LevelInfo, domain.LevelWarning, domain.LevelError},
		},
		{
			name:       "EmptyLevelsParamIgnored",
			query:      "?project=auth-svc&levels=",
			wantStatus: http.StatusOK,
			wantLevels: nil,
		},
		{
			name:       "MinLevel",
			query:      "?project=auth-svc&minLevel=warning",
			wantStatus: http.StatusOK,
			wantLevels: []domain.LogLevel{domain.LevelWarning, domain.LevelError, domain.LevelFatal},
		},
		{
			name:       "InvalidLevels",
			query:      "?project=auth-svc&levels=Warning,Trace",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "InvalidMinLevel",
			query:      "?project=auth-svc&minLevel=Trace",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store.lastQueryFilter = domain.QueryFilter{}
			rr := httptest.NewRecorder()
			s.handleQuery(rr, httptest.NewRequest(http.MethodGet, "/api/v1/logs"+tc.query, nil))
			if rr.Code != tc.wantStatus {
				t.Fatalf("GET /logs status = %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body.String())
			}

			store.lastStatsFilter = domain.QueryFilter{}
			rrStats := httptest.NewRecorder()
			s.handleStats(rrStats, httptest.NewRequest(http.MethodGet, "/api/v1/stats"+tc.query, nil))
			if rrStats.Code != tc.wantStatus {
				t.Fatalf("GET /stats status = %d, want %d; body=%s", rrStats.Code, tc.wantStatus, rrStats.Body.String())
			}

			if tc.wantStatus == http.StatusOK {
				for _, got := range [][]domain.LogLevel{
					store.lastQueryFilter.EffectiveLevels(),
					store.lastStatsFilter.EffectiveLevels(),
				} {
					if len(got) != len(tc.wantLevels) {
						t.Fatalf("EffectiveLevels() = %v, want %v", got, tc.wantLevels)
					}
					for i := range got {
						if got[i] != tc.wantLevels[i] {
							t.Fatalf("EffectiveLevels()[%d] = %q, want %q", i, got[i], tc.wantLevels[i])
						}
					}
				}
			}
		})
	}
}
