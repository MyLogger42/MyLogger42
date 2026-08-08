package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/mylogger42/mylogger42/internal/domain"
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

func (s *sqliteKeyStore) Create(ctx context.Context, k domain.ProjectKey, hmac string) error {
	const q = `INSERT INTO project_keys
	           (id, project_id, prefix, hmac, label, created_at)
	           VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, q,
		k.ID, k.ProjectID, k.Prefix, hmac, k.Label, k.CreatedAt.UTC())
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrAlreadyExists
	}
	return err
}

func (s *sqliteKeyStore) GetActiveByPrefix(ctx context.Context, prefix string) (*domain.ProjectKey, string, error) {
	const q = `SELECT id, project_id, prefix, hmac, label, created_at, last_used_at, revoked_at
	           FROM project_keys
	           WHERE prefix = ? AND revoked_at IS NULL`
	row := s.db.QueryRowContext(ctx, q, prefix)
	return scanSqliteKeyRow(row)
}

func (s *sqliteKeyStore) ListByProject(ctx context.Context, projectID string, includeRevoked bool) ([]domain.ProjectKey, error) {
	q := `SELECT id, project_id, prefix, hmac, label, created_at, last_used_at, revoked_at
	      FROM project_keys WHERE project_id = ?`
	if !includeRevoked {
		q += ` AND revoked_at IS NULL`
	}
	q += ` ORDER BY created_at ASC`
	rows, err := s.db.QueryContext(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ProjectKey
	for rows.Next() {
		k, _, err := scanSqliteKeyRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

func (s *sqliteKeyStore) GetByID(ctx context.Context, id string) (*domain.ProjectKey, error) {
	const q = `SELECT id, project_id, prefix, hmac, label, created_at, last_used_at, revoked_at
	           FROM project_keys WHERE id = ?`
	row := s.db.QueryRowContext(ctx, q, id)
	k, _, err := scanSqliteKeyRow(row)
	return k, err
}

func (s *sqliteKeyStore) Revoke(ctx context.Context, id string) error {
	const q = `UPDATE project_keys SET revoked_at = ?
	           WHERE id = ? AND revoked_at IS NULL`
	res, err := s.db.ExecContext(ctx, q, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		// Already revoked or not found. Idempotency: if the key exists at
		// all (revoked or not), return nil; otherwise ErrNotFound.
		if _, err := s.GetByID(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqliteKeyStore) NoteUsage(ctx context.Context, id string, when time.Time) error {
	const q = `UPDATE project_keys SET last_used_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, q, when.UTC(), id)
	return err
}

// scanSqliteKeyRow reads a single key row from *sql.Row. Returns the
// key, its hex-encoded HMAC (column 4), and any error. ErrNotFound on
// sql.ErrNoRows.
func scanSqliteKeyRow(row *sql.Row) (*domain.ProjectKey, string, error) {
	var (
		k        domain.ProjectKey
		hmac     string
		created  time.Time
		lastUsed sql.NullTime
		revoked  sql.NullTime
	)
	err := row.Scan(&k.ID, &k.ProjectID, &k.Prefix, &hmac, &k.Label,
		&created, &lastUsed, &revoked)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	k.CreatedAt = created.UTC()
	if lastUsed.Valid {
		t := lastUsed.Time.UTC()
		k.LastUsedAt = &t
	}
	if revoked.Valid {
		t := revoked.Time.UTC()
		k.RevokedAt = &t
	}
	return &k, hmac, nil
}

// scanSqliteKeyRows reads one row from *sql.Rows.
func scanSqliteKeyRows(rows *sql.Rows) (*domain.ProjectKey, string, error) {
	var (
		k        domain.ProjectKey
		hmac     string
		created  time.Time
		lastUsed sql.NullTime
		revoked  sql.NullTime
	)
	if err := rows.Scan(&k.ID, &k.ProjectID, &k.Prefix, &hmac, &k.Label,
		&created, &lastUsed, &revoked); err != nil {
		return nil, "", err
	}
	k.CreatedAt = created.UTC()
	if lastUsed.Valid {
		t := lastUsed.Time.UTC()
		k.LastUsedAt = &t
	}
	if revoked.Valid {
		t := revoked.Time.UTC()
		k.RevokedAt = &t
	}
	return &k, hmac, nil
}

type sqliteUserStore struct{ db *sql.DB }

func (s *sqliteUserStore) Create(ctx context.Context, u domain.User) error {
	const q = `INSERT INTO users (id, username, password_hash, created_at)
	           VALUES (?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, q,
		u.ID, u.Username, u.PasswordHash, u.CreatedAt.UTC())
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrAlreadyExists
	}
	return err
}

func (s *sqliteUserStore) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	const q = `SELECT id, username, password_hash, created_at, last_login_at
	           FROM users WHERE username = ?`
	row := s.db.QueryRowContext(ctx, q, username)
	var (
		u         domain.User
		created   time.Time
		lastLogin sql.NullTime
	)
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &created, &lastLogin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.CreatedAt = created.UTC()
	if lastLogin.Valid {
		t := lastLogin.Time.UTC()
		u.LastLoginAt = &t
	}
	return &u, nil
}

func (s *sqliteUserStore) UpdatePassword(ctx context.Context, username, newHash string) error {
	const q = `UPDATE users SET password_hash = ? WHERE username = ?`
	res, err := s.db.ExecContext(ctx, q, newHash, username)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *sqliteUserStore) NoteLogin(ctx context.Context, id string, when time.Time) error {
	const q = `UPDATE users SET last_login_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, q, when.UTC(), id)
	return err
}

func (s *sqliteUserStore) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}
