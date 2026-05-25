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

	toolDescription := `Explores service dependencies, infrastructure topology, connectivity, and call chains across Kubernetes and cloud (AWS/GCP/Azure) resources via the Knowledge Graph. ` +
		`Handles: what does X call/depend on, what calls X, what namespace/cluster hosts X, ` +
		`infrastructure discovery (find workloads/databases/services/cloud resources), load balancer routing, ` +
		`VPC/subnet topology. Use for ALL dependency, topology, and connectivity questions. ` +
		`` +
		`CRITICAL — Preserve scope context in the command you send: ` +
		`When the user's question names an account (e.g. "in aws-prod", "Account k8s-dev"), a namespace (e.g. "in the nudgebee namespace"), a cluster, or a cloud source, you MUST include those exact identifiers verbatim in the natural-language ` + "`command`" + ` you pass to this tool. ` +
		`Do NOT strip scoping qualifiers when summarizing the user's question — stripping them forces this tool to either guess or ask a clarifying question, which wastes a turn. ` +
		`Example: User says "What does hasura in the nudgebee namespace call? Account k8s-dev." → Correct command: "What does hasura in the nudgebee namespace call in account k8s-dev?" (keeps both scope qualifiers). Wrong command: "What does hasura in the nudgebee namespace call?" (drops the account — tool will then ask "which account?" because hasura exists in multiple). ` +
		`` +
		`CRITICAL — Handling this tool's responses: ` +
		`(1) If the response asks the user a clarifying question (e.g. "Which account should I analyze?", "Which namespace?", "Multiple matches found — which one?"), STOP IMMEDIATELY. Return that exact question to the user as your final answer. Do NOT call this tool again to "investigate all options" — that defeats the purpose of asking. Do NOT pick a default and proceed. Wait for the user's next turn. ` +
		`(2) Trust the response. If this tool gave you a complete answer about dependencies/topology/connectivity, do NOT call kubectl, fetch_logs, resource_search, or aws to "verify" or "double-check" — those tools cover different concerns (runtime state, logs, cloud CLI) and will not add KG topology data. ` +
		`(3) Cite the tool's evidence in your final answer. Do NOT add connections, hubs, or relationships that this tool did not explicitly return. If the tool's data shows a service has no inbound CALLS, state that — do not invent a "hub" role for it.`

	toolInput := "Provide question in natural language to get the dependencies of the service or cloud resource"
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
		"**Start narrow, broaden iteratively:** First call kg_traverse with max_depth=1 and the tightest sensible relationship_types filter for the question. Increase max_depth (cap 3) or drop filters ONLY if the immediate neighborhood is clearly insufficient. A `direction:both, max_depth:3` walk on a busy workload returns 5–10× more data than a typed depth=1 query, almost all of it irrelevant.",
		"**Truncation:** If kg_traverse returns truncated:true, refine the query (add namespace/node_types, reduce max_depth) before reporting results.",
		prompts_repo.GetPrompt(prompts_repo.PromptAgentKgUsage),
	}
	if config.Config.KGGetNodeEnabled {
		instructions = append(instructions,
			"**Drill-Down:** After kg_search_nodes or kg_traverse returns an interesting ID, call kg_get_node to retrieve full per-node detail (properties, labels, source) without re-querying.",
		)
	}

	constraints := []string{
		"Always specify namespace when available.",
		"Prefer specific relationship_types when the question implies an edge type (CALLS, RUNS_ON, EXPOSES, ROUTES_TO_*, PULLS_FROM, etc.). The filter runs inside the database query, so it cuts both server cost and response size. Omit only after a narrow query proves too restrictive.",
		"CALLS edges may terminate at any cloud-resource node type (Database, Storage, Cache, MessageQueue, LoadBalancer, APIGateway, CDN, ServerlessFunction) — not just Service/Workload/ExternalService. Treat these as data-dependencies. When the edge carries an `original_hostname` property, cite that hostname as the breadcrumb explaining how the workload reaches the resource.",
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
