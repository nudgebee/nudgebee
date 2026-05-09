package user

import (
	"fmt"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

type StatusTypeItem struct {
	Value string `json:"value" db:"value"`
}

type RoleItem struct {
	DisplayName string `json:"display_name" db:"display_name"`
	Value       string `json:"value" db:"value"`
}

type RolesListRequest struct {
	Filter string `json:"filter"`
}

type UserTenantItem struct {
	Name string `json:"name" db:"name"`
}

type UserTenantsListRequest struct {
	Username string `json:"username"`
}

type GroupNameExistsItem struct {
	Id   string `json:"id" db:"id"`
	Name string `json:"name" db:"name"`
}

type GroupNameExistsRequest struct {
	Name string `json:"name"`
}

func ListStatusTypes(ctx *security.RequestContext) ([]StatusTypeItem, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}

	var items []StatusTypeItem
	err = dbms.Db.Select(&items, "SELECT value FROM user_status_type ORDER BY value")
	if err != nil {
		return nil, fmt.Errorf("failed to query user_status_type: %w", err)
	}

	if items == nil {
		items = []StatusTypeItem{}
	}
	return items, nil
}

func ListRoles(ctx *security.RequestContext, request RolesListRequest) ([]RoleItem, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}

	var items []RoleItem
	if request.Filter != "" {
		err = dbms.Db.Select(&items, "SELECT display_name, value FROM roles WHERE value ILIKE $1 ORDER BY value", request.Filter)
	} else {
		err = dbms.Db.Select(&items, "SELECT display_name, value FROM roles ORDER BY value")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query roles: %w", err)
	}

	if items == nil {
		items = []RoleItem{}
	}
	return items, nil
}

func ListUserTenants(ctx *security.RequestContext, request UserTenantsListRequest) ([]UserTenantItem, error) {
	if request.Username == "" {
		return nil, fmt.Errorf("username is required")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}

	var items []UserTenantItem
	err = dbms.Db.Select(&items,
		`SELECT t.name FROM tenant t
		 INNER JOIN tenant_users tu ON tu.tenant = t.id
		 INNER JOIN users u ON u.id = tu.user
		 WHERE u.username = $1
		 ORDER BY t.name`, request.Username)
	if err != nil {
		return nil, fmt.Errorf("failed to query user tenants: %w", err)
	}

	if items == nil {
		items = []UserTenantItem{}
	}
	return items, nil
}

func CheckGroupNameExists(ctx *security.RequestContext, request GroupNameExistsRequest) ([]GroupNameExistsItem, error) {
	if request.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return nil, fmt.Errorf("unauthorized: missing tenant")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}

	var items []GroupNameExistsItem
	err = dbms.Db.Select(&items,
		"SELECT id, name FROM user_groups WHERE name = $1 AND tenant = $2",
		request.Name, tenantId)
	if err != nil {
		return nil, fmt.Errorf("failed to query user_groups: %w", err)
	}

	if items == nil {
		items = []GroupNameExistsItem{}
	}
	return items, nil
}
