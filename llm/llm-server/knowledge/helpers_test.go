package knowledge

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
)

// registerUnavailableMetastore makes the metastore unavailable for the test, so
// code under test runs from caches only. Same helper as tools/core's tests.
func registerUnavailableMetastore(t *testing.T) {
	t.Helper()
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return nil, errors.New("metastore unavailable in cache-only test")
	})
	t.Cleanup(func() { common.RegisterDatabaseManagerHook(common.Metastore, nil) })
}

// registerMockMetastore wires a sqlmock-backed DB as the Metastore. Same helper
// as tools/core's tests. The default sqlmock matcher is regexp, so query
// expectations match on substrings.
func registerMockMetastore(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		common.RegisterDatabaseManagerHook(common.Metastore, nil)
		_ = db.Close()
	})
	sqlxDB := sqlx.NewDb(db, "postgresql")
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlxDB}, nil
	})
	return mock
}
