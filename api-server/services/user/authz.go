package user

import (
	"database/sql"
	"errors"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// User administration is reachable two ways: the built-in tenant_admin role, and
// a dynamic-RBAC `users:Write` custom grant (the union both gates below express
// via CanManage). What a grant must NEVER buy is privilege administration — a
// `users:Write` holder may edit a person's profile and create people, but may not
// hand out or change a tenant role, since that is how a non-admin would mint
// itself an admin. tenant_admin keeps doing both.
const (
	moduleUsers = "users"
	classWrite  = "Write"
)

// canAdministerUsers reports whether the caller may run a user-administration
// operation at all: tenant_admin (built-in) OR a users:Write custom grant.
func canAdministerUsers(sc *security.SecurityContext) bool {
	return sc.CanManage(moduleUsers, classWrite)
}

// mayAssignTenantRole reports whether the caller may set or change a user's
// tenant role. Deliberately IsTenantAdmin-only: a custom grant admitted through
// canAdministerUsers must not be able to escalate itself or anyone else.
func mayAssignTenantRole(sc *security.SecurityContext) bool {
	return sc.IsTenantAdmin() || sc.IsSuperAdmin()
}

// tenantUser is a user resolved WITHIN a tenant, with the direct tenant roles
// they currently hold.
type tenantUser struct {
	id    string
	roles []string
}

// lookupTenantUser resolves a username to its id and current direct tenant roles,
// scoped to tenantId. found=false means "no such user in this tenant".
//
// The tenant join is load-bearing beyond the role comparison it feeds: the
// profile/status writes below key off `username` / `id` alone (`UPDATE users …
// WHERE username = $1` is global), so without resolving through tenant_users
// first, a caller in tenant A could rename — or with status='inactive', lock out
// — a user in tenant B. Harmless while these handlers were tenant-admin-only;
// not harmless once a grant can reach them.
func lookupTenantUser(manager *database.DatabaseManager, tenantId, username string) (tenantUser, bool, error) {
	var id string
	err := manager.Db.Get(&id, `SELECT u.id::varchar
		FROM users u
		JOIN tenant_users tu ON tu."user" = u.id AND tu.tenant = $1
		WHERE u.username = $2`, tenantId, username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No row — either no such username, or not a member of this tenant. Both
			// are "not found" to the caller; the distinction would leak membership of
			// other tenants.
			return tenantUser{}, false, nil
		}
		// Anything else (connection loss, query error) is NOT "not found".
		// Collapsing it into found=false would surface a database outage as a
		// 400 "User not found", and — worse for a gate — the caller treats
		// not-found as a clean negative, so a transient failure would look like a
		// definitive answer about who exists.
		return tenantUser{}, false, err
	}
	var roles []string
	err = manager.Db.Select(&roles,
		`SELECT role FROM user_roles WHERE tenant_id = $1 AND user_id = $2 AND entity_type = 'tenant' ORDER BY role`,
		tenantId, id)
	if err != nil {
		return tenantUser{}, false, err
	}
	return tenantUser{id: id, roles: roles}, true, nil
}

// changesTenantRole reports whether applying `requested` to this user would
// change their privilege. Compares against the CURRENT role rather than testing
// whether the field was sent: the user modal posts `role` on every save, so a
// presence check would refuse every legitimate profile edit. An empty string is
// a real change when the user holds a role — UpsertTenantUserRole DELETEs on
// empty.
func (tu tenantUser) changesTenantRole(requested string) bool {
	if requested == "" {
		return len(tu.roles) > 0
	}
	return len(tu.roles) != 1 || tu.roles[0] != requested
}
