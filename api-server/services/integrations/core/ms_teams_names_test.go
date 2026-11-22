package core

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

func TestCheckMsTeamsGenericWrite(t *testing.T) {
	const query = `SELECT type, name FROM integrations WHERE id = \$1 AND tenant_id = \$2`
	row := func(typ, name string) *sqlmock.Rows {
		return sqlmock.NewRows([]string{"type", "name"}).AddRow(typ, name)
	}
	values := func(names ...string) []IntegrationConfigValue {
		out := []IntegrationConfigValue{}
		for _, n := range names {
			out = append(out, IntegrationConfigValue{Name: n, Value: "v"})
		}
		return out
	}
	defaults := values("default_team_id", "default_team_name", "default_channel_id", "default_channel_name")

	t.Run("other types are created freely", func(t *testing.T) {
		assert.NoError(t, CheckMsTeamsGenericWrite(testTenant, "slack", "", "T1", nil))
		assert.NoError(t, CheckMsTeamsGenericWrite(testTenant, "google_chat_space", "", "spaces/AAA", nil))
	})

	t.Run("a generic create is refused in any letter case", func(t *testing.T) {
		for _, typ := range []string{"ms_teams", "ms_teams_team", "MS_Teams", " MS_TEAMS_TEAM "} {
			assert.ErrorIs(t, CheckMsTeamsGenericWrite(testTenant, typ, "", "victim", nil), ErrMsTeamsCreatedByConnect, typ)
		}
	})

	t.Run("the default destination of a connection can be saved", func(t *testing.T) {
		pkgMock.ExpectQuery(query).WithArgs("int-1", testTenant).WillReturnRows(row("ms_teams", "tenant-a"))
		assert.NoError(t, CheckMsTeamsGenericWrite(testTenant, "ms_teams", "int-1", "tenant-a", defaults))
	})

	t.Run("any other field of a connection is refused", func(t *testing.T) {
		for _, field := range []string{"app_id", "installed_by", "client_id", "access_token", "integration_config_name"} {
			pkgMock.ExpectQuery(query).WithArgs("int-1", testTenant).WillReturnRows(row("ms_teams", "tenant-a"))
			assert.ErrorIs(t, CheckMsTeamsGenericWrite(testTenant, "ms_teams", "int-1", "tenant-a",
				append(values(field), defaults...)), ErrMsTeamsFieldIsFixed, field)
		}
	})

	t.Run("an update that renames a connection is refused", func(t *testing.T) {
		pkgMock.ExpectQuery(query).WithArgs("int-1", testTenant).WillReturnRows(row("ms_teams", "tenant-a"))
		assert.ErrorIs(t, CheckMsTeamsGenericWrite(testTenant, "ms_teams", "int-1", "victim-tenant", defaults), ErrMsTeamsNameIsFixed)
	})

	t.Run("a team row can't be edited at all, whatever type the request names", func(t *testing.T) {
		for _, vals := range [][]IntegrationConfigValue{nil, defaults, values("service_url"), values("team_thread_id"), values("aad_tenant_id")} {
			pkgMock.ExpectQuery(query).WithArgs("int-2", testTenant).WillReturnRows(row("ms_teams_team", "group-1"))
			assert.ErrorIs(t, CheckMsTeamsGenericWrite(testTenant, "slack_webhook", "int-2", "group-1", vals), ErrMsTeamsTeamIsFixed)
		}
	})

	t.Run("renaming any other integration passes", func(t *testing.T) {
		pkgMock.ExpectQuery(query).WithArgs("int-3", testTenant).WillReturnRows(row("slack_webhook", "alerts"))
		assert.NoError(t, CheckMsTeamsGenericWrite(testTenant, "slack_webhook", "int-3", "alerts-renamed", values("webhook_url")))
	})

	t.Run("an unknown id is left to the save to reject", func(t *testing.T) {
		pkgMock.ExpectQuery(query).WithArgs("int-9", testTenant).WillReturnRows(sqlmock.NewRows([]string{"type", "name"}))
		assert.NoError(t, CheckMsTeamsGenericWrite(testTenant, "ms_teams", "int-9", "x", values("app_id")))
	})

	assert.NoError(t, pkgMock.ExpectationsWereMet())
}

func TestCheckMsTeamsGenericDeleteOrStatus(t *testing.T) {
	for _, typ := range []string{"ms_teams", "ms_teams_team", "MS_Teams", " MS_TEAMS_TEAM "} {
		assert.ErrorIs(t, CheckMsTeamsGenericDeleteOrStatus(typ), ErrMsTeamsDisconnectOnly, typ)
	}
	for _, typ := range []string{"slack", "ms_teams_webhook", "google_chat_space", ""} {
		assert.NoError(t, CheckMsTeamsGenericDeleteOrStatus(typ), typ)
	}
}
