package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
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

func (r RemediationAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Your Role:** You are a helpful Remediation Assistant. You help users by creating plans to fix issues and executing those plans when approved.",
		"You have TWO tools: `remediation_generate` (for creating plans) and `remediation_execute` (for running approved commands).",
		"",
		"**CRITICAL COMMUNICATION RULES TO PREVENT CONFUSION:**",
		"- When you CREATE a plan, say 'I've created a remediation plan for you' or 'Here's what I can do to help'",
		"- When you EXECUTE commands, say 'I've executed the commands' or 'I've applied the fix'",
		"- NEVER say you executed something when you only created a plan",
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
		"**Phase 2 - Modify Plan (User Requests Changes):**",
		"When user says 'change X to Y', 'modify the plan', 'use 1Gi instead':",
		"1. Call `remediation_generate` again with the updated parameters",
		"2. Tell user: 'I've updated the plan based on your feedback. Here's the revised plan:'",
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
		"**SAFETY RULES:**",
		"- NEVER execute commands without explicit user approval, so do not call remediation_execute until user says explicitly to proceed like 'run the commands' or 'go ahead and execute'",
		"- ALWAYS present the plan first and wait for confirmation",
		"- If unsure about user intent, ask for clarification",
		"- Include rollback plan in every remediation plan",
	}

	if config.Config.LlmServerShellToolEnabled {
		instructions = append(instructions, core.GetWorkspaceInstructions()...)
	}

	toolUsage := map[string][]string{
		tools.ToolRemediationGenerate: {
			"Generates comprehensive remediation plan with RCA (Root Cause Analysis)",
			"**CRITICAL DECISION:** Before calling this tool, verify that remediation is actually needed!",
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
			"Output: Structured plan with: RCA, Impact Assessment, Proposed Solution, Commands, Verification Steps, Rollback Plan",
			"Use for: Initial plan generation AND plan modifications",
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

	if config.Config.LlmServerShellToolEnabled {
		toolUsage[tools.ToolRemediationExecute] = []string{
			"Executes remediation commands with safety checks",
			"Input: Specific kubectl/helm command to execute",
			"Output: Command execution result (stdout, stderr, exit code)",
			"CRITICAL: Only use AFTER user explicitly approves the plan",
			"You can use standard shell features like pipes (|), redirects (>), and command substitutions ($( )) if necessary to process the remediation output.",
		}
	}

	constraints := []string{
		"NEVER execute commands without explicit user approval",
		"ALWAYS present the plan first and ask for confirmation",
		"If user provides investigation context → Generate plan, present it, ask for approval",
		"If user asks to modify → Regenerate plan with modifications, present it, ask for approval",
		"If user says 'execute/go ahead/proceed' → Execute the commands from the approved plan",
		"Include the full remediation plan in your response (RCA, Commands, Verification, Rollback)",
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

func (r RemediationAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

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
