package event

import (
	"database/sql/driver"
	"errors"
	"nudgebee/services/internal/database"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eventResolutionColumns mirrors the column list selected by
// ListEventResolutions.
var eventResolutionColumns = []string{
	"id", "created_at", "updated_at", "event_id", "type", "data", "status",
	"type_reference_id", "resolver_type", "resolver_id", "status_message",
	"pr_iteration_count", "pr_lifecycle_state", "last_pr_check_at",
}

// TestListEventResolutions is a regression test for two defects in
// ListEventResolutions:
//
//  1. the result set was never closed on the scan-error early-return path, so a
//     StructScan failure leaked the underlying connection for the process
//     lifetime; and
//  2. the loop never checked rows.Err(), so a mid-iteration driver error was
//     swallowed and the caller received a silently truncated slice with a nil
//     error.
//
// Both sub-tests drive the helper with a sqlmock-backed Metastore, so they run
// with no live database.
func TestListEventResolutions(t *testing.T) {
	manager := &database.DatabaseManager{}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return manager, nil
	})

	// newMock swaps a fresh sqlmock-backed connection onto the manager the
	// registry caches, giving each sub-test isolated expectations.
	newMock := func(t *testing.T) sqlmock.Sqlmock {
		t.Helper()
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		manager.Db = sqlx.NewDb(db, "postgres")
		return mock
	}

	t.Run("closes the result set when row scanning fails", func(t *testing.T) {
		mock := newMock(t)
		// A column with no destination field forces StructScan to fail, which
		// triggers the early return that used to leave the rows open.
		rows := sqlmock.NewRows([]string{"unexpected_column"}).AddRow("x")
		mock.ExpectQuery("FROM event_resolution").
			WillReturnRows(rows).
			RowsWillBeClosed()

		_, err := ListEventResolutions(nil, "event-1")

		require.Error(t, err)
		// RowsWillBeClosed fails against the pre-fix code, which returned
		// without closing the result set.
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("surfaces a mid-iteration error instead of truncating", func(t *testing.T) {
		mock := newMock(t)
		boom := errors.New("connection reset mid-stream")
		rows := sqlmock.NewRows(eventResolutionColumns).
			AddRow(make([]driver.Value, len(eventResolutionColumns))...).
			RowError(0, boom)
		mock.ExpectQuery("FROM event_resolution").
			WillReturnRows(rows).
			RowsWillBeClosed()

		resolutions, err := ListEventResolutions(nil, "event-1")

		// Pre-fix the iteration error was dropped: err was nil and a truncated
		// slice was returned.
		require.Error(t, err)
		assert.ErrorIs(t, err, boom)
		assert.Empty(t, resolutions)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
