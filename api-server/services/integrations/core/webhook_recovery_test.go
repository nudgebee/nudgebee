package core

import (
	"testing"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Both cutoffs are computed by the database (now() AT TIME ZONE 'utc'), so the
// only Go-side argument is the age in seconds — a Go-side timestamp would be in
// the pod's local zone and would shift the window against a UTC column.
//
// The reaper is the only thing that ever moves a row out of 'processing', and
// the only thing that re-drives a 'pending' row whose queue message was lost.
// Both queries are pinned here because getting either predicate wrong strands
// deliveries silently — the failure mode this whole file exists to end.

func mockMetastoreForRecovery(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	// Put back whatever metastore was there before, not a manager with a nil Db,
	// which nil-panics any later test in the package that reaches the metastore.
	previous, hadPrevious := database.GetDatabaseManagerIfInitialized(database.Metastore)
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, nil
	})
	t.Cleanup(func() {
		_ = db.Close()
		if hadPrevious {
			database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) { return previous, nil })
			return
		}
		database.RegisterDatabaseManagerHook(database.Metastore, nil)
	})
	return mock
}

func TestRecoverStuckWebhooksResetsStalledRowsBeforeClaiming(t *testing.T) {
	mock := mockMetastoreForRecovery(t)

	mock.ExpectExec("UPDATE event_incoming_webhooks SET processing_status = 'pending'").
		WithArgs(stuckWebhookProcessingAge.Seconds()).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("UPDATE event_incoming_webhooks SET processing_status = 'skipped'").
		WithArgs(stuckWebhookMaxAge().Seconds(), stuckWebhookExpiredError).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectQuery("SELECT id::text FROM event_incoming_webhooks").
		WithArgs(stuckWebhookPendingAge.Seconds(), stuckWebhookBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	err := RecoverStuckWebhooks(security.NewRequestContextForSuperAdmin(nil, nil, nil))

	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet(), "reset and expiry must run before the pending scan")
}

func TestRecoverStuckWebhooksStopsWhenNothingIsStuck(t *testing.T) {
	mock := mockMetastoreForRecovery(t)

	mock.ExpectExec("UPDATE event_incoming_webhooks").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE event_incoming_webhooks").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT id::text FROM event_incoming_webhooks").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	// No claim attempt should follow an empty scan.
	err := RecoverStuckWebhooks(security.NewRequestContextForSuperAdmin(nil, nil, nil))

	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRecoverStuckWebhooksClaimsEachStuckRow(t *testing.T) {
	mock := mockMetastoreForRecovery(t)

	mock.ExpectExec("UPDATE event_incoming_webhooks").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE event_incoming_webhooks").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT id::text FROM event_incoming_webhooks").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("row-1").AddRow("row-2"))
	// ProcessStoredWebhook claims pending → processing per row; 0 rows affected
	// means another worker got there first, which must not be an error.
	mock.ExpectExec("UPDATE event_incoming_webhooks SET processing_status = 'processing'").
		WithArgs("row-1").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE event_incoming_webhooks SET processing_status = 'processing'").
		WithArgs("row-2").WillReturnResult(sqlmock.NewResult(0, 0))

	err := RecoverStuckWebhooks(security.NewRequestContextForSuperAdmin(nil, nil, nil))

	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStuckWebhookMaxAgeDefaultsToOneHour(t *testing.T) {
	assert.Equal(t, time.Hour, stuckWebhookMaxAge())
}

func TestStuckWebhookMaxAgeZeroSkipsEveryPendingRow(t *testing.T) {
	previous := config.Config.StuckWebhookRecoveryMaxAgeMinutes
	t.Cleanup(func() { config.Config.StuckWebhookRecoveryMaxAgeMinutes = previous })

	config.Config.StuckWebhookRecoveryMaxAgeMinutes = 0
	assert.Equal(t, time.Duration(0), stuckWebhookMaxAge())
}
