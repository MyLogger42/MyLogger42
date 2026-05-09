// Package store contains the persistence layer for log entries.
//
// LogStore is the interface every backend implements. The package ships
// two implementations: SQLite (default, pure-Go via modernc.org/sqlite)
// and Postgres (via jackc/pgx). Selection is driven by the DB_DRIVER env
// var; see Open below.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/silverbaq/mylogger42/internal/domain"
)

// ErrNotFound is returned by Get when no row matches the supplied id.
var ErrNotFound = errors.New("log entry not found")

// LogStore is the persistence-layer port consumed by the API and worker
// layers. Implementations must be safe for concurrent use.
type LogStore interface {
	Insert(ctx context.Context, entry domain.LogEntry) error
	InsertBatch(ctx context.Context, entries []domain.LogEntry) error
	Get(ctx context.Context, id string) (*domain.LogEntry, error)
	Query(ctx context.Context, filter domain.QueryFilter) ([]domain.LogEntry, int, error)
	Sources(ctx context.Context) ([]domain.Source, error)
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	Ping(ctx context.Context) error
	Close() error
}

// Driver identifies a supported storage backend.
type Driver string

const (
	DriverSQLite   Driver = "sqlite"
	DriverPostgres Driver = "postgres"
)

// Open dispatches to the correct backend constructor based on driver. DSN
// is interpreted by the backend (filesystem path for sqlite, libpq URL
// for postgres).
func Open(ctx context.Context, driver Driver, dsn string) (LogStore, error) {
	switch driver {
	case DriverSQLite, "":
		return OpenSQLite(ctx, dsn)
	case DriverPostgres:
		return OpenPostgres(ctx, dsn)
	default:
		return nil, fmt.Errorf("unknown db driver %q", driver)
	}
}
