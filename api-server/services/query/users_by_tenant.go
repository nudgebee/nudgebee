package query

import "nudgebee/services/internal/database"

// usersKindFilter keeps a tenant user list to people unless bees are asked for.
// A bee account (an AI worker) is not a person: it must not appear in pickers,
// counts or recipient lists, only where a list shows who did something.
func usersKindFilter(includeBees bool) string {
	if includeBees {
		return ""
	}
	return "\n\t\t\tWHERE u.kind = 'person'"
}

// usersByTenantTable is the tenant user directory (users_list_by_tenant, and
// users_list_by_tenant_with_bees when includeBees).
func usersByTenantTable(name string, includeBees bool) TableDefinition {
	kindFilter := usersKindFilter(includeBees)
	return TableDefinition{
		Type:               Derived,
		Source:             database.Metastore,
		TenantIdColumnName: "tenant_id",
		// Tenant-scoped user directory — a users:Read (or Write) custom grant
		// authorizes this tenant-wide read.
		PermissionModule: "users",
		Name:             name,
		Def: `(
			SELECT
				u.id::text as id,
				u.display_name,
				u.status::text as status,
				u.kind,
				u.username as username,
				u.created_at,
				tu.tenant::text as tenant_id,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'id', ur.id,
						'role', ur.role,
						'entity_type', ur.entity_type,
						'entity_id', ur.entity_id,
						'role_display_name', COALESCE(r.display_name, ur.role)
					))
					FROM user_roles ur
					LEFT JOIN roles r ON r.value = ur.role
					WHERE ur.user_id = u.id AND ur.entity_id = tu.tenant::text),
					'[]'::json
				) as user_roles,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'name', ug.name,
						'id', ug.id,
						'group_roles', COALESCE(
							(SELECT json_agg(json_build_object('role', gr.role))
							FROM group_roles gr WHERE gr.group_id = ug.id),
							'[]'::json
						)
					))
					FROM usergroup_users ugu
					INNER JOIN user_groups ug ON ug.id = ugu."group"
					WHERE ugu."user" = u.id AND ug.tenant = tu.tenant),
					'[]'::json
				) as user_groups,
				(SELECT ua.accessed_at
				 FROM user_auths ua
				 WHERE ua."user" = u.id AND ua.tenant_id = tu.tenant
				 ORDER BY ua.accessed_at DESC
				 LIMIT 1
				) as last_accessed_at
			FROM users u
			INNER JOIN tenant_users tu ON tu."user" = u.id` + kindFilter + `
		) as users_by_tenant`,
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"display_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "display_name",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"kind": {
				Type: ColumnDefinitionTypeString,
				Def:  "kind",
			},
			"username": {
				Type: ColumnDefinitionTypeString,
				Def:  "username",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"user_roles": {
				Type: ColumnDefinitionTypeJson,
				Def:  "user_roles",
			},
			"user_groups": {
				Type: ColumnDefinitionTypeJson,
				Def:  "user_groups",
			},
			"last_accessed_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "last_accessed_at",
			},
		},
	}
}

// usersGroupingByTenantTable is the aggregate companion of usersByTenantTable.
func usersGroupingByTenantTable(name string, includeBees bool) TableDefinition {
	kindFilter := usersKindFilter(includeBees)
	return TableDefinition{
		Type:               Aggregate,
		Source:             database.Metastore,
		TenantIdColumnName: "tenant_id",
		// Aggregate companion of the tenant-scoped user directory — same
		// users:Read (or Write) grant authorizes the tenant-wide read.
		PermissionModule: "users",
		Name:             name,
		Def: `(
			SELECT
				u.id::text as id,
				u.display_name,
				u.status::text as status,
				u.kind,
				u.username as username,
				u.created_at,
				tu.tenant::text as tenant_id
			FROM users u
			INNER JOIN tenant_users tu ON tu."user" = u.id` + kindFilter + `
		) as users_grouping`,
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"display_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "display_name",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"kind": {
				Type: ColumnDefinitionTypeString,
				Def:  "kind",
			},
			"username": {
				Type: ColumnDefinitionTypeString,
				Def:  "username",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	}
}
