package security

import (
	"errors"
	"log/slog"
	"nudgebee/services/internal/database"

	"github.com/lib/pq"
)

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
// Custom roles are part of the product for every tenant: there is no switch, so
// this runs for every tenant's security context. Grants only add access, so a
// tenant with no custom role assigned authorizes exactly as with built-in roles. Additive — the caller
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
