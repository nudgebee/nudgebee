package core

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The agent's own logs / metrics / traces providers are one integrations row per
// tenant, shared by every cluster. Two things about that row have to be answered
// per account and cannot come from its status: whether this account has the
// provider configured (its link, unless a user disabled the row) and whether the
// account's own agent reports it up.
func TestAgentIntegrationState(t *testing.T) {
	matchState := regexp.MustCompile(`(?s)WITH linked AS \(.*FROM integrations i.*JOIN integrations_cloud_accounts ica.*WHERE ica\.cloud_account_id = \$1.*AND i\.tenant_id = \$2.*AND i\.type = \$3.*AND i\.name = i\.type.*AND i\.source = 'agent'.*NOT EXISTS \(.*FROM integration_config_values m.*m\.name = \$4.*EXISTS \(SELECT 1 FROM linked\).*JOIN agent a ON a\.cloud_account_id = l\.cloud_account_id AND a\.type != 'proxy'.*a\.status = 'CONNECTED'.*unnest\(\$5::text\[\]\).*a\.connection_status ->> flag = 'true'`)

	t.Run("configured but down: linked, agent does not report the signal up", func(t *testing.T) {
		pkgMock.ExpectQuery(matchState.String()).
			WithArgs("acc-1", testTenant, "prometheus", AgentIntegrationUserDisabledConfigName, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"configured", "usable"}).AddRow(true, false))

		configured, usable, err := AgentIntegrationState(ctxForTenant(t, testTenant), "acc-1", "prometheus", "prometheusConnection")
		require.NoError(t, err)
		assert.True(t, configured)
		assert.False(t, usable)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("the marker name is the one the collector reads", func(t *testing.T) {
		assert.Equal(t, "user_disabled", AgentIntegrationUserDisabledConfigName)
	})
}

// A user's Disable of an agent-source row has to outlive the agent's heartbeats.
// The collector sets that row's status from the user-disabled marker on every
// heartbeat, so the marker is written and removed together with the status.
func TestUpdateIntegrationConfigStatus_AgentRowMarker(t *testing.T) {
	const integrationId = "0b9d2f6e-1111-4111-8111-000000000001"
	matchLookup := `(?s)SELECT id, source\s+FROM integrations.*AND \(\$4 = '' OR source = \$4\)`
	matchStatus := `UPDATE integrations set status = \$2`
	matchEnableConflict := `(?s)SELECT ca\.account_name, i\.name.*AND i\.status = 'enabled'`

	expectLookup := func(source string) {
		pkgMock.ExpectQuery(matchLookup).
			WithArgs(testTenant, "prometheus", "prometheus", source).
			WillReturnRows(sqlmock.NewRows([]string{"id", "source"}).AddRow(integrationId, source))
	}

	t.Run("disabling the agent's row records the marker", func(t *testing.T) {
		ctx := ctxForTenant(t, testTenant)
		pkgMock.ExpectBegin()
		expectLookup("agent")
		pkgMock.ExpectExec(matchStatus).WithArgs(integrationId, "disabled").WillReturnResult(sqlmock.NewResult(0, 1))
		pkgMock.ExpectExec(`(?s)INSERT INTO integration_config_values.*ON CONFLICT\(integration_id, name\)`).
			WithArgs(sqlmock.AnyArg(), integrationId, AgentIntegrationUserDisabledConfigName, sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))
		pkgMock.ExpectCommit()

		require.NoError(t, UpdateIntegrationConfigStatus(ctx, "prometheus", "prometheus", "disabled", "agent"))
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("enabling the agent's row removes the marker", func(t *testing.T) {
		ctx := ctxForTenant(t, testTenant)
		pkgMock.ExpectBegin()
		expectLookup("agent")
		pkgMock.ExpectQuery(matchEnableConflict).WillReturnRows(sqlmock.NewRows([]string{"account_name", "name"}))
		pkgMock.ExpectExec(matchStatus).WithArgs(integrationId, "enabled").WillReturnResult(sqlmock.NewResult(0, 1))
		pkgMock.ExpectExec(`DELETE FROM integration_config_values WHERE integration_id = \$1 AND name = \$2`).
			WithArgs(integrationId, AgentIntegrationUserDisabledConfigName).
			WillReturnResult(sqlmock.NewResult(0, 1))
		pkgMock.ExpectCommit()

		require.NoError(t, UpdateIntegrationConfigStatus(ctx, "prometheus", "prometheus", "enabled", "agent"))
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("a hand-added row of the same type and name is found by its source and gets no marker", func(t *testing.T) {
		// No marker statement is expected: sqlmock fails the call if one is issued.
		ctx := ctxForTenant(t, testTenant)
		pkgMock.ExpectBegin()
		expectLookup("user")
		pkgMock.ExpectExec(matchStatus).WithArgs(integrationId, "disabled").WillReturnResult(sqlmock.NewResult(0, 1))
		pkgMock.ExpectCommit()

		require.NoError(t, UpdateIntegrationConfigStatus(ctx, "prometheus", "prometheus", "disabled", "user"))
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})
}
