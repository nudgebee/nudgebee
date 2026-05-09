package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
)

const ServiceDependencyGraph = "service_dependency_graph"

func init() {
	// V1 and V2 register under the SAME name ("service_dependency_graph") and are
	// mutually exclusive at process start. When the V2 flag is on, V2 owns the
	// registration; V1 yields. See agent_service_dependency_V2.go.
	if config.Config.ServiceDependencyGraphV2Enabled && config.Config.KGToolsEnabled {
		return
	}

	// This describes the 'service_dependency_graph' agent when it is used as a tool by another agent.
	toolDescription := `Identifies and returns upstream/downstream dependencies and their details for a given Kubernetes service, deployment, statefulset, daemonset, or pod.`

	// Broaden description when KG tools are enabled so parent planner delegates
	// topology/CALLS questions here too (not just runtime metrics).
	if config.Config.KGToolsEnabled {
		toolDescription = `Explores service dependencies, infrastructure topology, connectivity, and call chains. ` +
			`Handles: what does X call/depend on, what calls X, what namespace/cluster hosts X, ` +
			`infrastructure discovery (find workloads/databases/services), load balancer routing, ` +
			`and runtime metrics (latency, error rates, traffic volume) between services. ` +
			`Use for ALL dependency, topology, and connectivity questions.`
	}

	toolInput := "Provide question in natural language to get the dependencies of the service"
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

func (a ServiceDependencyGraphAgent) GetNameAliases() []string {
	return []string{"Service Dependency Graph", "Knowledge Graph", "KG"}
}

func (l ServiceDependencyGraphAgent) GetDescription() string {
	return `Identifies Service Dependencies in K8s Cluster`
}

func (l ServiceDependencyGraphAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolsList := []toolcore.NBTool{tools.ServiceDependencyGraphTool{}}
	if resourceSearchTool, ok := toolcore.GetNBTool(l.accountId, ResourceSearchAgentName); ok {
		toolsList = append(toolsList, resourceSearchTool)
	}
	// KG tools for static topology + CALLS edges (flag-gated).
	// TODO: once ServiceDependencyGraphV2Enabled becomes the default, remove this
	// KG branch — V2 owns the KG path.
	if config.Config.KGToolsEnabled {
		if t, ok := toolcore.GetNBTool(l.accountId, tools.ToolKGSearchNodes); ok {
			toolsList = append(toolsList, t)
		}
		if t, ok := toolcore.GetNBTool(l.accountId, tools.ToolKGTraverse); ok {
			toolsList = append(toolsList, t)
		}
		if config.Config.KGGetNodeEnabled {
			if t, ok := toolcore.GetNBTool(l.accountId, tools.ToolKGGetNode); ok {
				toolsList = append(toolsList, t)
			}
		}
	}
	return toolsList
}

func (l ServiceDependencyGraphAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Runtime Metrics:** Use `service_dependency_graph_execute` when you need latency, error rates, request volume, or P99/P95 metrics between services.",
		"**Resource Discovery:** If the user provides a partial or ambiguous resource name, use the `resource_search` tool to find the correct resource name.",
	}
	constraints := []string{
		"Always specify namespace when available.",
	}
	toolUsage := map[string][]string{
		tools.ToolServiceDependencyGraph: {
			"Use this tool to get runtime service dependency metrics (latency, error rates, traffic).",
			`Input: valid json format as {"name":"<>","namespace":"<>","type":"<>"}`,
			"Output: dependency data with metrics",
		},
		ResourceSearchAgentName: {
			"Use this tool for fuzzy resource matching when resources are not found.",
			"Input: JSON with search_type ('fuzzy', 'suggestions', 'namespace'), resource_name, resource_type, namespace",
			"Output: suggestions and search strategies",
		},
	}
	examples := []core.NBAgentPromptExample{
		{
			Question:    `Get me dependencies of deployment xyz in namespace abc`,
			Answer:      `{"name":"xyz","namespace":"abc","type":"deployment"}`,
			Explanation: "Generated query for service_dependency_graph_execute tool (runtime deps)",
		},
	}

	// Add KG-specific guidance when enabled.
	if config.Config.KGToolsEnabled {
		instructions = append(instructions,
			"**Dependency & Topology (Static):** Use `kg_traverse` for dependency chains, CALLS relationships, hosting topology, connectivity. Use `kg_search_nodes` for discovery (finding what exists by name/type/namespace).",
			"**KG vs SDG Decision:** Use kg_traverse/kg_search_nodes for ALL dependency, topology, and CALLS questions. Use service_dependency_graph_execute ONLY when you need runtime METRICS (latency, error rates, traffic volume).",
			"**Start narrow, broaden iteratively:** First call kg_traverse with max_depth=1 and the tightest sensible relationship_types filter for the question. Increase max_depth (cap 3) or drop filters ONLY if the immediate neighborhood is clearly insufficient. A `direction:both, max_depth:3` walk on a busy workload returns 5–10× more data than a typed depth=1 query, almost all of it irrelevant.",
			"**Truncation:** If kg_traverse returns truncated:true, refine the query (add namespace/node_types, reduce max_depth) before reporting results.",
		)
		if config.Config.KGGetNodeEnabled {
			instructions = append(instructions,
				"**Drill-Down:** After kg_search_nodes or kg_traverse returns an interesting ID, call kg_get_node to retrieve full per-node detail (properties, labels, source) without re-querying.",
			)
		}
		constraints = append(constraints,
			"Prefer specific relationship_types when the question implies an edge type (CALLS, RUNS_ON, EXPOSES, ROUTES_TO_*, PULLS_FROM, etc.). The filter runs inside the database query, so it cuts both server cost and response size. Omit only after a narrow query proves too restrictive.",
		)
		toolUsage[tools.ToolKGSearchNodes] = []string{
			"Search the KG to find resources by name, type, namespace, source, or labels.",
			`Input: {"query":"redis%","node_types":["Workload"],"namespace":"prod"}`,
			"Output: matching nodes with IDs (chain into kg_traverse)",
		}
		toolUsage[tools.ToolKGTraverse] = []string{
			"Traverse the KG to explore dependencies, hosting, connectivity, CALLS chains.",
			`Input: {"query":"llm-server","direction":"downstream","max_depth":1,"relationship_types":["CALLS"]}`,
			"Output: nodes and edges (relationships) in the subgraph",
		}
		if config.Config.KGGetNodeEnabled {
			toolUsage[tools.ToolKGGetNode] = []string{
				"Fetch full per-node detail (properties, labels, source, category) by node ID.",
				`Input: {"node_id":"<uuid>"}`,
				"Output: enriched node payload — chain after kg_search_nodes/kg_traverse.",
			}
		}
		examples = append(examples,
			core.NBAgentPromptExample{
				Question:    "What services does payment-service call?",
				Answer:      `kg_traverse(query:"payment-service", direction:"downstream", relationship_types:["CALLS"])`,
				Explanation: "Uses KG traverse for CALLS edges (static topology, no metrics needed)",
			},
			core.NBAgentPromptExample{
				Question:    "Find all databases in the prod namespace",
				Answer:      `kg_search_nodes(query:"", node_types:["Database"], namespace:"prod")`,
				Explanation: "Uses KG search for discovery by type and namespace",
			},
			core.NBAgentPromptExample{
				Question:    "What is the P99 latency between api-server and redis?",
				Answer:      `{"name":"api-server","namespace":"nudgebee","type":"deployment"}`,
				Explanation: "Uses service_dependency_graph_execute for runtime metrics",
			},
			core.NBAgentPromptExample{
				Question:    "What is the ingress path to workload app-dev?",
				Answer:      `kg_traverse(node_id:"<workload-uuid>", direction:"upstream", max_depth:1, relationship_types:["EXPOSES"])`,
				Explanation: "Start narrow: find the K8sService(s) that EXPOSE the workload. Then, from each K8sService, kg_traverse upstream with relationship_types:[\"ROUTES_TO_SERVICE\",\"ROUTES_TO_BACKEND\"] for the Ingress / LoadBalancer hop. Only fall back to direction:upstream, max_depth:3 (no filter) if step 1 returns nothing useful.",
			},
		)
		if config.Config.KGGetNodeEnabled {
			examples = append(examples,
				core.NBAgentPromptExample{
					Question:    "Show me the full properties of node 1af1b05d-38b2-5a01-b644-32077e5028e5",
					Answer:      `kg_get_node(node_id:"1af1b05d-38b2-5a01-b644-32077e5028e5")`,
					Explanation: "Drill-down: kg_get_node fetches the full KgNode payload (properties, labels) by ID",
				},
			)
		}
	}

	return core.NBAgentPrompt{
		Role:         "a knowledgeable and concise infrastructure and dependency expert, acting as an SRE",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
	}
}

func (l ServiceDependencyGraphAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (l ServiceDependencyGraphAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	if strings.EqualFold(toolRequest.Tool, tools.ToolServiceDependencyGraph) {
		resultsMap := map[string]any{}
		err := common.UnmarshalJson([]byte(toolResponse), &resultsMap)
		if err != nil {
			return toolResponse
		}

		// Clean up the response to reduce context size
		deps2 := make([]any, 0)
		if deps, ok := resultsMap["dependency"].([]any); ok {
			for _, d := range deps {
				if depMap, ok := d.(map[string]any); ok && depMap != nil && depMap["Id"] != nil {
					if idMap, ok := depMap["Id"].(map[string]any); ok && idMap != nil && idMap["kind"] == "ExternalService" && depMap["Instances"] != nil {
						// for external services if there are too many instances.. then skip, not useful from analsysis perspective
						if instanceArray, ok := depMap["Instances"].([]any); ok && len(instanceArray) > 10 {
							continue
						}
					}
				}
				deps2 = append(deps2, d)
			}
		}

		resultsMap["dependency"] = deps2

		cleanedResponse, err := common.MarshalJson(resultsMap)
		if err == nil {
			return string(cleanedResponse)
		}
	}
	return toolResponse
}
