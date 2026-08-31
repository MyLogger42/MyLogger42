package api

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mylogger42/mylogger42/internal/auth"
	"github.com/mylogger42/mylogger42/internal/domain"
	"github.com/mylogger42/mylogger42/internal/store"
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

	// lookups counts GetActiveByPrefix calls so the cache tests can
	// assert DB round trips were actually avoided.
	lookups int
}

func (f *fakeKeyStore) Create(context.Context, domain.ProjectKey, string) error {
	return store.ErrNotFound
}
func (f *fakeKeyStore) GetActiveByPrefix(_ context.Context, prefix string) (*domain.ProjectKey, string, error) {
	f.lookups++
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
	// lookups counts GetByID calls, the second of the two queries the
	// auth cache exists to remove.
	lookups int
}

func (f *fakeProjectStore) Create(context.Context, domain.Project) error      { return store.ErrNotFound }
func (f *fakeProjectStore) GetBySlug(context.Context, string) (*domain.Project, error) {
	return nil, store.ErrNotFound
}
func (f *fakeProjectStore) GetByID(_ context.Context, id string) (*domain.Project, error) {
	f.lookups++
	p, ok := f.byID[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	// A COPY, like every real store: returning the map's pointer would
	// make anything holding the result (the auth cache, say) alias this
	// store, so a later mutation here would appear to be seen live and a
	// staleness bug would test green.
	cp := *p
	return &cp, nil
}
func (f *fakeProjectStore) List(context.Context, bool) ([]domain.Project, error) {
	return nil, store.ErrNotFound
}
func (f *fakeProjectStore) Update(context.Context, string, string, *int) (*domain.Project, error) {
	return nil, store.ErrNotFound
}
func (f *fakeProjectStore) Archive(_ context.Context, slug string) (*domain.Project, error) {
	for _, p := range f.byID {
		if p.Slug == slug {
			now := time.Now().UTC()
			p.ArchivedAt = &now
			cp := *p
			return &cp, nil
		}
	}
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
	*authCache,
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

	cache := newAuthCache(authCacheTTL)
	mw := requireProjectKey(keys, projects, pepper, usage, cache)(downstream)
	return mw, keys, projects, usage, pepper, cache
}

func TestRequireProjectKeyHappyPath(t *testing.T) {
	mw, keys, _, usage, _, _ := setupProjectKeyMW(t)

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
	mw, _, _, _, _, _ := setupProjectKeyMW(t)

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
	mw, _, _, _, _, _ := setupProjectKeyMW(t)

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
	mw, _, _, _, _, _ := setupProjectKeyMW(t)

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
	mw, _, projects, _, _, _ := setupProjectKeyMW(t)
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
	// Issue against the real clock — requireSession verifies with
	// time.Now(), so a fixed date here rots into an expired token.
	tok, err := signer.Issue("u-1", "admin", time.Now().UTC())
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

// --- auth cache -------------------------------------------------------

const (
	validKey   = "mlk_abcdefghijkl_zyxwvutsrqponmlkjihgfedcba234567"
	unknownKey = "mlk_zzzzzzzzzzzz_zyxwvutsrqponmlkjihgfedcba234567"
)

// ingest fires one request through the middleware and returns the status.
func ingest(t *testing.T, mw http.Handler, apiKey string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/logs", nil)
	req.Header.Set("X-API-Key", apiKey)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	return rr.Code
}

func TestAuthCacheAvoidsRepeatLookups(t *testing.T) {
	mw, keys, projects, _, _, _ := setupProjectKeyMW(t)

	for i := 0; i < 5; i++ {
		if got := ingest(t, mw, validKey); got != http.StatusNoContent {
			t.Fatalf("request %d: status = %d, want 204", i, got)
		}
	}
	// Two queries total, not two per request.
	if keys.lookups != 1 {
		t.Errorf("key lookups = %d, want 1", keys.lookups)
	}
	if projects.lookups != 1 {
		t.Errorf("project lookups = %d, want 1", projects.lookups)
	}
}

// A hit-only cache would make an unknown prefix measurably slower than a
// known one, which is a prefix-existence oracle. Misses must be cached too.
func TestAuthCacheCachesMisses(t *testing.T) {
	mw, keys, _, _, _, _ := setupProjectKeyMW(t)

	for i := 0; i < 5; i++ {
		if got := ingest(t, mw, unknownKey); got != http.StatusUnauthorized {
			t.Fatalf("request %d: status = %d, want 401", i, got)
		}
	}
	if keys.lookups != 1 {
		t.Errorf("key lookups for unknown prefix = %d, want 1 (misses must be cached)", keys.lookups)
	}
}

// The cache stores facts, not verdicts: a wrong secret on a cached prefix
// must still fail the HMAC compare.
func TestAuthCacheStillVerifiesHMAC(t *testing.T) {
	mw, _, _, _, _, _ := setupProjectKeyMW(t)

	if got := ingest(t, mw, validKey); got != http.StatusNoContent {
		t.Fatalf("priming request: status = %d, want 204", got)
	}
	wrongSecret := "mlk_abcdefghijkl_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if got := ingest(t, mw, wrongSecret); got != http.StatusUnauthorized {
		t.Fatalf("wrong secret on cached prefix: status = %d, want 401", got)
	}
}

func TestAuthCacheExpires(t *testing.T) {
	mw, keys, _, _, _, cache := setupProjectKeyMW(t)

	if got := ingest(t, mw, validKey); got != http.StatusNoContent {
		t.Fatalf("priming request: status = %d, want 204", got)
	}
	// Force expiry rather than sleeping for the real TTL.
	cache.mu.Lock()
	for k, e := range cache.entries {
		e.expiresAt = time.Now().Add(-time.Second)
		cache.entries[k] = e
	}
	cache.mu.Unlock()

	if got := ingest(t, mw, validKey); got != http.StatusNoContent {
		t.Fatalf("post-expiry request: status = %d, want 204", got)
	}
	if keys.lookups != 2 {
		t.Errorf("key lookups = %d, want 2 (entry should have expired)", keys.lookups)
	}
}

// Revocation must take effect on the very next request, not after the TTL.
func TestAuthCacheInvalidateAllHonoursRevocation(t *testing.T) {
	mw, keys, _, _, _, cache := setupProjectKeyMW(t)

	if got := ingest(t, mw, validKey); got != http.StatusNoContent {
		t.Fatalf("priming request: status = %d, want 204", got)
	}
	// Revoke: GetActiveByPrefix stops returning the key.
	delete(keys.prefixes, "abcdefghijkl")
	cache.invalidateAll()

	if got := ingest(t, mw, validKey); got != http.StatusUnauthorized {
		t.Fatalf("after revoke+invalidate: status = %d, want 401", got)
	}
}

// The archived check runs per request, but against the project SNAPSHOT
// in the cache — so what actually stops ingest is invalidation (for
// mutations this process makes) or the TTL (for mutations it doesn't,
// e.g. the admin CLI). This test pins all three halves of that, because
// the stale window is the part that is easy to lose.
func TestAuthCacheArchivedProjectStaleUntilInvalidated(t *testing.T) {
	mw, _, projects, _, _, cache := setupProjectKeyMW(t)

	if got := ingest(t, mw, validKey); got != http.StatusNoContent {
		t.Fatalf("priming request: status = %d, want 204", got)
	}
	if _, err := projects.Archive(context.Background(), "auth-svc"); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	// Documented staleness: the cached snapshot predates the archive.
	// If this ever returns 403, the cache started re-reading the project
	// live and the perf premise changed — update the docs, don't just
	// flip the assertion.
	if got := ingest(t, mw, validKey); got != http.StatusNoContent {
		t.Fatalf("cached snapshot: status = %d, want 204 (stale window)", got)
	}

	// Invalidation is the mechanism, and it's what admin.go calls.
	cache.invalidateAll()
	if got := ingest(t, mw, validKey); got != http.StatusForbidden {
		t.Fatalf("after invalidate: status = %d, want 403", got)
	}
}

// TTL is the backstop for an archive this process never saw — the admin
// CLI writing to the same database.
func TestAuthCacheArchivedProjectRejectedAfterTTL(t *testing.T) {
	mw, _, projects, _, _, cache := setupProjectKeyMW(t)

	if got := ingest(t, mw, validKey); got != http.StatusNoContent {
		t.Fatalf("priming request: status = %d, want 204", got)
	}
	if _, err := projects.Archive(context.Background(), "auth-svc"); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	// Force expiry rather than sleeping out the real TTL.
	cache.mu.Lock()
	for k, e := range cache.entries {
		e.expiresAt = time.Now().Add(-time.Second)
		cache.entries[k] = e
	}
	cache.mu.Unlock()

	if got := ingest(t, mw, validKey); got != http.StatusForbidden {
		t.Fatalf("after TTL expiry: status = %d, want 403", got)
	}
}

// The admin archive handler is what makes the stale window zero in
// practice; nothing else asserted that it invalidates.
func TestArchiveProjectHandlerInvalidatesAuthCache(t *testing.T) {
	mw, _, projects, _, _, cache := setupProjectKeyMW(t)
	srv := &Server{
		projects:  projects,
		authCache: cache,
		logger:    log.New(io.Discard, "", 0),
	}

	if got := ingest(t, mw, validKey); got != http.StatusNoContent {
		t.Fatalf("priming request: status = %d, want 204", got)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/projects/auth-svc/archive", nil)
	req.SetPathValue("slug", "auth-svc")
	rr := httptest.NewRecorder()
	srv.handleArchiveProject(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("archive: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	if got := ingest(t, mw, validKey); got != http.StatusForbidden {
		t.Fatalf("ingest after archive: status = %d, want 403", got)
	}
}

func TestAuthCacheBoundsEntries(t *testing.T) {
	cache := newAuthCache(authCacheTTL)
	for i := 0; i < authCacheMaxEntries+100; i++ {
		cache.putMiss(fmt.Sprintf("prefix-%d", i))
	}
	cache.mu.RLock()
	n := len(cache.entries)
	cache.mu.RUnlock()
	if n > authCacheMaxEntries {
		t.Fatalf("cache holds %d entries, want <= %d", n, authCacheMaxEntries)
	}
}
