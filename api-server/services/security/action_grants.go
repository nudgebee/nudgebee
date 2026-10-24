package security

import (
	"slices"
	"strings"
)

// CanAccessAccount is the authorization check for an operation on accountId: a
// built-in role that grants `access` on the account (HasAccountAccess), OR a
// dynamic-RBAC custom grant for actionKey that applies to the account
// (tenant-wide, or bound to that account).
//
// actionKey is the "<module>:<Class>" key the app gateway classified the RPC
// action as (SetActionPermission records it, from the request). Taking the module from
// the classification means a handler cannot drift from the module the role
// editor grants (the bug class behind 9be4d473bc, c6f1820ff2, 3ace9ff156). With
// an empty key it is exactly HasAccountAccess. Custom grants never widen data
// scope: HasScopedPermission only admits accounts of the caller's own tenant.
// A nil context fails closed.
func (sc *SecurityContext) CanAccessAccount(accountId string, access SecurityAccessType) bool {
	if sc == nil {
		return false
	}
	return sc.canAccessAccount(accountId, access, sc.actionPermission)
}

func (sc *SecurityContext) canAccessAccount(accountId string, access SecurityAccessType, actionKey string) bool {
	if sc.HasAccountAccess(accountId, access) {
		return true
	}
	module, class, ok := actionGrant(actionKey, access)
	if !ok {
		return false
	}
	if sc.HasScopedPermission(accountId, module, class) {
		return true
	}
	// Write implies Read, as at the gateway.
	return class == "Read" && sc.HasScopedPermission(accountId, module, "Write")
}

// CanAccessTenant is the tenant-level counterpart of CanAccessAccount: a
// built-in tenant role (HasTenantAccess — tenant/super admin, read-only flavors
// for reads) OR a TENANT-WIDE custom grant for actionKey. An account-bound grant
// never satisfies it — a tenant-level operation has no account to bind to.
//
// Never use it for privilege administration (roles, groups, user-role
// assignments): those stay IsTenantAdmin()-only so a custom role cannot escalate
// itself. The gateway classifies those actions as non-grantable, so the key is
// empty for them anyway. A nil context fails closed.
func (sc *SecurityContext) CanAccessTenant(access SecurityAccessType) bool {
	if sc == nil {
		return false
	}
	return sc.canAccessTenant(access, sc.actionPermission)
}

// CanAccessTenantForAction is CanAccessTenant against another action's grant
// key (by name), for a check that decides on that action's behalf: the
// ownership reads ask whether the caller may assign an owner.
func (sc *SecurityContext) CanAccessTenantForAction(access SecurityAccessType, actionName string) bool {
	if sc == nil {
		return false
	}
	return sc.canAccessTenant(access, ActionKey(actionName, ""))
}

func (sc *SecurityContext) canAccessTenant(access SecurityAccessType, actionKey string) bool {
	if sc.HasTenantAccess(access) {
		return true
	}
	module, class, ok := actionGrant(actionKey, access)
	if !ok {
		return false
	}
	return sc.HasPermission(module, class) || (class == "Read" && sc.HasPermission(module, "Write"))
}

// SetActionPermission records the current action's grant key (ActionKey), which
// CanAccessAccount and CanAccessTenant check custom grants against.
func (sc *SecurityContext) SetActionPermission(key string) {
	if sc != nil {
		sc.actionPermission = key
	}
}

// CanReadAccountOrTenantUsage is the account check for a read that is also open
// to tenant_usage: a role (or the action's grant) that reads the account, or
// tenant_usage on an account of the caller's tenant (it has no per-account
// scope, so a tenant check is all it gets).
func (sc *SecurityContext) CanReadAccountOrTenantUsage(accountId string) bool {
	if sc == nil {
		return false
	}
	return sc.CanAccessAccount(accountId, SecurityAccessTypeRead) ||
		(slices.Contains(sc.GetRoles(), AUTH_TENANT_USAGE_ROLE) && sc.IsTenantAccount(accountId))
}

// actionGrant returns the (module, class) of an action's grant key when it may
// authorize `access`. A Read-classified action never authorizes a mutation: a
// handler asking for write access under a read classification means the
// classification is wrong, and that fails closed.
func actionGrant(actionKey string, access SecurityAccessType) (string, string, bool) {
	module, class, found := strings.Cut(actionKey, ":")
	if !found || module == "" || class == "" {
		return "", "", false
	}
	if class == "Read" && access != SecurityAccessTypeRead {
		return "", "", false
	}
	return module, class, true
}
