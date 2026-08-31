package migrations

import "embed"

// SQLite holds the bundled SQLite migration files.
//
//go:embed sqlite/*.sql
var SQLite embed.FS
