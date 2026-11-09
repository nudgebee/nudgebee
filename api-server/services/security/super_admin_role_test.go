package security

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The super-admin role strings, pinned. SUPER_ADMIN_ROLES in
// app/src/lib/superAdminRoles.ts and the CHECK constraints added by V1107 carry
// the same two; change all three together.
func TestIsSuperAdminRole(t *testing.T) {
	for _, role := range []string{"super_admin", "super_admin_readonly", "SUPER_ADMIN", "Super_Admin_ReadOnly"} {
		assert.True(t, IsSuperAdminRole(role), role)
	}
	for _, role := range []string{"", "admin", "tenant_admin", "tenant_admin_readonly", "account_admin", "super", "super_admin_x"} {
		assert.False(t, IsSuperAdminRole(role), role)
	}
}

// A super-admin role on a row scoped to the tenant, given directly or through a
// group, grants nothing. The context built for that tenant keeps every other role and drops it.
func TestTenantScopedSuperAdminRowsGrantNothing(t *testing.T) {
	db, mock, err := mockPg()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const (
		tenant = "aaaaaaaa-4364-4900-0000-000000000001"
		user   = "bbbbbbbb-4364-4900-0000-000000000001"
		group  = "cccccccc-4364-4900-0000-000000000001"
	)
	mock.ExpectQuery(`FROM cloud_accounts WHERE tenant`).WithArgs(tenant).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`FROM user_roles WHERE tenant_id`).WithArgs(tenant, user).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "entity_type", "entity_id", "role"}).
			AddRow(user, "tenant", tenant, "tenant_admin").
			AddRow(user, "tenant", tenant, "super_admin").
			AddRow(user, "tenant", tenant, "SUPER_ADMIN_READONLY"))
	mock.ExpectQuery(`from group_roles gr`).WithArgs(tenant, user).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "role", "entity_id", "entity_type"}).
			AddRow(group, "super_admin", tenant, "tenant").
			AddRow(group, "tenant_admin_readonly", tenant, "tenant"))
	mock.ExpectQuery(`from user_attrs`).
		WillReturnRows(sqlmock.NewRows([]string{"name", "value"}))
	mock.ExpectQuery(`FROM users WHERE id`).WithArgs(user).
		WillReturnRows(sqlmock.NewRows([]string{"display_name"}).AddRow(""))

	sc, err := NewSecurityContext(tenant, user)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"tenant_admin", "tenant_admin_readonly"}, sc.GetRoles())
	assert.False(t, sc.IsSuperAdmin())
	assert.False(t, sc.IsSuperAdminReadonly())
	assert.NoError(t, mock.ExpectationsWereMet())
}
