package common

import (
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testFeature = "EVENT_AUTO_AI_SUMMARY"
	testTenant  = "00000000-0000-0000-0000-000000000001"
	testAccount = "00000000-0000-0000-0000-000000000002"
)

// Package-level sqlmock so the DatabaseManager cache (global state) keeps
// pointing at a valid handle for every test. Each test sets its own
// expectations on pkgMock and calls ExpectationsWereMet() at the end.
var (
	pkgMockDB sqlmock.Sqlmock
	pkgRawDB  interface{ Close() error }
)

func TestMain(m *testing.M) {
	// QueryMatcherEqual — sqlmock's default is regex-based, which mangles
	// Postgres $N positional parameters (`$` is a regex anchor).
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		slog.Error("feature_flag_test: sqlmock.New failed", "error", err)
		os.Exit(1)
	}
	RegisterDatabaseManagerHook(Metastore, func() (*DatabaseManager, error) {
		return &DatabaseManager{Db: sqlx.NewDb(rawDB, "postgresql")}, nil
	})
	pkgMockDB = mock
	pkgRawDB = rawDB

	code := m.Run()
	_ = pkgRawDB.Close()
	os.Exit(code)
}

const (
	accountQuery = "SELECT status FROM feature_flag WHERE feature_id = $1 AND tenant_id = $2 AND account_id = $3"
	tenantQuery  = "SELECT status FROM feature_flag WHERE feature_id = $1 AND tenant_id = $2 AND account_id IS NULL"
)

func TestIsFeatureEnabledByDefaultForAccount_EmptyTenantId(t *testing.T) {
	// Guard short-circuits — no DB call expected.
	enabled, err := IsFeatureEnabledByDefaultForAccount(testFeature, "", testAccount)
	require.NoError(t, err)
	assert.True(t, enabled, "empty tenantId must default to enabled")
}

func TestIsFeatureEnabledByDefaultForAccount_AccountDisabledWins(t *testing.T) {
	pkgMockDB.ExpectQuery(accountQuery).
		WithArgs(testFeature, testTenant, testAccount).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("disabled"))

	enabled, err := IsFeatureEnabledByDefaultForAccount(testFeature, testTenant, testAccount)
	require.NoError(t, err)
	assert.False(t, enabled, "account-scoped 'disabled' row must turn the feature off")
	assert.NoError(t, pkgMockDB.ExpectationsWereMet())
}

func TestIsFeatureEnabledByDefaultForAccount_AccountEnabledWins(t *testing.T) {
	pkgMockDB.ExpectQuery(accountQuery).
		WithArgs(testFeature, testTenant, testAccount).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("enabled"))

	enabled, err := IsFeatureEnabledByDefaultForAccount(testFeature, testTenant, testAccount)
	require.NoError(t, err)
	assert.True(t, enabled, "account-scoped 'enabled' row must keep the feature on")
	assert.NoError(t, pkgMockDB.ExpectationsWereMet())
}

func TestIsFeatureEnabledByDefaultForAccount_FallsBackToTenant(t *testing.T) {
	pkgMockDB.ExpectQuery(accountQuery).
		WithArgs(testFeature, testTenant, testAccount).
		WillReturnError(errors.New("sql: no rows in result set"))
	pkgMockDB.ExpectQuery(tenantQuery).
		WithArgs(testFeature, testTenant).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("disabled"))

	enabled, err := IsFeatureEnabledByDefaultForAccount(testFeature, testTenant, testAccount)
	require.NoError(t, err)
	assert.False(t, enabled, "tenant-scoped 'disabled' must be honored when no account row exists")
	assert.NoError(t, pkgMockDB.ExpectationsWereMet())
}

func TestIsFeatureEnabledByDefaultForAccount_NoRowsDefaultsEnabled(t *testing.T) {
	pkgMockDB.ExpectQuery(accountQuery).
		WithArgs(testFeature, testTenant, testAccount).
		WillReturnError(errors.New("sql: no rows in result set"))
	pkgMockDB.ExpectQuery(tenantQuery).
		WithArgs(testFeature, testTenant).
		WillReturnError(errors.New("sql: no rows in result set"))

	enabled, err := IsFeatureEnabledByDefaultForAccount(testFeature, testTenant, testAccount)
	require.NoError(t, err)
	assert.True(t, enabled, "no row anywhere must default to enabled")
	assert.NoError(t, pkgMockDB.ExpectationsWereMet())
}

func TestIsFeatureEnabledByDefaultForAccount_EmptyAccountSkipsAccountQuery(t *testing.T) {
	// Empty accountId must skip the account query.
	pkgMockDB.ExpectQuery(tenantQuery).
		WithArgs(testFeature, testTenant).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("enabled"))

	enabled, err := IsFeatureEnabledByDefaultForAccount(testFeature, testTenant, "")
	require.NoError(t, err)
	assert.True(t, enabled)
	assert.NoError(t, pkgMockDB.ExpectationsWereMet())
}

// IsFeatureEnabledForAccount (used by llm-server/api's handleFollowupCancel
// for LLM_FOLLOWUP_NEW_ENABLED) is the opt-in, default-DISABLED sibling of
// IsFeatureEnabledByDefaultForAccount above — no row anywhere means false,
// not true — and it queries via Queryx/rows.Next() rather than Get(), so a
// "not found" case is zero rows, not a driver error. These four tests were
// previously uncovered.
const (
	isEnabledAccountQuery = "SELECT account_id FROM feature_flag WHERE feature_id = $1 AND status = 'enabled' AND account_id = $2"
	isEnabledTenantQuery  = "SELECT tenant_id FROM feature_flag WHERE feature_id = $1 and status = 'enabled' and tenant_id = $2"
)

func TestIsFeatureEnabledForAccount_AccountLevelRowWins(t *testing.T) {
	pkgMockDB.ExpectQuery(isEnabledAccountQuery).
		WithArgs(testFeature, testAccount).
		WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(testAccount))

	enabled, err := IsFeatureEnabledForAccount(testFeature, testTenant, testAccount)
	require.NoError(t, err)
	assert.True(t, enabled, "an enabled account-level row must short-circuit true without reaching the tenant fallback")
	assert.NoError(t, pkgMockDB.ExpectationsWereMet())
}

func TestIsFeatureEnabledForAccount_FallsBackToTenantWhenNoAccountRow(t *testing.T) {
	pkgMockDB.ExpectQuery(isEnabledAccountQuery).
		WithArgs(testFeature, testAccount).
		WillReturnRows(sqlmock.NewRows([]string{"account_id"})) // zero rows, not an error — Queryx, not Get
	pkgMockDB.ExpectQuery(isEnabledTenantQuery).
		WithArgs(testFeature, testTenant).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}).AddRow(testTenant))

	enabled, err := IsFeatureEnabledForAccount(testFeature, testTenant, testAccount)
	require.NoError(t, err)
	assert.True(t, enabled, "an enabled tenant-level row must be honored when no account row exists")
	assert.NoError(t, pkgMockDB.ExpectationsWereMet())
}

func TestIsFeatureEnabledForAccount_NoRowsAnywhereDefaultsDisabled(t *testing.T) {
	pkgMockDB.ExpectQuery(isEnabledAccountQuery).
		WithArgs(testFeature, testAccount).
		WillReturnRows(sqlmock.NewRows([]string{"account_id"}))
	pkgMockDB.ExpectQuery(isEnabledTenantQuery).
		WithArgs(testFeature, testTenant).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}))

	enabled, err := IsFeatureEnabledForAccount(testFeature, testTenant, testAccount)
	require.NoError(t, err)
	assert.False(t, enabled, "opt-in: no row anywhere must default to disabled, unlike the ByDefault variant")
	assert.NoError(t, pkgMockDB.ExpectationsWereMet())
}

func TestIsFeatureEnabledForAccount_DBErrorFailsClosed(t *testing.T) {
	pkgMockDB.ExpectQuery(isEnabledAccountQuery).
		WithArgs(testFeature, testAccount).
		WillReturnError(errors.New("connection reset by peer"))

	enabled, err := IsFeatureEnabledForAccount(testFeature, testTenant, testAccount)
	assert.False(t, enabled, "a real DB error must fail closed (false), not silently default to enabled")
	assert.Error(t, err, "the error must propagate so the caller can distinguish a DB failure from a genuine not-enabled")
	assert.NoError(t, pkgMockDB.ExpectationsWereMet())
}
