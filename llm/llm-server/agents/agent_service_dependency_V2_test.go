package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServiceDependencyV2_GetName_MatchesV1(t *testing.T) {
	agent := ServiceDependencyGraphAgentV2{accountId: "test-account"}
	assert.Equal(t, ServiceDependencyGraph, agent.GetName(),
		"V2 must register under V1's name to keep parent prompts/callers invariant")
	assert.Equal(t, "service_dependency_graph", agent.GetName())
}

func TestServiceDependencyV2_GetNameAliases(t *testing.T) {
	agent := ServiceDependencyGraphAgentV2{accountId: "test-account"}
	aliases := agent.GetNameAliases()
	assert.Contains(t, aliases, "Service Dependency Graph")
	assert.Contains(t, aliases, "Knowledge Graph")
	assert.Contains(t, aliases, "KG")
}

func TestServiceDependencyV2_GetPlannerType(t *testing.T) {
	agent := ServiceDependencyGraphAgentV2{accountId: "test-account"}
	assert.Equal(t, core.AgentPlannerTypeReAct, agent.GetPlannerType())
}

func TestServiceDependencyV2_GetCacheScope_Account(t *testing.T) {
	agent := ServiceDependencyGraphAgentV2{accountId: "test-account"}
	assert.Equal(t, core.CacheScopeAccount, agent.GetCacheScope(),
		"V2 must declare CacheScopeAccount so the embedded agent_kg_usage.txt sits in the 12h-cached prefix")
}

func TestServiceDependencyV2_GetDescription_CoversK8sAndCloud(t *testing.T) {
	agent := ServiceDependencyGraphAgentV2{accountId: "test-account"}
	desc := agent.GetDescription()
	assert.Contains(t, desc, "Knowledge Graph",
		"description must advertise the KG as the data source")
	for _, must := range []string{"K8s", "AWS", "GCP", "Azure"} {
		assert.Contains(t, desc, must,
			"V2 description must claim coverage for both Kubernetes and the major clouds")
	}
}

func TestServiceDependencyV2_GetSupportedTools_KGAndResourceSearchOnly(t *testing.T) {
	agent := ServiceDependencyGraphAgentV2{accountId: "test-account"}
	sc := security.NewRequestContextForSuperAdmin()

	prevGetNode := config.Config.KGGetNodeEnabled
	t.Cleanup(func() { config.Config.KGGetNodeEnabled = prevGetNode })

	// Flag OFF: kg_get_node must be absent; KG search/traverse + resource_search
	// must be present; the V1 runtime SDG tool must NEVER appear.
	config.Config.KGGetNodeEnabled = false
	supported := agent.GetSupportedTools(sc)
	names := toolNames(supported)

	assert.Contains(t, names, tools.ToolKGSearchNodes,
		"V2 must expose kg_search_nodes for KG discovery")
	assert.Contains(t, names, tools.ToolKGTraverse,
		"V2 must expose kg_traverse for dependency / topology / cloud-routing traversal")
	assert.Contains(t, names, ResourceSearchAgentName,
		"V2 must expose resource_search for fuzzy K8s + cloud resource matching")
	assert.NotContains(t, names, tools.ToolServiceDependencyGraph,
		"V2 must NOT expose the runtime SDG tool — KG-only")
	assert.NotContains(t, names, tools.ToolKGGetNode,
		"kg_get_node must be gated by KGGetNodeEnabled")

	// Flag ON: kg_get_node now appears; the rest of the tool set is unchanged.
	config.Config.KGGetNodeEnabled = true
	supported = agent.GetSupportedTools(sc)
	names = toolNames(supported)

	assert.Contains(t, names, tools.ToolKGGetNode,
		"with KGGetNodeEnabled=true the drill-down tool must be advertised")
	assert.Contains(t, names, tools.ToolKGSearchNodes)
	assert.Contains(t, names, tools.ToolKGTraverse)
	assert.Contains(t, names, ResourceSearchAgentName)
	assert.NotContains(t, names, tools.ToolServiceDependencyGraph)
}

func TestServiceDependencyV2_GetSystemPrompt_RoleAndConstraints(t *testing.T) {
	agent := ServiceDependencyGraphAgentV2{accountId: "test-account"}
	sc := security.NewRequestContextForSuperAdmin()

	prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{
		Query:     "what does payment-service call?",
		AccountId: "test-account",
	})

	// Role must position the agent across K8s + AWS/GCP/Azure so the planner
	// trusts it with cloud-side dependency questions, not just K8s ones.
	assert.Contains(t, prompt.Role, "Kubernetes")
	assert.Contains(t, prompt.Role, "AWS/GCP/Azure",
		"role must explicitly claim coverage for the major clouds — load-bearing for routing")
	assert.NotEmpty(t, prompt.Instructions)
	assert.NotEmpty(t, prompt.Examples)
	assert.NotEmpty(t, prompt.Constraints)
}

func TestServiceDependencyV2_GetSystemPrompt_EmbedsKgUsage(t *testing.T) {
	agent := ServiceDependencyGraphAgentV2{accountId: "test-account"}
	sc := security.NewRequestContextForSuperAdmin()

	prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{
		Query:     "what does payment-service call?",
		AccountId: "test-account",
	})

	joined := strings.Join(prompt.Instructions, "\n")

	// These markers live ONLY in agent_kg_usage.txt — they do NOT appear in
	// the agent's hard-coded Instructions. If the //go:embed wiring breaks
	// and GetPrompt(PromptAgentKgUsage) returns "", these assertions fail.
	// Avoid generic substrings like "kg_traverse" or "Knowledge Graph": those
	// also live in the inline instructions and would mask a broken embed.
	assert.Contains(t, joined, "Direction Guide",
		"agent_kg_usage.txt's 'Direction Guide' section must be embedded — unique-to-embed marker")
	assert.Contains(t, joined, "Relationship Types",
		"agent_kg_usage.txt's relationship-type matrix must be embedded")
	assert.Contains(t, joined, "Common Patterns",
		"agent_kg_usage.txt's pattern catalog must be embedded")
}

func TestServiceDependencyV2_GetSystemPrompt_ToolUsageEntries(t *testing.T) {
	agent := ServiceDependencyGraphAgentV2{accountId: "test-account"}
	sc := security.NewRequestContextForSuperAdmin()

	prevGetNode := config.Config.KGGetNodeEnabled
	t.Cleanup(func() { config.Config.KGGetNodeEnabled = prevGetNode })
	config.Config.KGGetNodeEnabled = false

	prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{
		Query:     "find rds databases",
		AccountId: "test-account",
	})

	assert.Contains(t, prompt.ToolUsage, tools.ToolKGSearchNodes)
	assert.Contains(t, prompt.ToolUsage, tools.ToolKGTraverse)
	assert.Contains(t, prompt.ToolUsage, ResourceSearchAgentName)
	assert.NotContains(t, prompt.ToolUsage, tools.ToolServiceDependencyGraph,
		"V2 must not advertise the runtime SDG tool in its prompt")
	assert.NotContains(t, prompt.ToolUsage, tools.ToolKGGetNode,
		"kg_get_node ToolUsage must be flag-gated")

	// kg_search_nodes guidance must mention `source` so the planner knows it
	// can filter the KG by aws/gcp/azure for cloud-side discovery.
	searchGuidance := strings.Join(prompt.ToolUsage[tools.ToolKGSearchNodes], "\n")
	assert.Contains(t, searchGuidance, "source",
		"kg_search_nodes guidance must reference the `source` filter — the cloud-discovery hook")

	// Flip flag on: kg_get_node ToolUsage must appear.
	config.Config.KGGetNodeEnabled = true
	prompt = agent.GetSystemPrompt(sc, core.NBAgentRequest{
		Query:     "drill into node",
		AccountId: "test-account",
	})
	assert.Contains(t, prompt.ToolUsage, tools.ToolKGGetNode,
		"with KGGetNodeEnabled=true kg_get_node ToolUsage must be advertised")
}

func TestServiceDependencyV2_GetSystemPrompt_ExamplesCoverK8sAndCloud(t *testing.T) {
	agent := ServiceDependencyGraphAgentV2{accountId: "test-account"}
	sc := security.NewRequestContextForSuperAdmin()

	prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{
		Query:     "examples coverage",
		AccountId: "test-account",
	})

	// Concatenate questions + answers + explanations so we can scan for required
	// coverage markers regardless of which example carried which keyword.
	var blob strings.Builder
	for _, ex := range prompt.Examples {
		blob.WriteString(ex.Question)
		blob.WriteString("\n")
		blob.WriteString(ex.Answer)
		blob.WriteString("\n")
		blob.WriteString(ex.Explanation)
		blob.WriteString("\n")
	}
	corpus := blob.String()

	// K8s coverage — at minimum CALLS edges and namespace/cluster hosting.
	assert.Contains(t, corpus, "CALLS",
		"examples must include a CALLS-edge traversal (the K8s service-call use case)")
	assert.Contains(t, corpus, "namespace",
		"examples must include a namespace/topology question")

	// Cloud coverage — RDS (AWS DB), load balancer routing, source-filter
	// discovery. These are the cloud questions V2 exists to handle.
	assert.Contains(t, corpus, "RDS",
		"examples must include an AWS-specific resource (RDS) so the planner sees cloud is in scope")
	assert.True(t,
		strings.Contains(strings.ToLower(corpus), "load balancer") ||
			strings.Contains(corpus, "LoadBalancer"),
		"examples must include a load-balancer routing case (cloud connectivity)")
	assert.Contains(t, corpus, `source:"aws"`,
		"examples must demonstrate the `source` filter — the KG hook for cloud discovery")
}

func toolNames(ts []toolcore.NBTool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name())
	}
	return out
}

// TestServiceDependencyV2KGExecute exercises KG tool routing for K8s and
// cloud (AWS/GCP/Azure) questions on the V2 KG-only agent. Env-gated.
