package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func k8sDebugReq(accountId string) core.NBAgentRequest {
	return core.NBAgentRequest{Query: "debug crashlooping pods", AccountId: accountId, ConversationId: "conv-123"}
}

// TestK8sOrchestratorAgent_DirectKubectlMode verifies the direct-kubectl mode of the
// single k8s_orchestrator agent (formerly the separate k8s_orchestrator_2). In this
// mode the agent holds kubectl_execute and its prompt renders the {{if .use_kubectl_direct}}
// branch, instead of delegating to the kubectl sub-agent.
func TestK8sOrchestratorAgent_DirectKubectlMode(t *testing.T) {
	accountId := "test-account-id"
	sc := security.NewRequestContextForSuperAdmin()

	// Clear any cached tools — GetName() is shared across modes, so a stale entry
	// from another mode would otherwise satisfy the lookup.
	InvalidateAgentSupportedToolsCache(accountId, AgentK8sOrchestratorName)

	// Mock the AccountConfigSummary in the cache
	summaryJson := `{"IntegrationTypes":{},"TenantIntegrationTypes":{},"CloudProviders":{"k8s":true},"HasAgent":true}`
	err := common.CacheSet(toolcore.CacheNamespaceLlmToolConfig, "account_config_summary:v2:"+accountId, []byte(summaryJson))
	assert.NoError(t, err)

	// Mock the ToolConfig list for kubectl_execute in the cache
	configsJson := `[{"id":"config-1","name":"kubectl_execute"}]`
	err = common.CacheSet(toolcore.CacheNamespaceLlmToolConfig, "list_tool_configs:"+accountId+":"+tools.ToolExecuteKubectlCommand, []byte(configsJson))
	assert.NoError(t, err)

	// Build the orchestrator in direct-kubectl mode (v2 behavior).
	agent := newK8sOrchestratorAgentMode(accountId, true)
	assert.Equal(t, AgentK8sOrchestratorName, agent.GetName())

	// Get supported tools
	supportedTools := agent.GetSupportedTools(sc)
	var toolNames []string
	for _, tool := range supportedTools {
		toolNames = append(toolNames, tool.Name())
	}

	// Verify that kubectl_execute is in the list, and the kubectl sub-agent is NOT
	assert.Contains(t, toolNames, tools.ToolExecuteKubectlCommand) // kubectl_execute
	assert.NotContains(t, toolNames, KubectlAgentName)             // kubectl

	// Get system prompt
	req := k8sDebugReq(accountId)
	prompt := agent.GetSystemPrompt(sc, req)
	assert.NotNil(t, prompt)

	// The whole prompt file parses into Instructions (it has no ## section headers),
	// so all direct-kubectl guidance renders there.
	promptStr := strings.Join(prompt.Instructions, "\n")

	// 1. Tool name tags should be replaced in XML examples
	assert.NotContains(t, promptStr, "<tool_name>kubectl</tool_name>")
	assert.Contains(t, promptStr, "<tool_name>kubectl_execute</tool_name>")

	// 2. Delegation guidance (else-branch) must NOT be present in direct mode
	assert.NotContains(t, promptStr, "Route ALL Kubernetes CLI work through the `kubectl` agent")
	assert.NotContains(t, promptStr, "Do NOT emit `kubectl_execute` as a direct action")

	// 3. Direct-execution directive should be present
	assert.Contains(t, promptStr, "You run kubectl DIRECTLY via the `kubectl_execute` tool.")

	// 4. Tool-precedence rule must steer away from forcing everything through kubectl
	assert.Contains(t, promptStr, "one tool among many")
	assert.Contains(t, promptStr, "`logs` for pod/app logs")

	// 5. Safety guards restored from the kubectl agent must be present
	assert.Contains(t, promptStr, "**Command-only mode:**")        // return command string, don't execute
	assert.Contains(t, promptStr, "kubectl get componentstatuses") // disambiguation: health check
	assert.Contains(t, promptStr, "Use `resource_search` proactively")
}

// TestK8sOrchestrator2Handle_RegistersDirect verifies the explicit @k8s_orchestrator_2
// eval handle: it registers under its own name (so @-invocation resolves), carries the
// k8s_debug_2 alias, and is always direct-kubectl regardless of the flag.
func TestK8sOrchestrator2Handle_RegistersDirect(t *testing.T) {
	accountId := "test-account-id-2"
	sc := security.NewRequestContextForSuperAdmin()
	InvalidateAgentSupportedToolsCache(accountId, AgentK8sOrchestrator2Name)

	// Identity: distinct name + alias (distinct from the primary → distinct cache key).
	agent := newK8sOrchestrator2Agent(accountId)
	assert.Equal(t, AgentK8sOrchestrator2Name, agent.GetName())
	assert.Contains(t, agent.GetNameAliases(), "k8s_debug_2")

	// Registration resolves by name (the init() factory is wired).
	resolved, ok := core.GetNBAgent(sc, AgentK8sOrchestrator2Name, accountId, core.AgentStatusEnabled)
	assert.True(t, ok)
	assert.NotNil(t, resolved)
	assert.Equal(t, AgentK8sOrchestrator2Name, resolved.GetName())

	// Always direct: prompt renders the direct-kubectl branch.
	promptStr := strings.Join(agent.GetSystemPrompt(sc, k8sDebugReq(accountId)).Instructions, "\n")
	assert.Contains(t, promptStr, "You run kubectl DIRECTLY via the `kubectl_execute` tool.")
	assert.NotContains(t, promptStr, "Route ALL Kubernetes CLI work through the `kubectl` agent")
}

// TestK8sOrchestratorAgent_DelegatingMode verifies the default (v1) mode still routes
// kubectl work through the kubectl sub-agent and renders the delegation prompt branch.
func TestK8sOrchestratorAgent_DelegatingMode(t *testing.T) {
	accountId := "test-account-id-v1"
	sc := security.NewRequestContextForSuperAdmin()
	InvalidateAgentSupportedToolsCache(accountId, AgentK8sOrchestratorName)

	agent := newK8sOrchestratorAgentMode(accountId, false)
	req := k8sDebugReq(accountId)
	promptStr := strings.Join(agent.GetSystemPrompt(sc, req).Instructions, "\n")

	// Delegation branch present; direct-execution directive absent.
	assert.Contains(t, promptStr, "Route ALL Kubernetes CLI work through the `kubectl` agent")
	assert.NotContains(t, promptStr, "You run kubectl DIRECTLY via the `kubectl_execute` tool.")
	assert.NotContains(t, promptStr, "<tool_name>kubectl_execute</tool_name>")
}
