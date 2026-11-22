package tenant

import (
	"testing"

	"nudgebee/services/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const integrationTypeQuery = "SELECT type FROM integrations WHERE tenant_id = $1 AND id = $2"

func TestUpdateIntegrationStatusByPkRefusesMicrosoftTeamsRows(t *testing.T) {
	const tenantId = "00000000-0000-0000-0000-00000000aa01"
	ctx := tenantAdminCtx(t, tenantId, "00000000-0000-0000-0000-00000000aa02")
	for _, typ := range []string{"ms_teams", "ms_teams_team"} {
		ffMockDB.ExpectQuery(integrationTypeQuery).WithArgs(tenantId, "int-1").
			WillReturnRows(sqlmock.NewRows([]string{"type"}).AddRow(typ))
		_, err := UpdateIntegrationStatusByPk(ctx, IntegrationUpdateStatusByPkRequest{Id: "int-1", Status: "enabled"})
		assert.ErrorIs(t, err, common.ErrMsTeamsDisconnectOnly, typ)
	}

	ffMockDB.ExpectQuery(integrationTypeQuery).WithArgs(tenantId, "int-2").
		WillReturnRows(sqlmock.NewRows([]string{"type"}).AddRow("slack_webhook"))
	ffMockDB.ExpectExec("UPDATE integrations SET status = $1 WHERE id = $2").WithArgs("disabled", "int-2").
		WillReturnResult(sqlmock.NewResult(0, 1))
	resp, err := UpdateIntegrationStatusByPk(ctx, IntegrationUpdateStatusByPkRequest{Id: "int-2", Status: "disabled"})
	require.NoError(t, err)
	assert.Equal(t, "int-2", resp.Id)
	assert.NoError(t, ffMockDB.ExpectationsWereMet())
}
