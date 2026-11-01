package core

import "strings"

// AccountScope says how a tool relates to the account a call is about. Every
// registered system tool declares one (see agents/tool_account_scopes.go); a
// test fails when a tool has none, so no tool can silently answer for the
// conversation's account when the question is about another.
type AccountScope string

const (
	// AccountScopeFollowsTarget tools read data for the call's account
	// (NbToolContext.DataAccountID()): events, metrics, recommendations.
	AccountScopeFollowsTarget AccountScope = "follows_target"
	// AccountScopeIndependent tools use no account-level data or APIs: web
	// search, docs, skills, reasoning, tenant-wide product state.
	AccountScopeIndependent AccountScope = "account_independent"
	// AccountScopeOwnSelector tools take their own cluster/account argument
	// and route the call themselves: kubectl, helm, shell, cloud CLIs.
	AccountScopeOwnSelector AccountScope = "own_selector"
	// AccountScopeOriginBound tools can act only on the conversation's
	// account; they are refused for a call about another account. The
	// reason says what would have to change for them to follow a target.
	AccountScopeOriginBound AccountScope = "origin_bound"
)

// OriginBoundReason records why an origin-bound tool cannot follow a target.
type OriginBoundReason string

const (
	// OriginBoundWrite: acts on the account; waits for target-scoped approval.
	OriginBoundWrite OriginBoundReason = "write"
	// OriginBoundIntegration: credentials come from an integration linked to
	// the conversation's account (Datadog, databases, ...).
	OriginBoundIntegration OriginBoundReason = "integration"
	// OriginBoundConversationState: per-account state such as memory or
	// workflows, not a cluster's data.
	OriginBoundConversationState OriginBoundReason = "conversation_state"
	// OriginBoundNotYetSupported: reads account data but has not been made to
	// follow a target yet.
	OriginBoundNotYetSupported OriginBoundReason = "not_yet_supported"
)

type AccountScopeDeclaration struct {
	Scope  AccountScope
	Reason OriginBoundReason // origin-bound tools only
}

// accountScopes is written only from init().
var (
	accountScopes         = map[string]AccountScopeDeclaration{}
	duplicateAccountScope []string
)

func declare(name string, d AccountScopeDeclaration) {
	key := strings.ToLower(name)
	if _, exists := accountScopes[key]; exists {
		duplicateAccountScope = append(duplicateAccountScope, key)
	}
	accountScopes[key] = d
}

// DeclareAccountScope records the account scope of the named tools.
func DeclareAccountScope(scope AccountScope, names ...string) {
	for _, name := range names {
		declare(name, AccountScopeDeclaration{Scope: scope})
	}
}

// DeclareOriginBound records origin-bound tools and why.
func DeclareOriginBound(reason OriginBoundReason, names ...string) {
	for _, name := range names {
		declare(name, AccountScopeDeclaration{Scope: AccountScopeOriginBound, Reason: reason})
	}
}

// AccountScopeOf returns a tool's declared account scope.
func AccountScopeOf(name string) (AccountScopeDeclaration, bool) {
	d, ok := accountScopes[strings.ToLower(name)]
	return d, ok
}

// DeclaredAccountScopeNames lists every declared tool name (lower case).
func DeclaredAccountScopeNames() []string {
	names := make([]string, 0, len(accountScopes))
	for name := range accountScopes {
		names = append(names, name)
	}
	return names
}

// DuplicateAccountScopeNames lists tools declared more than once; the later
// declaration would silently win.
func DuplicateAccountScopeNames() []string {
	return append([]string(nil), duplicateAccountScope...)
}

// ModelInputSchema is a tool's input schema as shown to the model and in
// tool listings: a follows-target tool that does not route a target itself
// gains the optional "target" input, so its author declares the scope once
// instead of adding the property.
func ModelInputSchema(tool NBTool) ToolSchema {
	schema := tool.InputSchema()
	if !takesTargetInput(tool) {
		return schema
	}
	if _, exists := schema.Properties["target"]; exists {
		return schema
	}
	properties := make(map[string]ToolSchemaProperty, len(schema.Properties)+1)
	for name, prop := range schema.Properties {
		properties[name] = prop
	}
	properties["target"] = ToolTargetSchemaProperty()
	schema.Properties = properties
	return schema
}

func takesTargetInput(tool NBTool) bool {
	if _, agentTool := tool.(DelegatedTargetSupport); agentTool {
		return false
	}
	if _, ownSelector := tool.(DelegatedTargetInputPreparer); ownSelector {
		return false
	}
	d, ok := AccountScopeOf(tool.Name())
	return ok && d.Scope == AccountScopeFollowsTarget
}
