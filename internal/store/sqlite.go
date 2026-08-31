package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mylogger42/mylogger42/internal/domain"
	"github.com/mylogger42/mylogger42/internal/store/migrations"

	_ "modernc.org/sqlite"
)

// sqliteStore is the SQLite-backed LogStore. We use modernc.org/sqlite —
// pure Go, no CGo — so the binary builds and runs without a C toolchain.
//
// Two handles, deliberately:
//
//   - db  is the WRITE handle, capped at one connection. SQLite allows
//     exactly one writer, so a bigger pool would only produce SQLITE_BUSY.
//   - rdb is the READ pool. WAL gives readers a consistent snapshot that
//     doesn't block (or get blocked by) the writer, so reads scale with
//     cores. Before this split, one `LIKE '%x%'` scan holding the single
//     shared connection stalled ingest completely: measured 41ms per
//     insert under concurrent search load versus 69µs idle.
//
// rdb may be nil — for in-memory DSNs a second handle would open a
// different, empty database, so reads fall back to db there. Use
// reader() rather than touching rdb directly.
type sqliteStore struct {
	db  *sql.DB
	rdb *sql.DB
}

// reader returns the handle read queries should use. Falls back to the
// write handle when no read pool exists (in-memory DSNs).
func (s *sqliteStore) reader() *sql.DB {
	if s.rdb != nil {
		return s.rdb
	}
	return s.db
}

// OpenSQLite opens a SQLite database at the given path and applies any
// pending migrations. Empty dsn falls back to ./data/mylogger42.db.
func OpenSQLite(ctx context.Context, dsn string) (LogStore, error) {
	if dsn == "" {
		dsn = "./data/mylogger42.db"
	}
	// Ensure the parent directory exists; modernc.org/sqlite will create
	// the .db file itself but not intermediate directories. We only
	// attempt this for filesystem paths — in-memory DSNs (":memory:" or
	// "file::memory:?cache=shared") are passed through unchanged.
	if !strings.HasPrefix(dsn, ":memory:") && !strings.HasPrefix(dsn, "file::memory:") {
		if dir := filepath.Dir(dsn); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create db dir %s: %w", dir, err)
			}
		}
	}
	// modernc.org/sqlite registers itself under the name "sqlite". We
	// append pragmas so writes are durable but not painfully slow, and
	// foreign keys behave intuitively.
	db, err := sql.Open("sqlite", sqliteDSN(dsn, false))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite allows exactly one writer; a larger pool here would only
	// manufacture SQLITE_BUSY. Reads go to the separate pool below.
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := runMigrations(ctx, db, migrations.SQLite, "sqlite"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate sqlite: %w", err)
	}

	store := &sqliteStore{db: db}

	// Read pool. Opened only after migrations, so the file and its
	// -wal/-shm siblings exist. Skipped for in-memory DSNs: a second
	// handle to ":memory:" is a different, empty database.
	if !isMemoryDSN(dsn) {
		rdb, err := sql.Open("sqlite", sqliteDSN(dsn, true))
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("open sqlite read pool: %w", err)
		}
		rdb.SetMaxOpenConns(readPoolSize)
		rdb.SetMaxIdleConns(readPoolSize)
		if err := rdb.PingContext(ctx); err != nil {
			_ = rdb.Close()
			_ = db.Close()
			return nil, fmt.Errorf("ping sqlite read pool: %w", err)
		}
		store.rdb = rdb
	}
	return store, nil
}

// readPoolSize caps concurrent readers. WAL readers don't contend with
// each other or with the writer, so this is really a bound on memory and
// goroutines rather than a correctness knob.
//
// ponytail: fixed size; make it configurable only if a deployment shows
// readers queueing.
const readPoolSize = 4

// isMemoryDSN reports whether dsn addresses an in-memory database, which
// cannot be shared across two sql.DB handles.
func isMemoryDSN(dsn string) bool {
	return strings.HasPrefix(dsn, ":memory:") || strings.HasPrefix(dsn, "file::memory:") ||
		strings.Contains(dsn, "mode=memory")
}

// sqliteDSN appends our connection pragmas to dsn, respecting any query
// string the caller already supplied (the in-memory form
// "file::memory:?cache=shared" carries one). Pragmas are per-connection
// apart from journal_mode, which lives in the file header — so both the
// write handle and every read-pool connection must set them.
//
// readOnly adds query_only, which makes the read pool structurally unable
// to write. We use the pragma rather than a "mode=ro" URI because mode=ro
// requires the file: URI form and is fussier about WAL sidecar files.
func sqliteDSN(dsn string, readOnly bool) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	pragmas := "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	if readOnly {
		pragmas += "&_pragma=query_only(1)"
	}
	return dsn + sep + pragmas
}

// runMigrations applies the SQLite migration scripts in lexicographic
// order. We don't track applied migrations in a separate table because
// every script uses IF NOT EXISTS / ADD COLUMN guards, so re-running on
// startup is safe and idempotent.
//
// Each filename is also looked up in sqliteHooks; if a Go-side hook is
// registered, it runs BEFORE that file's SQL exec. Hooks are used for
// preconditions that can't be expressed in portable SQL — see
// migration_hooks.go.
func runMigrations(ctx context.Context, db *sql.DB, fsys fs.FS, dir string) error {
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
		if hook, ok := sqliteHooks[n]; ok {
			if err := hook(ctx, db); err != nil {
				if errors.Is(err, errSkipMigration) {
					// Hook says "already applied" — skip the SQL and
					// move on. Used for migrations whose SQL isn't
					// natively idempotent on this driver (e.g. ALTER
					// TABLE ADD COLUMN on SQLite).
					continue
				}
				return fmt.Errorf("preflight %s: %w", n, err)
			}
		}
		body, err := fs.ReadFile(fsys, filepath.ToSlash(filepath.Join(dir, n)))
		if err != nil {
			return fmt.Errorf("read %s: %w", n, err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("exec %s: %w", n, err)
		}
	}
	return nil
}

func (s *sqliteStore) Insert(ctx context.Context, entry domain.LogEntry) error {
	meta, err := json.Marshal(entry.Metadata)
	if err != nil {
		return fmt.Errorf("encode metadata: %w", err)
	}
	const q = `INSERT INTO log_entries (id, project_id, service, level, message, timestamp, received_at, metadata)
	           VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = s.db.ExecContext(ctx, q,
		entry.ID, entry.ProjectID, entry.Service, string(entry.Level), entry.Message,
		entry.Timestamp.UTC(), entry.ReceivedAt.UTC(), string(meta),
	)
	return err
}

func (s *sqliteStore) InsertBatch(ctx context.Context, entries []domain.LogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	const q = `INSERT INTO log_entries (id, project_id, service, level, message, timestamp, received_at, metadata)
	           VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range entries {
		meta, err := json.Marshal(e.Metadata)
		if err != nil {
			return fmt.Errorf("encode metadata: %w", err)
		}
		if _, err := stmt.ExecContext(ctx, e.ID, e.ProjectID, e.Service, string(e.Level), e.Message,
			e.Timestamp.UTC(), e.ReceivedAt.UTC(), string(meta)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *sqliteStore) Get(ctx context.Context, projectID, id string) (*domain.LogEntry, error) {
	const q = `SELECT id, project_id, service, level, message, timestamp, received_at, metadata
	           FROM log_entries WHERE project_id = ? AND id = ?`
	row := s.reader().QueryRowContext(ctx, q, projectID, id)
	e, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows — lets the same
// scan logic serve Get and Query.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRow(r rowScanner) (*domain.LogEntry, error) {
	var (
		e        domain.LogEntry
		level    string
		ts       time.Time
		received time.Time
		meta     string
	)
	if err := r.Scan(&e.ID, &e.ProjectID, &e.Service, &level, &e.Message, &ts, &received, &meta); err != nil {
		return nil, err
	}
	e.Level = domain.LogLevel(level)
	e.Timestamp = ts.UTC()
	e.ReceivedAt = received.UTC()
	e.Metadata = map[string]any{}
	if meta != "" {
		if err := json.Unmarshal([]byte(meta), &e.Metadata); err != nil {
			// We tolerate corrupt metadata by surfacing an empty object so
			// listing doesn't break entirely on one bad row.
			e.Metadata = map[string]any{"_decodeError": err.Error(), "_raw": meta}
		}
	}
	return &e, nil
}

func (s *sqliteStore) Query(ctx context.Context, projectID string, f domain.QueryFilter) ([]domain.LogEntry, int, error) {
	// project_id always leads the WHERE clause — combined with the
	// composite (project_id, timestamp DESC) index, this is the v2 hot
	// path.
	where, args := buildWhereSQLite(projectID, f)

	// Count first so the API can return paginated metadata. We reuse the
	// same WHERE clause for consistency.
	var total int
	countSQL := "SELECT COUNT(*) FROM log_entries" + where
	if err := s.reader().QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
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
	listSQL := fmt.Sprintf(
		"SELECT id, project_id, service, level, message, timestamp, received_at, metadata FROM log_entries%s ORDER BY timestamp %s, id %s LIMIT ? OFFSET ?",
		where, order, order,
	)
	rows, err := s.reader().QueryContext(ctx, listSQL, append(append([]any{}, args...), limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	out := make([]domain.LogEntry, 0, limit)
	for rows.Next() {
		e, err := scanRow(rows)
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

// buildWhereSQLite constructs the WHERE clause and ordered args. The
// project_id predicate is always present (it's the v2 scoping anchor)
// and leads the clause list so the composite (project_id, timestamp DESC)
// index can satisfy queries via index prefix.
func buildWhereSQLite(projectID string, f domain.QueryFilter) (string, []any) {
	clauses := []string{"project_id = ?"}
	args := []any{projectID}
	if f.Service != "" {
		clauses = append(clauses, "service = ?")
		args = append(args, f.Service)
	}
	if f.Level != "" {
		clauses = append(clauses, "level = ?")
		args = append(args, string(f.Level))
	}
	if f.From != nil {
		clauses = append(clauses, "timestamp >= ?")
		args = append(args, f.From.UTC())
	}
	if f.To != nil {
		clauses = append(clauses, "timestamp <= ?")
		args = append(args, f.To.UTC())
	}
	if f.Search != "" {
		clauses = append(clauses, "message LIKE ?")
		args = append(args, "%"+f.Search+"%")
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func (s *sqliteStore) Sources(ctx context.Context, projectID string) ([]domain.Source, error) {
	const q = `SELECT service, MIN(timestamp) AS first_seen, MAX(timestamp) AS last_seen, COUNT(*) AS total
	           FROM log_entries WHERE project_id = ?
	           GROUP BY service ORDER BY service ASC`
	rows, err := s.reader().QueryContext(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Source
	for rows.Next() {
		var src domain.Source
		// modernc.org/sqlite returns aggregate (MIN/MAX) of DATETIME columns
		// as strings rather than time.Time — the column-type metadata is
		// lost through the aggregate. Scan as string and parse explicitly.
		var firstStr, lastStr string
		if err := rows.Scan(&src.Name, &firstStr, &lastStr, &src.TotalEntries); err != nil {
			return nil, err
		}
		first, err := parseSQLiteTime(firstStr)
		if err != nil {
			return nil, fmt.Errorf("parse first_seen %q: %w", firstStr, err)
		}
		last, err := parseSQLiteTime(lastStr)
		if err != nil {
			return nil, fmt.Errorf("parse last_seen %q: %w", lastStr, err)
		}
		src.FirstSeenAt = first.UTC()
		src.LastSeenAt = last.UTC()
		out = append(out, src)
	}
	return out, rows.Err()
}

// Stats aggregates the same filtered row set Query pages through. Two
// round trips: the level-over-time series (which also yields the totals)
// and the top repeated-message groups.
//
// Bucketing goes through strftime on a SUBSTRING of the timestamp, not on
// the column itself: the driver stores time.Time as Go's
// "2006-01-02 15:04:05.999999999 -0700 MST" (see parseSQLiteTime), and
// SQLite's date functions return NULL for that trailing " +0000 UTC".
// The first 19 chars are "YYYY-MM-DD HH:MM:SS", which they do parse, and
// timestamps are always stored UTC so no offset is lost.
func (s *sqliteStore) Stats(ctx context.Context, projectID string, f domain.QueryFilter, bucketSeconds int) (*domain.LogStats, error) {
	if bucketSeconds <= 0 {
		bucketSeconds = domain.DefaultBucketSeconds
	}
	where, args := buildWhereSQLite(projectID, f)

	out := &domain.LogStats{
		BucketSeconds: bucketSeconds,
		Series:        []domain.LevelBucket{},
		Totals:        map[domain.LogLevel]int64{},
		TopMessages:   []domain.MessageGroup{},
	}

	// ponytail: newest-first row cap instead of validating range/bucket up
	// front. 5000 (service-free) group rows is ~700 buckets even with all
	// seven levels present — far more columns than any chart renders. If a
	// caller ever needs a longer window, add an explicit 400 on
	// range/bucket > N rather than raising this.
	const maxSeriesRows = 5000
	const bucketExpr = `(CAST(strftime('%s', substr(timestamp, 1, 19)) AS INTEGER) / ? * ?)`

	seriesSQL := "SELECT " + bucketExpr + " AS bucket, level, COUNT(*) FROM log_entries" + where +
		" GROUP BY bucket, level ORDER BY bucket DESC LIMIT ?"
	// Placeholders bind in textual order, so the two bucket divisors come
	// before the WHERE args.
	seriesArgs := append([]any{bucketSeconds, bucketSeconds}, args...)
	seriesArgs = append(seriesArgs, maxSeriesRows)

	rows, err := s.reader().QueryContext(ctx, seriesSQL, seriesArgs...)
	if err != nil {
		return nil, fmt.Errorf("stats series: %w", err)
	}
	defer rows.Close()

	// Rows arrive newest-first and one per (bucket, level); fold them into
	// one LevelBucket per instant, then flip to ascending for the chart.
	byBucket := map[int64]map[domain.LogLevel]int64{}
	var order []int64
	for rows.Next() {
		var bucket, count int64
		var level string
		if err := rows.Scan(&bucket, &level, &count); err != nil {
			return nil, err
		}
		if _, seen := byBucket[bucket]; !seen {
			byBucket[bucket] = map[domain.LogLevel]int64{}
			order = append(order, bucket)
		}
		byBucket[bucket][domain.LogLevel(level)] += count
		out.Totals[domain.LogLevel(level)] += count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	for _, b := range order {
		out.Series = append(out.Series, domain.LevelBucket{
			Start:  time.Unix(b, 0).UTC(),
			Counts: byBucket[b],
		})
	}

	topSQL := `SELECT service, level, message, COUNT(*) AS c, MIN(timestamp), MAX(timestamp)
	           FROM log_entries` + where + `
	           GROUP BY service, level, message ORDER BY c DESC, MAX(timestamp) DESC LIMIT ?`
	topRows, err := s.reader().QueryContext(ctx, topSQL, append(append([]any{}, args...), domain.TopMessagesLimit)...)
	if err != nil {
		return nil, fmt.Errorf("stats top messages: %w", err)
	}
	defer topRows.Close()
	for topRows.Next() {
		var g domain.MessageGroup
		var level, firstStr, lastStr string
		if err := topRows.Scan(&g.Service, &level, &g.Message, &g.Count, &firstStr, &lastStr); err != nil {
			return nil, err
		}
		g.Level = domain.LogLevel(level)
		first, err := parseSQLiteTime(firstStr)
		if err != nil {
			return nil, fmt.Errorf("parse first_seen %q: %w", firstStr, err)
		}
		last, err := parseSQLiteTime(lastStr)
		if err != nil {
			return nil, fmt.Errorf("parse last_seen %q: %w", lastStr, err)
		}
		g.FirstSeenAt = first.UTC()
		g.LastSeenAt = last.UTC()
		out.TopMessages = append(out.TopMessages, g)
	}
	return out, topRows.Err()
}

// parseSQLiteTime parses the time formats modernc.org/sqlite emits for
// DATETIME columns. When passed a time.Time bound parameter, the driver
// stores it via Go's time.Time.String() ("2006-01-02 15:04:05.999999999
// -0700 MST"), so MIN/MAX aggregates come back in that form. We also
// accept RFC3339 and the legacy SQLite "YYYY-MM-DD HH:MM:SS" so older
// rows / future format changes don't break aggregation.
func parseSQLiteTime(s string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised time format: %s", s)
}

// retentionChunk bounds how many rows one DELETE statement removes. The
// write handle is a single connection shared with ingest, so an unbounded
// `DELETE FROM log_entries WHERE received_at < ?` over millions of rows
// holds it for the whole purge and stalls every incoming log. Chunking
// hands the connection back between statements.
const retentionChunk = 5000

func (s *sqliteStore) DeleteOlderThanForProject(ctx context.Context, projectID string, cutoff time.Time) (int64, error) {
	// The subquery is what LIMITs the delete — SQLite only supports
	// `DELETE ... LIMIT` when built with SQLITE_ENABLE_UPDATE_DELETE_LIMIT,
	// which this driver is not.
	const q = `DELETE FROM log_entries WHERE id IN (
	               SELECT id FROM log_entries
	               WHERE project_id = ? AND received_at < ?
	               LIMIT ?
	           )`
	var total int64
	for {
		// Stop between chunks rather than mid-purge on shutdown; rows
		// already deleted stay deleted, and the next sweep resumes.
		if err := ctx.Err(); err != nil {
			return total, err
		}
		res, err := s.db.ExecContext(ctx, q, projectID, cutoff.UTC(), retentionChunk)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
		if n < retentionChunk {
			return total, nil
		}
	}
}

func (s *sqliteStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *sqliteStore) Close() error {
	// Both handles, or the read pool leaks — main.go only ever calls
	// Close on the LogStore facet.
	var rerr error
	if s.rdb != nil {
		rerr = s.rdb.Close()
	}
	return errors.Join(rerr, s.db.Close())
}
