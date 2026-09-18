package security

import (
	"errors"
	"fmt"
	"slices"
)

// WorkspaceGrants is the part of a SecurityContext that a workspace execution
// token carries, so the workspace proxy can authorize a shim call as the user
// who issued the command instead of as tenant admin.
//
// It is a dedicated type, not the SecurityContext wire shape: the token is
// signed with a key other services also hold, so it must not be able to
// express super-admin or server-internal authority. Cross-tenant roles are
// reduced to their tenant-scoped equivalent when the grants are taken.
type WorkspaceGrants struct {
	UserId                  string                     `json:"user_id"`
	Roles                   []string                   `json:"roles"`
	AccountIds              []string                   `json:"account_ids"`
	ScopedEntityIds         map[string][]string        `json:"scoped_entity_ids,omitempty"`
	CustomPermissions       map[string]bool            `json:"custom_permissions,omitempty"`
	ScopedCustomPermissions map[string]map[string]bool `json:"scoped_custom_permissions,omitempty"`
}

var errWorkspaceGrantsCrossTenant = errors.New("workspace grants: cross-tenant roles are not allowed")

// WorkspaceTenantAccounts lists a tenant's account ids. It is a variable so
// tests can supply accounts without a database.
var WorkspaceTenantAccounts = GetAccountIdsForTenant

// WorkspaceGrantsFor returns the grants to carry for work in tenantId.
// A super admin (including the synthetic server-internal context) acts as
// tenant admin of that tenant, a read-only super admin as read-only tenant
// admin. A context of another tenant is refused. An empty user id becomes the
// system user so the proxy never sees an anonymous identity.
func WorkspaceGrantsFor(sc *SecurityContext, tenantId string) (WorkspaceGrants, error) {
	if tenantId == "" {
		return WorkspaceGrants{}, errors.New("workspace grants: tenant id is required")
	}
	if sc == nil {
		return tenantAdminWorkspaceGrants(GetSystemUserId(), AUTH_TENANT_ADMIN_ROLE, tenantId)
	}
	userId := sc.userId
	if userId == "" {
		userId = GetSystemUserId()
	}
	if sc.IsSuperAdmin() {
		return tenantAdminWorkspaceGrants(userId, AUTH_TENANT_ADMIN_ROLE, tenantId)
	}
	if sc.IsSuperAdminReadonly() {
		return tenantAdminWorkspaceGrants(userId, AUTH_TENANT_READ_ADMIN_ROLE, tenantId)
	}
	if sc.tenantId != tenantId {
		return WorkspaceGrants{}, fmt.Errorf("workspace grants: context tenant %q does not own this workspace", sc.tenantId)
	}
	return WorkspaceGrants{
		UserId:                  userId,
		Roles:                   slices.Clone(sc.roles),
		AccountIds:              slices.Clone(sc.accountIds),
		ScopedEntityIds:         sc.scopedEntityIds,
		CustomPermissions:       sc.customPermissions,
		ScopedCustomPermissions: sc.scopedCustomPermissions,
	}, nil
}

func tenantAdminWorkspaceGrants(userId, role, tenantId string) (WorkspaceGrants, error) {
	accountIds, err := WorkspaceTenantAccounts(tenantId)
	if err != nil {
		return WorkspaceGrants{}, fmt.Errorf("workspace grants: %w", err)
	}
	return WorkspaceGrants{UserId: userId, Roles: []string{role}, AccountIds: accountIds}, nil
}

// NewSecurityContextFromWorkspaceGrants rebuilds the caller's authority from
// token grants. Accounts outside tenantId are dropped, so a token can never
// reach another tenant's accounts, and cross-tenant roles are refused.
func NewSecurityContextFromWorkspaceGrants(tenantId string, g WorkspaceGrants) (*SecurityContext, error) {
	if tenantId == "" || g.UserId == "" {
		return nil, errors.New("workspace grants: tenant and user are required")
	}
	if slices.Contains(g.Roles, AUTH_SUPER_ADMIN_FULL_ROLE) || slices.Contains(g.Roles, AUTH_SUPER_ADMIN_READONLY_ROLE) {
		return nil, errWorkspaceGrantsCrossTenant
	}
	tenantAccounts, err := WorkspaceTenantAccounts(tenantId)
	if err != nil {
		return nil, fmt.Errorf("workspace grants: %w", err)
	}
	accountIds := make([]string, 0, len(g.AccountIds))
	for _, id := range g.AccountIds {
		if slices.Contains(tenantAccounts, id) {
			accountIds = append(accountIds, id)
		}
	}
	return &SecurityContext{
		tenantId:                tenantId,
		userId:                  g.UserId,
		roles:                   slices.Clone(g.Roles),
		accountIds:              accountIds,
		scopedEntityIds:         g.ScopedEntityIds,
		customPermissions:       g.CustomPermissions,
		scopedCustomPermissions: g.ScopedCustomPermissions,
	}, nil
}
