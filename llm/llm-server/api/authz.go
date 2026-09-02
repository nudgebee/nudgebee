package api

import "nudgebee/llm/security"

// Authorization here is the UNION of two independent paths: the built-in
// account/namespace roles (HasAccountAccess) and dynamic-RBAC custom-role grants
// on the module the gateway classified the action under. The gateway admits a
// caller on either path, so a service check that consults only the built-in half
// turns every granted action into a 401 the user cannot explain — the UI offers
// it, the gateway allows it, and the handler refuses it.
//
// granted() is the GRANT HALF ONLY. Call sites keep their existing
// HasAccountAccess expression verbatim and OR this in, which makes the change
// provably additive: no built-in role gains or loses anything.
//
// The module strings must match what classifyAction (app/src/lib/permissionCatalog.ts)
// assigns to the actions routed to that endpoint — gateway and handler then agree
// by construction. They are named here rather than inlined so a re-homed action
// is a one-line change.
const (
	moduleAiAgents        = "ai_agents"
	moduleAiConversations = "ai_conversations"
	moduleAiFunctions     = "ai_functions"
	moduleAiGeneration    = "ai_generation"
	moduleAiGuardrails    = "ai_guardrails"
	moduleAiKbs           = "ai_kbs"
	moduleAiMemory        = "ai_memory"
	moduleAiMisc          = "ai_misc"
	moduleAiRca           = "ai_rca"
	moduleAiTools         = "ai_tools"
)

// granted reports whether the caller holds a custom-role grant for
// (module, one of classes) that applies to accountId. HasScopedPermission covers
// both flavors: a tenant-global grant (bounded to accounts in the caller's
// tenant) and an account-scoped one.
func granted(sc *security.SecurityContext, accountId, module string, classes ...string) bool {
	for _, class := range classes {
		if sc.HasScopedPermission(accountId, module, class) {
			return true
		}
	}
	return false
}

// grantedWrite is the common case for a mutation: the module's Write grant.
func grantedWrite(sc *security.SecurityContext, accountId, module string) bool {
	return granted(sc, accountId, module, "Write")
}

// grantedRun is the common case for an operation the catalog classifies as
// Execute (run an investigation, generate a query, sync a knowledge base).
// Write is accepted too — someone who may rewrite the thing can already make it
// do whatever running it would.
func grantedRun(sc *security.SecurityContext, accountId, module string) bool {
	return granted(sc, accountId, module, "Execute", "Write")
}
