package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	tocore "nudgebee/llm/tools/core"
)

// Lean-only: delegating/direct dropped 2026-08-11 (#32503 Phase 1).
// The AWS orchestrator now runs the lean-loop implementation under the
// primary handle; the always-direct eval handle (aws_orchestrator_2) and
// the direct/delegating-mode branch were removed with the collapse.
const (
	// AgentAwsOrchestratorName is the name for the AWS orchestrator.
	AgentAwsOrchestratorName = "aws_orchestrator"
	// AwsAgentName is retained for one release as a compat shim after the
	// wrapping AWS agent was retired in Phase 3d (#32503). The short handle
	// `"aws"` now resolves to `aws_execute` via tool alias (see
	// tools/tool_cloud_aws.go init).
	AwsAgentName = "aws"
)

func init() {
	// Legacy aliases (`aws_orchestrator_2`, `aws_debug_2`, `aws_orchestrator_lean`,
	// `aws_debug_lean`) kept as registry lookup keys so stored conversation history
	// referencing the pre-collapse eval / lean handles still resolves to the primary.
	// Not surfaced in GetNameAliases so the @-picker isn't polluted.
	core.RegisterNBAgentFactoryWithAliases(AgentAwsOrchestratorName, func(accountId string) (core.NBAgent, error) {
		return newAwsOrchestratorAgent(accountId), nil
	}, "aws_debug", "aws_orchestrator_2", "aws_debug_2", "aws_orchestrator_lean", "aws_debug_lean")
	// The lean-only orchestrator preloads the reduced cloud core and reaches
	// specialists on-demand via search_tools + delegate_agent; its tool set is
	// cached per account and must be invalidated on agent-config or enabled-tool
	// changes so a stale surface is not served after a change.
	core.RegisterAgentCacheInvalidator(func(accountId string, agentName string) {
		if agentName == "" || agentName == AgentAwsOrchestratorName {
			InvalidateAgentSupportedToolsCache(accountId, AgentAwsOrchestratorName)
		}
	})
	tocore.RegisterToolCacheInvalidator(func(accountId string) {
		InvalidateAgentSupportedToolsCache(accountId, AgentAwsOrchestratorName)
	})
}

// AwsOrchestratorAgent is a deliberately minimal AWS orchestrator: the reduced
// cloud core (aws_execute + service_dependency_graph + events + recommendations
// + websearch + delegate_agent + search_tools) plus a short principle-level
// prompt (agent_aws_lean). Every specialist (databases, kubectl, aws_observability,
// other clouds, github, tickets, …) is dropped from context and reached on-demand
// via search_tools + delegate_agent. Everything else — including the answer
// critique — runs through the same ReAct3 planner under the standard gates.
type AwsOrchestratorAgent struct {
	accountId string
}

// newAwsOrchestratorAgent is the router-selected constructor. Lean-only; the
// former mode switch (delegating / direct / lean) was collapsed away in #32503
// Phase 1.
func newAwsOrchestratorAgent(accountId string) core.NBAgent {
	return &AwsOrchestratorAgent{accountId: accountId}
}

func (a *AwsOrchestratorAgent) GetName() string { return AgentAwsOrchestratorName }

func (a *AwsOrchestratorAgent) GetNameAliases() []string {
	return []string{"aws debug", "amazon_aws_debug", "aws_debug"}
}

func (a *AwsOrchestratorAgent) GetDescription() string {
	return `Lean-loop AWS SRE/DevOps troubleshooting orchestrator: minimal principle-level prompt, direct aws_execute, specialists reached on-demand via search_tools + delegate_agent.`
}

func (a *AwsOrchestratorAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeOrchestrating
}

func (a *AwsOrchestratorAgent) GetModelCategory() core.ModelTier { return core.ModelTierReasoning }
func (a *AwsOrchestratorAgent) GetCacheScope() core.CacheScope   { return core.CacheScopeAccount }

// IsWatchCapable: drives action sub-agents whose async outcome completes later,
// so it may register a background watch.
func (a *AwsOrchestratorAgent) IsWatchCapable() bool { return true }

// NB: no CritiqueEnabled() method on purpose — the orchestrator does not
// implement NBAgentReActPlannerCritiqueSupport, so critique is governed by the
// standard gate (LlmServerReActCritiqueEnabled && top-level && investigation).

func (a *AwsOrchestratorAgent) GetSupportedTools(ctx *security.RequestContext) []tocore.NBTool {
	// Reduced cloud core (aws_execute + SDG + events + recommendations + delegate
	// + search_tools); specialists reached on-demand via search_tools.
	return getCloudLeanSupportedTools(ctx, a.accountId, a.GetName(), tools.ToolExecuteAwsCliCommand)
}

func (a *AwsOrchestratorAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText, promptErr := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptAwsLean, query.AccountId)
	if promptErr != nil {
		// Return nothing rather than continue: everything appended below is
		// decoration, so carrying on yields a "system prompt" that is just a memory
		// nudge — worse than empty, because it looks like a prompt. MustResolveAll
		// covers default/v1 at startup; this catches a malformed provider- or
		// version-specific override added later.
		ctx.GetLogger().Error("aws orchestrator: system prompt failed to load", "error", promptErr)
		return core.NBAgentPrompt{}
	}
	if n := memoryNudgeIfEnabled(); n != "" {
		promptText += "\n\n" + n
	}
	return core.ParsePromptToNBAgentPrompt(promptText)
}
