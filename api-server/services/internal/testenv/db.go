package testenv

import (
	"context"
	"testing"
	"time"

	"nudgebee/services/internal/database"
)

// requireDB skips the test unless the named database manager initializes and
// its connection is live. Many backend tests obtain their connection via the
// database manager, which eagerly dials and pings the database; without one
// they fail hard (or panic on a nil handle) rather than skip. The manager is
// cached process-wide, so a sibling test may have registered (and then closed)
// a stub manager, leaving a non-nil but dead handle behind. We therefore also
// ping the connection and skip when it is unreachable. Call this first so such
// tests skip cleanly in the default run.
func requireDB(t testing.TB, name database.DatabaseManagerType) *database.DatabaseManager {
	t.Helper()
	mgr, err := database.GetDatabaseManager(name)
	if err != nil {
		t.Skipf("skipping: %s database unavailable (%v)", name, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if mgr == nil || mgr.Db == nil {
		t.Skipf("skipping: %s database unavailable (nil handle)", name)
	}
	if err := mgr.Db.PingContext(ctx); err != nil {
		t.Skipf("skipping: %s database unreachable (%v)", name, err)
	}
	return mgr
}

// RequireMetastore skips the test unless the Postgres metastore is reachable.
func RequireMetastore(t testing.TB) *database.DatabaseManager {
	t.Helper()
	return requireDB(t, database.Metastore)
}

// RequireWarehouse skips the test unless the Clickhouse warehouse is reachable.
func RequireWarehouse(t testing.TB) *database.DatabaseManager {
	t.Helper()
	return requireDB(t, database.Warehouse)
}
