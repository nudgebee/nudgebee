package tenant

import (
	"context"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Must match securityContextCacheNamespace in security/security_context.go.
const securityContextCacheNs = "security_context_v3"

const tenantUserCountQuery = `SELECT count(*) as cnt FROM tenant_users WHERE tenant = $1 AND "user" = $2`
const tenantUserCountQueryAccount = `SELECT count(*) FROM tenant_users WHERE tenant = $1 AND "user" = $2`

// tenantAdminCtx is a tenant_admin caller in tenantId, acting on another user.
func tenantAdminCtx(t *testing.T, tenantId, adminId string) *security.RequestContext {
	t.Helper()
	sc := contextFor(t, `{"TenantId":"`+tenantId+`","UserId":"`+adminId+`","Roles":["tenant_admin"]}`)
	return security.NewRequestContext(context.Background(), sc, nil, nil, nil)
}

// seedCachedContext stores a security context the way NewSecurityContext does,
// so a test can observe whether a role change evicts it.
func seedCachedContext(t *testing.T, tenantId, userId string) {
	t.Helper()
	require.NoError(t, common.CacheSet(securityContextCacheNs, tenantId+":"+userId, []byte(`{}`),
		common.CacheSetWithTags("tenant:"+tenantId, "user:"+userId)))
}

func isCached(tenantId, userId string) bool {
	_, ok := common.CacheGet(securityContextCacheNs, tenantId+":"+userId)
	return ok
}

func TestUpsertTenantUserRole_RemoveIsTenantScopedAndEvictsTarget(t *testing.T) {
	const tenantId, otherTenant, adminId, targetId = "t-role-remove", "t-role-other", "admin-remove", "target-remove"
	seedCachedContext(t, tenantId, targetId)
	seedCachedContext(t, otherTenant, targetId)

	ffMockDB.ExpectQuery(tenantUserCountQuery).
		WithArgs(tenantId, targetId).
		WillReturnRows(sqlmock.NewRows([]string{"cnt"}).AddRow(int64(1)))
	ffMockDB.ExpectExec("DELETE FROM user_roles WHERE entity_type = 'tenant' AND user_id = $1 AND tenant_id = $2").
		WithArgs(targetId, tenantId).
		WillReturnResult(sqlmock.NewResult(0, 1))

	_, err := UpsertTenantUserRole(tenantAdminCtx(t, tenantId, adminId), TenantUserRoleUpsertRequest{UserId: targetId, Role: ""})
	require.NoError(t, err)
	require.NoError(t, ffMockDB.ExpectationsWereMet())

	assert.False(t, isCached(tenantId, targetId), "target's cached context must be evicted so the change applies immediately")
	assert.False(t, isCached(otherTenant, targetId), "eviction is per user, across every tenant")
}

func TestUpsertTenantUserRole_GrantEvictsTarget(t *testing.T) {
	const tenantId, adminId, targetId = "t-role-grant", "admin-grant", "target-grant"
	seedCachedContext(t, tenantId, targetId)

	ffMockDB.ExpectQuery(tenantUserCountQuery).
		WithArgs(tenantId, targetId).
		WillReturnRows(sqlmock.NewRows([]string{"cnt"}).AddRow(int64(1)))
	ffMockDB.ExpectQuery("SELECT * FROM user_roles WHERE entity_id = $1 AND entity_type = 'tenant' AND user_id = $2").
		WithArgs(tenantId, targetId).
		WillReturnRows(sqlmock.NewRows([]string{"id", "role"}))
	ffMockDB.ExpectExec("INSERT INTO user_roles (created_by, entity_id, entity_type, role, user_id, tenant_id) VALUES ($1, $2, $3, $4, $5, $6)").
		WithArgs(adminId, tenantId, "tenant", security.AUTH_TENANT_ADMIN_ROLE, targetId, tenantId).
		WillReturnResult(sqlmock.NewResult(0, 1))

	_, err := UpsertTenantUserRole(tenantAdminCtx(t, tenantId, adminId), TenantUserRoleUpsertRequest{UserId: targetId, Role: security.AUTH_TENANT_ADMIN_ROLE})
	require.NoError(t, err)
	require.NoError(t, ffMockDB.ExpectationsWereMet())

	assert.False(t, isCached(tenantId, targetId), "a freshly granted role must not wait out the 30m cache TTL")
}

func TestUpsertAccountUserRole_ReplaceIsTenantScopedAndEvictsTarget(t *testing.T) {
	const tenantId, adminId, targetId = "t-acct-role", "admin-acct", "target-acct"
	seedCachedContext(t, tenantId, targetId)

	ffMockDB.ExpectQuery(tenantUserCountQueryAccount).
		WithArgs(tenantId, targetId).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	ffMockDB.ExpectExec("DELETE FROM user_roles WHERE entity_type = $2 AND user_id = $1 AND (tenant_id = $3 OR tenant_id IS NULL)").
		WithArgs(targetId, security.RBAC_ENTITY_TYPE_ACCOUNT, tenantId).
		WillReturnResult(sqlmock.NewResult(0, 1))

	_, err := UpsertAccountUserRole(tenantAdminCtx(t, tenantId, adminId), AccountUserRoleUpsertRequest{UserId: targetId, AccountRoles: []AccountRole{}})
	require.NoError(t, err)
	require.NoError(t, ffMockDB.ExpectationsWereMet())

	assert.False(t, isCached(tenantId, targetId))
}

func TestUpsertK8sAccountNamespaceUserRole_ReplaceIsTenantScopedAndEvictsTarget(t *testing.T) {
	const tenantId, adminId, targetId = "t-ns-role", "admin-ns", "target-ns"
	seedCachedContext(t, tenantId, targetId)

	ffMockDB.ExpectQuery(tenantUserCountQueryAccount).
		WithArgs(tenantId, targetId).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	ffMockDB.ExpectExec("DELETE FROM user_roles WHERE entity_type = $2 AND user_id = $1 AND (tenant_id = $3 OR tenant_id IS NULL)").
		WithArgs(targetId, security.RBAC_ENTITY_TYPE_K8S_NAMESPACE, tenantId).
		WillReturnResult(sqlmock.NewResult(0, 1))

	_, err := UpsertK8sAccountNamespaceUserRole(tenantAdminCtx(t, tenantId, adminId), K8sAccountNamespaceUserRoleUpsertRequest{UserId: targetId, K8sAccountNamespaceRoles: []AccountNamespaceRole{}})
	require.NoError(t, err)
	require.NoError(t, ffMockDB.ExpectationsWereMet())

	assert.False(t, isCached(tenantId, targetId))
}
