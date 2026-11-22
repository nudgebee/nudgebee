package event

import (
	"database/sql"
	"testing"

	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Event ids are global, and several handlers load an event by the id a request
// names before they know whose it is. GetEvent therefore only finds events of
// the caller's tenant: an id from another tenant reads as not found.
func TestGetEventIsScopedToTheCallersTenant(t *testing.T) {
	t.Run("a caller with a tenant finds only that tenant's events", func(t *testing.T) {
		mock := setupApplyResolutionTest(t)
		mock.ExpectQuery(`FROM events WHERE id = \$1 AND tenant = \$2`).
			WithArgs(applyTestEventID, applyTestOtherTenant).
			WillReturnRows(sqlmock.NewRows([]string{"id"}))

		ctx := applyResolutionTestContext(t, `{"TenantId":"`+applyTestOtherTenant+`","UserId":"u1","Roles":["tenant_admin"]}`)
		_, err := GetEvent(ctx, applyTestEventID)
		assert.ErrorIs(t, err, sql.ErrNoRows)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a server-internal context loads by id alone", func(t *testing.T) {
		// The queue consumers have no tenant until the event tells them.
		mock := setupApplyResolutionTest(t)
		mock.ExpectQuery(`FROM events WHERE id = \$1$`).
			WithArgs(applyTestEventID).
			WillReturnRows(sqlmock.NewRows([]string{"id", "tenant"}).AddRow(applyTestEventID, applyTestTenant))

		ctx := security.NewRequestContext(t.Context(), security.NewSecurityContextForSuperAdmin(), nil, nil, nil)
		ev, err := GetEvent(ctx, applyTestEventID)
		require.NoError(t, err)
		assert.Equal(t, applyTestEventID, ev.Id)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no tenant and not server-internal finds nothing", func(t *testing.T) {
		mock := setupApplyResolutionTest(t)
		for name, ctx := range map[string]*security.RequestContext{
			"nil context":     nil,
			"no tenant":       applyResolutionTestContext(t, `{"UserId":"u1","Roles":["tenant_admin"]}`),
			"no security ctx": security.NewRequestContext(t.Context(), nil, nil, nil, nil),
		} {
			_, err := GetEvent(ctx, applyTestEventID)
			assert.ErrorIs(t, err, sql.ErrNoRows, name)
		}
		require.NoError(t, mock.ExpectationsWereMet(), "no query is run")
	})
}
