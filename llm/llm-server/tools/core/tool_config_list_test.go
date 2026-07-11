package core

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// registerMockMetastore wires a sqlmock-backed DB as the Metastore so
// ListAllToolConfigs runs against canned rows. The default sqlmock matcher is
// regexp, so query expectations match on substrings.
func registerMockMetastore(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	sqlxDB := sqlx.NewDb(db, "postgresql")
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlxDB}, nil
	})
	return mock
}

// TestListAllToolConfigs_IncludesTenantScopedIntegrations is the regression
// guard for #32019: tenant-scoped integrations (messaging / ticketing) carry no
// integrations_cloud_accounts row, and the old INNER JOIN on that table silently
// dropped them — so the workflow builder reported a configured Slack workspace as
// "not configured". The query must now return both account-scoped integrations
// mapped to this account AND tenant-scoped integrations with no account mapping.
func TestListAllToolConfigs_IncludesTenantScopedIntegrations(t *testing.T) {
	mock := registerMockMetastore(t)

	const accountId = "acct-1"
	const tenantId = "tenant-1"

	// 1. tenant lookup for the account
	mock.ExpectQuery(`SELECT tenant FROM cloud_accounts WHERE id = \$1`).
		WithArgs(accountId).
		WillReturnRows(sqlmock.NewRows([]string{"tenant"}).AddRow(tenantId))

	// 2. cloud accounts for the tenant (none — keeps the test focused on integrations)
	mock.ExpectQuery(`FROM cloud_accounts\s+WHERE tenant = \$1`).
		WithArgs(tenantId).
		WillReturnRows(sqlmock.NewRows([]string{"id", "account_name", "account_number", "cloud_provider"}))

	// 3. integrations: account-scoped (postgres, mapped) + tenant-scoped (slack, unmapped).
	//    The fix passes (tenantId, accountId) — assert that arg order too.
	integrationCols := []string{"id", "name", "type", "labels", "config_name", "config_value", "config_encrypted"}
	mock.ExpectQuery(`FROM integrations i.*NOT EXISTS`).
		WithArgs(tenantId, accountId).
		WillReturnRows(sqlmock.NewRows(integrationCols).
			AddRow("int-pg", "prod-pg", "postgresql", "{}", "host", "db.internal", false).
			AddRow("int-slack", "acme-workspace", "slack", "{}", "default_channel_id", "C123", false))

	ctx := &security.RequestContext{}
	configs, err := ListAllToolConfigs(ctx, accountId)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	byType := map[string]string{} // lower(type) -> name
	for _, cfg := range configs {
		byType[cfg.Schema.ConfigType] = cfg.Name
	}

	assert.Equal(t, "prod-pg", byType["postgresql"], "account-scoped integration must still be returned")
	assert.Equal(t, "acme-workspace", byType["slack"],
		"tenant-scoped integration (no integrations_cloud_accounts row) must be returned — #32019")
}
