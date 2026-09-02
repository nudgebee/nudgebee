package tenant

import (
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// Group administration is reachable two ways: the built-in tenant_admin role,
// and a dynamic-RBAC `usergroups:Write` custom grant (the union CanManage
// expresses). Before this, every usergroups_* handler was IsTenantAdmin()-only,
// so the grant was advertised in the Roles editor and silently did nothing —
// a holder got "Not Allowed" on create and update.
//
// What the grant must NEVER buy is privilege administration. A group is not just
// a list of people: `group_roles` can carry a tenant-wide role, and
// custom_role_assignments can bind a custom role to it, so every member inherits
// that surface. Adding YOURSELF to such a group is therefore a way to mint your
// own privileges — the same escalation `mayAssignTenantRole` closes on the user
// side (services/user/authz.go). Membership of a privilege-carrying group stays
// tenant-admin-only; membership of an ordinary group is delegable.
//
// Assigning a role TO a group is a separate action (`userroles_upsert_group`) in
// the `userroles` module, which is non-grantable by construction
// (NON_GRANTABLE_MODULES in app/src/lib/permissionCatalog.ts) and re-checked
// tenant-admin-only in SyncUserRoles. So a usergroups:Write holder cannot create
// a privileged group either — only a real admin can, and this guard stops the
// grant holder joining one after the fact.
const (
	moduleUserGroups     = "usergroups"
	classWriteUserGroups = "Write"
)

// canAdministerUserGroups reports whether the caller may run a group-administration
// operation at all: tenant_admin (built-in) OR a usergroups:Write custom grant.
func canAdministerUserGroups(sc *security.SecurityContext) bool {
	return sc.CanManage(moduleUserGroups, classWriteUserGroups)
}

// mayChangePrivilegedGroupMembership reports whether the caller may change the
// membership of a group that confers privilege. Deliberately IsTenantAdmin-only,
// mirroring mayAssignTenantRole: a custom grant admitted through
// canAdministerUserGroups must not be able to escalate itself or anyone else.
func mayChangePrivilegedGroupMembership(sc *security.SecurityContext) bool {
	return sc.IsTenantAdmin() || sc.IsSuperAdmin()
}

// groupConfersPrivilege reports whether joining `groupId` would grant a member
// anything beyond bare membership — a tenant-wide built-in role, or any custom
// role bound to the group.
//
// Account- and namespace-scoped group_roles rows are deliberately NOT counted:
// they widen which accounts a member can operate in, not what authority they
// hold over the tenant, and treating every role-carrying group as privileged
// would make the grant useless (groups exist largely to carry account roles).
// The line is drawn where self-elevation to an administrator becomes possible.
//
// Errors propagate: a failed lookup must never be read as "not privileged",
// because the caller treats false as a definitive clearance to proceed.
func groupConfersPrivilege(manager *database.DatabaseManager, tenantId, groupId string) (bool, error) {
	var tenantRoleCount int64
	err := manager.Db.Get(&tenantRoleCount,
		`SELECT count(*) FROM group_roles WHERE entity_type = 'tenant' AND entity_id = $1 AND group_id = $2`,
		tenantId, groupId)
	if err != nil {
		return false, err
	}
	if tenantRoleCount > 0 {
		return true, nil
	}

	var customRoleCount int64
	err = manager.Db.Get(&customRoleCount,
		`SELECT count(*) FROM custom_role_assignments
		 WHERE tenant_id = $1 AND principal_type = 'group' AND principal_id = $2`,
		tenantId, groupId)
	if err != nil {
		return false, err
	}
	return customRoleCount > 0, nil
}
