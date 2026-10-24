package security

import (
	"context"
	"fmt"
	"strings"
)

// Can reports whether the caller holds `module:class` on accountId: a built-in
// account role that allows the class (Read: read, Execute: create, Write:
// update), or a custom grant for the module on that account. A Write grant
// implies Read; it does not imply Execute. runbook-server's helper of the same name answers
// the same (runbook-server/services/security/testdata/can_cases.json).
func (sc *SecurityContext) Can(accountId string, module string, class string) bool {
	if sc == nil {
		return false
	}
	switch class {
	case "Read":
		return sc.CanReadAccountData(accountId, module)
	case "Execute":
		return sc.HasAccountAccess(accountId, SecurityAccessTypeCreate) ||
			sc.HasScopedPermission(accountId, module, "Execute")
	case "Write":
		return sc.HasAccountAccess(accountId, SecurityAccessTypeUpdate) ||
			sc.HasScopedPermission(accountId, module, "Write")
	}
	return false
}

// IntegrationModule is the custom-role module that governs integrations of
// one stored type: "integrations.<type>", lower-cased.
func IntegrationModule(integrationType string) string {
	return "integrations." + strings.ToLower(strings.TrimSpace(integrationType))
}

// MayUseIntegration reports whether the principal may use an integration of
// integrationType on accountId: write to make changes through it, else to read
// through it. A built-in account role that allows the change (create) or the
// read on the account allows any integration, as it allows any module (Can);
// otherwise it takes a use grant on integrations.<type>: Read reads through
// the integration, Execute also makes changes. Write on that module is the
// right to manage the integration and does not let the holder use it. A bee
// holds no built-in role, so for a bee only its grants count. runbook-server's helper of the same name
// answers the same.
func (sc *SecurityContext) MayUseIntegration(accountId string, integrationType string, write bool) bool {
	if sc == nil {
		return false
	}
	access := SecurityAccessTypeRead
	if write {
		access = SecurityAccessTypeCreate
	}
	if sc.HasAccountAccess(accountId, access) {
		return true
	}
	module := IntegrationModule(integrationType)
	if sc.HasScopedPermission(accountId, module, "Execute") {
		return true
	}
	return !write && sc.HasScopedPermission(accountId, module, "Read")
}

type grantScopedRunKey struct{}

// WithGrantScopedRun marks ctx as a run whose principal acts only through its
// custom-role grants (a bee's run): on the tool path, account access is
// SecurityContext.Can on the module the tool touches, and each integration a
// tool uses needs MayUseIntegration. A run without the mark keeps the built-in
// role checks.
func WithGrantScopedRun(ctx *RequestContext) *RequestContext {
	if ctx == nil || IsGrantScopedRun(ctx) {
		return ctx
	}
	return ctx.WithContext(context.WithValue(ctx.GetContext(), grantScopedRunKey{}, true))
}

// IsGrantScopedRun reports whether ctx is a grant-scoped run (WithGrantScopedRun).
func IsGrantScopedRun(ctx *RequestContext) bool {
	if ctx == nil || ctx.GetContext() == nil {
		return false
	}
	scoped, _ := ctx.GetContext().Value(grantScopedRunKey{}).(bool)
	return scoped
}

// IsGrantCheckedRun reports whether this run's use of tools on accountId is
// decided by custom grants rather than built-in roles: always in a grant-scoped
// run (a Bee), and for a person who holds no built-in read on the account (their
// access there comes from custom roles only). A person with a built-in role on the
// account keeps the built-in checks, unchanged. With no context it reports true:
// true applies the grant checks, so a missing context is checked, not waved through.
func IsGrantCheckedRun(ctx *RequestContext, accountId string) bool {
	if IsGrantScopedRun(ctx) {
		return true
	}
	if ctx == nil || ctx.GetSecurityContext() == nil {
		return true
	}
	return !ctx.GetSecurityContext().HasAccountAccess(accountId, SecurityAccessTypeRead)
}

// grantClassFor maps a tool's access to the grant class it needs: read: Read,
// create: Execute (running a change), update and delete: Write.
func grantClassFor(access SecurityAccessType) string {
	switch access {
	case SecurityAccessTypeRead:
		return "Read"
	case SecurityAccessTypeCreate:
		return "Execute"
	default:
		return "Write"
	}
}

// ToolPathAccountAccess is the account check on the tool path: in a
// grant-scoped run, Can on module (read: Read, create: Execute, update and
// delete: Write); otherwise a built-in role that allows the access, or the same
// custom grant on module (a person whose access comes from custom roles).
func ToolPathAccountAccess(ctx *RequestContext, accountId string, module string, access SecurityAccessType) bool {
	if ctx == nil || ctx.GetSecurityContext() == nil {
		return false
	}
	sc := ctx.GetSecurityContext()
	if !IsGrantScopedRun(ctx) {
		return sc.HasAccountAccess(accountId, access) ||
			(module != "" && sc.Can(accountId, module, grantClassFor(access)))
	}
	return sc.Can(accountId, module, grantClassFor(access))
}

// ToolPathTargetReadable reports whether a command may target accountId (a
// cluster or a cloud account) as a read: a built-in role that reads it, or a
// Read grant on k8s or cloud there.
func ToolPathTargetReadable(ctx *RequestContext, accountId string) bool {
	return ToolPathAccountAccess(ctx, accountId, "k8s", SecurityAccessTypeRead) ||
		ToolPathAccountAccess(ctx, accountId, "cloud", SecurityAccessTypeRead)
}

// CheckIntegrationUse refuses, in a grant-checked run (IsGrantCheckedRun: a Bee,
// or a person without a built-in role on the account), a tool's use of an
// integration of integrationType on accountId that the principal holds no use
// grant for; the error names the grant. An empty type means the integration
// could not be told and is refused. A person with a built-in role on the account
// is allowed, as before.
func CheckIntegrationUse(ctx *RequestContext, accountId string, integrationType string, write bool) error {
	if !IsGrantCheckedRun(ctx, accountId) {
		return nil
	}
	class := "Read"
	if write {
		class = "Execute"
	}
	if strings.TrimSpace(integrationType) == "" {
		return fmt.Errorf("permission denied: this run may use an integration only with a grant for its type, and this call's integration type is unknown")
	}
	if ctx == nil || ctx.GetSecurityContext() == nil || !ctx.GetSecurityContext().MayUseIntegration(accountId, integrationType, write) {
		return fmt.Errorf("permission denied: this run needs %s:%s on account %s to use this %s integration",
			IntegrationModule(integrationType), class, accountId, integrationType)
	}
	return nil
}
