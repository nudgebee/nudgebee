package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const KubectlAgentName = "kubectl"

// Phase 3d (#32503): agent registration removed. The kubectl agent was a thin
// wrapper — its whole runtime contribution was the log-condenser
// (`filterKubectlLogResponse`), which now lives in agent_k8s_orchestrator.go
// where the k8s orchestrator (lean + native) that actually uses it already
// imports it. The short handle `"kubectl"` continues to resolve to
// `kubectl_execute` via tool alias (see tool_kubectl.go init). Kept the type
// with the same NBAgent surface for one release so any external caller (tests,
// stored history bridges) still compiles; delete after bake.

// KubectlAgent is deprecated (Phase 3d #32503). Runtime registration is gone;
// the log condenser lives on the k8s orchestrator now. Kept for compat.
type KubectlAgent struct{}

func (l KubectlAgent) GetName() string          { return KubectlAgentName }
func (l KubectlAgent) GetNameAliases() []string { return []string{"Kubectl", "K8s", "Kubernetes"} }

func (l KubectlAgent) GetDescription() string {
	return `Deprecated (Phase 3d #32503) — use ` + tools.ToolExecuteKubectlCommand + ` directly.`
}

func (l KubectlAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{tools.KubectlExecuteTool{}}
}

func (l KubectlAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{}
}

func (l KubectlAgent) GetPlannerType() core.AgentPlannerType { return core.AgentPlannerTypeReAct }
