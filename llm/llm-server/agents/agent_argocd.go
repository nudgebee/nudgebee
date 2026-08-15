package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const ArgoCDAgentName = "argocd"

// Phase 3f (#32503): agent registration removed. The argocd agent was a
// 3-tool bundle (argocd_execute + kubectl_execute + github + MCP) with a
// GitOps investigation methodology — but dev DB showed ZERO agent
// invocations in 30d while argocd_execute itself saw ~3 tool calls (via
// k8s_orchestrator's search_tools discovery), so the bundle wasn't adding
// capability, it was DUPLICATING what the orchestrator already has: kubectl
// preloaded, github reachable via search_tools. The short handle `"argocd"`
// resolves to `argocd_execute` via tool alias (see tool_argocd.go init) and
// the argocd-specific safety + sync-status guidance lives on
// `ArgoCDExecuteTool.ToolPrompt()`. Kept the type + minimal NBAgent surface
// for one release so external references still compile; delete after bake.

// ArgoCDAgent is deprecated (Phase 3f #32503). Type kept for compat; runtime
// registration and guidance live on ArgoCDExecuteTool now. Orchestrator
// handles multi-tool combos (kubectl + github) directly.
type ArgoCDAgent struct{}

func (l ArgoCDAgent) GetName() string          { return ArgoCDAgentName }
func (a ArgoCDAgent) GetNameAliases() []string { return []string{"ArgoCD", "Argo CD"} }

func (l ArgoCDAgent) GetDescription() string {
	return `Deprecated (Phase 3f #32503) — use ` + tools.ToolExecuteArgoCDCommand + ` directly.`
}

func (l ArgoCDAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{tools.ArgoCDExecuteTool{}}
}

func (l ArgoCDAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{}
}

func (l ArgoCDAgent) GetPlannerType() core.AgentPlannerType { return core.AgentPlannerTypeReAct }
