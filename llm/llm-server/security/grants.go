package security

import (
	"context"
	"fmt"
	"strings"
)

// Can reports whether the caller holds `module:class` on accountId: a built-in
// account role that allows the class (Read: read, Execute: create, Write:
// update), or a custom grant for the module on that account. A Write grant
// implies Read and Execute. runbook-server's helper of the same name answers
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
			sc.HasScopedPermission(accountId, module, "Execute") ||
			sc.HasScopedPermission(accountId, module, "Write")
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

// MayUseIntegration reports whether a principal whose integration use is
// granted per type (a bee) may use an integration of integrationType on
// accountId: Read lets it read through the integration, Execute also lets it
// make changes. Write on the module is the right to manage the integration and
// does not let the holder use it. runbook-server's helper of the same name
// answers the same.
func (sc *SecurityContext) MayUseIntegration(accountId string, integrationType string, write bool) bool {
	if sc == nil {
		return false
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

// ToolPathAccountAccess is the account check on the tool path: in a
// grant-scoped run, Can on module (read: Read, create: Execute, update and
// delete: Write); otherwise HasAccountAccess, as before.
func ToolPathAccountAccess(ctx *RequestContext, accountId string, module string, access SecurityAccessType) bool {
	if ctx == nil || ctx.GetSecurityContext() == nil {
		return false
	}
	sc := ctx.GetSecurityContext()
	if !IsGrantScopedRun(ctx) {
		return sc.HasAccountAccess(accountId, access)
	}
	switch access {
	case SecurityAccessTypeRead:
		return sc.Can(accountId, module, "Read")
	case SecurityAccessTypeCreate:
		return sc.Can(accountId, module, "Execute")
	default:
		return sc.Can(accountId, module, "Write")
	}
}

// ToolPathTargetReadable reports whether a command may target accountId (a
// cluster or a cloud account) as a read: in a grant-scoped run, a Read grant on
// k8s or cloud there; otherwise read access through a built-in role.
func ToolPathTargetReadable(ctx *RequestContext, accountId string) bool {
	return ToolPathAccountAccess(ctx, accountId, "k8s", SecurityAccessTypeRead) ||
		IsGrantScopedRun(ctx) && ToolPathAccountAccess(ctx, accountId, "cloud", SecurityAccessTypeRead)
}

// CheckIntegrationUse refuses, in a grant-scoped run, a tool's use of an
// integration of integrationType on accountId that the principal holds no use
// grant for; the error names the grant. An empty type means the integration
// could not be told and is refused. Outside a grant-scoped run it allows.
func CheckIntegrationUse(ctx *RequestContext, accountId string, integrationType string, write bool) error {
	if !IsGrantScopedRun(ctx) {
		return nil
	}
	class := "Read"
	if write {
		class = "Execute"
	}
	if strings.TrimSpace(integrationType) == "" {
		return fmt.Errorf("permission denied: this run may use an integration only with a grant for its type, and this call's integration type is unknown")
	}
	if ctx.GetSecurityContext() == nil || !ctx.GetSecurityContext().MayUseIntegration(accountId, integrationType, write) {
		return fmt.Errorf("permission denied: this run needs %s:%s on account %s to use this %s integration",
			IntegrationModule(integrationType), class, accountId, integrationType)
	}
	return nil
}
