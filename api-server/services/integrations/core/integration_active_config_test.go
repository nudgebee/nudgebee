package core

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestListActiveIntegrationConfigs_AccountAndTenantScoped(t *testing.T) {
	ctx := ctxForTenant(t, testTenant)
	pkgMock.ExpectQuery(`(?s)SELECT DISTINCT i\.id::text.*FROM integrations i.*i\.type = 'ES'.*i\.tenant_id = '.*ica\.cloud_account_id = 'acc-1'.*i\.status != 'disabled'.*ica\.tenant_id = i\.tenant_id.*ca\.tenant = i\.tenant_id`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "account_id", "name", "labels", "source", "type"}))
	rows, err := ListActiveIntegrationConfigs(ctx, "acc-1", "ES")
	require.NoError(t, err)
	require.Empty(t, rows)
	require.NoError(t, pkgMock.ExpectationsWereMet())
}

func TestListActiveIntegrationConfigs_Preview(t *testing.T) {
	ctx := WithConfigOverride(overrideCtx(t), sampleOverride())
	failDB(t)
	rows, err := ListActiveIntegrationConfigs(ctx, "acc-1", "ES")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Empty(t, rows[0].Id)
	require.Equal(t, "https://localhost:9200", rows[0].Configs[0].Value)
	_, err = ListActiveIntegrationConfigs(ctx, "acc-2", "ES")
	require.Error(t, err)
	_, err = ListActiveIntegrationConfigs(ctx, "acc-1", "pinot")
	require.Error(t, err)
	_, err = ListActiveIntegrationConfigs(ctx, "", "ES")
	require.Error(t, err)
}
