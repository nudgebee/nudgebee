// KG-only implementation of the service_dependency_graph agent.
//
// The legacy V1 implementation (a mixed runtime-metrics + KG agent that lived
// in agent_service_dependency.go) was removed when V2 became the default and
// its runtime-metrics tool (service_dependency_graph_execute) went unused for
// the full 7-day observation window (see project_tool_reliability memo dated
// 2026-07-03). This file retains its _V2 suffix to preserve git blame; do not
// treat it as a versioned variant.
package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// ServiceDependencyGraph is the registered agent + tool name. Kept stable at
// "service_dependency_graph" (not versioned) so parent prompts and callers
// don't need to change across implementation swaps. The constant used to live
// in the deleted V1 file; consolidating here removes the last cross-file
// dependency on that file.
const ServiceDependencyGraph = "service_dependency_graph"

func init() {
	toolDescription := `Explores service dependencies, topology, connectivity, and call chains across Kubernetes and cloud (AWS/GCP/Azure) via the Knowledge Graph — what calls/depends on X, what X calls, what namespace/cluster hosts X, resource discovery (workloads/databases/services/cloud resources), load-balancer routing, VPC/subnet topology. Use for ALL dependency, topology, and connectivity questions. ` +
		`How to call it: ` +
		`(A) Preserve scope — copy any account, namespace, cluster, or cloud source the user named verbatim into the plain-language ` + "`command`" + ` (e.g. "what does webapp in the nudgebee namespace call in account k8s-dev?"); omitting them forces a clarifying question. ` +
		`(B) State intent, not mechanics — send the goal in plain language; never pre-decompose into node IDs, node types, or graph traversal (the tool resolves those itself). ` +
		`(C) If the reply is a clarifying question, STOP and return it to the user verbatim — do not re-call the tool to investigate options or pick a default. ` +
		`(D) Trust the reply — do not re-verify its topology with kubectl, aws, fetch_logs, or resource_search_execute (they carry no KG topology). ` +
		`(E) Cite the reply's evidence; never add connections or hubs it did not return (if a service has no inbound CALLS, say so).`

	toolInput := "A plain-language question describing what you want to know about dependencies/topology/connectivity (e.g. \"what does llm-server in the nudgebee namespace call?\"). State intent only — do NOT mention node IDs, node types, or graph traversal."
	toolOutput := "The tool will return the output of the question"

	core.RegisterNBAgentFactoryAndTool(ServiceDependencyGraph, func(accountId string) (core.NBAgent, error) {
		return newServiceDependencyGraphAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func newServiceDependencyGraphAgent(accountId string) ServiceDependencyGraphAgent {
	return ServiceDependencyGraphAgent{
		accountId: accountId,
	}
}

type ServiceDependencyGraphAgent struct {
	accountId string
}

func (l ServiceDependencyGraphAgent) GetName() string {
	return ServiceDependencyGraph
}

func (l ServiceDependencyGraphAgent) GetNameAliases() []string {
	return []string{"Service Dependency Graph", "Knowledge Graph", "KG"}
}

func (l ServiceDependencyGraphAgent) GetDescription() string {
	return `Identifies service and cloud-resource dependencies via the Knowledge Graph (K8s + AWS/GCP/Azure)`
}

func (l ServiceDependencyGraphAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolsList := []toolcore.NBTool{}
	if rs, ok := toolcore.GetNBTool(l.accountId, tools.ToolResourceSearch); ok {
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

func (l ServiceDependencyGraphAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Resource Discovery:** If the user provides a partial or ambiguous resource name, use the `resource_search_execute` tool to find the correct resource name.",
		"**Dependency & Topology:** Use `kg_traverse` for dependency chains, CALLS relationships, hosting topology, connectivity (K8s and cloud). Use `kg_search_nodes` for discovery (finding what exists by name/type/namespace/source).",
		prompts.GetPrompt(ctx.GetContext(), prompts.PromptKgUsage, ""),
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
		tools.ToolResourceSearch: resourceSearchToolUsage,
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

func (l ServiceDependencyGraphAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// GetCacheScope places the system prompt — including the embedded agent_kg_usage.txt
// guidance — in the per-account cached prefix (12h TTL). The 4KB embed is paid once
// per account per cache window, not per ReAct iteration.
func (l ServiceDependencyGraphAgent) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}

// Compile-time assertion that V2 opts out of default-tool injection.
var _ core.DefaultToolsOptOut = ServiceDependencyGraphAgent{}

// OptOutDefaultTools declines the planner's automatic default-tool injection
// (shell_execute, load_skills). This agent is deliberately KG-only — its tool set
// is curated in GetSupportedTools (kg_search_nodes, kg_traverse, kg_get_node,
// resource_search_execute). shell_execute is out of scope here and was observed driving
// spurious no-op shell calls; load_skills has no KB role for topology questions.
func (l ServiceDependencyGraphAgent) OptOutDefaultTools() bool {
	return true
}
