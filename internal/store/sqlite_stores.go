package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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

func (s *sqliteProjectStore) Create(ctx context.Context, p domain.Project) error {
	const q = `INSERT INTO projects (id, slug, name, retention_days, created_at)
	           VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, q,
		p.ID, p.Slug, p.Name, p.RetentionDays, p.CreatedAt.UTC())
	if err != nil {
		// modernc.org/sqlite surfaces UNIQUE-constraint violations in the
		// error message rather than via a typed sentinel; matching on the
		// substring is the canonical idiom for this driver.
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (s *sqliteProjectStore) GetBySlug(ctx context.Context, slug string) (*domain.Project, error) {
	const q = `SELECT id, slug, name, retention_days, created_at, archived_at
	           FROM projects WHERE slug = ?`
	return scanSqliteProjectRow(s.db.QueryRowContext(ctx, q, slug))
}

func (s *sqliteProjectStore) GetByID(ctx context.Context, id string) (*domain.Project, error) {
	const q = `SELECT id, slug, name, retention_days, created_at, archived_at
	           FROM projects WHERE id = ?`
	return scanSqliteProjectRow(s.db.QueryRowContext(ctx, q, id))
}

func (s *sqliteProjectStore) List(ctx context.Context, includeArchived bool) ([]domain.Project, error) {
	q := `SELECT id, slug, name, retention_days, created_at, archived_at FROM projects`
	if !includeArchived {
		q += ` WHERE archived_at IS NULL`
	}
	q += ` ORDER BY slug ASC`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Project
	for rows.Next() {
		p, err := scanSqliteProjectRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *sqliteProjectStore) Update(ctx context.Context, slug, name string, retentionDays *int) (*domain.Project, error) {
	const q = `UPDATE projects SET name = ?, retention_days = ? WHERE slug = ?`
	res, err := s.db.ExecContext(ctx, q, name, retentionDays, slug)
	if err != nil {
		return nil, err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return nil, ErrNotFound
	}
	return s.GetBySlug(ctx, slug)
}

func (s *sqliteProjectStore) Archive(ctx context.Context, slug string) (*domain.Project, error) {
	const q = `UPDATE projects SET archived_at = ? WHERE slug = ? AND archived_at IS NULL`
	res, err := s.db.ExecContext(ctx, q, time.Now().UTC(), slug)
	if err != nil {
		return nil, err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		// Either not found, or already archived. Differentiate by lookup —
		// archive should be idempotent if archived_at is already set.
		if p, err := s.GetBySlug(ctx, slug); err == nil {
			return p, nil
		}
		return nil, ErrNotFound
	}
	return s.GetBySlug(ctx, slug)
}

func (s *sqliteProjectStore) Unarchive(ctx context.Context, slug string) (*domain.Project, error) {
	const q = `UPDATE projects SET archived_at = NULL WHERE slug = ?`
	res, err := s.db.ExecContext(ctx, q, slug)
	if err != nil {
		return nil, err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return nil, ErrNotFound
	}
	return s.GetBySlug(ctx, slug)
}

func (s *sqliteProjectStore) Delete(ctx context.Context, slug string) error {
	const q = `DELETE FROM projects WHERE slug = ?`
	res, err := s.db.ExecContext(ctx, q, slug)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *sqliteProjectStore) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&n)
	return n, err
}

// scanSqliteProjectRow / scanSqliteProjectRows: shared scan logic for the
// single-row (*sql.Row) and multi-row (*sql.Rows) cases. Splitting them
// avoids generics here; the *sql.Row API differs from *sql.Rows enough
// that a single helper would still need a wrapper.

func scanSqliteProjectRow(row *sql.Row) (*domain.Project, error) {
	var (
		p         domain.Project
		retention sql.NullInt64
		archived  sql.NullTime
		created   time.Time
	)
	if err := row.Scan(&p.ID, &p.Slug, &p.Name, &retention, &created, &archived); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.CreatedAt = created.UTC()
	if retention.Valid {
		v := int(retention.Int64)
		p.RetentionDays = &v
	}
	if archived.Valid {
		t := archived.Time.UTC()
		p.ArchivedAt = &t
	}
	return &p, nil
}

func scanSqliteProjectRows(rows *sql.Rows) (*domain.Project, error) {
	var (
		p         domain.Project
		retention sql.NullInt64
		archived  sql.NullTime
		created   time.Time
	)
	if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &retention, &created, &archived); err != nil {
		return nil, err
	}
	p.CreatedAt = created.UTC()
	if retention.Valid {
		v := int(retention.Int64)
		p.RetentionDays = &v
	}
	if archived.Valid {
		t := archived.Time.UTC()
		p.ArchivedAt = &t
	}
	return &p, nil
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
