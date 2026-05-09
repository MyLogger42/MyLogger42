CREATE TABLE IF NOT EXISTS log_entries (
    id TEXT PRIMARY KEY,
    service TEXT NOT NULL,
    level TEXT NOT NULL,
    message TEXT NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS idx_service ON log_entries(service);
CREATE INDEX IF NOT EXISTS idx_level ON log_entries(level);
CREATE INDEX IF NOT EXISTS idx_timestamp ON log_entries(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_received_at ON log_entries(received_at);
