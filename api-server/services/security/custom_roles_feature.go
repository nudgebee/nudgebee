package security

import (
	"errors"
	"log/slog"
	"nudgebee/services/internal/database"
	"os"
	"strings"

	"github.com/lib/pq"
)

// EnvDisableCustomRoles is a deployment-level kill switch. Set
// CUSTOM_ROLES_DISABLED=true on the pod to force every tenant back to
// built-in-role behavior — the escape hatch for when grant resolution itself is
// the problem. Custom roles are otherwise part of the product for every tenant;
// there is no per-tenant switch.
const EnvDisableCustomRoles = "CUSTOM_ROLES_DISABLED"

// CustomRolesEnabled reports whether dynamic-RBAC custom roles apply to
// tenantId: always, unless the deployment sets CUSTOM_ROLES_DISABLED=true. An
// empty tenant id (server-internal synthetic contexts) has no custom roles.
//
// Every custom-grant gate in the product funnels through the two grant maps the
// resolver populates (HasPermission / HasScopedPermission), so when it returns
// false:
//
//	CanManage(module, class)            == IsTenantAdmin() || IsSuperAdmin()
//	CanReadAccountData(account, module) == HasAccountAccess(account, read)
//	ScopedAccountIdsForModule(module)   == nil  (query engine denies as before)
//	GetCustomPermissions()              == []   (empty session -> inert gateway)
//
// i.e. authorization is bit-for-bit the built-in-role behavior. With it true
// and no custom role assigned, authorization is the same: grants only add
// access. A database without the custom-role tables resolves no grants (see
// resolveCustomGrants) rather than failing every request.
func CustomRolesEnabled(tenantId string) bool {
	if strings.EqualFold(os.Getenv(EnvDisableCustomRoles), "true") {
		return false
	}
	return tenantId != ""
}

// resolveCustomGrants loads the dynamic-RBAC grants a user holds, directly and
// via the groups they belong to, and splits them into tenant-global grants and
// per-account grants.
//
// Scope lives on the ASSIGNMENT (custom_role_assignments.entity_type/entity_id):
// entity_type NULL = tenant-global (applies to every account); entity_type
// 'account' = the whole role applies only to that account. This is the "Group G
// holds Role R on Account A" binding — the same role can be bound tenant-wide
// for one group and per-account for another, because scope is a property of the
// binding, not of the role. System roles (is_system) are EXCLUDED: built-in
// enforcement handles those, and the filter prevents a stray assignment from
// injecting a system role's full grant set.
//
// Called only when CustomRolesEnabled(tenantId) is true, so the custom_role_*
// tables are never read with the kill switch set. Additive — the caller
// never lets these grants affect data scoping.
func resolveCustomGrants(dbms *database.DatabaseManager, tenantId string, userId string) (map[string]bool, map[string]map[string]bool, error) {
	customPermissions := map[string]bool{}
	scopedCustomPermissions := map[string]map[string]bool{}
	rows, err := dbms.Db.Queryx(`select crp.module::varchar, crp.class,
			coalesce(cra.entity_type::varchar, '') as etype, coalesce(cra.entity_id, '') as eid
		from custom_role_permissions crp
		join custom_role_assignments cra on cra.custom_role_id = crp.custom_role_id
		join custom_roles cr on cr.id = crp.custom_role_id
		where cra.tenant_id = $1 and coalesce(cr.is_system, false) = false and (
			(cra.principal_type = 'user' and cra.principal_id = $2)
			or (cra.principal_type = 'group' and cra.principal_id in (
				select distinct ug.id
				from user_groups ug
				join usergroup_users ugg on ug.id = ugg.group
				where ug.tenant = $1 and ugg.user = $2
			))
		)`, tenantId, userId)
	if err != nil {
		if isUndefinedTable(err) {
			// The custom-role tables are not there yet: on an upgrade the migration
			// Job runs after the new pods start (post-install/post-upgrade hook).
			// Resolve no grants, i.e. built-in roles, instead of failing every request.
			slog.Warn("custom roles: custom-role tables missing; using built-in roles only", "tenant_id", tenantId)
			return customPermissions, scopedCustomPermissions, nil
		}
		return nil, nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("Error closing rows", "error", err)
		}
	}()
	for rows.Next() {
		var module, class, etype, eid string
		if err := rows.Scan(&module, &class, &etype, &eid); err != nil {
			return nil, nil, err
		}
		key := module + ":" + class
		if etype == "" {
			// tenant-global grant — applies to every account.
			customPermissions[key] = true
		} else {
			// Account-scoped binding: eid is the account id, and every grant the
			// role carries applies only there.
			if scopedCustomPermissions[eid] == nil {
				scopedCustomPermissions[eid] = map[string]bool{}
			}
			scopedCustomPermissions[eid][key] = true
		}
	}
	// Check for an iteration error so a mid-stream failure can't silently yield a
	// partial grant set (which would under-authorize, but must still be surfaced).
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return customPermissions, scopedCustomPermissions, nil
}

// isUndefinedTable reports whether err is a Postgres undefined_table (SQLSTATE
// 42P01).
func isUndefinedTable(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "42P01"
}
