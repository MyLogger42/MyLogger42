-- v2: multi-project support (Postgres flavor — mirrors sqlite/002).
--
-- Differences from the SQLite version:
--   - TIMESTAMPTZ instead of DATETIME (Postgres preserves zone)
--   - explicit FK CONSTRAINT name for clean error surfacing
--   - ALTER TABLE uses ADD COLUMN IF NOT EXISTS (Postgres-specific) so the
--     migration is fully idempotent without a Go-side guard
--
-- The Go-side pre-flight check still runs first; see migration_hooks.go.

CREATE TABLE IF NOT EXISTS projects (
    id              TEXT PRIMARY KEY,
    slug            TEXT NOT NULL UNIQUE,
    name            TEXT NOT NULL,
    retention_days  INTEGER,
    created_at      TIMESTAMPTZ NOT NULL,
    archived_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_projects_slug ON projects(slug);

CREATE TABLE IF NOT EXISTS project_keys (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL,
    prefix       TEXT NOT NULL UNIQUE,
    hmac         TEXT NOT NULL,
    label        TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    CONSTRAINT fk_project_keys_project
        FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_project_keys_project ON project_keys(project_id);
CREATE INDEX IF NOT EXISTS idx_project_keys_active
    ON project_keys(prefix) WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    last_login_at TIMESTAMPTZ
);

ALTER TABLE log_entries ADD COLUMN IF NOT EXISTS project_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_log_entries_project_timestamp
    ON log_entries(project_id, timestamp DESC);
DROP INDEX IF EXISTS idx_timestamp;
