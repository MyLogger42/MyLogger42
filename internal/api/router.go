package api

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/silverbaq/mylogger42/internal/store"
	"github.com/silverbaq/mylogger42/internal/ws"
)

// Server is the API layer. It owns no goroutines of its own; lifecycle
// (hub.Run, retention worker) is managed by main.go.
type Server struct {
	store     store.LogStore
	hub       *ws.Hub
	apiKey    string
	logger    *log.Logger
	startedAt time.Time
	driver    string
	staticFS  fs.FS // optional embedded SPA assets
}

// Config bundles the dependencies required to build a Server.
type Config struct {
	Store    store.LogStore
	Hub      *ws.Hub
	APIKey   string
	Logger   *log.Logger
	Driver   string
	StaticFS fs.FS
}

// NewServer wires up handler dependencies. It does not start anything.
func NewServer(cfg Config) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		store:     cfg.Store,
		hub:       cfg.Hub,
		apiKey:    cfg.APIKey,
		logger:    logger,
		startedAt: time.Now(),
		driver:    cfg.Driver,
		staticFS:  cfg.StaticFS,
	}
}

// Routes returns the configured http.Handler. Standard library mux is
// enough for our tiny route table — adding chi/gorilla/mux would be
// gratuitous.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Auth-protected ingest endpoints.
	mux.Handle("POST /api/v1/logs", requireAPIKey(s.apiKey, http.HandlerFunc(s.handleIngestSingle)))
	mux.Handle("POST /api/v1/logs/batch", requireAPIKey(s.apiKey, http.HandlerFunc(s.handleIngestBatch)))

	// Read endpoints (no auth per spec).
	mux.HandleFunc("GET /api/v1/logs", s.handleQuery)
	mux.HandleFunc("GET /api/v1/logs/{id}", s.handleGetByIDRoute)
	mux.HandleFunc("GET /api/v1/sources", s.handleSources)
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)

	// WebSocket live-tail.
	mux.HandleFunc("GET /api/v1/logs/stream", s.handleStream)

	// Static SPA assets (optional). When the binary is built without the
	// embedded web/dist tree, staticFS is nil and we skip these routes.
	if s.staticFS != nil {
		mux.Handle("/", s.spaHandler())
	}

	// Outermost middleware: CORS first (so OPTIONS short-circuits before
	// logging), then access logging.
	return withCORS(withLogging(s.logger, mux))
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

