package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// ai_create_agent, ai_update_agent, ai_delete_agent and
// ai_create_agent_extension on an account agent: a built-in role that writes
// the account, as before, or an ai_agents:Write grant there (tenant-wide or on
// the account). Read, Execute, another module's grant and an account-bound
// grant on another account are refused.
func TestCustomAgentAccountWriteGrants(t *testing.T) {
	const b = agentGrantAccountB
	perm := func(m, c string) map[string]bool { return map[string]bool{m + ":" + c: true} }
	callers := []struct {
		name   string
		caller *security.RequestContext
		allow  bool
	}{
		{"tenant_admin", agentGrantCaller(t, []string{security.AUTH_TENANT_ADMIN_ROLE}, nil, nil, nil), true},
		{"account_admin_b", agentGrantCaller(t, []string{security.AUTH_ACCOUNT_ADMIN_ROLE},
			map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {b}}, nil, nil), true},
		{"tenant_admin_readonly", agentGrantCaller(t, []string{security.AUTH_TENANT_READ_ADMIN_ROLE}, nil, nil, nil), false},
		{"account_admin_a", agentGrantCaller(t, []string{security.AUTH_ACCOUNT_ADMIN_ROLE},
			map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {agentGrantAccountA}}, nil, nil), false},
		{"no_role", agentGrantCaller(t, nil, nil, nil, nil), false},
		{"ai_agents:Write", agentGrantCaller(t, nil, nil, perm("ai_agents", "Write"), nil), true},
		{"ai_agents:Write on b", agentGrantCaller(t, nil, nil, nil, map[string]map[string]bool{b: perm("ai_agents", "Write")}), true},
		{"ai_agents:Write on a", agentGrantCaller(t, nil, nil, nil,
			map[string]map[string]bool{agentGrantAccountA: perm("ai_agents", "Write")}), false},
		{"ai_agents:Read", agentGrantCaller(t, nil, nil, perm("ai_agents", "Read"), nil), false},
		{"ai_agents:Execute", agentGrantCaller(t, nil, nil, perm("ai_agents", "Execute"), nil), false},
		{"ai_tools:Write", agentGrantCaller(t, nil, nil, perm("ai_tools", "Write"), nil), false},
	}
	name := "deploy_checker"
	actions := []struct {
		action  string
		account bool // the call locks the agent's owner (an account agent) first
		call    func(*security.RequestContext) error
	}{
		{"ai_create_agent", false, func(sc *security.RequestContext) error {
			_, err := CreateCustomAgent(sc, b, AgentDto{Name: name, Description: "checks a deploy", SystemPrompt: "check it"}, nil, false)
			return err
		}},
		{"ai_update_agent", true, func(sc *security.RequestContext) error {
			_, err := UpdateCustomAgent(sc, b, AgentUpdateDto{Id: agentGrantItem, Name: &name, Status: AgentStatusEnabled})
			return err
		}},
		{"ai_delete_agent", false, func(sc *security.RequestContext) error {
			return DeleteCustomAgent(sc, b, name)
		}},
		{"ai_create_agent_extension", false, func(sc *security.RequestContext) error {
			_, err := CreateAgentExtension(sc, b, AgentExtension{AgentName: "k8s_orchestrator"}, "u1")
			return err
		}},
	}
	for _, a := range actions {
		t.Run(a.action, func(t *testing.T) {
			for _, tc := range callers {
				t.Run(tc.name, func(t *testing.T) {
					mock := agentGrantMockDB(t)
					if a.account {
						expectLockedAgentOwner(mock, agentGrantTenant, toolcore.VisibilityAccount)
					}
					// The update path wraps the refusal in its transaction error.
					err := a.call(tc.caller)
					refused := err != nil && strings.HasSuffix(err.Error(), agentUnauthorized)
					assert.Equal(t, !tc.allow, refused, "got %v", err)
				})
			}
		})
	}
}
