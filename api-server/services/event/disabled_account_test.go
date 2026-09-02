package event

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// InvestigateEvent is the one place a switched-off cloud account can be honoured:
// every producer funnels through it — the trigger_investigation RPC used by
// k8s-collector and cloud-collector, and every incoming webhook via
// integrationcore.InvestigateEventFn. Nothing tells a customer's agent or a
// third-party alert source to stop sending when an account is disabled, so
// without this check we keep running the full enrichment path for accounts that
// are off, including relay calls back to their own unresponsive agents.
//
// The direction of the fail-open cases below is the important part. Reporting
// "disabled" on a lookup that did not actually say so would silently stop
// ingestion for a live tenant, which is far worse than processing a few events
// for an account that is off.

const (
	testDisabledAccountID = "c4a4f1d9-f6d9-4783-9a46-53dd02ea8117"
	testActiveAccountID   = "0b30143a-c0c4-43eb-9fdf-e5472f71d405"
)

// mockMetastoreForAccountStatus builds a throwaway manager rather than touching
// the package-global registry. isCloudAccountDisabled takes its manager as an
// argument, so there is nothing to gain from RegisterDatabaseManagerHook here —
// and something to lose: TestListEventResolutions registers a hook closing over
// its own manager, so a competing registration leaves it resolving ours and its
// expectations never see their query.
func mockMetastoreForAccountStatus(t *testing.T) (*database.DatabaseManager, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, mock
}

// The namespace is process-global and registered in init(), so leftovers from an
// earlier test would decide a later one.
func clearAccountStatusCache(t *testing.T) {
	t.Helper()
	require.NoError(t, common.CacheClear(disabledAccountCacheNamespace))
	t.Cleanup(func() { _ = common.CacheClear(disabledAccountCacheNamespace) })
}

// isCloudAccountDisabled only reads the logger off the context. Build a bare one
// rather than a tenant-admin context: NewSecurityContextForTenantAdmin resolves
// the tenant's account ids on construction, and that stray query lands on the
// same sqlmock and muddies the expectations these tests assert on.
func accountStatusTestContext() *security.RequestContext {
	return security.NewRequestContext(context.Background(), nil, slog.Default(), nil, nil)
}

func TestIsCloudAccountDisabled(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		want   bool
	}{
		{"disabled account is stopped", "disabled", true},
		{"active account is processed", "active", false},
		{"unrecognised status is processed", "suspended", false},
		{"empty status is processed", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearAccountStatusCache(t)
			manager, mock := mockMetastoreForAccountStatus(t)
			mock.ExpectQuery("SELECT COALESCE\\(status").
				WithArgs(testDisabledAccountID).
				WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(tc.status))

			got := isCloudAccountDisabled(accountStatusTestContext(), manager, testDisabledAccountID)
			assert.Equal(t, tc.want, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestIsCloudAccountDisabledFailsOpen(t *testing.T) {
	t.Run("unknown account", func(t *testing.T) {
		clearAccountStatusCache(t)
		manager, mock := mockMetastoreForAccountStatus(t)
		mock.ExpectQuery("SELECT COALESCE\\(status").
			WithArgs(testActiveAccountID).
			WillReturnRows(sqlmock.NewRows([]string{"status"}))

		assert.False(t, isCloudAccountDisabled(accountStatusTestContext(), manager, testActiveAccountID))
	})

	t.Run("query error", func(t *testing.T) {
		clearAccountStatusCache(t)
		manager, mock := mockMetastoreForAccountStatus(t)
		mock.ExpectQuery("SELECT COALESCE\\(status").
			WithArgs(testActiveAccountID).
			WillReturnError(errors.New("connection refused"))

		assert.False(t, isCloudAccountDisabled(accountStatusTestContext(), manager, testActiveAccountID))
	})
}

// An account id that is not a uuid reaches here from webhook payloads. It must
// short-circuit before the query, or the ::uuid cast errors on every event.
func TestIsCloudAccountDisabledSkipsQueryForUnusableIDs(t *testing.T) {
	for _, accountID := range []string{"", "not-a-uuid"} {
		t.Run("id="+accountID, func(t *testing.T) {
			clearAccountStatusCache(t)
			manager, mock := mockMetastoreForAccountStatus(t)

			assert.False(t, isCloudAccountDisabled(accountStatusTestContext(), manager, accountID))
			// No ExpectQuery was registered, so any query at all fails this.
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestIsCloudAccountDisabledCachesTheStatus(t *testing.T) {
	clearAccountStatusCache(t)
	manager, mock := mockMetastoreForAccountStatus(t)
	mock.ExpectQuery("SELECT COALESCE\\(status").
		WithArgs(testDisabledAccountID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("disabled"))

	sc := accountStatusTestContext()
	assert.True(t, isCloudAccountDisabled(sc, manager, testDisabledAccountID))
	// Only one query is queued; a second round trip would error here.
	assert.True(t, isCloudAccountDisabled(sc, manager, testDisabledAccountID))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A missing row is a race (the account may be mid-creation), not a decision, so
// it must not be remembered — otherwise the first event for a brand new account
// pins "not disabled" for the whole TTL.
func TestIsCloudAccountDisabledDoesNotCacheUnknownAccounts(t *testing.T) {
	clearAccountStatusCache(t)
	manager, mock := mockMetastoreForAccountStatus(t)
	mock.ExpectQuery("SELECT COALESCE\\(status").
		WithArgs(testDisabledAccountID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}))
	mock.ExpectQuery("SELECT COALESCE\\(status").
		WithArgs(testDisabledAccountID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("disabled"))

	sc := accountStatusTestContext()
	assert.False(t, isCloudAccountDisabled(sc, manager, testDisabledAccountID))
	assert.True(t, isCloudAccountDisabled(sc, manager, testDisabledAccountID))
	assert.NoError(t, mock.ExpectationsWereMet())
}
