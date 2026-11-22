package core

import (
	"log/slog"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A workspace's ms_teams integration is written only when a team is bound to
// it, so the backfill never creates one from a legacy row.
func TestBackfillMessagingIntegrationsReadsOnlySlack(t *testing.T) {
	columns := []string{"id", "tenant_id", "platform", "team_id", "team_name", "token", "refresh_token", "bot_id",
		"app_id", "client_id", "username", "scopes", "channels", "token_expires_at"}
	pkgMock.ExpectQuery(`(?s)FROM messaging_platforms.*WHERE platform = 'slack'`).
		WillReturnRows(sqlmock.NewRows(columns))

	result, err := BackfillMessagingIntegrations(slog.Default(), nil, nil)
	require.NoError(t, err)
	assert.Equal(t, MessagingBackfillResult{}, result)
	assert.NoError(t, pkgMock.ExpectationsWereMet(), "no integration is looked up or created")
}
