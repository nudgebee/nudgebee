package agents

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// productionToolNames skips the mock_* tools other tests in this package
// register.
func productionToolNames() []string {
	var names []string
	for _, name := range toolcore.ListRegisteredSystemToolNames() {
		if !strings.HasPrefix(strings.ToLower(name), "mock_") {
			names = append(names, name)
		}
	}
	return names
}

// agentLocalToolNames are tools an agent carries without registering them
// (e.g. the events agent's event_summary).
func agentLocalToolNames() map[string]bool {
	ctx := security.NewRequestContextForSuperAdmin()
	account := "00000000-0000-0000-0000-000000000001"
	local := map[string]bool{}
	for _, name := range productionToolNames() {
		agent, ok := core.GetNBAgent(ctx, name, account, core.AgentStatusEnabled)
		if !ok {
			continue
		}
		func() {
			defer func() { _ = recover() }() // some agents need a database to list tools
			for _, tool := range agent.GetSupportedTools(ctx) {
				if _, registered := toolcore.GetNBTool(account, tool.Name()); !registered {
					local[strings.ToLower(tool.Name())] = true
				}
			}
		}()
	}
	return local
}

// A tool without a declared account scope would silently answer for the
// conversation's account when a question is about another one.
func TestEveryToolDeclaresAnAccountScope(t *testing.T) {
	registered := map[string]bool{}
	for _, name := range productionToolNames() {
		registered[strings.ToLower(name)] = true
		_, ok := toolcore.AccountScopeOf(name)
		assert.True(t, ok, "%s has no account scope: declare it in tool_account_scopes.go of the package that registers it", name)
	}
	for name := range agentLocalToolNames() {
		registered[name] = true
		_, ok := toolcore.AccountScopeOf(name)
		assert.True(t, ok, "agent-local tool %s has no account scope: declare it where its agent is", name)
	}
	for _, name := range toolcore.DeclaredAccountScopeNames() {
		assert.True(t, registered[name], "%s has an account scope declared but no such tool exists", name)
	}
	assert.Empty(t, toolcore.DuplicateAccountScopeNames(), "declared more than once across the tool_account_scopes.go files")
}

// The declarations must describe what each tool does today, so replacing the
// per-tool target code with them changes no behaviour.
func TestAccountScopesMatchCurrentBehaviour(t *testing.T) {
	for _, name := range productionToolNames() {
		decl, ok := toolcore.AccountScopeOf(name)
		require.True(t, ok, name)
		tool, found := toolcore.GetNBTool("00000000-0000-0000-0000-000000000001", name)
		if !found || tool == nil {
			continue // factory needs a database (logs_execute_v2)
		}
		_, preparer := tool.(toolcore.DelegatedTargetInputPreparer)
		supports := toolcore.SupportsDelegatedTarget(tool)
		switch decl.Scope {
		case toolcore.AccountScopeFollowsTarget:
			assert.True(t, supports, "%s is declared follows_target but does not take a target", name)
		case toolcore.AccountScopeOwnSelector:
			assert.True(t, supports && preparer, "%s is declared own_selector but does not route a target itself", name)
		case toolcore.AccountScopeIndependent:
			assert.True(t, toolcore.IsTargetNeutral(name), "%s is declared account_independent but is not target-neutral", name)
		case toolcore.AccountScopeOriginBound:
			assert.False(t, supports || toolcore.IsTargetNeutral(name), "%s is declared origin_bound but runs under a target today", name)
			assert.NotEmpty(t, decl.Reason, "%s is origin_bound without a reason", name)
		default:
			t.Errorf("%s has unknown account scope %q", name, decl.Scope)
		}
	}
}
