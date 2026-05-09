package migrations

import "embed"

// SQLite holds the bundled SQLite migration files.
//
//go:embed sqlite/*.sql
var SQLite embed.FS

// Postgres holds the bundled Postgres migration files.
//
//go:embed postgres/*.sql
var Postgres embed.FS
