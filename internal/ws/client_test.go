package ws

import (
	"context"
	"testing"
	"time"

	"github.com/silverbaq/mylogger42/internal/domain"
	"github.com/silverbaq/mylogger42/internal/store"
)

// fakeProjects is a minimal store.ProjectStore for the WS unit tests.
// Only GetBySlug carries real behaviour — the other methods return
// ErrNotFound so a stray call is loud rather than silent.
type fakeProjects struct {
	bySlug map[string]*domain.Project
}

func (f *fakeProjects) Create(context.Context, domain.Project) error { return store.ErrNotFound }
func (f *fakeProjects) GetBySlug(_ context.Context, slug string) (*domain.Project, error) {
	if p, ok := f.bySlug[slug]; ok {
		return p, nil
	}
	return nil, store.ErrNotFound
}
func (f *fakeProjects) GetByID(context.Context, string) (*domain.Project, error) {
	return nil, store.ErrNotFound
}
func (f *fakeProjects) List(context.Context, bool) ([]domain.Project, error) {
	return nil, store.ErrNotFound
}
func (f *fakeProjects) Update(context.Context, string, string, *int) (*domain.Project, error) {
	return nil, store.ErrNotFound
}
func (f *fakeProjects) Archive(context.Context, string) (*domain.Project, error) {
	return nil, store.ErrNotFound
}
func (f *fakeProjects) Unarchive(context.Context, string) (*domain.Project, error) {
	return nil, store.ErrNotFound
}
func (f *fakeProjects) Delete(context.Context, string) error    { return store.ErrNotFound }
func (f *fakeProjects) Count(context.Context) (int, error)      { return 0, nil }

// newTestClient builds a minimal Client for parseAndResolve / SetFilter
// testing. We don't open a real WS; the tests poke parseAndResolve
// directly.
func newTestClient(t *testing.T, slugs map[string]string) *Client {
	t.Helper()
	bySlug := make(map[string]*domain.Project, len(slugs))
	for slug, id := range slugs {
		bySlug[slug] = &domain.Project{ID: id, Slug: slug, Name: slug, CreatedAt: time.Now().UTC()}
	}
	return &Client{
		projects: &fakeProjects{bySlug: bySlug},
	}
}

func TestParseAndResolveHappyPath(t *testing.T) {
	c := newTestClient(t, map[string]string{
		"auth-svc": "p-auth",
		"billing":  "p-billing",
	})
	msg := []byte(`{"projects":["auth-svc","billing"],"services":["signin"],"levels":["Error","Fatal"]}`)
	got, err := c.parseAndResolve(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 2 || got.Projects[0] != "p-auth" || got.Projects[1] != "p-billing" {
		t.Fatalf("Projects = %v, want [p-auth p-billing]", got.Projects)
	}
	if len(got.Services) != 1 || got.Services[0] != "signin" {
		t.Fatalf("Services = %v", got.Services)
	}
	if len(got.Levels) != 2 {
		t.Fatalf("Levels = %v, want 2 entries", got.Levels)
	}
}

func TestParseAndResolveRejectsEmptyProjects(t *testing.T) {
	c := newTestClient(t, map[string]string{"auth-svc": "p-auth"})
	msg := []byte(`{"projects":[],"services":["x"]}`)
	if _, err := c.parseAndResolve(context.Background(), msg); err == nil {
		t.Fatal("expected error for empty projects array")
	}
	// Also: missing "projects" entirely (zero value is empty slice).
	if _, err := c.parseAndResolve(context.Background(), []byte(`{"services":["x"]}`)); err == nil {
		t.Fatal("expected error when projects field absent")
	}
}

func TestParseAndResolveRejectsBadSlug(t *testing.T) {
	c := newTestClient(t, map[string]string{"auth-svc": "p-auth"})
	msg := []byte(`{"projects":["Bad-Slug"]}`)
	if _, err := c.parseAndResolve(context.Background(), msg); err == nil {
		t.Fatal("expected error for uppercase slug")
	}
}

func TestParseAndResolveRejectsUnknownProject(t *testing.T) {
	c := newTestClient(t, map[string]string{"auth-svc": "p-auth"})
	msg := []byte(`{"projects":["missing"]}`)
	if _, err := c.parseAndResolve(context.Background(), msg); err == nil {
		t.Fatal("expected error for unknown slug")
	}
}

func TestParseAndResolveDropsUnknownLevels(t *testing.T) {
	// Unknown levels should be silently dropped (they'd never match
	// anyway). The frame is otherwise valid, so parsing succeeds.
	c := newTestClient(t, map[string]string{"auth-svc": "p-auth"})
	msg := []byte(`{"projects":["auth-svc"],"levels":["Error","Trace","Fatal","Bogus"]}`)
	got, err := c.parseAndResolve(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Levels) != 2 {
		t.Fatalf("Levels = %v, want exactly Error+Fatal", got.Levels)
	}
}

func TestParseAndResolveRejectsMalformedJSON(t *testing.T) {
	c := newTestClient(t, map[string]string{"auth-svc": "p-auth"})
	if _, err := c.parseAndResolve(context.Background(), []byte("not json")); err == nil {
		t.Fatal("expected error on malformed json")
	}
}
