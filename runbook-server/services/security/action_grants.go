package security

import "strings"

// CanAccessAccount is the authorization check for an operation on accountId: a
// built-in role that grants `access` on the account (HasAccountAccess), OR a
// custom grant for actionKey that applies to the account (tenant-wide, or bound
// to that account). actionKey is the action's "<module>:<Class>" key
// (ActionKey). With an empty key it is exactly HasAccountAccess. A nil context
// fails closed. Mirrors api-server security/action_grants.go.
func (sc *SecurityContext) CanAccessAccount(accountId string, access SecurityAccessType) bool {
	if sc == nil {
		return false
	}
	return sc.canAccessAccount(accountId, access, sc.actionPermission)
}

// CanReadAccount is a Read check against the Read key of the action's module,
// so a read the action makes on another account (the accounts a trigger
// listens to) needs that module's Read grant there, not the action's Write.
func (sc *SecurityContext) CanReadAccount(accountId string) bool {
	if sc == nil {
		return false
	}
	return sc.canAccessAccount(accountId, SecurityAccessTypeRead, readKey(sc.actionPermission))
}

// SetActionPermission records the current action's grant key (ActionKey).
func (sc *SecurityContext) SetActionPermission(key string) {
	if sc != nil {
		sc.actionPermission = key
	}
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

// CanAccessTenant is the tenant-level counterpart: a built-in tenant role
// (HasTenantAccess) OR a tenant-wide custom grant for actionKey. Never use it
// for privilege administration. A nil context fails closed.
func (sc *SecurityContext) CanAccessTenant(access SecurityAccessType) bool {
	if sc == nil {
		return false
	}
	actionKey := sc.actionPermission
	if sc.HasTenantAccess(access) {
		return true
	}
	module, class, ok := actionGrant(actionKey, access)
	if !ok {
		return false
	}
	return sc.HasPermission(module, class) || (class == "Read" && sc.HasPermission(module, "Write"))
}

// actionGrant returns the (module, class) of an action's grant key when it may
// authorize `access`. A Read-classified action never authorizes a mutation.
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

// readKey is the Read key of an action key's module ("workflows:Write" ->
// "workflows:Read"); empty for an empty or malformed key.
func readKey(actionKey string) string {
	module, _, found := strings.Cut(actionKey, ":")
	if !found || module == "" {
		return ""
	}
	return module + ":Read"
}
