// V2 KG-only implementation of the service_dependency_graph agent.
//
// Registers under V1's name ("service_dependency_graph") when
// config.Config.ServiceDependencyGraphV2Enabled is true. V1 and V2 are mutually
// exclusive at process start — V1's init() yields when the flag is on. The
// registered tool name does not change, so parent prompts and callers remain
// invariant across the swap.
package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

func init() {
	if !config.Config.ServiceDependencyGraphV2Enabled {
		return
	}

	toolDescription := `Explores service dependencies, topology, connectivity, and call chains across Kubernetes and cloud (AWS/GCP/Azure) via the Knowledge Graph — what calls/depends on X, what X calls, what namespace/cluster hosts X, resource discovery (workloads/databases/services/cloud resources), load-balancer routing, VPC/subnet topology. Use for ALL dependency, topology, and connectivity questions. ` +
		`How to call it: ` +
		`(A) Preserve scope — copy any account, namespace, cluster, or cloud source the user named verbatim into the plain-language ` + "`command`" + ` (e.g. "what does webapp in the nudgebee namespace call in account k8s-dev?"); omitting them forces a clarifying question. ` +
		`(B) State intent, not mechanics — send the goal in plain language; never pre-decompose into node IDs, node types, or graph traversal (the tool resolves those itself). ` +
		`(C) If the reply is a clarifying question, STOP and return it to the user verbatim — do not re-call the tool to investigate options or pick a default. ` +
		`(D) Trust the reply — do not re-verify its topology with kubectl, aws, fetch_logs, or resource_search (they carry no KG topology). ` +
		`(E) Cite the reply's evidence; never add connections or hubs it did not return (if a service has no inbound CALLS, say so).`

	toolInput := "A plain-language question describing what you want to know about dependencies/topology/connectivity (e.g. \"what does llm-server in the nudgebee namespace call?\"). State intent only — do NOT mention node IDs, node types, or graph traversal."
	toolOutput := "The tool will return the output of the question"

	core.RegisterNBAgentFactoryAndTool(ServiceDependencyGraph, func(accountId string) (core.NBAgent, error) {
		return newServiceDependencyGraphAgentV2(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func newServiceDependencyGraphAgentV2(accountId string) ServiceDependencyGraphAgentV2 {
	return ServiceDependencyGraphAgentV2{
		accountId: accountId,
	}
}

type ServiceDependencyGraphAgentV2 struct {
	accountId string
}

func (l ServiceDependencyGraphAgentV2) GetName() string {
	return ServiceDependencyGraph
}

func (l ServiceDependencyGraphAgentV2) GetNameAliases() []string {
	return []string{"Service Dependency Graph", "Knowledge Graph", "KG"}
}

func (l ServiceDependencyGraphAgentV2) GetDescription() string {
	return `Identifies service and cloud-resource dependencies via the Knowledge Graph (K8s + AWS/GCP/Azure)`
}

func (l ServiceDependencyGraphAgentV2) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolsList := []toolcore.NBTool{}
	if rs, ok := toolcore.GetNBTool(l.accountId, ResourceSearchAgentName); ok {
		toolsList = append(toolsList, rs)
	}
	for _, name := range []string{tools.ToolKGSearchNodes, tools.ToolKGTraverse} {
		if t, ok := toolcore.GetNBTool(l.accountId, name); ok {
			toolsList = append(toolsList, t)
		}
	}
	if config.Config.KGGetNodeEnabled {
		if t, ok := toolcore.GetNBTool(l.accountId, tools.ToolKGGetNode); ok {
			toolsList = append(toolsList, t)
		}
	}
	return toolsList
}

func (l ServiceDependencyGraphAgentV2) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Resource Discovery:** If the user provides a partial or ambiguous resource name, use the `resource_search` tool to find the correct resource name.",
		"**Dependency & Topology:** Use `kg_traverse` for dependency chains, CALLS relationships, hosting topology, connectivity (K8s and cloud). Use `kg_search_nodes` for discovery (finding what exists by name/type/namespace/source).",
		prompts_repo.GetPrompt(prompts_repo.PromptAgentKgUsage),
	}
	if config.Config.KGGetNodeEnabled {
		instructions = append(instructions,
			"**Drill-Down:** After kg_search_nodes or kg_traverse returns an interesting ID, call kg_get_node to retrieve full per-node detail (properties, labels, source) without re-querying.",
		)
	}

	constraints := []string{
		"Always specify namespace when available.",
	}

	toolUsage := map[string][]string{
		ResourceSearchAgentName: {
			"Use this tool for fuzzy resource matching when resources are not found.",
			"Input: JSON with search_type ('fuzzy', 'suggestions', 'namespace'), resource_name, resource_type, namespace",
			"Output: suggestions and search strategies",
		},
		tools.ToolKGSearchNodes: {
			"Search the KG to find resources by name, type, namespace, source, or labels (covers K8s and cloud).",
			`Input: {"query":"redis%","node_types":["Workload"],"namespace":"prod"}`,
			"Output: matching nodes with IDs (chain into kg_traverse)",
		},
		tools.ToolKGTraverse: {
			"Traverse the KG to explore dependencies, hosting, connectivity, CALLS chains, cloud routing.",
			`Input: {"query":"llm-server","direction":"downstream","max_depth":1,"relationship_types":["CALLS"]}`,
			"Output: nodes and edges (relationships) in the subgraph",
		},
	}
	if config.Config.KGGetNodeEnabled {
		toolUsage[tools.ToolKGGetNode] = []string{
			"Fetch full per-node detail (properties, labels, source, category) by node ID.",
			`Input: {"node_id":"<uuid>"}`,
			"Output: enriched node payload — chain after kg_search_nodes/kg_traverse.",
		}
	}

	examples := []core.NBAgentPromptExample{
		{
			Question:    "What services does payment-service call?",
			Answer:      `kg_traverse(query:"payment-service", direction:"downstream", relationship_types:["CALLS"])`,
			Explanation: "KG traverse for CALLS edges (static topology)",
		},
		{
			Question:    "Find all databases in the prod namespace",
			Answer:      `kg_search_nodes(query:"", node_types:["Database"], namespace:"prod")`,
			Explanation: "KG search for discovery by type and namespace",
		},
		{
			Question:    "Find all RDS databases in our AWS account",
			Answer:      `kg_search_nodes(query:"", node_types:["Database"], source:"aws")`,
			Explanation: "Cloud-side discovery — KG covers aws/gcp/azure via the source filter",
		},
		{
			Question:    "Which workloads does the api-server load balancer route to?",
			Answer:      `kg_traverse(query:"api-server", node_types:["LoadBalancer"], direction:"both")`,
			Explanation: "Load-balancer routing — bidirectional traverse",
		},
		{
			Question:    "Which namespace and cluster host the llm-server workload?",
			Answer:      `kg_traverse(query:"llm-server", direction:"downstream", relationship_types:["RUNS_ON"])`,
			Explanation: "Hosting topology via RUNS_ON edges",
		},
		{
			Question:    "What is the ingress path to workload app-dev?",
			Answer:      `kg_traverse(node_id:"<workload-uuid>", direction:"upstream", max_depth:1, relationship_types:["EXPOSES"])`,
			Explanation: "Start narrow: find the K8sService(s) that EXPOSE the workload. Then, from each K8sService, kg_traverse upstream with relationship_types:[\"ROUTES_TO_SERVICE\",\"ROUTES_TO_BACKEND\"] for the Ingress / LoadBalancer hop. Only fall back to direction:upstream, max_depth:3 (no filter) if step 1 returns nothing useful.",
		},
	}
	if config.Config.KGGetNodeEnabled {
		examples = append(examples,
			core.NBAgentPromptExample{
				Question:    "Show me the full properties of node 1af1b05d-38b2-5a01-b644-32077e5028e5",
				Answer:      `kg_get_node(node_id:"1af1b05d-38b2-5a01-b644-32077e5028e5")`,
				Explanation: "Drill-down: kg_get_node fetches the full KgNode payload (properties, labels) by ID",
			},
		)
	}

	return core.NBAgentPrompt{
		Role:         "a knowledgeable and concise infrastructure and dependency expert covering Kubernetes and cloud (AWS/GCP/Azure) resources, acting as an SRE",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
	}
}

func (l ServiceDependencyGraphAgentV2) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// GetCacheScope places the system prompt — including the embedded agent_kg_usage.txt
// guidance — in the per-account cached prefix (12h TTL). The 4KB embed is paid once
// per account per cache window, not per ReAct iteration.
func (l ServiceDependencyGraphAgentV2) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}

// Compile-time assertion that V2 opts out of default-tool injection.
var _ core.DefaultToolsOptOut = ServiceDependencyGraphAgentV2{}

// OptOutDefaultTools declines the planner's automatic default-tool injection
// (shell_execute, load_skills). This agent is deliberately KG-only — its tool set
// is curated in GetSupportedTools (kg_search_nodes, kg_traverse, kg_get_node,
// resource_search). shell_execute is out of scope here and was observed driving
// spurious no-op shell calls; load_skills has no KB role for topology questions.
func (l ServiceDependencyGraphAgentV2) OptOutDefaultTools() bool {
	return true
}
