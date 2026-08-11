package security

import (
	"log/slog"
	"nudgebee/services/internal/database"
	"os"
	"strings"
)

// FeatureCustomRoles is the per-tenant feature that switches the whole
// dynamic-RBAC custom-role mechanism on. Registered in the existing
// public.feature catalog (see the custom_roles_feature_flag migration) and
// enabled per tenant with a public.feature_flag row:
//
//	INSERT INTO public.feature_flag (feature_id, tenant_id, status)
//	VALUES ('CUSTOM_ROLES', '<tenant-uuid>', 'enabled');
const FeatureCustomRoles = "CUSTOM_ROLES"

// EnvDisableCustomRoles is a deployment-level kill switch that beats the
// per-tenant flag. Set CUSTOM_ROLES_DISABLED=true on the pod to force every
// tenant back to built-in-role behavior without touching the database — the
// escape hatch for when the flag rows themselves are the problem.
const EnvDisableCustomRoles = "CUSTOM_ROLES_DISABLED"

// CustomRolesEnabled reports whether dynamic-RBAC custom roles are active for
// tenantId.
//
// This is THE on/off seam for the feature. Every custom-grant gate in the
// product funnels through the two grant maps this switch populates
// (HasPermission / HasScopedPermission), so when it returns false:
//
//	CanManage(module, class)            == IsTenantAdmin() || IsSuperAdmin()
//	CanReadAccountData(account, module) == HasAccountAccess(account, read)
//	ScopedAccountIdsForModule(module)   == nil  (query engine denies as before)
//	GetCustomPermissions()              == []   (empty session -> inert gateway)
//
// i.e. authorization is bit-for-bit the built-in-role behavior.
//
// Opt-in and fail-safe: only an explicit `status='enabled'` feature_flag row
// turns it on. No row, `status='disabled'`, an unreachable metastore, or a
// database that has not run the custom-role migrations yet all resolve to
// false, so a deploy whose migration Job has not completed degrades to built-in
// roles instead of failing every request.
func CustomRolesEnabled(tenantId string) bool {
	if strings.EqualFold(os.Getenv(EnvDisableCustomRoles), "true") {
		return false
	}
	if tenantId == "" {
		return false
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("custom roles: unable to get database manager", "error", err)
		return false
	}
	var status string
	err = dbms.Db.Get(&status,
		"SELECT status FROM feature_flag WHERE feature_id = $1 AND tenant_id = $2::uuid AND account_id IS NULL",
		FeatureCustomRoles, tenantId)
	if err != nil {
		// No row (the common case) — the feature stays off for this tenant.
		return false
	}
	return status == "enabled"
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
// tables are never read for a tenant with the feature off. Additive — the caller
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
