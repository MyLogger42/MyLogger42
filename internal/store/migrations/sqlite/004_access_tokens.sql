-- MCP read tokens: global (not per-project), read-only credentials for AI
-- agents. Same (prefix, hmac) storage as project_keys — the plaintext
-- secret is never stored. Self-idempotent: runMigrations replays every
-- script on boot.

CREATE TABLE IF NOT EXISTS access_tokens (
    id          TEXT PRIMARY KEY,
    prefix      TEXT NOT NULL UNIQUE,
    hmac        TEXT NOT NULL,
    label       TEXT NOT NULL,
    created_at  DATETIME NOT NULL,
    revoked_at  DATETIME
);
CREATE INDEX IF NOT EXISTS idx_access_tokens_active
    ON access_tokens(prefix) WHERE revoked_at IS NULL;
