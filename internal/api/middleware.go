package api

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mylogger42/mylogger42/internal/auth"
	"github.com/mylogger42/mylogger42/internal/domain"
	"github.com/mylogger42/mylogger42/internal/store"
)

// --- ctx keys ---------------------------------------------------------
//
// Typed keys, unexported, so other packages can't accidentally collide.
// Accessor functions below give the API handlers a clean interface to
// pull values back out without needing to know the key types.

type ctxKey int

const (
	ctxKeyProject ctxKey = iota
	ctxKeyKeyID
	ctxKeySession
)

// ProjectFromContext returns the Project stamped onto the request by
// requireProjectKey. Returns nil and false on a request that didn't go
// through that middleware (e.g. a read endpoint or an admin route).
func ProjectFromContext(ctx context.Context) (*domain.Project, bool) {
	p, ok := ctx.Value(ctxKeyProject).(*domain.Project)
	return p, ok
}

// KeyIDFromContext returns the project_keys.id of the credential that
// authenticated the current ingest request. Used for last_used_at
// throttling. Empty string if not set.
func KeyIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyKeyID).(string)
	return id
}

// SessionFromContext returns the verified session claims attached by
// requireSession.
func SessionFromContext(ctx context.Context) (*auth.Claims, bool) {
	c, ok := ctx.Value(ctxKeySession).(*auth.Claims)
	return c, ok
}

// --- ingest auth (X-API-Key → project) --------------------------------

// requireProjectKey is the v2 replacement for v1's requireAPIKey.
//
// Pipeline:
//
//  1. Parse X-API-Key as the canonical "mlk_<prefix>_<secret>" shape.
//     Malformed input returns 401 immediately.
//  2. Look up the active key by prefix in the keystore.
//     On miss, run a dummy HMAC computation so timing doesn't leak the
//     existence-or-not of the prefix.
//  3. Constant-time HMAC compare against the stored fingerprint.
//  4. Resolve the project. If archived, return 403 — archived projects
//     accept no new ingest.
//  5. Attach Project + key.ID to the request context, lazily refresh
//     last_used_at via the throttler, and call next.
//
// We intentionally do NOT short-circuit on the prefix-lookup miss to
// avoid a timing oracle. See FakeVerifyForTimingParity in internal/auth.
//
// The lookups are fronted by an authCache. Note what the cache does NOT
// skip: the HMAC compare still runs on every request (a cache hit saves
// the two DB round trips, never the crypto), and misses are cached too so
// an unknown prefix stays as fast as a known one.
//
// The archived-project check also runs on every request, but against the
// project SNAPSHOT in the cache entry — it is not a live read. What makes
// it fresh is invalidation: every admin mutation calls
// authCache.invalidateAll(), so an archive stops ingest on the next call.
// Archiving is API-only (the CLI has no archive subcommand), so that path
// is always immediate.
//
// The gap is mutations made by another process — `mylogger42 admin
// revoke-key` / `delete-project`, or a second server against the same
// database. Those are bounded by authCacheTTL, and only by it: a
// CLI-revoked key keeps being accepted until its entry expires. Closing
// that would mean a DB round trip per ingest, which is the cost this
// cache exists to remove; operators who need instant revocation should
// use the API.
func requireProjectKey(
	keys store.KeyStore,
	projects store.ProjectStore,
	pepper []byte,
	usage *lastUsedThrottle,
	cache *authCache,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := r.Header.Get("X-API-Key")
			prefix, secret, err := auth.ParseKey(raw)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid api key")
				return
			}

			ctx := r.Context()

			var (
				rec        *domain.ProjectKey
				storedHMAC string
				project    *domain.Project
				known      bool
			)
			if cached, hit := cache.get(prefix); hit {
				if !cached.known {
					// Cached miss. Same work as a live miss so the two
					// are indistinguishable by timing.
					auth.FakeVerifyForTimingParity(pepper)
					writeError(w, http.StatusUnauthorized, "invalid api key")
					return
				}
				rec, storedHMAC, project, known = cached.key, cached.hmac, cached.project, true
			}

			if !known {
				var lookupErr error
				rec, storedHMAC, lookupErr = keys.GetActiveByPrefix(ctx, prefix)
				if errors.Is(lookupErr, store.ErrNotFound) {
					cache.putMiss(prefix)
					// Burn equivalent CPU so timing doesn't reveal "no such
					// prefix" vs "wrong secret".
					auth.FakeVerifyForTimingParity(pepper)
					writeError(w, http.StatusUnauthorized, "invalid api key")
					return
				}
				if lookupErr != nil {
					// Storage error — log and fail closed. Never cached.
					writeError(w, http.StatusInternalServerError, "storage error")
					return
				}
				project, err = projects.GetByID(ctx, rec.ProjectID)
				if err != nil {
					// A FK constraint guarantees the row exists; if we hit
					// this, the DB is in an unexpected state.
					writeError(w, http.StatusInternalServerError, "storage error")
					return
				}
				cache.putHit(prefix, rec, storedHMAC, project)
			}

			if !auth.VerifyKey(pepper, secret, storedHMAC) {
				writeError(w, http.StatusUnauthorized, "invalid api key")
				return
			}

			// Not cached as a verdict — but `project` may be a cached
			// snapshot, so this is only as fresh as the entry. See the
			// invalidation / TTL note in the doc comment above.
			if project.ArchivedAt != nil {
				writeError(w, http.StatusForbidden, "project archived")
				return
			}

			ctx = context.WithValue(ctx, ctxKeyProject, project)
			ctx = context.WithValue(ctx, ctxKeyKeyID, rec.ID)
			r = r.WithContext(ctx)

			// Best-effort, non-blocking. The throttler swallows errors
			// internally — a failed last_used_at update must not fail
			// the ingest call.
			if usage != nil {
				usage.note(rec.ID)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// --- ingest auth cache ------------------------------------------------
//
// Ingest auth cost two DB queries per log before this: one to resolve the
// key by prefix, one to resolve its project. At any real ingest rate that
// dominated the write path, and on SQLite it contended with the single
// write connection.
//
// Three properties worth keeping when editing this:
//
//  1. Misses are cached as well as hits. A hit-only cache would make a
//     known prefix measurably faster than an unknown one, handing an
//     attacker a prefix-existence oracle — precisely what
//     FakeVerifyForTimingParity exists to prevent.
//  2. The cache stores facts (this key, this hmac, this project), never
//     verdicts. VerifyKey and the archived check still run per request.
//  3. Correctness does not depend on the TTL. Admin mutations invalidate
//     explicitly, so the TTL is only a backstop for changes made outside
//     this process (e.g. the admin CLI).

// cachedAuth is one resolved prefix. known=false is a cached miss.
type cachedAuth struct {
	known     bool
	key       *domain.ProjectKey
	hmac      string
	project   *domain.Project
	expiresAt time.Time
}

// authCacheTTL bounds staleness for mutations this process didn't make —
// the `mylogger42 admin` CLI writes to the same DB from another process.
const authCacheTTL = 60 * time.Second

// authCacheMaxEntries caps memory. Because misses are cached, an attacker
// spraying random prefixes would otherwise grow the map without bound.
const authCacheMaxEntries = 10_000

type authCache struct {
	mu      sync.RWMutex
	ttl     time.Duration
	entries map[string]cachedAuth
}

func newAuthCache(ttl time.Duration) *authCache {
	return &authCache{ttl: ttl, entries: make(map[string]cachedAuth)}
}

func (c *authCache) get(prefix string) (cachedAuth, bool) {
	c.mu.RLock()
	e, ok := c.entries[prefix]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expiresAt) {
		return cachedAuth{}, false
	}
	return e, true
}

func (c *authCache) putHit(prefix string, k *domain.ProjectKey, hmac string, p *domain.Project) {
	c.put(prefix, cachedAuth{known: true, key: k, hmac: hmac, project: p})
}

func (c *authCache) putMiss(prefix string) {
	c.put(prefix, cachedAuth{known: false})
}

func (c *authCache) put(prefix string, e cachedAuth) {
	e.expiresAt = time.Now().Add(c.ttl)
	c.mu.Lock()
	defer c.mu.Unlock()
	// ponytail: drop everything at the cap instead of tracking LRU. The
	// cache refills in one request per active key; swap in an LRU only if
	// a deployment shows thrashing.
	if len(c.entries) >= authCacheMaxEntries {
		c.entries = make(map[string]cachedAuth)
	}
	c.entries[prefix] = e
}

// invalidateAll drops every entry. Called after any admin mutation that
// could change a key or project.
//
// ponytail: whole-map invalidation rather than a keyID→prefix reverse
// index. Admin writes are rare and the cache refills in one request; the
// index would be more state to keep correct for no measurable gain.
func (c *authCache) invalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]cachedAuth)
}

// lastUsedThrottle keeps an in-memory map of "the last time we wrote
// last_used_at for this key." If the entry is younger than throttleEvery
// we skip the DB write. State is non-durable — a restart loses it,
// which means at most one extra write per active key per restart.
//
// The throttler runs no goroutine of its own here; main.go starts a
// flusher (StartLastUsedFlusher) that drains pending writes on a 60s
// ticker. note() just records the intent.
//
// Spec note: the spec puts ownership of the map in the keystore. We
// keep it in the API layer for v2 because (a) it's the only consumer,
// and (b) the keystore impls are the SQL boundary and shouldn't carry
// state. The flusher pulls from here.
type lastUsedThrottle struct {
	mu      sync.Mutex
	pending map[string]time.Time // keyID → "wants update at"
	keys    store.KeyStore
}

const throttleEvery = 60 * time.Second

func newLastUsedThrottle(keys store.KeyStore) *lastUsedThrottle {
	return &lastUsedThrottle{
		pending: make(map[string]time.Time),
		keys:    keys,
	}
}

// note records that a key was used. The actual DB write is deferred to
// the flusher.
func (t *lastUsedThrottle) note(keyID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending[keyID] = time.Now().UTC()
}

// flush writes the pending updates to the DB and clears the map. Safe
// to call concurrently with note(); flush() drains under the same mutex.
func (t *lastUsedThrottle) flush(ctx context.Context) {
	t.mu.Lock()
	if len(t.pending) == 0 {
		t.mu.Unlock()
		return
	}
	pending := t.pending
	t.pending = make(map[string]time.Time)
	t.mu.Unlock()

	for id, when := range pending {
		_ = t.keys.NoteUsage(ctx, id, when) // best-effort
	}
}

// StartLastUsedFlusher launches a goroutine that drains the throttle
// map every throttleEvery. Stops when ctx is cancelled.
//
// Exposed so main.go can wire it up next to hub.Run / worker.Run.
func StartLastUsedFlusher(ctx context.Context, t *lastUsedThrottle) {
	go func() {
		tick := time.NewTicker(throttleEvery)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				// Drain once on shutdown so we don't lose the last
				// minute of usage records.
				t.flush(context.Background())
				return
			case <-tick.C:
				t.flush(ctx)
			}
		}
	}()
}

// --- session auth (cookie → claims) -----------------------------------

// sessionCookieName is the documented cookie name. Kept as a constant
// so the auth handlers (login/logout) reference the same string.
const sessionCookieName = "session"

// requireSession verifies the session JWT cookie and attaches the
// parsed claims to the request context. On any failure (missing cookie,
// bad signature, expired) it returns 401 and stops.
//
// Sliding renewal: if the claims' iat is more than renewAfter old, we
// re-issue the cookie in the response so an active user never has to
// log in again until they go silent for the full TTL.
func requireSession(
	signer *auth.SessionSigner,
	cookieSecure func(*http.Request) bool,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(sessionCookieName)
			if err != nil || c.Value == "" {
				writeError(w, http.StatusUnauthorized, "session required")
				return
			}
			now := time.Now().UTC()
			claims, err := signer.Verify(c.Value, now)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "session invalid")
				return
			}

			// Sliding renewal — only re-issue when iat is old enough,
			// to avoid setting a fresh cookie on every single request.
			if signer.ShouldRenew(claims.IssuedAt, now) {
				if newTok, err := signer.Issue(claims.Subject, claims.Username, now); err == nil {
					http.SetCookie(w, buildSessionCookie(newTok, cookieSecure(r)))
				}
			}

			ctx := context.WithValue(r.Context(), ctxKeySession, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// buildSessionCookie returns the canonical session cookie. Used by both
// the renewal path in requireSession and the login handler. SameSite=Lax
// blocks cross-site form POSTs; HttpOnly prevents JS access. Secure is
// set per the cookieSecure resolver (auto-detect from r.TLS or trust
// X-Forwarded-Proto when configured).
func buildSessionCookie(value string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((7 * 24 * time.Hour).Seconds()),
	}
}

// CookieSecureResolver returns a function the middleware can call to
// decide whether to mark cookies Secure. The result depends on:
//
//   - request was over TLS (r.TLS != nil), OR
//   - operator opted into TRUST_FORWARDED_PROTO and the request carries
//     X-Forwarded-Proto: https (i.e. terminated TLS at a reverse proxy).
//
// Otherwise (plain HTTP local dev), Secure is false so login works on
// http://localhost.
func CookieSecureResolver(trustForwardedProto bool) func(*http.Request) bool {
	return func(r *http.Request) bool {
		if r.TLS != nil {
			return true
		}
		if trustForwardedProto && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			return true
		}
		return false
	}
}

// --- CSRF (Origin/Host match) -----------------------------------------

// requireSameOrigin is a belt-and-suspenders check on top of
// SameSite=Lax for admin write endpoints. If an Origin header is
// present (mutating browser methods always send it), it must match Host.
// Missing Origin (e.g. direct curl, top-level navigation) is allowed —
// SameSite + the cookie's HttpOnly already rule out the relevant attack
// surfaces in those paths.
func requireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		// Origin is "scheme://host[:port]"; we only care about host[:port].
		// Find the third "/" or end-of-string.
		host := origin
		if idx := strings.Index(origin, "://"); idx >= 0 {
			host = origin[idx+3:]
		}
		if slash := strings.Index(host, "/"); slash >= 0 {
			host = host[:slash]
		}
		if !strings.EqualFold(host, r.Host) {
			writeError(w, http.StatusForbidden, "origin mismatch")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- CORS --------------------------------------------------------------

// withCORS adds permissive CORS headers for ingest endpoints —
// `Access-Control-Allow-Origin: *` lets any client POST with X-API-Key
// without cookies. Used on the ingest routes only; the admin/auth
// routes that need cookies use withCORSCredentials below.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withCORSCredentials is the variant the SPA's auth/admin/read calls
// use. CORS specs forbid `Allow-Origin: *` together with credentials,
// so we echo the request's Origin (when present) and add Vary: Origin
// so caches don't poison cross-origin responses.
func withCORSCredentials(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- access logging ---------------------------------------------------

// withLogging emits a one-line access log per request. We deliberately
// don't pull in a structured logger — the server's own observability is
// minimal in v1.
func withLogging(logger interface {
	Printf(format string, v ...any)
}, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rr := &recordingWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rr, r)
		logger.Printf("http %s %s -> %d (%s)", r.Method, r.URL.Path, rr.status, time.Since(start))
	})
}

// recordingWriter captures the response status for the access log. It
// transparently passes through Hijack so the WebSocket upgrade on
// /api/v1/logs/stream can take over the underlying TCP connection — the
// stdlib's default ResponseWriter is a Hijacker but interface-wrapping
// hides it, so we forward explicitly.
type recordingWriter struct {
	http.ResponseWriter
	status int
}

func (r *recordingWriter) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *recordingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	// A successful hijack means we never see a WriteHeader call, so the
	// access log will report the default 200. That's fine — the WS
	// upgrade itself responds 101 to the client; only our visibility is
	// approximate.
	return hj.Hijack()
}
