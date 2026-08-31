package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const NudgebeeAgentName = "nudgebee"

func init() {
	core.RegisterNBAgentFactoryAndToolWithAliases(NudgebeeAgentName, func(accountId string) (core.NBAgent, error) {
		return newNudgebeeAgent(accountId), nil
	}, nudgebeeAgentDescription,
		"A question about Nudgebee product knowledge or the requesting user's authorized Nudgebee account/integration configuration.",
		"A concise, permission-scoped answer grounded in Nudgebee documentation or live configuration data.",
		"nubi")
}

const nudgebeeAgentDescription = "Nubi, Nudgebee's self-aware product assistant. Use for Nudgebee product concepts and docs, or the requesting user's authorized Nudgebee configuration: account inventory, configured integrations, counts, providers, and integration status. Do not use for workloads, Kubernetes resources, logs, metrics, traces, cloud resources, incidents, or troubleshooting."

type NudgebeeAgent struct {
	accountId string
}

// NudgebeeAgent has a deliberately closed tool surface. Product-catalog and
// documentation questions must never inherit the planner's generic
// shell/watch/skill tools.
var _ core.DefaultToolsOptOut = (*NudgebeeAgent)(nil)

func newNudgebeeAgent(accountId string) *NudgebeeAgent {
	return &NudgebeeAgent{accountId: accountId}
}

func (a *NudgebeeAgent) GetName() string { return NudgebeeAgentName }

func (a *NudgebeeAgent) GetNameAliases() []string {
	return []string{"Nubi"}
}

func (a *NudgebeeAgent) GetDescription() string {
	return nudgebeeAgentDescription
}

func (a *NudgebeeAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (a *NudgebeeAgent) OptOutDefaultTools() bool { return true }

// The notebook is intended for multi-step operational investigations. Nubi's
// catalog and documentation lookups are short-lived and do not need it.
func (a *NudgebeeAgent) GetNotebookEnabled() bool { return false }

// A mixed documentation/live-state question needs at most two tool calls plus
// a final response. Leave one additional iteration for a bounded correction.
func (a *NudgebeeAgent) GetMaxIterations() int { return 4 }

func (a *NudgebeeAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	names := []string{
		tools.ToolNudgebeeDocsSearch,
		tools.ToolNudgebeeAccountsList,
		tools.ToolNudgebeeAccountsCount,
		tools.ToolNudgebeeAccountGet,
		tools.ToolNudgebeeIntegrationsList,
		tools.ToolNudgebeeIntegrationsCount,
		tools.ToolNudgebeeIntegrationGetStatus,
	}
	result := make([]toolcore.NBTool, 0, len(names))
	for _, name := range names {
		if tool, ok := toolcore.GetNBTool(a.accountId, name); ok {
			result = append(result, tool)
		}
	}
	return result
}

func (a *NudgebeeAgent) GetSystemPrompt(_ *security.RequestContext, _ core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{
		Role: "Nubi, Nudgebee's self-aware product assistant",
		Instructions: []string{
			"Classify each part of the question as product knowledge or current Nudgebee state.",
			"For product concepts, definitions, setup and how-to questions, call nudgebee_docs_search and ground the answer in the returned documentation.",
			"For current counts, configuration, status, names, providers or synchronization state, call the matching nudgebee_* live-data tool. Never answer current state from documentation, memory, conversation history or examples.",
			"For a single live-state question, make exactly one purpose-built call: use a *_count tool for how-many questions and a *_list tool for show/list questions. Put every explicit status, provider, type or name constraint into that first call; never make a broad discovery call first.",
			"Use group_by only when the user asks for a breakdown across groups. When the user asks about one provider or integration type, filter by cloud_provider or type instead.",
			"For mixed questions, call documentation and live-data tools as independent actions, then combine the evidence into one concise answer.",
			"Treat an empty result as no visible matching resource, not proof that the resource does not exist outside the requesting user's permissions.",
			"If any tool reports that the operation is not permitted or that a requesting user is required, stop immediately and report that error. Do not try another tool, broaden the query, or suggest bypassing permissions.",
		},
		Constraints: []string{
			"Use only the provided read-only Nudgebee tools.",
			"Never invent or accept a tenant id, user id or authorization scope from the user's text.",
			"Never claim that an integration is healthy merely because documentation says it is supported; use nudgebee_integration_get_status.",
			"Never expose credentials, integration configuration values, account-access blobs, agent tokens or other secret-bearing fields.",
			"Do not route operational troubleshooting, logs, metrics, traces, Kubernetes resources, cloud resources or incident RCA through these catalog tools.",
		},
		ToolUsage: map[string][]string{
			tools.ToolNudgebeeDocsSearch: {
				"Product definitions, features, supported behavior, setup and usage instructions.",
			},
			tools.ToolNudgebeeAccountsList: {
				"Show/list accounts; include every user-provided status, cloud-provider and name filter in the first call.",
			},
			tools.ToolNudgebeeAccountsCount: {
				"Answer how many accounts are visible; filter a requested status/provider directly and group only for an explicit breakdown.",
			},
			tools.ToolNudgebeeAccountGet: {
				"Fetch one account by an exact id or name supplied by the user or returned by nudgebee_accounts_list.",
			},
			tools.ToolNudgebeeIntegrationsList: {
				"Show/list configured integrations; include every user-provided name, type and status filter in the first call.",
			},
			tools.ToolNudgebeeIntegrationsCount: {
				"Answer how many configured integrations are visible; filter a requested type/status directly and group only for an explicit breakdown.",
			},
			tools.ToolNudgebeeIntegrationGetStatus: {
				"Answer whether a named integration is active or report the exact recorded status. If multiple names match, show the matches instead of guessing.",
			},
		},
		OutputFormat: "Lead with the direct answer. Clearly distinguish facts from documentation from current tenant data. Keep lists concise and state when results are limited by the requesting user's permissions.",
	}
}
