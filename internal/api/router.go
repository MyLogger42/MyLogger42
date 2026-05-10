package api

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/silverbaq/mylogger42/internal/auth"
	"github.com/silverbaq/mylogger42/internal/store"
	"github.com/silverbaq/mylogger42/internal/ws"
)

// Server is the API layer. It owns no goroutines of its own; lifecycle
// (hub.Run, retention worker, last_used flusher) is managed by main.go.
type Server struct {
	store        store.LogStore
	keys         store.KeyStore
	projects     store.ProjectStore
	users        store.UserStore
	hub          *ws.Hub
	logger       *log.Logger
	startedAt    time.Time
	driver       string
	staticFS     fs.FS // optional embedded SPA assets
	pepper       []byte
	usage        *lastUsedThrottle
	signer       *auth.SessionSigner
	cookieSecure func(*http.Request) bool
}

// Config bundles the dependencies required to build a Server.
//
// The v1 APIKey field is gone — ingest auth is per-project in v2 via
// the requireProjectKey middleware, which needs the keystore + project
// store + HMAC pepper to verify a presented X-API-Key.
//
// Read endpoints and admin handlers (Phase 9) are gated behind
// requireSession, which uses Signer + CookieSecure.
type Config struct {
	Store        store.LogStore
	Keys         store.KeyStore
	Projects     store.ProjectStore
	Users        store.UserStore
	Hub          *ws.Hub
	Logger       *log.Logger
	Driver       string
	StaticFS     fs.FS
	Pepper       []byte
	Signer       *auth.SessionSigner
	CookieSecure func(*http.Request) bool
}

// NewServer wires up handler dependencies. It does not start anything.
func NewServer(cfg Config) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	cookieSecure := cfg.CookieSecure
	if cookieSecure == nil {
		// Conservative default: never mark Secure unless an explicit
		// resolver opts in. Local dev (http://localhost) just works.
		cookieSecure = func(*http.Request) bool { return false }
	}
	return &Server{
		store:        cfg.Store,
		keys:         cfg.Keys,
		projects:     cfg.Projects,
		users:        cfg.Users,
		hub:          cfg.Hub,
		logger:       logger,
		startedAt:    time.Now(),
		driver:       cfg.Driver,
		staticFS:     cfg.StaticFS,
		pepper:       cfg.Pepper,
		usage:        newLastUsedThrottle(cfg.Keys),
		signer:       cfg.Signer,
		cookieSecure: cookieSecure,
	}
}

// Throttle exposes the lastUsedThrottle so main.go can pass it to
// StartLastUsedFlusher. The flusher goroutine writes accumulated
// last_used_at updates to the DB on a 60s ticker — see middleware.go.
func (s *Server) Throttle() *lastUsedThrottle { return s.usage }

// Routes returns the configured http.Handler. Standard library mux is
// enough for our tiny route table — adding chi/gorilla/mux would be
// gratuitous.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Ingest endpoints — protected by the project-key middleware.
	// requireProjectKey resolves X-API-Key → Project, attaches it to ctx,
	// and rejects archived projects with 403.
	ingestAuth := requireProjectKey(s.keys, s.projects, s.pepper, s.usage)
	mux.Handle("POST /api/v1/logs", ingestAuth(http.HandlerFunc(s.handleIngestSingle)))
	mux.Handle("POST /api/v1/logs/batch", ingestAuth(http.HandlerFunc(s.handleIngestBatch)))

	// Read endpoints — gated behind the session middleware (admin login
	// required). Each handler resolves a required ?project=<slug> param
	// and scopes the underlying store query to that project.
	sessionAuth := requireSession(s.signer, s.cookieSecure)
	mux.Handle("GET /api/v1/logs", sessionAuth(http.HandlerFunc(s.handleQuery)))
	mux.Handle("GET /api/v1/logs/{id}", sessionAuth(http.HandlerFunc(s.handleGetByIDRoute)))
	mux.Handle("GET /api/v1/sources", sessionAuth(http.HandlerFunc(s.handleSources)))

	// Auth surface. login is public; logout + /me require a session.
	// (logout is also wrapped in requireSameOrigin per the spec's CSRF
	// posture — it's a state-mutating cookie-authenticated POST.)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.Handle("POST /api/v1/auth/logout",
		sessionAuth(requireSameOrigin(http.HandlerFunc(s.handleLogout))))
	mux.Handle("GET /api/v1/auth/me", sessionAuth(http.HandlerFunc(s.handleMe)))

	// Admin surface. Read methods get session only; write methods get
	// session + same-origin (CSRF defense in depth on top of SameSite).
	adminWrite := func(h http.HandlerFunc) http.Handler {
		return sessionAuth(requireSameOrigin(h))
	}
	adminRead := func(h http.HandlerFunc) http.Handler {
		return sessionAuth(h)
	}
	mux.Handle("POST /api/v1/admin/projects", adminWrite(s.handleCreateProject))
	mux.Handle("GET /api/v1/admin/projects", adminRead(s.handleListProjects))
	mux.Handle("GET /api/v1/admin/projects/{slug}", adminRead(s.handleGetProject))
	mux.Handle("PATCH /api/v1/admin/projects/{slug}", adminWrite(s.handleUpdateProject))
	mux.Handle("POST /api/v1/admin/projects/{slug}/archive", adminWrite(s.handleArchiveProject))
	mux.Handle("POST /api/v1/admin/projects/{slug}/unarchive", adminWrite(s.handleUnarchiveProject))
	mux.Handle("POST /api/v1/admin/projects/{slug}/keys", adminWrite(s.handleIssueKey))
	mux.Handle("GET /api/v1/admin/projects/{slug}/keys", adminRead(s.handleListKeys))
	mux.Handle("DELETE /api/v1/admin/projects/{slug}/keys/{keyId}", adminWrite(s.handleRevokeKey))

	// Public — health check stays open.
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)

	// WebSocket live-tail.
	mux.HandleFunc("GET /api/v1/logs/stream", s.handleStream)

	// Static SPA assets (optional). When the binary is built without the
	// embedded web/dist tree, staticFS is nil and we skip these routes.
	if s.staticFS != nil {
		mux.Handle("/", s.spaHandler())
	}

	// Outermost middleware: CORS first (so OPTIONS short-circuits before
	// logging), then access logging. The credential-aware CORS variant
	// is required because read/admin endpoints rely on the session
	// cookie — `Allow-Origin: *` + credentials is forbidden.
	return withCORSCredentials(withLogging(s.logger, mux))
}

// handleGetByIDRoute is the wrapper that pulls the path param off the
// 1.22 mux and delegates to the typed handler.
func (s *Server) handleGetByIDRoute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.handleGetByID(w, r, id)
}

// handleHealth implements GET /api/v1/health.
//
// 200 when the store is reachable, 503 when not. Uptime is reported in
// Go duration format (e.g. "4h32m"), trimmed of subsecond precision.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	uptime := time.Since(s.startedAt).Round(time.Second).String()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status":  "unhealthy",
			"storage": s.driver,
			"uptime":  uptime,
			"error":   err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"storage": s.driver,
		"uptime":  uptime,
	})
}

// handleStream upgrades the HTTP connection to a WebSocket and registers
// the new client with the hub.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		writeError(w, http.StatusServiceUnavailable, "live tail not enabled")
		return
	}
	ws.ServeWS(s.hub, s.logger, w, r)
}

// spaHandler serves the embedded React build. Unknown paths under the
// SPA root fall back to index.html so client-side routing works.
func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.staticFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't intercept API routes — they're registered separately and
		// this handler only matches paths the mux routes here.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		// If the requested file doesn't exist in the embedded FS, serve
		// index.html so React Router can resolve the route.
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" {
			clean = "index.html"
		}
		if _, err := fs.Stat(s.staticFS, clean); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

