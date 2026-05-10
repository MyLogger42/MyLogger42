package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/silverbaq/mylogger42/internal/domain"
	"github.com/silverbaq/mylogger42/internal/store/migrations"
)

// pgStore is the Postgres-backed LogStore. We use jackc/pgx with a pool;
// pgx is the canonical modern Postgres driver and gives us proper typed
// scanning (especially for jsonb metadata).
type pgStore struct {
	pool *pgxpool.Pool
}

// OpenPostgres opens a Postgres connection pool and applies migrations.
func OpenPostgres(ctx context.Context, dsn string) (LogStore, error) {
	if dsn == "" {
		return nil, errors.New("postgres DSN is required (DB_DSN)")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if err := runPgMigrations(ctx, pool, migrations.Postgres, "postgres"); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate postgres: %w", err)
	}
	return &pgStore{pool: pool}, nil
}

// runPgMigrations is the Postgres equivalent of runMigrations — same
// hook-then-exec contract, see migration_hooks.go.
func runPgMigrations(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, dir string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		if hook, ok := postgresHooks[n]; ok {
			if err := hook(ctx, pool); err != nil {
				return fmt.Errorf("preflight %s: %w", n, err)
			}
		}
		body, err := fs.ReadFile(fsys, filepath.ToSlash(filepath.Join(dir, n)))
		if err != nil {
			return fmt.Errorf("read %s: %w", n, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("exec %s: %w", n, err)
		}
	}
	return nil
}

func (s *pgStore) Insert(ctx context.Context, e domain.LogEntry) error {
	meta, err := json.Marshal(e.Metadata)
	if err != nil {
		return fmt.Errorf("encode metadata: %w", err)
	}
	const q = `INSERT INTO log_entries (id, service, level, message, timestamp, received_at, metadata)
	           VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err = s.pool.Exec(ctx, q,
		e.ID, e.Service, string(e.Level), e.Message,
		e.Timestamp.UTC(), e.ReceivedAt.UTC(), meta,
	)
	return err
}

func (s *pgStore) InsertBatch(ctx context.Context, entries []domain.LogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// pgx CopyFrom would be faster but uses a binary protocol that needs
	// jsonb encoded as bytes; the per-row insert with prepared statement
	// is plenty for batch sizes <=500 and keeps the path simple.
	const q = `INSERT INTO log_entries (id, service, level, message, timestamp, received_at, metadata)
	           VALUES ($1, $2, $3, $4, $5, $6, $7)`
	for _, e := range entries {
		meta, err := json.Marshal(e.Metadata)
		if err != nil {
			return fmt.Errorf("encode metadata: %w", err)
		}
		if _, err := tx.Exec(ctx, q, e.ID, e.Service, string(e.Level), e.Message,
			e.Timestamp.UTC(), e.ReceivedAt.UTC(), meta); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *pgStore) Get(ctx context.Context, id string) (*domain.LogEntry, error) {
	const q = `SELECT id, service, level, message, timestamp, received_at, metadata
	           FROM log_entries WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, id)
	e, err := scanPgRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

type pgScanner interface {
	Scan(dest ...any) error
}

func scanPgRow(r pgScanner) (*domain.LogEntry, error) {
	var (
		e        domain.LogEntry
		level    string
		ts       time.Time
		received time.Time
		meta     []byte
	)
	if err := r.Scan(&e.ID, &e.Service, &level, &e.Message, &ts, &received, &meta); err != nil {
		return nil, err
	}
	e.Level = domain.LogLevel(level)
	e.Timestamp = ts.UTC()
	e.ReceivedAt = received.UTC()
	e.Metadata = map[string]any{}
	if len(meta) > 0 {
		if err := json.Unmarshal(meta, &e.Metadata); err != nil {
			e.Metadata = map[string]any{"_decodeError": err.Error(), "_raw": string(meta)}
		}
	}
	return &e, nil
}

func (s *pgStore) Query(ctx context.Context, f domain.QueryFilter) ([]domain.LogEntry, int, error) {
	where, args := buildWherePg(f)

	var total int
	countSQL := "SELECT COUNT(*) FROM log_entries" + where
	if err := s.pool.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count: %w", err)
	}

	order := "DESC"
	if f.Sort == domain.SortAsc {
		order = "ASC"
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	limPlaceholder := fmt.Sprintf("$%d", len(args)+1)
	offPlaceholder := fmt.Sprintf("$%d", len(args)+2)
	listSQL := fmt.Sprintf(
		"SELECT id, service, level, message, timestamp, received_at, metadata FROM log_entries%s ORDER BY timestamp %s, id %s LIMIT %s OFFSET %s",
		where, order, order, limPlaceholder, offPlaceholder,
	)
	rows, err := s.pool.Query(ctx, listSQL, append(append([]any{}, args...), limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	out := make([]domain.LogEntry, 0, limit)
	for rows.Next() {
		e, err := scanPgRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// buildWherePg uses Postgres's $N placeholders.
func buildWherePg(f domain.QueryFilter) (string, []any) {
	var clauses []string
	var args []any
	add := func(clause string, val any) {
		args = append(args, val)
		clauses = append(clauses, fmt.Sprintf(clause, len(args)))
	}
	if f.Service != "" {
		add("service = $%d", f.Service)
	}
	if f.Level != "" {
		add("level = $%d", string(f.Level))
	}
	if f.From != nil {
		add("timestamp >= $%d", f.From.UTC())
	}
	if f.To != nil {
		add("timestamp <= $%d", f.To.UTC())
	}
	if f.Search != "" {
		add("message ILIKE $%d", "%"+f.Search+"%")
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func (s *pgStore) Sources(ctx context.Context) ([]domain.Source, error) {
	const q = `SELECT service, MIN(timestamp), MAX(timestamp), COUNT(*)
	           FROM log_entries GROUP BY service ORDER BY service ASC`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Source
	for rows.Next() {
		var src domain.Source
		var first, last time.Time
		if err := rows.Scan(&src.Name, &first, &last, &src.TotalEntries); err != nil {
			return nil, err
		}
		src.FirstSeenAt = first.UTC()
		src.LastSeenAt = last.UTC()
		out = append(out, src)
	}
	return out, rows.Err()
}

func (s *pgStore) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	const q = `DELETE FROM log_entries WHERE received_at < $1`
	tag, err := s.pool.Exec(ctx, q, cutoff.UTC())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *pgStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *pgStore) Close() error {
	s.pool.Close()
	return nil
}
