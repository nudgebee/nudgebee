package security

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stubWorkspaceTenantAccounts(t *testing.T, accounts map[string][]string) {
	t.Helper()
	orig := WorkspaceTenantAccounts
	WorkspaceTenantAccounts = func(tenantId string) ([]string, error) { return accounts[tenantId], nil }
	t.Cleanup(func() { WorkspaceTenantAccounts = orig })
}

func TestWorkspaceGrantsForCopiesTenantUser(t *testing.T) {
	sc := &SecurityContext{
		tenantId:        "t1",
		userId:          "u1",
		roles:           []string{AUTH_ACCOUNT_READ_ADMIN_ROLE},
		accountIds:      []string{"a1", "a2"},
		scopedEntityIds: map[string][]string{AUTH_ACCOUNT_READ_ADMIN_ROLE: {"a1"}},
	}
	g, err := WorkspaceGrantsFor(sc, "t1")
	require.NoError(t, err)
	assert.Equal(t, "u1", g.UserId)
	assert.Equal(t, []string{AUTH_ACCOUNT_READ_ADMIN_ROLE}, g.Roles)
	assert.Equal(t, []string{"a1", "a2"}, g.AccountIds)

	_, err = WorkspaceGrantsFor(sc, "t2")
	require.ErrorContains(t, err, "does not own this workspace")
	_, err = WorkspaceGrantsFor(sc, "")
	require.Error(t, err)
}

func TestWorkspaceGrantsForReducesCrossTenantRoles(t *testing.T) {
	stubWorkspaceTenantAccounts(t, map[string][]string{"t1": {"a1", "a2"}})

	for name, tc := range map[string]struct {
		sc       *SecurityContext
		wantRole string
		wantUser string
	}{
		"server internal": {NewSecurityContextForSuperAdmin(), AUTH_TENANT_ADMIN_ROLE, GetSystemUserId()},
		"nil context":     {nil, AUTH_TENANT_ADMIN_ROLE, GetSystemUserId()},
		"human super":     {&SecurityContext{tenantId: "home", userId: "staff", roles: []string{AUTH_SUPER_ADMIN_FULL_ROLE}}, AUTH_TENANT_ADMIN_ROLE, "staff"},
		"readonly super":  {&SecurityContext{tenantId: "home", userId: "staff", roles: []string{AUTH_SUPER_ADMIN_READONLY_ROLE}}, AUTH_TENANT_READ_ADMIN_ROLE, "staff"},
	} {
		t.Run(name, func(t *testing.T) {
			g, err := WorkspaceGrantsFor(tc.sc, "t1")
			require.NoError(t, err)
			assert.Equal(t, []string{tc.wantRole}, g.Roles)
			assert.Equal(t, tc.wantUser, g.UserId)
			assert.Equal(t, []string{"a1", "a2"}, g.AccountIds)
		})
	}

	// A tenant-admin automated flow without a user is stamped as the system user.
	g, err := WorkspaceGrantsFor(&SecurityContext{tenantId: "t1", roles: []string{AUTH_TENANT_ADMIN_ROLE}, accountIds: []string{"a1"}}, "t1")
	require.NoError(t, err)
	assert.Equal(t, GetSystemUserId(), g.UserId)
	assert.Equal(t, []string{"a1"}, g.AccountIds, "an account-restricted context must stay restricted")
}

func TestNewSecurityContextFromWorkspaceGrants(t *testing.T) {
	stubWorkspaceTenantAccounts(t, map[string][]string{"t1": {"a1", "a2"}})

	sc, err := NewSecurityContextFromWorkspaceGrants("t1", WorkspaceGrants{
		UserId:          "u1",
		Roles:           []string{AUTH_ACCOUNT_READ_ADMIN_ROLE},
		AccountIds:      []string{"a1", "other-tenant-account"},
		ScopedEntityIds: map[string][]string{AUTH_ACCOUNT_READ_ADMIN_ROLE: {"a1", "other-tenant-account"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "u1", sc.GetUserId())
	assert.Equal(t, "t1", sc.GetTenantId())
	assert.False(t, sc.IsSuperAdmin())
	assert.True(t, sc.HasAccountAccess("a1", SecurityAccessTypeRead))
	assert.False(t, sc.HasAccountAccess("a1", SecurityAccessTypeCreate), "read-only grant must not write")
	assert.False(t, sc.HasAccountAccess("a2", SecurityAccessTypeRead), "no grant on a2")
	assert.False(t, sc.HasAccountAccess("other-tenant-account", SecurityAccessTypeRead), "accounts outside the tenant are dropped")

	for _, role := range []string{AUTH_SUPER_ADMIN_FULL_ROLE, AUTH_SUPER_ADMIN_READONLY_ROLE} {
		_, err = NewSecurityContextFromWorkspaceGrants("t1", WorkspaceGrants{UserId: "u1", Roles: []string{role}})
		require.Error(t, err, role)
	}
	_, err = NewSecurityContextFromWorkspaceGrants("t1", WorkspaceGrants{Roles: []string{AUTH_TENANT_ADMIN_ROLE}})
	require.Error(t, err, "user is required")
	_, err = NewSecurityContextFromWorkspaceGrants("", WorkspaceGrants{UserId: "u1"})
	require.Error(t, err, "tenant is required")
}
