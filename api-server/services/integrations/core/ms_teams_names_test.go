package core

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

func TestMsTeamsAadTenantId(t *testing.T) {
	assert.Equal(t, "tenant-a", MsTeamsAadTenantId("object-1.tenant-a"))
	assert.Equal(t, "tenant-a", MsTeamsAadTenantId("tenant-a"))
	assert.Equal(t, "tenant-a", MsTeamsAadTenantId(" tenant-a "))
	assert.Equal(t, "object-1.", MsTeamsAadTenantId("object-1."))
	assert.Equal(t, "", MsTeamsAadTenantId(""))
}

func TestCheckMsTeamsGenericWrite(t *testing.T) {
	const query = `SELECT type, name FROM integrations WHERE id = \$1 AND tenant_id = \$2`
	row := func(typ, name string) *sqlmock.Rows {
		return sqlmock.NewRows([]string{"type", "name"}).AddRow(typ, name)
	}

	t.Run("other types are created freely", func(t *testing.T) {
		assert.NoError(t, CheckMsTeamsGenericWrite(testTenant, "slack", "", "T1"))
		assert.NoError(t, CheckMsTeamsGenericWrite(testTenant, "google_chat_space", "", "spaces/AAA"))
	})

	t.Run("a generic create is refused in any letter case", func(t *testing.T) {
		for _, typ := range []string{"ms_teams", "ms_teams_team", "MS_Teams", " MS_TEAMS_TEAM "} {
			assert.ErrorIs(t, CheckMsTeamsGenericWrite(testTenant, typ, "", "victim"), ErrMsTeamsCreatedByConnect, typ)
		}
	})

	t.Run("an update that keeps the name passes", func(t *testing.T) {
		pkgMock.ExpectQuery(query).WithArgs("int-1", testTenant).WillReturnRows(row("ms_teams", "tenant-a"))
		assert.NoError(t, CheckMsTeamsGenericWrite(testTenant, "ms_teams", "int-1", "tenant-a"))
	})

	t.Run("an update that renames is refused", func(t *testing.T) {
		pkgMock.ExpectQuery(query).WithArgs("int-1", testTenant).WillReturnRows(row("ms_teams", "tenant-a"))
		assert.ErrorIs(t, CheckMsTeamsGenericWrite(testTenant, "ms_teams", "int-1", "victim-tenant"), ErrMsTeamsNameIsFixed)
	})

	t.Run("the stored type decides, not the type the request names", func(t *testing.T) {
		pkgMock.ExpectQuery(query).WithArgs("int-2", testTenant).WillReturnRows(row("ms_teams_team", "group-1"))
		assert.ErrorIs(t, CheckMsTeamsGenericWrite(testTenant, "slack_webhook", "int-2", "victim-team"), ErrMsTeamsNameIsFixed)
	})

	t.Run("renaming any other integration passes", func(t *testing.T) {
		pkgMock.ExpectQuery(query).WithArgs("int-3", testTenant).WillReturnRows(row("slack_webhook", "alerts"))
		assert.NoError(t, CheckMsTeamsGenericWrite(testTenant, "slack_webhook", "int-3", "alerts-renamed"))
	})

	t.Run("an unknown id is left to the save to reject", func(t *testing.T) {
		pkgMock.ExpectQuery(query).WithArgs("int-9", testTenant).WillReturnRows(sqlmock.NewRows([]string{"type", "name"}))
		assert.NoError(t, CheckMsTeamsGenericWrite(testTenant, "ms_teams", "int-9", "x"))
	})

	assert.NoError(t, pkgMock.ExpectationsWereMet())
}
