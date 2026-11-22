package account

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A legacy Microsoft Teams row is never a connection: Teams is reached only
// through a bound team.
func TestListMessagingPlatformsLeavesOutMicrosoftTeams(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"tenant-1","UserId":"u1","Roles":["tenant_admin"]}`), &sc))
	ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
	cols := []string{"id", "username", "team_name", "created_at", "team_id", "channels", "platform"}

	mock.ExpectQuery(`FROM messaging_platforms WHERE tenant_id = \$1 AND platform IS DISTINCT FROM 'ms_teams'`).
		WithArgs("tenant-1").WillReturnRows(sqlmock.NewRows(cols))
	_, err = ListMessagingPlatforms(ctx, MessagingPlatformListRequest{})
	require.NoError(t, err)

	mock.ExpectQuery(`FROM messaging_platforms WHERE tenant_id = \$1 AND platform = \$2 AND platform <> 'ms_teams'`).
		WithArgs("tenant-1", "ms_teams").WillReturnRows(sqlmock.NewRows(cols))
	resp, err := ListMessagingPlatforms(ctx, MessagingPlatformListRequest{Platform: "ms_teams"})
	require.NoError(t, err)
	assert.Empty(t, resp.Data)
	assert.NoError(t, mock.ExpectationsWereMet())
}
