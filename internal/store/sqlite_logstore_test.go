package store

import "testing"

// TestSQLiteLogStoreConformance runs the shared LogStore contract against
// the SQLite backend. SQLite supports every capability; the VictoriaLogs
// runner (build tag `vlogs`) declares its divergences instead.
func TestSQLiteLogStoreConformance(t *testing.T) {
	runLogStoreConformance(t, func(t *testing.T) LogStore {
		return openTestStores(t).Logs
	}, storeCaps{
		substringSearch:  true,
		deletes:          true,
		sourceTimestamps: true,
	})
}
