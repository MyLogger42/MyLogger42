package api

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
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
		logger:   log.New(io.Discard, "", 0),
	}
}

// fakeLogStore returns a no-op LogStore so the query handlers can
// reach the storage layer without crashing. Returning empty results is
// fine — these tests assert on status codes and the project-scoping
// path, not on the items themselves.
type fakeLogStore struct{}

func (f *fakeLogStore) Insert(context.Context, domain.LogEntry) error          { return nil }
func (f *fakeLogStore) InsertBatch(context.Context, []domain.LogEntry) error  { return nil }
func (f *fakeLogStore) Get(context.Context, string, string) (*domain.LogEntry, error) {
	return nil, store.ErrNotFound
}
func (f *fakeLogStore) Query(context.Context, string, domain.QueryFilter) ([]domain.LogEntry, int, error) {
	return []domain.LogEntry{}, 0, nil
}
func (f *fakeLogStore) Sources(context.Context, string) ([]domain.Source, error) {
	return []domain.Source{}, nil
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
