package store

import (
	"context"
	"path/filepath"
	"testing"
)

// openTestStores opens a fresh SQLite database under t.TempDir, runs all
// migrations, and registers cleanup via t.Cleanup. Returns the *Stores
// aggregate so tests can exercise any of the four interfaces.
//
// Tests that need raw *sql.DB access can dig in via Stores.Logs (it's an
// *sqliteStore inside this package) — but most tests should stick to
// the public store-interface methods.
func openTestStores(t *testing.T) *Stores {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	stores, err := OpenSQLiteStores(context.Background(), dsn)
	if err != nil {
		t.Fatalf("OpenSQLiteStores: %v", err)
	}
	t.Cleanup(func() { _ = stores.Logs.Close() })
	return stores
}
