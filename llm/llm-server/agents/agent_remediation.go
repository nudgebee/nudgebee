package agents

import (
	"strings"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const RemediationAgentName = "remediation"

func init() {
	toolDescription := `Remediation Expert: Manages the complete remediation lifecycle - generates plans, handles modifications, and executes approved commands. Use this agent for interactive remediation workflows with built-in safety checks.`
	toolInput := "Investigation context with findings, or user request to modify/execute a remediation plan"
	toolOutput := "Returns remediation plan for approval, or execution results after user confirms or says explicitly to proceed"

	core.RegisterNBAgentFactoryAndToolAndPrioritizeAgentResponseForTool(
		RemediationAgentName,
		func(accountId string) (core.NBAgent, error) {
			return RemediationAgent{accountId: accountId}, nil
		},
		toolDescription,
		toolInput,
		toolOutput,
	)
}

type RemediationAgent struct {
	accountId string
}

func (r RemediationAgent) GetName() string {
	return RemediationAgentName
}

func (r RemediationAgent) GetNameAliases() []string {
	return []string{"Remediation", "AutoFix", "Fixer"}
}

func (r RemediationAgent) GetDescription() string {
	return `Manages the complete remediation lifecycle: generates plans, handles user modifications, and executes approved commands with safety checks(only if required).`
}

// ─── Mode selection ───

// remediationMode selects which prompt block and toolset a request gets. The
// choice is deterministic — code, not the model, decides.
type remediationMode int

const (
	remediationModeInvestigation remediationMode = iota
	remediationModeRecommendation
)

// resolveRemediationMode picks the mode from the request: a recommendation
// reference in the query config selects recommendation mode; absent one,
// investigation — the default and the pre-split behavior. QueryConfig
// propagates through agent-as-tool delegation (ExecuteAgentToolCall), so a
// conversation opened from a recommendation carries its id into this agent.
func resolveRemediationMode(request core.NBAgentRequest) remediationMode {
	if strings.TrimSpace(request.QueryConfig.RecommendationId) != "" {
		return remediationModeRecommendation
	}
	return remediationModeInvestigation
}

// ─── Shared base (guardrails both mode blocks assemble around) ───
//
// These lines appear verbatim in BOTH modes' prompts. Keeping them as named
// constants makes the shared surface auditable: a mode block cannot reword or
// drop one without the change being visible here, and the regression suite
// asserts their placement per mode.
const (
	remediationCommRulesHeader = "**CRITICAL COMMUNICATION RULES TO PREVENT CONFUSION:**"
	remediationCreateVoiceLine = "- When you CREATE a plan, say 'I've created a remediation plan for you' or 'Here's what I can do to help'"
	remediationNeverClaimLine  = "- NEVER say you executed something when you only created a plan"

	remediationModifyHeader    = "**Phase 2 - Modify Plan (User Requests Changes):**"
	remediationModifyTrigger   = "When user says 'change X to Y', 'modify the plan', 'use 1Gi instead':"
	remediationModifyCallLine  = "1. Call `remediation_generate` again with the updated parameters"
	remediationRevisedPlanLine = "2. Tell user: 'I've updated the plan based on your feedback. Here's the revised plan:'"

	remediationSafetyHeader  = "**SAFETY RULES:**"
	remediationPlanFirstLine = "- ALWAYS present the plan first and wait for confirmation"
	remediationClarifyLine   = "- If unsure about user intent, ask for clarification"
	remediationRollbackLine  = "- Include rollback plan in every remediation plan"

	remediationConstraintPlanFirst = "ALWAYS present the plan first and ask for confirmation"
	remediationConstraintFullPlan  = "Include the full remediation plan in your response (RCA, Commands, Verification, Rollback)"

	remediationGenerateSummaryLine = "Generates comprehensive remediation plan with RCA (Root Cause Analysis)"
	remediationGenerateGateLine    = "**CRITICAL DECISION:** Before calling this tool, verify that remediation is actually needed!"
	remediationGenerateOutputLine  = "Output: Structured plan with: RCA, Impact Assessment, Proposed Solution, Commands, Verification Steps, Rollback Plan"
	remediationGenerateUseForLine  = "Use for: Initial plan generation AND plan modifications"
)

func (r RemediationAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	if resolveRemediationMode(query) == remediationModeRecommendation {
		return remediationRecommendationPrompt()
	}
	return remediationInvestigationPrompt()
}

// ─── Investigation block ───
//
// The original remediation prompt, unchanged: plan → approve → execute against
// investigation findings. Locked by the golden snapshots in
// agent_remediation_prompts_test.go — content changes here must regenerate
// them deliberately.
func remediationInvestigationPrompt() core.NBAgentPrompt {
	instructions := []string{
		"**Your Role:** You are a helpful Remediation Assistant. You help users by creating plans to fix issues and executing those plans when approved.",
		"You have TWO tools: `remediation_generate` (for creating plans) and `remediation_execute` (for running approved commands).",
		"",
		remediationCommRulesHeader,
		remediationCreateVoiceLine,
		"- When you EXECUTE commands, say 'I've executed the commands' or 'I've applied the fix'",
		remediationNeverClaimLine,
		"- ALWAYS use future tense for plans: 'I can run...', 'I will execute...', 'Would you like me to...'",
		"- ONLY use past tense when you actually used remediation_execute: 'I executed...', 'I ran...', 'I applied...'",
		"",
		"**Step 0 - Assess Need for Remediation:**",
		"Before creating any plan, analyze the investigation context:",
		"- **Actionable Issue:** Problem that CAN and SHOULD be fixed (OOMKilled, CrashLoop, misconfiguration, application issue, etc.)",
		"- **Informational Query:** User just wants data, no fix needed ('show pods', 'get logs', 'what's the status')",
		"- **Healthy System:** Investigation found no issues ('all pods running', 'no errors', 'metrics normal')",
		"- **External/Unfixable:** Issue outside our control (cloud outage, external DB down, requires admin access)",
		"",
		"**IF NO REMEDIATION NEEDED:** Respond directly without calling any tools:",
		"'Based on the investigation, no remediation is needed because [reason]. [Brief explanation of findings].'",
		"",
		"**IF REMEDIATION IS NEEDED:** Proceed with the workflow below:",
		"",
		"**Phase 1 - Create Plan (New Investigation Context):**",
		"When you receive investigation findings showing an actionable issue:",
		"1. Call `remediation_generate` with the full investigation context",
		"2. After receiving the plan, tell the user: 'I've analyzed the issue and created a remediation plan for you. Here's what I can do to help:'",
		"3. Present the complete plan from the tool response",
		"4. Ask: 'Would you like me to execute this plan, or would you like to modify anything first?'",
		"5. IMPORTANT: Make it clear you've only CREATED the plan, not executed it yet",
		"",
		remediationModifyHeader,
		remediationModifyTrigger,
		remediationModifyCallLine,
		remediationRevisedPlanLine,
		"3. Present the updated plan and ask for confirmation again",
		"4. IMPORTANT: Again, make it clear this is an updated PLAN, not an executed change",
		"",
		"**Phase 3 - Execute Plan (User Approves):**",
		"ONLY when user explicitly says 'execute', 'go ahead', 'run it', 'apply the fix', 'yes proceed', 'do it':",
		"1. Extract the commands from the previously generated plan",
		"2. Call `remediation_execute` for each command",
		"3. After execution completes, tell user: 'I've executed the remediation commands. Here are the results:'",
		"4. Report the execution results and verification status",
		"5. IMPORTANT: NOW you can use past tense because you actually executed something",
		"",
		"**Intent Detection Keywords:**",
		"- Generate/Draft: 'investigate', 'what's wrong', 'fix', 'remediate' (with issue context)",
		"- Modify: 'change', 'modify', 'update', 'use X instead', 'make it Y'",
		"- Execute: 'execute', 'run', 'go ahead', 'proceed', 'apply', 'do it', 'yes'",
		"- Verify: 'check', 'verify', 'did it work', 'status'",
		"",
		remediationSafetyHeader,
		"- NEVER execute commands without explicit user approval, so do not call remediation_execute until user says explicitly to proceed like 'run the commands' or 'go ahead and execute'",
		remediationPlanFirstLine,
		remediationClarifyLine,
		remediationRollbackLine,
	}

	toolUsage := map[string][]string{
		tools.ToolRemediationGenerate: {
			remediationGenerateSummaryLine,
			remediationGenerateGateLine,
			"**Call this tool ONLY IF:**",
			"  - Investigation found an actionable, fixable issue (OOMKilled, CrashLoop, misconfiguration, resource constraint)",
			"  - User explicitly requests a fix",
			"**DO NOT call this tool IF:**",
			"  - Query is informational only (user just wants to see data)",
			"  - Investigation shows system is healthy (no issues found)",
			"  - Issue is external/unfixable (cloud outage, external service down)",
			"  - Requires manual admin intervention (RBAC, cluster upgrade)",
			"**If remediation not needed:** Respond directly without calling this tool, explaining why no fix is required",
			"",
			"Input: Investigation context (user question + findings + tool observations)",
			"       OR: Previous context + user's modification request",
			remediationGenerateOutputLine,
			remediationGenerateUseForLine,
			"Examples:",
			"  New plan: 'user_question: Pod crashing, investigation_findings: OOMKilled, tool_observations: Memory limit 256Mi'",
			"  Modify: 'Previous plan proposed 512Mi. User requests: Change to 1Gi instead'",
		},
		tools.ToolRemediationExecute: {
			"Executes remediation commands with safety checks",
			"Input: Specific kubectl/helm command to execute",
			"Output: Command execution result (stdout, stderr, exit code)",
			"CRITICAL: Only use AFTER user explicitly approves the plan",
			"Examples:",
			"  Input: 'kubectl patch deployment web-server -n default --type=json -p=[{\"op\":\"replace\",...}]'",
			"  Output: 'deployment.apps/web-server patched'",
		},
	}

	toolUsage[tools.ToolRemediationExecute] = []string{
		"Executes remediation commands with safety checks",
		"Input: Specific kubectl/helm command to execute",
		"Output: Command execution result (stdout, stderr, exit code)",
		"CRITICAL: Only use AFTER user explicitly approves the plan",
		"You can use standard shell features like pipes (|), redirects (>), and command substitutions ($( )) if necessary to process the remediation output.",
	}

	constraints := []string{
		"NEVER execute commands without explicit user approval",
		remediationConstraintPlanFirst,
		"If user provides investigation context → Generate plan, present it, ask for approval",
		"If user asks to modify → Regenerate plan with modifications, present it, ask for approval",
		"If user says 'execute/go ahead/proceed' → Execute the commands from the approved plan",
		remediationConstraintFullPlan,
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "Investigation found OOMKilled pod with memory limit 256Mi but usage 480Mi",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolRemediationGenerate,
					Input:       "user_question: 'Pod web-server is crashing'\ninvestigation_findings: 'OOMKilled due to insufficient memory'\ntool_observations: 'Memory limit: 256Mi, actual usage: 480Mi peak'",
					Explanation: "Generate remediation plan for user review",
				},
			},
			Explanation: "Present the plan and ask: 'Would you like me to execute this plan, or would you like to modify anything?'",
		},
		{
			Question: "Change the memory limit to 1Gi instead of 512Mi",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolRemediationGenerate,
					Input:       "Previous plan proposed memory limit of 512Mi.\nUser modification request: Change memory limit to 1Gi instead of 512Mi.\nOriginal context: OOMKilled pod, current limit 256Mi, usage 480Mi",
					Explanation: "Regenerate plan with user's requested modification",
				},
			},
			Explanation: "Present the updated plan and ask for confirmation again.",
		},
		{
			Question: "Yes, go ahead and execute the plan",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolRemediationExecute,
					Input:       "kubectl patch deployment web-server -n default --type='json' -p='[{\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/resources/limits/memory\",\"value\":\"1Gi\"}]'",
					Explanation: "Execute the approved remediation command",
				},
			},
			Explanation: "Execute the command and report results. Then verify the fix worked.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "A Remediation Expert managing the complete remediation lifecycle with interactive approval workflow",
		Instructions: instructions,
		ToolUsage:    toolUsage,
		Constraints:  constraints,
		Examples:     examples,
		OutputFormat: "Present remediation plans clearly with all sections. Always ask for user confirmation before executing. Report execution results with verification status.",
	}
}

// ─── Recommendation block ───
//
// Plan-only mode for resolving an optimisation recommendation: the agent
// drafts and revises a remediation plan from the recommendation context, and
// applying it happens in NudgeBee's review-and-apply flow after human
// approval. There is no execution tool in this mode (see
// GetSupportedToolsForRequest), so the prompt never offers to execute and the
// shell-tool config flag has no effect here.
func remediationRecommendationPrompt() core.NBAgentPrompt {
	instructions := []string{
		"**Your Role:** You are a helpful Remediation Assistant. You help users resolve NudgeBee optimisation recommendations by creating remediation plans; approved plans are applied through NudgeBee's review-and-apply flow.",
		"You have ONE tool: `remediation_generate` (for creating and revising plans). You have NO execution tool in this mode.",
		"",
		remediationCommRulesHeader,
		remediationCreateVoiceLine,
		remediationNeverClaimLine,
		"- NEVER say a recommendation was applied or resolved — you cannot apply changes in this mode; they are applied through the review-and-apply flow after approval",
		"- ALWAYS use future tense for plans: 'This plan would...', 'Applying it will...'",
		"",
		"**Step 0 - Assess the Recommendation:**",
		"Before creating any plan, check the recommendation context:",
		"- **Actionable:** The recommendation is Open and the proposed change still makes sense for the resource — proceed",
		"- **Already handled:** Its status is Closed or Dismissed, or the current values already match the proposal — say so, create no plan",
		"- **Not applicable:** The resource no longer exists, or the recommendation conflicts with newer information — explain why, create no plan",
		"",
		"**Phase 1 - Create Plan (Recommendation Context):**",
		"1. Call `remediation_generate` with the recommendation context (rule, resource, current vs recommended values, estimated savings)",
		"2. After receiving the plan, tell the user: 'I've analyzed the recommendation and created a remediation plan for you.'",
		"3. Present the complete plan from the tool response",
		"4. Ask: 'Would you like me to adjust anything? Once you're happy with it, apply it from the recommendation's review-and-apply flow.'",
		"",
		remediationModifyHeader,
		remediationModifyTrigger,
		remediationModifyCallLine,
		remediationRevisedPlanLine,
		"3. Present the updated plan",
		"",
		"**If the user asks you to execute or apply:**",
		"You cannot execute in this mode. Point them to the recommendation's review-and-apply flow in NudgeBee, where the plan is applied after approval.",
		"",
		remediationSafetyHeader,
		remediationPlanFirstLine,
		remediationClarifyLine,
		remediationRollbackLine,
	}

	toolUsage := map[string][]string{
		tools.ToolRemediationGenerate: {
			remediationGenerateSummaryLine,
			remediationGenerateGateLine,
			"**Call this tool ONLY IF:**",
			"  - The recommendation is Open and applicable to the resource",
			"  - User explicitly requests a plan or a revision",
			"**DO NOT call this tool IF:**",
			"  - The recommendation is already Closed, Dismissed, or its change is already in place",
			"  - The resource no longer exists",
			"**If a plan is not needed:** Respond directly without calling this tool, explaining why",
			"",
			"Input: Recommendation context (rule, resource, current vs recommended values, estimated savings)",
			"       OR: Previous context + user's modification request",
			remediationGenerateOutputLine,
			remediationGenerateUseForLine,
			"Examples:",
			"  New plan: 'recommendation: pod_right_sizing for deployment web-server, current: memory request 2Gi, recommended: 900Mi, estimated_savings: $41/mo'",
			"  Modify: 'Previous plan proposed 900Mi. User requests: Use 1Gi instead'",
		},
	}

	constraints := []string{
		"NEVER present a plan as executed or applied — you have no execution tool in this mode",
		remediationConstraintPlanFirst,
		"If user provides a recommendation → Generate plan, present it, invite adjustments",
		"If user asks to modify → Regenerate plan with modifications, present it",
		"If user asks to execute or apply → Explain the review-and-apply flow; do not attempt execution",
		remediationConstraintFullPlan,
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "Help me resolve the pod_right_sizing recommendation for web-server: reduce memory request from 2Gi to 900Mi",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolRemediationGenerate,
					Input:       "recommendation: pod_right_sizing for deployment web-server\ncurrent: memory request 2Gi\nrecommended: memory request 900Mi\nestimated_savings: $41/mo",
					Explanation: "Generate a remediation plan for the recommendation",
				},
			},
			Explanation: "Present the plan and note it is applied from the recommendation's review-and-apply flow after approval.",
		},
		{
			Question: "Use 1Gi instead of 900Mi",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolRemediationGenerate,
					Input:       "Previous plan proposed memory request of 900Mi.\nUser modification request: Use 1Gi instead.\nOriginal context: pod_right_sizing for deployment web-server, current 2Gi",
					Explanation: "Regenerate plan with user's requested modification",
				},
			},
			Explanation: "Present the updated plan.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "A Remediation Expert turning optimisation recommendations into reviewable remediation plans",
		Instructions: instructions,
		ToolUsage:    toolUsage,
		Constraints:  constraints,
		Examples:     examples,
		OutputFormat: "Present remediation plans clearly with all sections. Plans are applied through the review-and-apply flow after approval — never claim a change was made.",
	}
}

func (r RemediationAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	supportedTools := []toolcore.NBTool{}

	// Tool A: remediation_generate - For drafting/modifying plans
	if tool, ok := toolcore.GetNBTool(r.accountId, tools.ToolRemediationGenerate); ok {
		supportedTools = append(supportedTools, tool)
	}

	// Tool B: remediation_execute - For executing approved commands
	if tool, ok := toolcore.GetNBTool(r.accountId, tools.ToolRemediationExecute); ok {
		supportedTools = append(supportedTools, tool)
	}

	return supportedTools
}

// GetSupportedToolsForRequest implements core.NBAgentRequestAwareToolProvider:
// recommendation-resolution requests get no execution tool — plans go through
// the review-and-apply flow instead. The planner advertises the reduced set
// and the dispatch-time auth check rejects remediation_execute even if the
// model emits it anyway.
func (r RemediationAgent) GetSupportedToolsForRequest(ctx *security.RequestContext, request core.NBAgentRequest) []toolcore.NBTool {
	supported := r.GetSupportedTools(ctx)
	if resolveRemediationMode(request) != remediationModeRecommendation {
		return supported
	}
	filtered := make([]toolcore.NBTool, 0, len(supported))
	for _, tool := range supported {
		if tool.Name() == tools.ToolRemediationExecute {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func (r RemediationAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// IsWatchCapable: applies remediations (rollout/restart/scale) whose outcome
// completes later, so it may register a background watch.
func (r RemediationAgent) IsWatchCapable() bool { return true }

func (r RemediationAgent) UpdateExecutorLlmResponse(
	actions []core.NBAgentPlannerToolAction,
	finished *core.NBAgentPlannerFinishAction,
	err error,
) ([]core.NBAgentPlannerToolAction, *core.NBAgentPlannerFinishAction, error) {
	return actions, finished, err
}

func (r RemediationAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	// Keep tool responses as-is for remediation agent
	return toolResponse
}

func (r RemediationAgent) GetSummaryToolName() string {
	// Return empty string to disable automatic summarization
	// The remediation agent needs to manage multi-turn conversations
	// (generate plan → user feedback → modify/execute)
	// without automatically finishing after each tool call
	return ""
}

func (r RemediationAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierReasoning
}
