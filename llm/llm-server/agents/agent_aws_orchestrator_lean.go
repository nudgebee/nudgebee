package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/security"
	tocore "nudgebee/llm/tools/core"
)

// AgentAwsOrchestratorLeanName is the EXPERIMENTAL, @-invocable lean eval handle for AWS
// (mirrors @k8s_orchestrator_lean). The router never selects it; invoke via
// @aws_orchestrator_lean and A/B against @aws_orchestrator_2 on the same query. When
// AwsOrchestratorMode is "lean", the router-selected primary runs the lean agent under
// AgentAwsOrchestratorName so routing/history/@aws_debug are unchanged — only the prompt
// differs. Deferred-then-added so the aws/gcp/azure lean loop-shape can be A/B'd against
// the k8s efficiency work; enable via the mode flag only after the benchmark confirms it.
const AgentAwsOrchestratorLeanName = "aws_orchestrator_lean"

func init() {
	core.RegisterNBAgentFactoryWithAliases(AgentAwsOrchestratorLeanName, func(accountId string) (core.NBAgent, error) {
		return newAwsLeanAgentNamed(accountId, AgentAwsOrchestratorLeanName), nil
	}, "aws_debug_lean")
}

// AwsLeanAgent is a deliberately minimal AWS orchestrator: same direct tool set as the
// direct (v2) orchestrator (aws_execute + aws_observability + the shared specialists,
// with the `aws` sub-agent dropped), but a short principle-level prompt (agent_aws_lean.txt).
// Everything else — including the answer critique — runs through the same ReAct3 planner
// under the standard gates, so this isolates the PROMPT as the single variable.
type AwsLeanAgent struct {
	accountId string
	// name is the handle this instance runs under: AgentAwsOrchestratorLeanName for the
	// always-lean eval handle, or AgentAwsOrchestratorName when the primary runs lean.
	// Distinct name → distinct cache key.
	name string
}

func newAwsLeanAgentNamed(accountId, name string) *AwsLeanAgent {
	return &AwsLeanAgent{accountId: accountId, name: name}
}

func (l *AwsLeanAgent) GetName() string { return l.name }

func (l *AwsLeanAgent) GetNameAliases() []string {
	// Under the primary name it must answer to the primary's aliases so stored history
	// and @aws_debug invocations keep resolving.
	if l.name == AgentAwsOrchestratorName {
		return []string{"aws debug", "amazon_aws_debug", "aws_debug"}
	}
	return []string{"aws_debug_lean"}
}

func (l *AwsLeanAgent) GetDescription() string {
	return `Experimental lean-loop AWS SRE/DevOps troubleshooting agent: minimal principle-level prompt, direct aws_execute. For eval only.`
}

func (l *AwsLeanAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeOrchestrating
}

func (l *AwsLeanAgent) GetModelCategory() core.ModelTier { return core.ModelTierReasoning }
func (l *AwsLeanAgent) GetCacheScope() core.CacheScope   { return core.CacheScopeAccount }
func (l *AwsLeanAgent) IsWatchCapable() bool             { return true }

// NB: no CritiqueEnabled() method on purpose — like the heavy/direct AWS orchestrator, the
// lean agent does not implement NBAgentReActPlannerCritiqueSupport, so critique is governed
// by the standard gate (LlmServerReActCritiqueEnabled && top-level && investigation).

func (l *AwsLeanAgent) GetSupportedTools(ctx *security.RequestContext) []tocore.NBTool {
	// Same direct (v2) tool set: aws_execute + aws_observability + the shared specialists,
	// with the `aws` sub-agent dropped. Distinct name → distinct tool cache key.
	return getAwsPlannerSupportedTools(ctx, l.accountId, l.GetName(), true)
}

func (l *AwsLeanAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText := prompts_repo.GetPrompt(prompts_repo.PromptAgentAwsLean)
	if n := memoryNudgeIfEnabled(); n != "" {
		promptText += "\n\n" + n
	}
	return core.ParsePromptToNBAgentPrompt(promptText)
}
