package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationSQLHook runs against a *sql.DB before the named migration's SQL
// is executed. Returning a non-nil error aborts the migration with that
// error message surfaced to the operator.
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

// postgresHooks mirrors sqliteHooks for the Postgres runner.
var postgresHooks = map[string]migrationPgxHook{
	"002_projects.sql": preflight002Pg,
}

// preflight002SQL refuses to add the project_id column when log_entries
// has rows but no projects exist — that would orphan every existing row.
//
// Safe states:
//   - log_entries empty: nothing to orphan, proceed.
//   - log_entries non-empty AND projects non-empty: an earlier upgrade
//     already ran; idempotent re-run is fine.
//
// Unsafe state — log_entries non-empty AND no projects: refuse with a
// clear remediation message. v2 ships no v1→v2 data migration tool (no
// production data exists per the spec); operators must drop the database
// to upgrade.
func preflight002SQL(ctx context.Context, db *sql.DB) error {
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

// preflight002Pg is the Postgres equivalent of preflight002SQL.
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
