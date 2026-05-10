// Command server is the MyLogger42 HTTP entry point.
//
// It wires up config, storage, the WebSocket hub, the retention worker,
// and the HTTP API, then blocks until SIGINT/SIGTERM.
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/silverbaq/mylogger42/internal/admin"
	"github.com/silverbaq/mylogger42/internal/api"
	"github.com/silverbaq/mylogger42/internal/auth"
	"github.com/silverbaq/mylogger42/internal/domain"
	"github.com/silverbaq/mylogger42/internal/retention"
	"github.com/silverbaq/mylogger42/internal/store"
	"github.com/silverbaq/mylogger42/internal/ws"

	mylogger42 "github.com/silverbaq/mylogger42"
)

func main() {
	// Subcommand dispatch: `mylogger42 admin <sub>` runs the CLI surface
	// rather than starting the server. Two of the subcommands
	// (generate-secrets, hash-password) need no env at all; the others
	// share the server's loadConfig so DB-touching env vars are
	// validated identically. We split the dispatch on that line.
	if len(os.Args) >= 2 && os.Args[1] == "admin" {
		runAdmin(os.Args[2:])
		return
	}

	logger := log.New(os.Stdout, "mylogger42 ", log.LstdFlags|log.Lmsgprefix)

	cfg, err := loadConfig()
	if err != nil {
		logger.Fatalf("config: %v", err)
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger.Printf("starting mylogger42 (driver=%s, port=%d, retention=%dd)", cfg.DBDriver, cfg.HTTPPort, cfg.RetentionDays)

	openCtx, openCancel := context.WithTimeout(rootCtx, 10*time.Second)
	stores, err := store.OpenStores(openCtx, store.Driver(cfg.DBDriver), cfg.DBDSN)
	openCancel()
	if err != nil {
		logger.Fatalf("storage: %v", err)
	}
	defer func() {
		if err := stores.Logs.Close(); err != nil {
			logger.Printf("storage close: %v", err)
		}
	}()

	bootstrapCtx, bootstrapCancel := context.WithTimeout(rootCtx, 5*time.Second)
	if err := bootstrapAdminUser(bootstrapCtx, stores.Users, cfg, logger); err != nil {
		logger.Fatalf("bootstrap admin: %v", err)
	}
	bootstrapCancel()

	hub := ws.NewHub(logger)
	go hub.Run(rootCtx)

	worker := retention.New(stores.Logs, stores.Projects, cfg.RetentionDays, logger)
	go worker.Run(rootCtx)

	staticFS, err := mylogger42.StaticAssets()
	if err != nil {
		// Not fatal — the server still serves the API. We just log so
		// operators know the SPA isn't being served from this binary.
		logger.Printf("static assets unavailable: %v", err)
	}

	srv := api.NewServer(api.Config{
		Store:        stores.Logs,
		Keys:         stores.Keys,
		Projects:     stores.Projects,
		Users:        stores.Users,
		Hub:          hub,
		Logger:       logger,
		Driver:       cfg.DBDriver,
		StaticFS:     staticFS,
		Pepper:       cfg.KeyPepper,
		Signer:       auth.NewSessionSigner(cfg.SessionSecret),
		CookieSecure: api.CookieSecureResolver(cfg.TrustForwardedProto),
	})

	// Drains the in-memory last_used_at updates accumulated by ingest
	// requests and writes them to the DB on a 60s ticker. Stops when
	// rootCtx is cancelled (and drains once on the way out).
	api.StartLastUsedFlusher(rootCtx, srv.Throttle())

	httpServer := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.HTTPPort),
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		// We intentionally leave WriteTimeout unset because the WebSocket
		// handler relies on long-lived connections; per-handler deadlines
		// inside ws/client.go bound writes there.
	}

	go func() {
		logger.Printf("http listening on %s", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("http: %v", err)
		}
	}()

	<-rootCtx.Done()
	logger.Printf("shutdown signal received")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Printf("http shutdown: %v", err)
	}
}

// config holds the runtime configuration parsed from environment vars.
//
// In v2 the single INGEST_API_KEY is gone — ingest auth is per-project
// and lives in the project_keys table. Two new required secrets:
//
//   KEY_PEPPER     — HMAC pepper for project-key fingerprints.
//   SESSION_SECRET — HS256 secret for admin session JWTs.
//
// Both are validated as ≥32 hex bytes. ADMIN_USERNAME and
// ADMIN_PASSWORD_HASH are optional bootstrap-only values; on the first
// start when users is empty AND both are set, an admin row is inserted.
// On subsequent starts they're ignored.
type config struct {
	DBDriver            string
	DBDSN               string
	RetentionDays       int
	HTTPPort            int
	LogLevel            string
	KeyPepper           []byte
	SessionSecret       []byte
	AdminUsername       string
	AdminPasswordHash   string
	TrustForwardedProto bool
}

func loadConfig() (config, error) {
	cfg := config{
		DBDriver:      getEnv("DB_DRIVER", "sqlite"),
		DBDSN:         getEnv("DB_DSN", "./data/mylogger42.db"),
		HTTPPort:      8080,
		RetentionDays: 30,
		LogLevel:      strings.ToLower(getEnv("LOG_LEVEL", "info")),
	}

	pepper, err := decodeHexSecret("KEY_PEPPER")
	if err != nil {
		return cfg, err
	}
	cfg.KeyPepper = pepper

	secret, err := decodeHexSecret("SESSION_SECRET")
	if err != nil {
		return cfg, err
	}
	cfg.SessionSecret = secret

	cfg.AdminUsername = os.Getenv("ADMIN_USERNAME")
	cfg.AdminPasswordHash = os.Getenv("ADMIN_PASSWORD_HASH")
	cfg.TrustForwardedProto = parseBool(os.Getenv("TRUST_FORWARDED_PROTO"))

	if v := os.Getenv("HTTP_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, errors.New("HTTP_PORT must be an integer")
		}
		cfg.HTTPPort = n
	}
	if v := os.Getenv("RETENTION_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, errors.New("RETENTION_DAYS must be an integer")
		}
		cfg.RetentionDays = n
	}
	return cfg, nil
}

// decodeHexSecret reads `name` from the env, decodes it as hex, and
// validates the byte length is ≥32. Returns an error with the env-var
// name in the message so config failures are easy to diagnose.
//
// The 32-byte floor is the v2 spec's security minimum for both the HMAC
// pepper and the session JWT secret. Below that, brute-force becomes
// tractable for a determined attacker. Operators should run
// `mylogger42 admin generate-secrets` (Phase 13) for fresh values.
func decodeHexSecret(name string) ([]byte, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return nil, fmt.Errorf("%s is required (set to ≥32 random hex bytes; "+
			"`mylogger42 admin generate-secrets` will print fresh values)", name)
	}
	b, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be hex-encoded: %w", name, err)
	}
	if len(b) < 32 {
		return nil, fmt.Errorf("%s is %d bytes after hex decode; need ≥32", name, len(b))
	}
	return b, nil
}

// parseBool accepts "true"/"1"/"yes" (case-insensitive) as true; anything
// else is false. Used for opt-in feature flags whose default is "off".
func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// bootstrapAdminUser inserts an admin user on first start if (and only
// if) the users table is empty AND both ADMIN_USERNAME and
// ADMIN_PASSWORD_HASH env vars are set.
//
// On subsequent starts (users non-empty) the env vars are ignored — the
// DB is the source of truth. If users is empty and the env vars are
// absent, the server still runs but the UI will be unusable until
// `mylogger42 admin create-user` is invoked out-of-band.
//
// We only validate that the password hash starts with `$2` (the bcrypt
// magic prefix). A wrong-but-bcrypt-shaped hash will fail at login time
// and produce an actionable error.
func bootstrapAdminUser(ctx context.Context, users store.UserStore, cfg config, logger *log.Logger) error {
	count, err := users.Count(ctx)
	if err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		// Already bootstrapped; ignore env vars even if set, so a stale
		// .env can't shadow a real DB user.
		return nil
	}
	if cfg.AdminUsername == "" || cfg.AdminPasswordHash == "" {
		logger.Printf("admin: no admin user configured (set ADMIN_USERNAME and " +
			"ADMIN_PASSWORD_HASH for first-start bootstrap, or run " +
			"`mylogger42 admin create-user`); UI will be unusable until then")
		return nil
	}
	if !strings.HasPrefix(cfg.AdminPasswordHash, "$2") {
		return fmt.Errorf("ADMIN_PASSWORD_HASH does not look like a bcrypt hash " +
			"(expected $2[aby]$...); use `mylogger42 admin hash-password`")
	}

	// crypto/rand → 32 hex chars makes a valid TEXT primary key. We don't
	// pull in google/uuid here just for one ID; the auth package already
	// gives us GenerateSecret which is exactly the right shape.
	id, err := auth.GenerateSecret(16)
	if err != nil {
		return fmt.Errorf("generate admin id: %w", err)
	}
	user := domain.User{
		ID:           id,
		Username:     cfg.AdminUsername,
		PasswordHash: cfg.AdminPasswordHash,
		CreatedAt:    time.Now().UTC(),
	}
	if err := users.Create(ctx, user); err != nil {
		return fmt.Errorf("create admin: %w", err)
	}
	logger.Printf("admin: bootstrapped user %q from env vars", cfg.AdminUsername)
	return nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// runAdmin dispatches `mylogger42 admin <subcommand>`. Two subcommands
// (generate-secrets, hash-password) need no env or DB. The rest load
// config the same way the server does, so a single .env validates for
// both server and CLI flows.
//
// Errors are printed to stderr and exit non-zero; subcommand stdout
// stays clean (machine-readable values only).
func runAdmin(args []string) {
	if len(args) > 0 && (args[0] == "generate-secrets" || args[0] == "hash-password" ||
		args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		// Env-free subcommands.
		if err := admin.Run(args, admin.Config{}); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		return
	}
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(2)
	}
	adminCfg := admin.Config{
		DBDriver:  cfg.DBDriver,
		DBDSN:     cfg.DBDSN,
		KeyPepper: cfg.KeyPepper,
	}
	if err := admin.Run(args, adminCfg); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
