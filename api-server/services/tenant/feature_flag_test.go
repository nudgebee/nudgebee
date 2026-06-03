package tenant

import (
	"errors"
	"log/slog"
	"nudgebee/services/internal/database"
	"os"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	ffTenant  = "00000000-0000-0000-0000-000000000001"
	ffAccount = "00000000-0000-0000-0000-000000000002"
)

// Package-level sqlmock — the DatabaseManager cache (global) keeps pointing
// at one handle for the whole run. The existing TestDeleteTenant in this
// package gates on TEST_DELETE_TENANT_ID via testenv.RequireEnv and skips
// when unset, so it doesn't try to issue real SQL against the mock.
var (
	ffMockDB sqlmock.Sqlmock
	ffRawDB  interface{ Close() error }
)

func TestMain(m *testing.M) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		slog.Error("feature_flag_test: sqlmock.New failed", "error", err)
		os.Exit(1)
	}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgresql")}, nil
	})
	ffMockDB = mock
	ffRawDB = rawDB

	code := m.Run()
	_ = ffRawDB.Close()
	os.Exit(code)
}

const (
	accountQueryAPI = "SELECT status FROM feature_flag WHERE feature_id = $1 AND tenant_id = $2::uuid AND account_id = $3::uuid"
	tenantQueryAPI  = "SELECT status FROM feature_flag WHERE feature_id = $1 AND tenant_id = $2::uuid AND account_id IS NULL"
)

// Each test uses a unique feature id so the package-level featureFlagCache
// can't bleed values between tests.

func TestIsFeatureEnabledByDefaultForAccount_EmptyTenantId(t *testing.T) {
	enabled := IsFeatureEnabledByDefaultForAccount(nil, "", ffAccount, "TEST_FF_EMPTY_TENANT")
	assert.True(t, enabled, "empty tenantId must default to enabled with no DB call")
}

func TestIsFeatureEnabledByDefaultForAccount_AccountDisabledWins(t *testing.T) {
	feature := "TEST_FF_ACCOUNT_DISABLED"
	ffMockDB.ExpectQuery(accountQueryAPI).
		WithArgs(feature, ffTenant, ffAccount).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("disabled"))

	enabled := IsFeatureEnabledByDefaultForAccount(nil, ffTenant, ffAccount, feature)
	assert.False(t, enabled, "account-scoped 'disabled' row must turn the feature off")
	require.NoError(t, ffMockDB.ExpectationsWereMet())
}

func TestIsFeatureEnabledByDefaultForAccount_AccountEnabledWins(t *testing.T) {
	feature := "TEST_FF_ACCOUNT_ENABLED"
	ffMockDB.ExpectQuery(accountQueryAPI).
		WithArgs(feature, ffTenant, ffAccount).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("enabled"))

	enabled := IsFeatureEnabledByDefaultForAccount(nil, ffTenant, ffAccount, feature)
	assert.True(t, enabled, "account-scoped 'enabled' row keeps feature on")
	require.NoError(t, ffMockDB.ExpectationsWereMet())
}

func TestIsFeatureEnabledByDefaultForAccount_FallsBackToTenant(t *testing.T) {
	feature := "TEST_FF_TENANT_FALLBACK"
	ffMockDB.ExpectQuery(accountQueryAPI).
		WithArgs(feature, ffTenant, ffAccount).
		WillReturnError(errors.New("sql: no rows in result set"))
	ffMockDB.ExpectQuery(tenantQueryAPI).
		WithArgs(feature, ffTenant).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("disabled"))

	enabled := IsFeatureEnabledByDefaultForAccount(nil, ffTenant, ffAccount, feature)
	assert.False(t, enabled, "tenant 'disabled' honored when no account row exists")
	require.NoError(t, ffMockDB.ExpectationsWereMet())
}

func TestIsFeatureEnabledByDefaultForAccount_NoRowsDefaultsEnabled(t *testing.T) {
	feature := "TEST_FF_NO_ROWS"
	ffMockDB.ExpectQuery(accountQueryAPI).
		WithArgs(feature, ffTenant, ffAccount).
		WillReturnError(errors.New("sql: no rows in result set"))
	ffMockDB.ExpectQuery(tenantQueryAPI).
		WithArgs(feature, ffTenant).
		WillReturnError(errors.New("sql: no rows in result set"))

	enabled := IsFeatureEnabledByDefaultForAccount(nil, ffTenant, ffAccount, feature)
	assert.True(t, enabled, "no row anywhere must default to enabled")
	require.NoError(t, ffMockDB.ExpectationsWereMet())
}

func TestIsFeatureEnabledByDefaultForAccount_EmptyAccountDelegatesTenantOnly(t *testing.T) {
	feature := "TEST_FF_EMPTY_ACCOUNT"
	// Empty accountId → call delegates to IsFeatureEnabledByDefault, which
	// hits only the tenant query.
	ffMockDB.ExpectQuery(tenantQueryAPI).
		WithArgs(feature, ffTenant).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("enabled"))

	enabled := IsFeatureEnabledByDefaultForAccount(nil, ffTenant, "", feature)
	assert.True(t, enabled)
	require.NoError(t, ffMockDB.ExpectationsWereMet())
}

func TestIsFeatureEnabledByDefaultForAccount_CacheHitSkipsDB(t *testing.T) {
	feature := "TEST_FF_CACHE_HIT"
	// First call populates cache.
	ffMockDB.ExpectQuery(accountQueryAPI).
		WithArgs(feature, ffTenant, ffAccount).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("disabled"))
	_ = IsFeatureEnabledByDefaultForAccount(nil, ffTenant, ffAccount, feature)
	require.NoError(t, ffMockDB.ExpectationsWereMet())

	// Second call must hit cache only — no new expectation set, so any DB
	// call would surface as an unexpected query and fail.
	enabled := IsFeatureEnabledByDefaultForAccount(nil, ffTenant, ffAccount, feature)
	assert.False(t, enabled, "cache must replay the first call's result")
	require.NoError(t, ffMockDB.ExpectationsWereMet())
}
