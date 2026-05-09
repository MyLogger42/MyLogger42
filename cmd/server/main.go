// Command server is the MyLogger42 HTTP entry point.
//
// It wires up config, storage, the WebSocket hub, the retention worker,
// and the HTTP API, then blocks until SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/silverbaq/mylogger42/internal/api"
	"github.com/silverbaq/mylogger42/internal/retention"
	"github.com/silverbaq/mylogger42/internal/store"
	"github.com/silverbaq/mylogger42/internal/ws"

	mylogger42 "github.com/silverbaq/mylogger42"
)

func main() {
	logger := log.New(os.Stdout, "mylogger42 ", log.LstdFlags|log.Lmsgprefix)

	cfg, err := loadConfig()
	if err != nil {
		logger.Fatalf("config: %v", err)
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger.Printf("starting mylogger42 (driver=%s, port=%d, retention=%dd)", cfg.DBDriver, cfg.HTTPPort, cfg.RetentionDays)

	openCtx, openCancel := context.WithTimeout(rootCtx, 10*time.Second)
	storage, err := store.Open(openCtx, store.Driver(cfg.DBDriver), cfg.DBDSN)
	openCancel()
	if err != nil {
		logger.Fatalf("storage: %v", err)
	}
	defer func() {
		if err := storage.Close(); err != nil {
			logger.Printf("storage close: %v", err)
		}
	}()

	hub := ws.NewHub(logger)
	go hub.Run(rootCtx)

	worker := retention.New(storage, cfg.RetentionDays, logger)
	go worker.Run(rootCtx)

	staticFS, err := mylogger42.StaticAssets()
	if err != nil {
		// Not fatal — the server still serves the API. We just log so
		// operators know the SPA isn't being served from this binary.
		logger.Printf("static assets unavailable: %v", err)
	}

	srv := api.NewServer(api.Config{
		Store:    storage,
		Hub:      hub,
		APIKey:   cfg.IngestAPIKey,
		Logger:   logger,
		Driver:   cfg.DBDriver,
		StaticFS: staticFS,
	})

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
type config struct {
	IngestAPIKey  string
	DBDriver      string
	DBDSN         string
	RetentionDays int
	HTTPPort      int
	LogLevel      string
}

func loadConfig() (config, error) {
	cfg := config{
		DBDriver:      getEnv("DB_DRIVER", "sqlite"),
		DBDSN:         getEnv("DB_DSN", "./data/mylogger42.db"),
		HTTPPort:      8080,
		RetentionDays: 30,
		LogLevel:      strings.ToLower(getEnv("LOG_LEVEL", "info")),
	}
	cfg.IngestAPIKey = os.Getenv("INGEST_API_KEY")
	if cfg.IngestAPIKey == "" {
		return cfg, errors.New("INGEST_API_KEY is required")
	}
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

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
