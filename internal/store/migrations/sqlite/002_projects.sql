-- v2: multi-project support.
--
-- Adds the projects/keys/users tables, a project_id column on log_entries,
-- and the project-scoped composite index. The standalone idx_timestamp is
-- dropped because every query now scopes by project first; the composite
-- (project_id, timestamp DESC) index serves both project-only lookups
-- (via index prefix) and project-scoped time-ordered scans.
--
-- A Go-side pre-flight check in internal/store/migration_hooks.go refuses
-- to apply the project_id column when log_entries has rows but projects
-- is empty — that would orphan every existing row.

CREATE TABLE IF NOT EXISTS projects (
    id              TEXT PRIMARY KEY,
    slug            TEXT NOT NULL UNIQUE,
    name            TEXT NOT NULL,
    retention_days  INTEGER,
    created_at      DATETIME NOT NULL,
    archived_at     DATETIME
);
CREATE INDEX IF NOT EXISTS idx_projects_slug ON projects(slug);

CREATE TABLE IF NOT EXISTS project_keys (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL,
    prefix       TEXT NOT NULL UNIQUE,
    hmac         TEXT NOT NULL,
    label        TEXT NOT NULL,
    created_at   DATETIME NOT NULL,
    last_used_at DATETIME,
    revoked_at   DATETIME,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_project_keys_project ON project_keys(project_id);
CREATE INDEX IF NOT EXISTS idx_project_keys_active
    ON project_keys(prefix) WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    DATETIME NOT NULL,
    last_login_at DATETIME
);

ALTER TABLE log_entries ADD COLUMN project_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_log_entries_project_timestamp
    ON log_entries(project_id, timestamp DESC);
DROP INDEX IF EXISTS idx_timestamp;
