-- v2 perf: cut per-insert index write amplification and give the
-- retention sweep an index it can actually use.
--
-- Every query path scopes by project_id first (buildWhereSQLite always
-- leads with it), so the single-column indexes from 001_init are dead
-- weight — they cost a B-tree write per insert and serve no query. That
-- took each insert from 4 index writes down to 2.
--
-- idx_received_at went the same way: DeleteOlderThanForProject filters
-- `project_id = ? AND received_at < ?`, which a project-led composite
-- serves and a bare received_at index does not.
--
-- Both statement forms are self-idempotent, so this file is safe to
-- re-run on every boot — runMigrations has no schema_migrations table
-- and replays every script.

CREATE INDEX IF NOT EXISTS idx_log_entries_project_received
    ON log_entries(project_id, received_at);

DROP INDEX IF EXISTS idx_service;
DROP INDEX IF EXISTS idx_level;
DROP INDEX IF EXISTS idx_received_at;
