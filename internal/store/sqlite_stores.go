package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/silverbaq/mylogger42/internal/domain"
)

// OpenSQLiteStores opens a SQLite DB and returns the v2 *Stores aggregate.
// The four stores share one *sql.DB; closing Logs (the v1 facet) closes
// the underlying handle for all of them.
//
// Implementation note: this constructor delegates to OpenSQLite for the
// actual file open + migration run, then wraps the same *sql.DB in the
// project / key / user store implementations. That keeps OpenSQLite
// compatible with v1 callers during the migration window.
func OpenSQLiteStores(ctx context.Context, dsn string) (*Stores, error) {
	logStore, err := OpenSQLite(ctx, dsn)
	if err != nil {
		return nil, err
	}
	// We need access to the raw *sql.DB on the LogStore to share it with
	// the new stores. This is internal-package coupling, justified
	// because all four stores must share one connection.
	ss, ok := logStore.(*sqliteStore)
	if !ok {
		return nil, errors.New("sqlite: unexpected LogStore impl")
	}
	return &Stores{
		Logs:     ss,
		Projects: &sqliteProjectStore{db: ss.db},
		Keys:     &sqliteKeyStore{db: ss.db},
		Users:    &sqliteUserStore{db: ss.db},
	}, nil
}

// --- Stub implementations ---------------------------------------------
//
// These return ErrNotImplemented for every call. They exist so the
// *Stores aggregate has non-nil fields after construction; real impls
// land in the next few commits.

// errNotImplemented is local to this file; callers should not depend on
// its identity. Sentinel ErrNotFound / ErrAlreadyExists from store.go are
// the public errors.
var errNotImplemented = errors.New("not implemented (v2 WIP)")

type sqliteProjectStore struct{ db *sql.DB }

func (s *sqliteProjectStore) Create(context.Context, domain.Project) error {
	return errNotImplemented
}
func (s *sqliteProjectStore) GetBySlug(context.Context, string) (*domain.Project, error) {
	return nil, errNotImplemented
}
func (s *sqliteProjectStore) GetByID(context.Context, string) (*domain.Project, error) {
	return nil, errNotImplemented
}
func (s *sqliteProjectStore) List(context.Context, bool) ([]domain.Project, error) {
	return nil, errNotImplemented
}
func (s *sqliteProjectStore) Update(context.Context, string, string, *int) (*domain.Project, error) {
	return nil, errNotImplemented
}
func (s *sqliteProjectStore) Archive(context.Context, string) (*domain.Project, error) {
	return nil, errNotImplemented
}
func (s *sqliteProjectStore) Unarchive(context.Context, string) (*domain.Project, error) {
	return nil, errNotImplemented
}
func (s *sqliteProjectStore) Delete(context.Context, string) error {
	return errNotImplemented
}
func (s *sqliteProjectStore) Count(context.Context) (int, error) {
	return 0, errNotImplemented
}

type sqliteKeyStore struct{ db *sql.DB }

func (s *sqliteKeyStore) Create(context.Context, domain.ProjectKey, string) error {
	return errNotImplemented
}
func (s *sqliteKeyStore) GetActiveByPrefix(context.Context, string) (*domain.ProjectKey, string, error) {
	return nil, "", errNotImplemented
}
func (s *sqliteKeyStore) ListByProject(context.Context, string, bool) ([]domain.ProjectKey, error) {
	return nil, errNotImplemented
}
func (s *sqliteKeyStore) GetByID(context.Context, string) (*domain.ProjectKey, error) {
	return nil, errNotImplemented
}
func (s *sqliteKeyStore) Revoke(context.Context, string) error {
	return errNotImplemented
}
func (s *sqliteKeyStore) NoteUsage(context.Context, string, time.Time) error {
	return errNotImplemented
}

type sqliteUserStore struct{ db *sql.DB }

func (s *sqliteUserStore) Create(context.Context, domain.User) error {
	return errNotImplemented
}
func (s *sqliteUserStore) GetByUsername(context.Context, string) (*domain.User, error) {
	return nil, errNotImplemented
}
func (s *sqliteUserStore) UpdatePassword(context.Context, string, string) error {
	return errNotImplemented
}
func (s *sqliteUserStore) NoteLogin(context.Context, string, time.Time) error {
	return errNotImplemented
}
func (s *sqliteUserStore) Count(context.Context) (int, error) {
	return 0, errNotImplemented
}
