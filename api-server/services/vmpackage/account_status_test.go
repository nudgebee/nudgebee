package vmpackage

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"

	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const accountStatusQuery = `SELECT count\(\*\) FROM cloud_accounts WHERE id::text = ANY\(\$1\) AND status = 'active'`

func TestAccountsActive_AllActive(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery(accountStatusQuery).
		WithArgs(pq.Array([]string{"vm-account", "aws-account"})).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	active, err := accountsActive(mockDBManager, "vm-account", "aws-account")
	require.NoError(t, err)
	assert.True(t, active)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountsActive_OneNotActive(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery(accountStatusQuery).
		WithArgs(pq.Array([]string{"vm-account", "aws-account"})).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	active, err := accountsActive(mockDBManager, "vm-account", "aws-account")
	require.NoError(t, err)
	assert.False(t, active)
}

// A datasource with no target account, or whose target is its own account,
// checks that one account once: counting it twice would never reach two.
func TestAccountsActive_SkipsEmptyAndDuplicateIDs(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery(accountStatusQuery).
		WithArgs(pq.Array([]string{"vm-account"})).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	active, err := accountsActive(mockDBManager, "vm-account", "vm-account", "")
	require.NoError(t, err)
	assert.True(t, active)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountsActive_ReadErrorIsReturned(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery(accountStatusQuery).WillReturnError(errors.New("connection reset by peer"))

	active, err := accountsActive(mockDBManager, "vm-account")
	require.Error(t, err)
	assert.False(t, active)
}

func scanCtxWithLog(buf *bytes.Buffer) *security.RequestContext {
	return security.NewRequestContextForSuperAdmin(slog.New(slog.NewTextHandler(buf, nil)), nil, nil)
}

// A datasource on an account that is not active stops before the relay sweep:
// no datasource_key lookup, so no relay call and nothing written.
func TestScanDiscoveryDatasource_SkipsAccountNotActive(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery(accountStatusQuery).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	var logs bytes.Buffer
	ScanDiscoveryDatasource(scanCtxWithLog(&logs), mockDBManager, DiscoveryDatasource{
		IntegrationID:   "int-1",
		TenantID:        "tenant-1",
		AccountID:       "vm-account",
		TargetAccountID: "aws-account",
	})

	require.NoError(t, mock.ExpectationsWereMet())
	assert.Contains(t, logs.String(), "account not active, skipping")
	assert.NotContains(t, logs.String(), "datasource_key")
}

func TestScanDiscoveryDatasource_ActiveAccountProceeds(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery(accountStatusQuery).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT value FROM integration_config_values").
		WithArgs("int-1").
		WillReturnError(errors.New("stop here"))

	var logs bytes.Buffer
	ScanDiscoveryDatasource(scanCtxWithLog(&logs), mockDBManager, DiscoveryDatasource{
		IntegrationID: "int-1",
		TenantID:      "tenant-1",
		AccountID:     "vm-account",
	})

	require.NoError(t, mock.ExpectationsWereMet())
	assert.Contains(t, logs.String(), "resolve datasource_key failed")
}

func TestScanPackages_RejectsAccountNotActive(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("FROM cloud_accounts WHERE id = \\$1").
		WithArgs("vm-account").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant", "status", "account_type"}).
			AddRow("vm-account", "", "disabled", "vm"))

	_, err := ScanPackages(security.NewRequestContextForSuperAdmin(nil, nil, nil), ScanRequest{
		AccountId:       "vm-account",
		CloudResourceId: "resource-1",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not active")
	require.NoError(t, mock.ExpectationsWereMet())
}
