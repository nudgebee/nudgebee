package agents

import (
	"slices"
	"strings"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/events"
	llmruntime "nudgebee/llm/llms/runtime"
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
	return []string{"Remediation Planner & Executor", "Remediation", "AutoFix", "Fixer"}
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
	// remediationModeAlertPlan: an alert conversation in a tenant with the loop on (#40734). The alert has a stored
	// plan whose every step is decided and recorded once, from any surface, so here the agent works on that
	// plan (read, propose a different one, act on a step) and has no tool that runs a command outside it.
	remediationModeAlertPlan
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
	if !events.RemediationLoopOn(request.AccountId) {
		return remediationModeInvestigation
	}
	if _, alert := events.AlertFingerprintOfSession(requestSession(request)); alert {
		return remediationModeAlertPlan
	}
	return remediationModeInvestigation
}

// requestSession is the session of the conversation request belongs to. A var so tests can stub it.
var requestSession = func(request core.NBAgentRequest) string {
	if request.SessionId != "" || request.ConversationId == "" {
		return request.SessionId
	}
	dao := core.GetConversationDao()
	if dao == nil {
		return ""
	}
	conversation, err := dao.GetConversation(request.ConversationId)
	if err != nil {
		return ""
	}
	return conversation.SessionID
}

// remediationAlertPlanTools: tools in an alert conversation with the loop on; remediationAlertCheckTools: in its
// read-only follow-up check. Both add alertCliTool; the check only kubectl, limited to strict reads (NBQueryConfig.StrictReadOnly).
var (
	remediationAlertPlanTools  = []string{toolcore.ToolGetRemediationPlan, toolcore.ToolProposeFix, toolcore.ToolRunStep}
	remediationAlertCheckTools = []string{toolcore.ToolGetRemediationPlan, toolcore.ToolProposeFix, toolcore.ToolScheduleRecheck,
		tools.ToolQueryPrometheus, tools.ToolGetEventById, tools.ToolGetEventRules}
	// remediationAlertPlanningTools: in NudgeBee's background planning turn (NBQueryConfig.RemediationPlanTurn), the
	// check's reads and propose_fix; no re-check, which belongs to a follow-up.
	remediationAlertPlanningTools = []string{toolcore.ToolGetRemediationPlan, toolcore.ToolProposeFix,
		tools.ToolQueryPrometheus, tools.ToolGetEventById, tools.ToolGetEventRules}
)

// readOnlyTurn reports whether request's turn may only read: a follow-up check, or an automatic investigation.
func readOnlyTurn(ctx *security.RequestContext, request core.NBAgentRequest) bool {
	return core.IsReadOnlyRun(ctx) || request.QueryConfig.ReadOnlyRun
}

// alertAccountProvider looks up the account's cloud. A var so tests can stub it.
var alertAccountProvider = tools.GetCloudProviderForAccount

// alertCliTool is the account's own CLI tool: kubectl_execute on a Kubernetes account, else its cloud's
// (aws_execute, azure_execute, gcloud_execute); false for an account with neither.
func alertCliTool(accountId string) (toolcore.NBTool, bool) {
	name := map[string]string{"k8s": tools.ToolExecuteKubectlCommand, "aws": tools.ToolExecuteAwsCliCommand,
		"azure": tools.ToolExecuteAzureCliCommand, "gcp": tools.ToolExecuteGcpCliCommand}[strings.ToLower(alertAccountProvider(accountId))]
	if name == "" {
		return nil, false
	}
	return toolcore.GetNBTool(accountId, name)
}

// withAlertPlanTools adds the alert's plan tools to an orchestrator's for a tenant with the remediation loop
// on, so a fix asked for in an alert's chat can run as the plan's step (run_step), recorded once. Each refuses
// outside an alert's conversation. A read-only run (the automatic investigation) gets none.
func withAlertPlanTools(ctx *security.RequestContext, accountId string, supported []toolcore.NBTool) []toolcore.NBTool {
	if !events.RemediationLoopOn(accountId) || core.IsReadOnlyRun(ctx) {
		return supported
	}
	return appendTools(accountId, supported, remediationAlertPlanTools...)
}

// appendTools appends the registered tools named that supported does not have yet.
func appendTools(accountId string, supported []toolcore.NBTool, names ...string) []toolcore.NBTool {
	for _, name := range names {
		if slices.ContainsFunc(supported, func(t toolcore.NBTool) bool { return t.Name() == name }) {
			continue
		}
		if tool, ok := toolcore.GetNBTool(accountId, name); ok {
			supported = append(supported, tool)
		}
	}
	return supported
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
	switch resolveRemediationMode(query) {
	case remediationModeRecommendation:
		return remediationRecommendationPrompt()
	case remediationModeAlertPlan:
		if readOnlyTurn(ctx, query) && query.QueryConfig.RemediationPlanTurn != "" {
			return remediationAlertPlanningPrompt()
		}
		if readOnlyTurn(ctx, query) {
			return remediationAlertCheckPrompt()
		}
		return remediationAlertPlanPrompt()
	}
	return remediationInvestigationPrompt()
}

// remediationAlertPlanPrompt is the prompt of remediationModeAlertPlan.
func remediationAlertPlanPrompt() core.NBAgentPrompt {
	return core.NBAgentPrompt{
		Role: "A Remediation Assistant working on this alert's remediation plan with the person in the conversation",
		Instructions: []string{
			"This alert has a remediation plan that NudgeBee keeps: each step is run, declined or marked done once, from the remediation panel, the alert's Slack thread or this conversation, and every decision is recorded on the alert.",
			"Start by reading the plan with `get_remediation_plan`: its steps, what was decided about each, and what NudgeBee's follow-up checks found after each run.",
			"When the person asks to apply, roll back, skip or mark done a step, call `run_step` for that step with the plan_version and the step's command as the plan shows them. They confirm the call before it acts. Report the result, then say what comes next: the next step, a check, or a different plan.",
			"When the person asks for a different fix, or the plan's steps did not work, write the plan yourself with `propose_fix`: one runnable command per step on this alert's own account, with how to check it and how to undo it. It replaces the current plan, and every command still needs a person's confirmation.",
			"When a tool refuses, read its reason: it says what to correct or what a person must decide. Do not repeat a refused call unchanged.",
			remediationNeverClaimLine,
		},
		Constraints: []string{
			"Act on a step only when the person asked for that step; never run steps on your own initiative",
			"Use past tense only for what run_step reported as run",
		},
		OutputFormat: "Short, in markdown: what was decided or proposed (each step once, with its command once), what the commands printed when they ran, and the next step. No long paragraphs.",
	}
}

// remediationAlertCheckPrompt is the prompt of a read-only turn in remediationModeAlertPlan: NudgeBee's
// follow-up check after a run of the alert's plan, with no person in the turn.
func remediationAlertCheckPrompt() core.NBAgentPrompt {
	return core.NBAgentPrompt{
		Role: "A Remediation Assistant checking one alert after a remediation step, reading only",
		Instructions: []string{
			"You can only read: one read-only command at a time on the alert's own cluster with kubectl_execute, without a cluster argument (on a cloud account there is no CLI: read its metrics and events); prometheus_execute, get_event_by_id, get_event_rules and get_remediation_plan; and propose_fix and schedule_recheck. No person can answer during your turn.",
			"The message quotes the step that ran and what it printed. Read the plan (get_remediation_plan), then find out whether the problem the alert reports is gone: the alert itself (get_event_by_id) and the workload or resource. Judge it by the signal that fired the alert, over a window that starts after the step. Prefer a few targeted reads, and stop once the evidence settles it.",
			"Alert sources close alerts on their own schedule (the Kubernetes collector waits until every pod has been ready for 10 minutes, 60 after an out-of-memory kill), so a healthy workload with the alert still open is not proof the fix failed; say which you observed.",
			"Then do one of these: it is gone, or healthy and not yet closed: say so with the evidence and propose nothing. The plan has steps still to run that explain what you find: say which step a person should run next and why. It persists, you know why and you are confident in a different, safe fix: write that plan and call propose_fix once (one command per step on this alert's account, with a read that checks it and a command that undoes it; order dependent steps with after). Too early to tell: call schedule_recheck with the minutes to wait and what the next check should confirm. Otherwise report what a person needs to decide.",
			"When a tool refuses or fails, read its reason, correct the call or try another read, and say what you could not check. Do not repeat a refused call unchanged.",
		},
		Constraints: []string{
			"Change nothing: a person decides each step in the remediation panel, the alert's Slack thread or this conversation",
		},
		OutputFormat: "Short, in markdown, read on the web and in the alert's Slack thread: **Status:** one line (fixed, not fixed yet, or unclear, and why). **Evidence:** up to four bullets, each a read and what it showed. **Uncertain:** one line, only when something is. **Next:** one line (the step to run next, the plan you proposed, a re-check, or what a person decides). No other headings, no long paragraphs.",
	}
}

// remediationAlertPlanningPrompt is the prompt of NudgeBee's background planning turn in remediationModeAlertPlan
// (#40734): read-only, no person in the turn, it writes the alert's plan with propose_fix. Its own prompt, because
// the follow-up check's is about judging a step that ran and proposes only when a fix did not hold.
func remediationAlertPlanningPrompt() core.NBAgentPrompt {
	return core.NBAgentPrompt{
		Role: "A Remediation Assistant writing one alert's remediation plan, reading only",
		Instructions: []string{
			"You write this alert's remediation plan for the person who will act on it. No person can answer during your turn. You can read (kubectl_execute with one strict read at a time on the alert's own cluster, without a cluster argument; prometheus_execute, get_event_by_id, get_event_rules, get_remediation_plan) and write the plan with propose_fix.",
			"The message says what to write and gives the investigation of the alert's current firing and what was already tried. Plan from it; read only what you need to make each command exact (names, namespaces, current values).",
			"Call propose_fix with the plan: each step one command on this alert's own account, with a read that checks it and a command that undoes it, or, when the durable fix is a change in code or a deployment source, a step with a required_change (with its repo when known); order dependent steps with after. Include cause_key {\"what\", \"how\", \"against\"}: what failed (kind and name), how it failed in a word or two (oom, crashloop, timeout), and against what (the limit, dependency or change it hit), without numbers; leave a field empty when the investigation does not establish it. Include goal: one plain sentence saying when the alert counts as fixed, in terms of the signal that fired it.",
			"When nothing a command can do would help, call propose_fix with an empty actions list and say why in the summary.",
			"When propose_fix refuses, read its reason: it says what to correct, and may show the live state of what your plan changes. Correct the plan and call it again; repeat a call unchanged only when the refusal says to.",
		},
		Constraints: []string{
			"Change nothing: a person decides each step in the remediation panel, the alert's Slack thread or its chat",
		},
		OutputFormat: "One or two lines: the plan you wrote (its cause and first step), or why there is none.",
	}
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

	// The alert's tools, for a tenant with the remediation loop on (#40734): the follow-up check's in a
	// read-only run, else the plan's. GetSupportedToolsForRequest narrows the list to the request's mode.
	if events.RemediationLoopOn(r.accountId) {
		if cli, ok := alertCliTool(r.accountId); ok {
			supportedTools = append(supportedTools, cli)
		}
		names := remediationAlertPlanTools
		if core.IsReadOnlyRun(ctx) {
			names = remediationAlertCheckTools
		}
		supportedTools = appendTools(r.accountId, supportedTools, names...)
	}
	return supportedTools
}

// GetSupportedToolsForRequest implements core.NBAgentRequestAwareToolProvider:
// recommendation-resolution requests get no execution tool — plans go through
// the review-and-apply flow instead. The planner advertises the reduced set
// and the dispatch-time auth check rejects remediation_execute even if the
// model emits it anyway.
func (r RemediationAgent) GetSupportedToolsForRequest(ctx *security.RequestContext, request core.NBAgentRequest) []toolcore.NBTool {
	mode := resolveRemediationMode(request)
	if mode == remediationModeAlertPlan {
		names, readOnly := remediationAlertPlanTools, readOnlyTurn(ctx, request)
		if readOnly {
			names = remediationAlertCheckTools
			if request.QueryConfig.RemediationPlanTurn != "" {
				names = remediationAlertPlanningTools
			}
		}
		var out []toolcore.NBTool
		// A read-only turn gets the CLI only when its reads are strict (the follow-up check): nobody vets them.
		// There, only kubectl: cloud CLIs fail closed (tools.readOnlyCheckRefusal).
		if cli, ok := alertCliTool(r.accountId); ok && (!readOnly || request.QueryConfig.StrictReadOnly && cli.Name() == tools.ToolExecuteKubectlCommand) {
			out = append(out, cli)
		}
		for _, name := range names {
			if tool, ok := toolcore.GetNBTool(r.accountId, name); ok {
				out = append(out, tool)
			}
		}
		return out
	}
	supported := r.GetSupportedTools(ctx)
	if mode != remediationModeRecommendation {
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

// CritiqueEnabled: the answer critique is for investigations (a causal chain, no status-only update). With the
// remediation loop on, this agent's answers are a plan, a step's result, or a follow-up check's status report,
// which it rejects; with it off the agent is critiqued as on main.
func (r RemediationAgent) CritiqueEnabled() bool { return !events.RemediationLoopOn(r.accountId) }

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

func (r RemediationAgent) GetModelCategory() llmruntime.ModelTier {
	return llmruntime.ModelTierReasoning
}
