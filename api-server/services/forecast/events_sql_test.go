package forecast

import (
	"testing"
	"time"

	"nudgebee/services/internal/database"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

func mockMetastore(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })
	return mock
}

// A Deployment and a StatefulSet can share a name in one namespace. The self-repair
// must not close the other workload's memory alerts as a stale container.
func TestStaleContainerFingerprints_ScopedToTheWorkloadKind(t *testing.T) {
	mock := mockMetastore(t)
	res := memoryBreach()
	mock.ExpectQuery(`subject_owner_kind = \$10`).
		WithArgs(target.AccountId, target.TenantId, AggregationKey(res.Condition), BreachedAggregationKey(res.Condition),
			Source, "shop", "orders", string(ConditionMemorySaturation), "api", "deployment").
		WillReturnRows(sqlmock.NewRows([]string{"fingerprint"}))

	_, err := staleContainerFingerprints(target, res)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCloseUnverifiedEvents_OnlyADayUnconfirmed(t *testing.T) {
	mock := mockMetastore(t)
	orig := nowFn
	t.Cleanup(func() { nowFn = orig })
	nowFn = func() time.Time { return syncNow }

	mock.ExpectQuery(`UPDATE events`).
		WithArgs(target.TenantId, target.AccountId, "fp", Source, syncNow, CloseUnverified, syncNow.Add(-24*time.Hour)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	require.NoError(t, closeUnverifiedEvents(target, "fp"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// An unverified close says nothing about the workload, so it starts no cooldown.
func TestRecentlyClosed_SkipsUnverified(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectQuery(`NOT IN \(\$6, \$7, \$8\)`).
		WithArgs(target.TenantId, target.AccountId, "fp", Source, syncNow, CloseUnwatched, CloseLevelChange, CloseUnverified).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	_, err := recentlyClosed(target, "fp", syncNow)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
