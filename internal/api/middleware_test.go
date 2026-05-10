package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/silverbaq/mylogger42/internal/auth"
	"github.com/silverbaq/mylogger42/internal/domain"
	"github.com/silverbaq/mylogger42/internal/store"
)

// --- fakes ------------------------------------------------------------
//
// The middleware tests don't need a real DB — fakes that satisfy
// store.KeyStore and store.ProjectStore for the methods the middleware
// actually calls are enough. Other methods return ErrNotFound to make
// accidental calls obvious in test failures.

type fakeKeyStore struct {
	prefixes map[string]struct {
		key  *domain.ProjectKey
		hmac string
	}
	notes []string // keyIDs noted via NoteUsage
}

func (f *fakeKeyStore) Create(context.Context, domain.ProjectKey, string) error {
	return store.ErrNotFound
}
func (f *fakeKeyStore) GetActiveByPrefix(_ context.Context, prefix string) (*domain.ProjectKey, string, error) {
	rec, ok := f.prefixes[prefix]
	if !ok {
		return nil, "", store.ErrNotFound
	}
	return rec.key, rec.hmac, nil
}
func (f *fakeKeyStore) ListByProject(context.Context, string, bool) ([]domain.ProjectKey, error) {
	return nil, store.ErrNotFound
}
func (f *fakeKeyStore) GetByID(context.Context, string) (*domain.ProjectKey, error) {
	return nil, store.ErrNotFound
}
func (f *fakeKeyStore) Revoke(context.Context, string) error                    { return store.ErrNotFound }
func (f *fakeKeyStore) NoteUsage(_ context.Context, id string, _ time.Time) error {
	f.notes = append(f.notes, id)
	return nil
}

type fakeProjectStore struct {
	byID map[string]*domain.Project
}

func (f *fakeProjectStore) Create(context.Context, domain.Project) error      { return store.ErrNotFound }
func (f *fakeProjectStore) GetBySlug(context.Context, string) (*domain.Project, error) {
	return nil, store.ErrNotFound
}
func (f *fakeProjectStore) GetByID(_ context.Context, id string) (*domain.Project, error) {
	p, ok := f.byID[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return p, nil
}
func (f *fakeProjectStore) List(context.Context, bool) ([]domain.Project, error) {
	return nil, store.ErrNotFound
}
func (f *fakeProjectStore) Update(context.Context, string, string, *int) (*domain.Project, error) {
	return nil, store.ErrNotFound
}
func (f *fakeProjectStore) Archive(context.Context, string) (*domain.Project, error) {
	return nil, store.ErrNotFound
}
func (f *fakeProjectStore) Unarchive(context.Context, string) (*domain.Project, error) {
	return nil, store.ErrNotFound
}
func (f *fakeProjectStore) Delete(context.Context, string) error { return store.ErrNotFound }
func (f *fakeProjectStore) Count(context.Context) (int, error)   { return 0, nil }

// helper that builds the middleware + a downstream handler that asserts
// the project came through on the context.

func setupProjectKeyMW(t *testing.T) (
	http.Handler,
	*fakeKeyStore,
	*fakeProjectStore,
	*lastUsedThrottle,
	[]byte, // pepper
) {
	t.Helper()
	pepper := []byte("pepper")
	proj := &domain.Project{ID: "proj-1", Slug: "auth-svc", Name: "Auth"}
	keyRec := &domain.ProjectKey{ID: "key-1", ProjectID: proj.ID, Prefix: "abcdefghijkl", Label: "ci"}
	storedHMAC := auth.ComputeHMAC(pepper, "zyxwvutsrqponmlkjihgfedcba234567")

	keys := &fakeKeyStore{
		prefixes: map[string]struct {
			key  *domain.ProjectKey
			hmac string
		}{
			"abcdefghijkl": {keyRec, storedHMAC},
		},
	}
	projects := &fakeProjectStore{
		byID: map[string]*domain.Project{proj.ID: proj},
	}
	usage := newLastUsedThrottle(keys)

	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := ProjectFromContext(r.Context())
		if !ok || got.ID != proj.ID {
			http.Error(w, "project not on ctx", http.StatusInternalServerError)
			return
		}
		if KeyIDFromContext(r.Context()) != "key-1" {
			http.Error(w, "key id not on ctx", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mw := requireProjectKey(keys, projects, pepper, usage)(downstream)
	return mw, keys, projects, usage, pepper
}

func TestRequireProjectKeyHappyPath(t *testing.T) {
	mw, keys, _, usage, _ := setupProjectKeyMW(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/logs", nil)
	req.Header.Set("X-API-Key", "mlk_abcdefghijkl_zyxwvutsrqponmlkjihgfedcba234567")
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	// usage.note() defers the actual DB write to flush(); drain it here
	// the way the real flusher does.
	usage.flush(context.Background())
	if len(keys.notes) != 1 || keys.notes[0] != "key-1" {
		t.Fatalf("usage notes = %v, want [key-1]", keys.notes)
	}
}

func TestRequireProjectKeyMalformed(t *testing.T) {
	mw, _, _, _, _ := setupProjectKeyMW(t)

	for _, bad := range []string{"", "garbage", "mlk_short_x", "wrong_prefix_secret"} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/logs", nil)
		req.Header.Set("X-API-Key", bad)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("X-API-Key=%q: status = %d, want 401", bad, rr.Code)
		}
	}
}

func TestRequireProjectKeyUnknownPrefix(t *testing.T) {
	mw, _, _, _, _ := setupProjectKeyMW(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/logs", nil)
	// Right shape, prefix not in the fake's map.
	req.Header.Set("X-API-Key", "mlk_zzzzzzzzzzzz_zyxwvutsrqponmlkjihgfedcba234567")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestRequireProjectKeyWrongSecret(t *testing.T) {
	mw, _, _, _, _ := setupProjectKeyMW(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/logs", nil)
	// Right prefix, wrong secret (still 32 base32-lowercase chars).
	req.Header.Set("X-API-Key", "mlk_abcdefghijkl_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestRequireProjectKeyArchivedProject(t *testing.T) {
	mw, _, projects, _, _ := setupProjectKeyMW(t)
	// Archive the project after setup.
	now := time.Now().UTC()
	projects.byID["proj-1"].ArchivedAt = &now

	req := httptest.NewRequest(http.MethodPost, "/api/v1/logs", nil)
	req.Header.Set("X-API-Key", "mlk_abcdefghijkl_zyxwvutsrqponmlkjihgfedcba234567")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("archived project: status = %d, want 403", rr.Code)
	}
}

// --- session middleware ---

func TestRequireSessionHappyPath(t *testing.T) {
	signer := auth.NewSessionSigner([]byte("secret"))
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	tok, err := signer.Issue("u-1", "admin", now)
	if err != nil {
		t.Fatal(err)
	}

	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := SessionFromContext(r.Context())
		if !ok || c.Subject != "u-1" || c.Username != "admin" {
			http.Error(w, "claims missing", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mw := requireSession(signer, func(*http.Request) bool { return false })(downstream)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
}

func TestRequireSessionMissingCookie(t *testing.T) {
	signer := auth.NewSessionSigner([]byte("secret"))
	mw := requireSession(signer, func(*http.Request) bool { return false })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			t.Fatal("downstream should not be called")
		}),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestRequireSessionInvalidToken(t *testing.T) {
	signer := auth.NewSessionSigner([]byte("secret"))
	mw := requireSession(signer, func(*http.Request) bool { return false })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			t.Fatal("downstream should not be called")
		}),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "garbage"})
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

// --- origin check ---

func TestRequireSameOrigin(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	mw := requireSameOrigin(next)

	cases := []struct {
		name       string
		host       string
		origin     string
		wantStatus int
	}{
		{"no origin allowed", "localhost:8080", "", http.StatusNoContent},
		{"matching origin", "localhost:8080", "http://localhost:8080", http.StatusNoContent},
		{"different host", "localhost:8080", "http://evil.example", http.StatusForbidden},
		{"different port", "localhost:8080", "http://localhost:9999", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/projects", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rr := httptest.NewRecorder()
			mw.ServeHTTP(rr, req)
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusNoContent && !called {
				t.Fatal("downstream not called")
			}
			if tc.wantStatus == http.StatusForbidden && called {
				t.Fatal("downstream called despite origin mismatch")
			}
		})
	}
}

// --- last_used throttle ---

func TestLastUsedThrottleNoteAndFlush(t *testing.T) {
	keys := &fakeKeyStore{}
	t1 := newLastUsedThrottle(keys)

	t1.note("k1")
	t1.note("k2")
	t1.note("k1") // overwrites timestamp; still 2 entries
	t1.flush(context.Background())

	if len(keys.notes) != 2 {
		t.Fatalf("flushed %d updates, want 2", len(keys.notes))
	}
	// After flush the pending map is empty.
	t1.flush(context.Background())
	if len(keys.notes) != 2 {
		t.Fatalf("second flush wrote extra: %d", len(keys.notes))
	}
}
