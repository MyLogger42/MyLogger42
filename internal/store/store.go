// Package store contains the persistence layer for log entries, projects,
// ingest keys, and admin users.
//
// The v1 LogStore interface persists. v2 adds three more focused
// interfaces (ProjectStore, KeyStore, UserStore) and a Stores aggregate
// that bundles all four. Implementations share one DB connection / pool
// — closing any one closes the underlying handle.
//
// The package ships SQLite (default, pure-Go via modernc.org/sqlite) and
// Postgres (via jackc/pgx) backends. Selection is driven by DB_DRIVER;
// see Open / OpenStores.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/silverbaq/mylogger42/internal/domain"
)

// Sentinel errors returned by all stores.
var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
)

// LogStore is the persistence-layer port consumed by the API and worker
// layers. Implementations must be safe for concurrent use.
//
// All read methods are project-scoped: the projectID is the FIRST data
// argument. Insert/InsertBatch read the project ID from the entry's
// ProjectID field — both forms keep project scoping at the boundary so a
// caller that forgot to plumb a project can't accidentally bypass it.
//
// DeleteOlderThanForProject is the v2 replacement for v1's global
// DeleteOlderThan. The retention worker iterates projects and calls it
// per-project so per-project retention windows can apply.
type LogStore interface {
	Insert(ctx context.Context, entry domain.LogEntry) error
	InsertBatch(ctx context.Context, entries []domain.LogEntry) error
	Get(ctx context.Context, projectID, id string) (*domain.LogEntry, error)
	Query(ctx context.Context, projectID string, filter domain.QueryFilter) ([]domain.LogEntry, int, error)
	Sources(ctx context.Context, projectID string) ([]domain.Source, error)
	DeleteOlderThanForProject(ctx context.Context, projectID string, cutoff time.Time) (int64, error)
	Ping(ctx context.Context) error
	Close() error
}

// ProjectStore manages the projects catalog. Slug is the human-facing
// key; ID is the UUID used for foreign-key references.
//
// v2 single-admin: anyone with a session can read/mutate any project. v3
// multi-user can layer membership checks on top without changing this
// interface.
type ProjectStore interface {
	Create(ctx context.Context, p domain.Project) error
	GetBySlug(ctx context.Context, slug string) (*domain.Project, error)
	GetByID(ctx context.Context, id string) (*domain.Project, error)
	List(ctx context.Context, includeArchived bool) ([]domain.Project, error)
	Update(ctx context.Context, slug, name string, retentionDays *int) (*domain.Project, error)
	Archive(ctx context.Context, slug string) (*domain.Project, error)
	Unarchive(ctx context.Context, slug string) (*domain.Project, error)
	Delete(ctx context.Context, slug string) error // hard delete; CLI only
	Count(ctx context.Context) (int, error)
}

// KeyStore manages ingest credentials. Plaintext key values are never
// stored — only their (prefix, hmac) tuple. Verify is the hot path: one
// indexed Lookup-by-prefix followed by an HMAC compare in the auth
// middleware.
type KeyStore interface {
	Create(ctx context.Context, k domain.ProjectKey, hmac string) error
	// GetActiveByPrefix returns the key + its stored hex HMAC. The hmac
	// is returned alongside (rather than on the struct) because no other
	// consumer needs it; this keeps domain.ProjectKey free of crypto
	// material at rest.
	GetActiveByPrefix(ctx context.Context, prefix string) (key *domain.ProjectKey, hexHMAC string, err error)
	ListByProject(ctx context.Context, projectID string, includeRevoked bool) ([]domain.ProjectKey, error)
	GetByID(ctx context.Context, id string) (*domain.ProjectKey, error)
	Revoke(ctx context.Context, id string) error
	NoteUsage(ctx context.Context, id string, when time.Time) error
}

// UserStore manages admin users. v2 expects exactly one row.
type UserStore interface {
	Create(ctx context.Context, u domain.User) error
	GetByUsername(ctx context.Context, username string) (*domain.User, error)
	UpdatePassword(ctx context.Context, username, newHash string) error
	NoteLogin(ctx context.Context, id string, when time.Time) error
	Count(ctx context.Context) (int, error)
}

// Stores bundles all four interfaces so callers can pass one value
// through the bootstrap path. Closing the LogStore closes the shared
// underlying connection — the other stores become unusable.
type Stores struct {
	Logs     LogStore
	Projects ProjectStore
	Keys     KeyStore
	Users    UserStore
}

// Driver identifies a supported storage backend.
type Driver string

const (
	DriverSQLite   Driver = "sqlite"
	DriverPostgres Driver = "postgres"
)

// Open dispatches to the correct backend's LogStore constructor.
//
// Deprecated: Use OpenStores. Open exists for v1 callers that haven't
// migrated yet; it returns only the LogStore facet.
func Open(ctx context.Context, driver Driver, dsn string) (LogStore, error) {
	s, err := OpenStores(ctx, driver, dsn)
	if err != nil {
		return nil, err
	}
	return s.Logs, nil
}

// OpenStores opens the database, runs migrations, and returns all four
// store facets bundled in a *Stores aggregate.
func OpenStores(ctx context.Context, driver Driver, dsn string) (*Stores, error) {
	switch driver {
	case DriverSQLite, "":
		return OpenSQLiteStores(ctx, dsn)
	case DriverPostgres:
		return OpenPostgresStores(ctx, dsn)
	default:
		return nil, fmt.Errorf("unknown db driver %q", driver)
	}
}
