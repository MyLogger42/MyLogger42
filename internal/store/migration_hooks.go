package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// errSkipMigration is a sentinel a hook returns to tell the runner the
// migration has already been applied and the SQL file should be skipped.
// This is needed because SQLite doesn't support `ALTER TABLE ADD COLUMN
// IF NOT EXISTS`, so a vanilla re-run would fail on the second startup.
var errSkipMigration = errors.New("migration already applied")

// migrationSQLHook runs against a *sql.DB before the named migration's SQL
// is executed. Returning a non-nil error aborts the migration with that
// error message surfaced to the operator. Returning errSkipMigration
// tells the runner the migration's effects are already present and the
// SQL file should be skipped.
type migrationSQLHook func(context.Context, *sql.DB) error

// migrationPgxHook is the pgx variant — runs against a pgxpool.Pool. We
// can't share the *sql.DB hook directly because pgx uses its own pool and
// we don't want a second driver layer just for the migration runner.
type migrationPgxHook func(context.Context, *pgxpool.Pool) error

// sqliteHooks are pre-flight checks run before specific SQLite migration
// files. The map key is the bare filename (no directory).
var sqliteHooks = map[string]migrationSQLHook{
	"002_projects.sql": preflight002SQL,
}

// postgresHooks mirrors sqliteHooks for the Postgres runner. The Postgres
// 002 SQL is already idempotent (uses ADD COLUMN IF NOT EXISTS), so the
// pg hook only enforces the orphaned-rows check.
var postgresHooks = map[string]migrationPgxHook{
	"002_projects.sql": preflight002Pg,
}

// preflight002SQL has two responsibilities:
//
//  1. Detect whether 002 has already been applied (project_id column
//     present on log_entries) and return errSkipMigration so the runner
//     doesn't re-execute the SQL — SQLite's ALTER TABLE ADD COLUMN is
//     not idempotent and will fail with "duplicate column name".
//
//  2. Refuse to add the project_id column when log_entries has rows but
//     no projects exist — that would orphan every existing row. v2
//     ships no v1→v2 data migration tool, so the only remediation is to
//     drop the database.
func preflight002SQL(ctx context.Context, db *sql.DB) error {
	// Has 002 already been applied? PRAGMA table_info returns one row per
	// column; if any of them is named "project_id" we're done.
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(log_entries)")
	if err == nil {
		applied := false
		defer rows.Close()
		for rows.Next() {
			var (
				cid     int
				name    string
				typ     string
				notnull int
				dflt    sql.NullString
				pk      int
			)
			if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
				continue
			}
			if name == "project_id" {
				applied = true
				break
			}
		}
		if applied {
			return errSkipMigration
		}
	}

	var entries int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM log_entries").Scan(&entries); err != nil {
		// Table may not exist on a brand-new DB — fine, nothing to check.
		// Migration 001 will create it before any preflight on later
		// migrations, so this branch only fires on the very first run.
		return nil
	}
	if entries == 0 {
		return nil
	}
	var projects int
	row := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects")
	if err := row.Scan(&projects); err != nil {
		// projects table doesn't exist yet. With log_entries non-empty,
		// that's the unsafe state.
		return errors.New("log_entries has rows but no projects table; " +
			"delete the database (no v1 data migration shipped) before upgrading")
	}
	if projects == 0 {
		return fmt.Errorf("log_entries has %d rows but no projects exist; "+
			"delete the database (no v1 data migration shipped) before upgrading", entries)
	}
	return nil
}

// preflight002Pg mirrors preflight002SQL but only enforces the
// orphaned-rows check. The Postgres 002 SQL uses ADD COLUMN IF NOT
// EXISTS so a duplicate-column skip path isn't needed.
func preflight002Pg(ctx context.Context, pool *pgxpool.Pool) error {
	var entries int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM log_entries").Scan(&entries); err != nil {
		return nil
	}
	if entries == 0 {
		return nil
	}
	var projects int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM projects").Scan(&projects); err != nil {
		return errors.New("log_entries has rows but no projects table; " +
			"drop the database (no v1 data migration shipped) before upgrading")
	}
	if projects == 0 {
		return fmt.Errorf("log_entries has %d rows but no projects exist; "+
			"drop the database (no v1 data migration shipped) before upgrading", entries)
	}
	return nil
}
