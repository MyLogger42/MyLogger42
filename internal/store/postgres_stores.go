package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mylogger42/mylogger42/internal/domain"
)

// OpenPostgresStores is the Postgres counterpart to OpenSQLiteStores.
// See its godoc for the lifecycle contract.
func OpenPostgresStores(ctx context.Context, dsn string) (*Stores, error) {
	logStore, err := OpenPostgres(ctx, dsn)
	if err != nil {
		return nil, err
	}
	ps, ok := logStore.(*pgStore)
	if !ok {
		return nil, errors.New("postgres: unexpected LogStore impl")
	}
	return &Stores{
		Logs:     ps,
		Projects: &pgProjectStore{pool: ps.pool},
		Keys:     &pgKeyStore{pool: ps.pool},
		Users:    &pgUserStore{pool: ps.pool},
	}, nil
}

// --- Stub implementations (Postgres) ---------------------------------
// Same contract as the SQLite stubs: every call returns errNotImplemented
// until the real impl lands.

type pgProjectStore struct{ pool *pgxpool.Pool }

func (s *pgProjectStore) Create(context.Context, domain.Project) error {
	return errNotImplemented
}
func (s *pgProjectStore) GetBySlug(context.Context, string) (*domain.Project, error) {
	return nil, errNotImplemented
}
func (s *pgProjectStore) GetByID(context.Context, string) (*domain.Project, error) {
	return nil, errNotImplemented
}
func (s *pgProjectStore) List(context.Context, bool) ([]domain.Project, error) {
	return nil, errNotImplemented
}
func (s *pgProjectStore) Update(context.Context, string, string, *int) (*domain.Project, error) {
	return nil, errNotImplemented
}
func (s *pgProjectStore) Archive(context.Context, string) (*domain.Project, error) {
	return nil, errNotImplemented
}
func (s *pgProjectStore) Unarchive(context.Context, string) (*domain.Project, error) {
	return nil, errNotImplemented
}
func (s *pgProjectStore) Delete(context.Context, string) error {
	return errNotImplemented
}
func (s *pgProjectStore) Count(context.Context) (int, error) {
	return 0, errNotImplemented
}

type pgKeyStore struct{ pool *pgxpool.Pool }

func (s *pgKeyStore) Create(context.Context, domain.ProjectKey, string) error {
	return errNotImplemented
}
func (s *pgKeyStore) GetActiveByPrefix(context.Context, string) (*domain.ProjectKey, string, error) {
	return nil, "", errNotImplemented
}
func (s *pgKeyStore) ListByProject(context.Context, string, bool) ([]domain.ProjectKey, error) {
	return nil, errNotImplemented
}
func (s *pgKeyStore) GetByID(context.Context, string) (*domain.ProjectKey, error) {
	return nil, errNotImplemented
}
func (s *pgKeyStore) Revoke(context.Context, string) error {
	return errNotImplemented
}
func (s *pgKeyStore) NoteUsage(context.Context, string, time.Time) error {
	return errNotImplemented
}

type pgUserStore struct{ pool *pgxpool.Pool }

func (s *pgUserStore) Create(context.Context, domain.User) error {
	return errNotImplemented
}
func (s *pgUserStore) GetByUsername(context.Context, string) (*domain.User, error) {
	return nil, errNotImplemented
}
func (s *pgUserStore) UpdatePassword(context.Context, string, string) error {
	return errNotImplemented
}
func (s *pgUserStore) NoteLogin(context.Context, string, time.Time) error {
	return errNotImplemented
}
func (s *pgUserStore) Count(context.Context) (int, error) {
	return 0, errNotImplemented
}
