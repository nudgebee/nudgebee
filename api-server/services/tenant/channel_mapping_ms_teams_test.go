package tenant

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	mapTenant      = "00000000-0000-0000-0000-00000000bb01"
	mapOtherTenant = "00000000-0000-0000-0000-00000000bb09"
	ownTeamQuery   = `SELECT count(*) FROM integrations WHERE tenant_id = $1 AND type = 'ms_teams_team' AND name = $2 AND status = 'enabled'`
	insertMapping  = `INSERT INTO notification_channel_account_mappings (account_id, platform, team_id, channel_id, created_by, tenant_id)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, account_id, platform, team_id, channel_id, created_by, created_at`
	conflictQuery = `SELECT ca.account_name, t.name, m.tenant_id::text
		FROM notification_channel_account_mappings m
		JOIN cloud_accounts ca ON ca.id = m.account_id
		JOIN tenant t ON t.id = m.tenant_id
		WHERE m.platform = $1 AND m.team_id = $2 AND m.channel_id = $3 LIMIT 1`
	mappingPlatformQuery = "SELECT platform FROM notification_channel_account_mappings WHERE id = $1 AND tenant_id = $2"
)

func teamsMapping() NotificationChannelMappingCreateRequest {
	return NotificationChannelMappingCreateRequest{Platform: "ms_teams", TeamId: "group-1", ChannelId: "19:ops"}
}

func TestATeamsMappingNeedsATeamBoundToTheWorkspace(t *testing.T) {
	ctx := tenantAdminCtx(t, mapTenant, "00000000-0000-0000-0000-00000000bb02")
	ffMockDB.ExpectQuery(ownTeamQuery).WithArgs(mapTenant, "group-1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	_, err := CreateNotificationChannelMapping(ctx, teamsMapping())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "isn't connected to this workspace")

	ffMockDB.ExpectQuery(mappingPlatformQuery).WithArgs("map-1", mapTenant).
		WillReturnRows(sqlmock.NewRows([]string{"platform"}).AddRow("ms_teams"))
	ffMockDB.ExpectQuery(ownTeamQuery).WithArgs(mapTenant, "group-9").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	_, err = UpdateNotificationChannelMapping(ctx, NotificationChannelMappingUpdateRequest{Id: "map-1", TeamId: "group-9", ChannelId: "19:x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "isn't connected to this workspace")
	assert.NoError(t, ffMockDB.ExpectationsWereMet(), "nothing is written")
}

func TestAMappingConflictNamesOnlyTheCallersOwnAccount(t *testing.T) {
	ctx := tenantAdminCtx(t, mapTenant, "00000000-0000-0000-0000-00000000bb02")
	for _, tc := range []struct {
		owner string
		want  string
	}{
		{mapOtherTenant, "This channel is already mapped"},
		{mapTenant, "19:ops is already mapped with account prod in Acme tenant"},
	} {
		ffMockDB.ExpectQuery(ownTeamQuery).WithArgs(mapTenant, "group-1").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		ffMockDB.ExpectQuery(insertMapping).WillReturnError(&pq.Error{Code: "23505"})
		ffMockDB.ExpectQuery(conflictQuery).WithArgs("ms_teams", "group-1", "19:ops").
			WillReturnRows(sqlmock.NewRows([]string{"account_name", "name", "tenant_id"}).AddRow("prod", "Acme", tc.owner))
		_, err := CreateNotificationChannelMapping(ctx, teamsMapping())
		require.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
		if tc.owner != mapTenant {
			assert.NotContains(t, err.Error(), "prod")
			assert.NotContains(t, err.Error(), "Acme")
		}
	}
	assert.NoError(t, ffMockDB.ExpectationsWereMet())
}
