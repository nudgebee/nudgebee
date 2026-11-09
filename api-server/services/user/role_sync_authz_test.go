package user

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These cases pin what a tenant admin may do outside its own tenant role set:
// role sync writes only tenant and account roles, and logins are added or
// removed only for the caller's own user or by the sign-in service.

const testTenant = "aaaaaaaa-0000-0000-0000-000000000001"

func callerWith(t *testing.T, wire string) *security.RequestContext {
	t.Helper()
	var sc security.SecurityContext
	require.NoError(t, common.UnmarshalJson([]byte(wire), &sc))
	return security.NewRequestContext(context.Background(), &sc, nil, nil, nil)
}

func tenantAdmin(t *testing.T) *security.RequestContext {
	return callerWith(t, `{"TenantId":"`+testTenant+`","UserId":"bbbbbbbb-0000-0000-0000-000000000009","Roles":["tenant_admin"]}`)
}

func TestRoleSyncAssignsOnlyTenantAndAccountRoles(t *testing.T) {
	// Case-insensitive like the citext column: a SAML mapping in another case keeps syncing.
	for _, role := range []string{"tenant_admin", "tenant_admin_readonly", "account_admin", "account_admin_readonly", "TENANT_ADMIN_READONLY", "Account_Admin"} {
		assert.True(t, syncableRole(role), role)
	}
	for _, role := range []string{"super_admin", "super_admin_readonly", "SUPER_ADMIN", "k8s_namespace_admin", "k8s_namespace_admin_readonly", "tenant_select", "user", ""} {
		assert.False(t, syncableRole(role), role)
	}
}

func expectTenant(mock sqlmock.Sqlmock, id string) {
	mock.ExpectQuery(`FROM tenant WHERE id`).WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "created_at", "updated_at", "created_by", "updated_by", "type"}).
			AddRow(id, "Free Org", time.Now(), time.Now(), nil, nil, "free"))
}

func TestATenantAdminCannotSyncSuperAdminOntoAnyone(t *testing.T) {
	for _, role := range []string{"super_admin", "super_admin_readonly"} {
		t.Run(role, func(t *testing.T) {
			mock := mockMetastore(t)
			expectTenant(mock, testTenant)

			_, err := SyncUserRoles(tenantAdmin(t), UserSyncRolesRequest{
				Username: "me@corp.com", TenantId: testTenant, TargetRoles: []string{"tenant_admin", role}, RemoveOldRoles: true,
			})
			var cerr common.Error
			require.ErrorAs(t, err, &cerr)
			assert.Equal(t, 400, cerr.Code)
			assert.NoError(t, mock.ExpectationsWereMet(), "refused before any user or role row is read")
		})
	}
}

// SAML group mappings assign account roles through the same handler; they still pass.
func TestRoleSyncStillAcceptsTheRolesSAMLAssigns(t *testing.T) {
	mock := mockMetastore(t)
	expectTenant(mock, testTenant)
	// Past the role check the handler resolves the user; answering "no such user" ends the call there.
	mock.ExpectQuery(`SELECT id FROM users WHERE username`).WithArgs("me@corp.com").WillReturnError(sql.ErrNoRows)

	_, err := SyncUserRoles(tenantAdmin(t), UserSyncRolesRequest{
		Username: "me@corp.com", TenantId: testTenant, TargetRoles: []string{"account_admin", "account_admin_readonly", "tenant_admin_readonly"},
	})
	assert.ErrorContains(t, err, "user not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestALoginCannotBeAddedToSomeoneElse(t *testing.T) {
	const caller = "bbbbbbbb-0000-0000-0000-000000000009"
	callers := map[string]string{
		"tenant admin":           `{"TenantId":"` + testTenant + `","UserId":"` + caller + `","Roles":["tenant_admin"]}`,
		"read-only account role": `{"TenantId":"` + testTenant + `","UserId":"` + caller + `","Roles":["account_admin_readonly"]}`,
		"super admin by role":    `{"TenantId":"` + testTenant + `","UserId":"` + caller + `","Roles":["super_admin"]}`,
	}
	for name, wire := range callers {
		t.Run(name, func(t *testing.T) {
			mock := mockMetastore(t)
			_, err := CreateUserAuth(callerWith(t, wire), UserCreateAuthRequest{
				UserId: "cccccccc-0000-0000-0000-000000000001", Provider: "google", ProviderType: "oauth", AccountId: "someone-elses-subject", Name: "google", Status: "active",
			})
			var cerr common.Error
			require.ErrorAs(t, err, &cerr)
			assert.Equal(t, 403, cerr.Code)
			assert.NoError(t, mock.ExpectationsWereMet(), "no user_auths row is written")
		})
	}
}

func TestALoginCanStillBeAddedToYourOwnAccount(t *testing.T) {
	mock := mockMetastore(t)
	// keyOwner's session carries its ids upper-cased; the gate still recognises them.
	// Past the gate the handler inserts the row: failing the insert ends the call there.
	errPastGate := errors.New("past the gate")
	mock.ExpectQuery(`INSERT INTO user_auths`).WillReturnError(errPastGate)

	_, err := CreateUserAuth(keyOwner(t), UserCreateAuthRequest{
		UserId: storedUser, Provider: "google", ProviderType: "oauth", AccountId: "1", Name: "google", Status: "active",
	})
	assert.ErrorIs(t, err, errPastGate, "the call got past the own-account gate")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOnlyTheSignInServiceRemovesALogin(t *testing.T) {
	const login = "dddddddd-0000-0000-0000-000000000001"
	const caller = "bbbbbbbb-0000-0000-0000-000000000009"
	callers := map[string]string{
		"tenant admin":        `{"TenantId":"` + testTenant + `","UserId":"` + caller + `","Roles":["tenant_admin"]}`,
		"super admin by role": `{"TenantId":"` + testTenant + `","UserId":"` + caller + `","Roles":["super_admin"]}`,
	}
	for name, wire := range callers {
		t.Run(name, func(t *testing.T) {
			mock := mockMetastore(t)
			_, err := DeleteUserAuth(callerWith(t, wire), UserDeleteAuthRequest{Id: login})
			var cerr common.Error
			require.ErrorAs(t, err, &cerr)
			assert.Equal(t, 403, cerr.Code)
			assert.NoError(t, mock.ExpectationsWereMet(), "no row is deleted")
		})
	}

	t.Run("the sign-in service", func(t *testing.T) {
		mock := mockMetastore(t)
		mock.ExpectExec(`DELETE FROM user_auths WHERE id = \$1`).WithArgs(login).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(`DELETE FROM llm_gateway_access_policies`).WillReturnResult(sqlmock.NewResult(0, 0))
		_, err := DeleteUserAuth(serverInternal(t), UserDeleteAuthRequest{Id: login})
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// With no security context at all, both login handlers refuse before touching the database.
func TestLoginHandlersFailClosedWithoutASecurityContext(t *testing.T) {
	noIdentity := security.NewRequestContext(context.Background(), nil, nil, nil, nil)

	mock := mockMetastore(t)
	_, err := CreateUserAuth(noIdentity, UserCreateAuthRequest{
		UserId: "cccccccc-0000-0000-0000-000000000001", Provider: "google", ProviderType: "oauth", AccountId: "1", Name: "google", Status: "active",
	})
	var cerr common.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, 403, cerr.Code)

	_, err = DeleteUserAuth(noIdentity, UserDeleteAuthRequest{Id: "dddddddd-0000-0000-0000-000000000001"})
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, 403, cerr.Code)
	assert.NoError(t, mock.ExpectationsWereMet(), "nothing is read or written")
}

// NextAuth's callbacks run as the server and add logins for whoever is signing in.
func serverInternal(t *testing.T) *security.RequestContext {
	return callerWith(t, `{"IsServerInternal":true}`)
}
