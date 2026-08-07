package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
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
	// Lean reach pattern (parity with azure_lean / gcp_lean / k8s_lean): preload
	// only aws_execute + the small cross-cutting investigation set, drop every
	// specialist (aws_observability, tickets, github, visualizer, dbs, kubectl,
	// workflow, incident_assembly, sub-agents like `aws`). Specialists are
	// reached on-demand via search_tools + delegate_agent — same contract the
	// other three lean orchestrators follow.
	//
	// This replaces the previous shortcut that returned getAwsPlannerSupportedTools(true)
	// (the FULL direct-orchestrator tool set, ~25 tools). That left aws_lean lean
	// in prompt only, not in tool surface — while the other three leans are lean in
	// BOTH. See PR that landed this: aws_lean was the odd one out; consolidating
	// aligns the four leans so a single "reduced core" concept applies everywhere.
	// Distinct name → distinct tool cache key (unchanged).
	return getCloudLeanSupportedTools(ctx, l.accountId, l.GetName(), tools.ToolExecuteAwsCliCommand)
}

func (l *AwsLeanAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText, promptErr := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptAwsLean, query.AccountId)
	if promptErr != nil {
		// Return nothing rather than continue: everything appended below is
		// decoration, so carrying on yields a "system prompt" that is just a memory
		// nudge — worse than empty, because it looks like a prompt. MustResolveAll
		// covers default/v1 at startup; this catches a malformed provider- or
		// version-specific override added later.
		ctx.GetLogger().Error("aws lean: system prompt failed to load", "error", promptErr)
		return core.NBAgentPrompt{}
	}
	if n := memoryNudgeIfEnabled(); n != "" {
		promptText += "\n\n" + n
	}
	return core.ParsePromptToNBAgentPrompt(promptText)
}
