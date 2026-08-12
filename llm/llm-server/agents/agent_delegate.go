package agents

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

const DelegateAgentToolName = "delegate_agent"

// defaultDelegateMaxIterations is the default iteration budget for a delegated sub-agent.
const defaultDelegateMaxIterations = 5

// maxDelegateMaxIterations caps the iteration budget to prevent runaway sub-agents.
const maxDelegateMaxIterations = 15

// minDelegateMaxIterations rejects degenerate single-iteration delegations.
// A sub-agent that only gets one tool call doesn't need a ReAct loop — the parent should
// either call the tool directly or skip delegation entirely. Empirically (audit 2026-05-03),
// every max_iterations=1 call we observed was misuse: pre-finish narration or text formatting
// that should have been a plain LLM call.
const minDelegateMaxIterations = 2

// notebookMisusePromptRe rejects prompts that ask the sub-agent to update
// the parent's notebook. The description has forbidden this since PR #32246
// ("DO NOT use to record findings — write to your own notebook with
// <update_notebook>"), but the 2026-06-27 recheck showed 28.6% of post-#32246
// calls still match this shape (8 of 28 — essentially unchanged from the 32%
// pre-rewrite baseline). Same lesson as the think-tool tighten arc:
// description-only doesn't move misuse share; programmatic enforcement does.
//
// Pattern is intentionally narrow: matches when the prompt STARTS with
// "update the notebook" or "updating the notebook" (with the customary
// possessive forms). Every observed misuse case began with this exact
// stem; legitimate investigation prompts open with "Investigate",
// "Check", "Fetch", "Find", "Verify", etc. — clearly different verbs.
// Mid-prompt mentions of "notebook" (e.g., "investigate why the notebook
// update is failing") are not matched.
var notebookMisusePromptRe = regexp.MustCompile(`(?i)^\s*(update|updating)\s+(the\s+|my\s+|our\s+|your\s+)?notebook\b`)

func init() {
	toolcore.RegisterNBToolFactory(DelegateAgentToolName, func(accountId string) (toolcore.NBTool, error) {
		return &delegateAgentTool{accountId: accountId}, nil
	})
}

// delegateAgentTool is a tool that spawns an isolated, dynamically-composed ReAct
// sub-agent at runtime. The parent planner provides a custom system prompt, a subset
// of tools, and an iteration budget. The sub-agent executes in its own scope (own
// planner, own scratchpad) and returns its findings to the parent.
//
// Security: the sub-agent can only use tools that the parent explicitly lists AND
// that are resolvable for the account. There is no privilege escalation path.
type delegateAgentTool struct {
	accountId string
}

func (t *delegateAgentTool) Name() string { return DelegateAgentToolName }

func (t *delegateAgentTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }

func (t *delegateAgentTool) Description() string {
	return `Spawn a scoped specialist sub-investigator with its own prompt, tool list, and iteration budget.
Input: {"prompt": <brief>, "tools": [<tool names>], "max_iterations": <int>} — "tools" is required; omitting it leaves the sub-agent LLM-only, which is almost always misuse. max_iterations defaults to 5, minimum 2, maximum 15.
USE when a specific sub-question needs 3+ tool calls to answer and would otherwise pollute your own scratchpad with serial discovery (e.g. "investigate DNS for service X", "check egress firewall for namespace Y").
DO NOT use when 1-2 tool calls suffice — call the tool directly.
DO NOT use to record findings — write to your own notebook with <update_notebook>. Prompts that start with "update the notebook" / "updating the notebook" are rejected at the tool boundary with an error pointing here.
DO NOT use to format, summarize, or rewrite text — call the LLM tool directly.
DO NOT use as a final-answer preamble.`
}

func (t *delegateAgentTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"prompt": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "Detailed instructions for the sub-agent. Include: what to investigate, methodology, constraints, and what to report back.",
			},
			"tools": {
				Type:        toolcore.ToolSchemaTypeArray,
				Description: "List of tool names the sub-agent should use (e.g., [\"mysql_query\", \"prometheus\"]). Must be tools available in the current account.",
			},
			"max_iterations": {
				Type:        toolcore.ToolSchemaTypeInteger,
				Description: "Maximum tool calls the sub-agent can make. Default: 5, Min: 2, Max: 15. A value of 1 is rejected — single-iteration sub-agents are degenerate; either iterate or don't delegate.",
				Default:     defaultDelegateMaxIterations,
			},
		},
		Required: []string{"prompt"},
	}
}

func (t *delegateAgentTool) Call(ctx toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	// Parse the input — either from Arguments (structured) or from Command (raw JSON string)
	prompt, toolNames, maxIter, err := parseDelegateInput(input)
	if err != nil {
		return toolcore.NBToolResponse{
			Status: toolcore.NBToolResponseStatusError,
			Data:   fmt.Sprintf("Invalid input: %s", err.Error()),
		}, nil
	}

	// Resolve requested tools with security validation. flattenInstructions carries
	// the static guidance of any flattenable agent (helm/redis) whose leaf tools were
	// inlined, so the sub-agent keeps that agent's methodology without the extra hop.
	resolvedTools, flattenInstructions, unresolvedNames := resolveToolsForDelegate(ctx.Ctx, t.accountId, toolNames)
	if len(unresolvedNames) > 0 {
		ctx.Ctx.GetLogger().Warn("delegate_agent: some requested tools could not be resolved",
			"unresolved", unresolvedNames, "resolved_count", len(resolvedTools))
	}
	if len(flattenInstructions) > 0 {
		prompt += "\n\nGuidelines for the specialist tools above:\n- " + strings.Join(flattenInstructions, "\n- ")
	}

	// Prevent recursive delegation — remove delegate_agent from the sub-agent's tool list.
	// Without this, a delegated sub-agent could spawn further delegates, causing unbounded
	// recursion and cost amplification.
	resolvedTools = filterOutTool(resolvedTools, DelegateAgentToolName)

	// Enforce the account's allowed_tools/disabled_tools capability filter on the
	// delegated set. The dispatch auth gate (IsAgentToolAuthorizedToProcessRequest)
	// only checks membership in the sub-agent's supported tools — it does NOT re-apply
	// capabilities — so a disabled or non-allow-listed tool named here would otherwise
	// execute inside the sub-agent, bypassing the account's restriction. Applied before
	// the mandatory LLM tool is appended, so reasoning is never filtered out. No-op when
	// no capabilities are set (the common case).
	resolvedTools = core.FilterTools(resolvedTools, ctx.QueryConfig.Capabilities)

	// Always include the LLM tool so the sub-agent can reason/summarize. The LLM tool
	// is essential for the ReAct planner — fail fast if it cannot be resolved.
	llmTool, llmOk := toolcore.GetNBTool(t.accountId, core.ToolLlm)
	if !llmOk {
		ctx.Ctx.GetLogger().Error("delegate_agent: LLM tool not found for account", "account_id", t.accountId)
		return toolcore.NBToolResponse{
			Status: toolcore.NBToolResponseStatusError,
			Data:   "Internal error: LLM tool not available for this account.",
		}, nil
	}
	hasLlm := false
	for _, rt := range resolvedTools {
		if strings.EqualFold(rt.Name(), core.ToolLlm) {
			hasLlm = true
			break
		}
	}
	if !hasLlm {
		resolvedTools = append(resolvedTools, llmTool)
	}

	// Build the dynamic agent — use a fixed name consistent with other agents.
	// Unique identity comes from the agent_id (UUID) generated by executeAgent.
	dynamicAgent := &dynamicReActAgent{
		name:          DelegateAgentToolName,
		prompt:        prompt,
		tools:         resolvedTools,
		maxIterations: maxIter,
		accountId:     t.accountId,
	}

	ctx.Ctx.GetLogger().Info("delegate_agent: spawning sub-agent",
		"tool_count", len(resolvedTools),
		"max_iterations", maxIter,
		"prompt_len", len(prompt))

	// Execute via the standard agent execution path. Pass the extracted prompt as
	// the sub-agent's Command so the sub-agent's Query is the actual investigative
	// brief, not the raw JSON envelope. The system prompt also embeds the prompt
	// (via dynamicReActAgent.GetSystemPrompt) for explicit instructions.
	subInput := toolcore.NBToolCallRequest{
		Command: prompt,
		Context: input.Context,
	}
	resp, err := core.ExecuteAgentToolCall(ctx, dynamicAgent, subInput)

	// Build additionalDetails ONCE, upfront, so every return path below (including
	// the early err != nil path) carries agent_id. Without agent_id, the parent's
	// tool_call row is saved with child_agent_id = null and the UI cannot recurse
	// into the delegated sub-agent's tool calls — orphaned children get mis-nested
	// under the neighbouring tool. resp.AgentId is set by executeAgent at the end
	// of its normal execution path (executor.go:984, `AgentId: agentId.String()`),
	// so it's populated for terminal success/failure/waiting statuses. Early-error
	// paths (agent lookup failure, terminated conversation, invalid state) return
	// zero-value NBAgentResponse with an empty AgentId — the conditional set below
	// filters those out so we never write an empty string as agent_id.
	//
	// Per-call execution metadata is appended so the parent agent (and downstream
	// debugging/analytics) can see how the sub-agent spent its budget — without it,
	// a vague summary is indistinguishable from a real investigation that just found
	// nothing. iterations_used counts external tool calls only (LLM reasoning steps
	// filtered), to match the "tool call budget" framing the sub-agent's system
	// prompt presents. total_steps retains the raw ReAct step count for debugging.
	// budget_exhausted is surfaced explicitly because iterations_used can be <
	// iteration_budget even when the sub-agent was force-terminated by the step cap
	// (LLM steps count against the cap but not against iterations_used).
	toolsUsed := collectToolsUsed(resp.AgentStepResponse)
	totalSteps := len(resp.AgentStepResponse)
	additionalDetails := map[string]any{
		"iteration_budget": maxIter,
		"iterations_used":  sumHistogram(toolsUsed),
		"total_steps":      totalSteps,
		"budget_exhausted": totalSteps >= maxIter,
	}
	// Only set agent_id when it carries a real value. resp.AgentId is populated
	// upfront by ExecuteAgentToolCall (via SaveConversationAgent) before the
	// sub-agent runs, so it's typically non-empty even on error — but a failure
	// early enough (ctx cancel during the record insert, validation reject) can
	// leave it as "". planner_callback_handler.go:214 already guards `!= ""`
	// before writing child_agent_id, so writing an empty string here would be
	// harmless today; skipping it explicitly makes the wrapper self-consistent
	// (never emit meaningless metadata) and keeps that safety property from
	// depending on the downstream guard staying in place across refactors.
	if resp.AgentId != "" {
		additionalDetails[core.NBToolCallAdditionalDetailsAgentId] = resp.AgentId
	}
	if len(toolsUsed) > 0 {
		additionalDetails["tools_used"] = toolsUsed
	}

	// Build the bounded evidence manifest from the delegated sub-agent's
	// chronological steps. This wrapper bypasses factory_agent's generic path,
	// so without this a delegated sub-agent drops its evidence at the boundary.
	// Keyed by the delegated agent's name so the log line is greppable per target.
	// Built BEFORE the err != nil check so partial steps executed before the
	// failure are still surfaced to the parent — otherwise a mid-run sub-agent
	// crash gives the parent no visibility into what actually ran.
	subAgentEvidence := core.BuildSubAgentEvidenceForTool(ctx.Ctx, dynamicAgent.GetName(), resp.AgentStepResponse)

	if err != nil {
		ctx.Ctx.GetLogger().Error("delegate_agent: sub-agent execution failed", "error", err)
		return toolcore.NBToolResponse{
			Status:            toolcore.NBToolResponseStatusError,
			Data:              fmt.Sprintf("Sub-agent execution failed: %s", err.Error()),
			IsTerminal:        resp.IsTerminal,
			AdditionalDetails: additionalDetails,
			SubAgentEvidence:  subAgentEvidence,
		}, nil
	}

	// A delegated sub-agent that hits a followup gate (confirmation for a write
	// tool, clarification question) returns ConversationStatusWaiting with a
	// populated FollowupRequest. Without a branch here, the check at the
	// len(resp.Response) > 0 point below would silently convert Waiting to
	// Success and drop the FollowupRequest — the user is never asked, and the
	// parent orchestrator sees the destructive/interactive operation as complete
	// even though the sub-agent stopped short. Mirror factory_agent.go's generic
	// wrapper here so the callback layer emits the followup to the UI. Note:
	// resume routing for delegated followups is a separate concern (needs the
	// dynamic sub-agent to be reconstructable from persisted state); until that
	// lands, PR-β blocks write/interactive tools inside delegations at spawn time.
	if resp.Status == core.ConversationStatusWaiting {
		additionalDetails[core.NBToolCallAdditionalDetailsFollowupRequest] = resp.FollowupRequest
		additionalDetails[core.NBToolCallAdditionalDetailsQuery] = resp.Query
		data := ""
		if len(resp.Response) > 0 {
			data = resp.Response[0]
		}
		return toolcore.NBToolResponse{
			Data:              data,
			Status:            toolcore.NBToolResponseStatusWaiting,
			Type:              toolcore.NBToolResponseTypeText,
			IsTerminal:        resp.IsTerminal,
			AdditionalDetails: additionalDetails,
			SubAgentEvidence:  subAgentEvidence,
		}, nil
	}

	if resp.Status == core.ConversationStatusFailed {
		responseData := "Sub-agent failed to provide a response."
		if len(resp.Response) > 0 {
			responseData = resp.Response[0]
		}
		return toolcore.NBToolResponse{
			Data:              responseData,
			Status:            toolcore.NBToolResponseStatusError,
			Type:              toolcore.NBToolResponseTypeText,
			IsTerminal:        resp.IsTerminal,
			AdditionalDetails: additionalDetails,
			SubAgentEvidence:  subAgentEvidence,
		}, nil
	}

	if len(resp.Response) > 0 {
		return toolcore.NBToolResponse{
			Data:              resp.Response[0],
			Status:            toolcore.NBToolResponseStatusSuccess,
			Type:              toolcore.NBToolResponseTypeText,
			IsTerminal:        resp.IsTerminal,
			AdditionalDetails: additionalDetails,
			SubAgentEvidence:  subAgentEvidence,
		}, nil
	}

	// Fallback: return last non-empty step observation
	for i := len(resp.AgentStepResponse) - 1; i >= 0; i-- {
		if resp.AgentStepResponse[i].Response.Content != "" {
			return toolcore.NBToolResponse{
				Data:              resp.AgentStepResponse[i].Response.Content,
				Status:            toolcore.NBToolResponseStatusSuccess,
				Type:              toolcore.NBToolResponseTypeText,
				IsTerminal:        resp.IsTerminal,
				AdditionalDetails: additionalDetails,
				SubAgentEvidence:  subAgentEvidence,
			}, nil
		}
	}

	return toolcore.NBToolResponse{
		Data:              "Sub-agent completed but produced no output.",
		Status:            toolcore.NBToolResponseStatusError,
		Type:              toolcore.NBToolResponseTypeText,
		IsTerminal:        resp.IsTerminal,
		AdditionalDetails: additionalDetails,
	}, nil
}

// parseDelegateInput extracts prompt, tool names, and max_iterations from the tool input.
// It handles both structured Arguments and raw JSON Command strings.
func parseDelegateInput(input toolcore.NBToolCallRequest) (prompt string, toolNames []string, maxIter int, err error) {
	maxIter = defaultDelegateMaxIterations

	// Try structured Arguments first (from NBMultiCommandTool-style parsing)
	if input.Arguments != nil {
		if p, ok := input.Arguments["prompt"].(string); ok && p != "" {
			prompt = p
		}
		if tools, ok := input.Arguments["tools"].([]any); ok {
			for _, t := range tools {
				if s, ok := t.(string); ok {
					toolNames = append(toolNames, s)
				}
			}
		}
		if mi, ok := input.Arguments["max_iterations"]; ok {
			switch v := mi.(type) {
			case float64:
				if v > 0 {
					maxIter = int(v)
				}
			case int:
				if v > 0 {
					maxIter = v
				}
			}
		}
	}

	// Fallback: parse Command as JSON
	if prompt == "" && input.Command != "" {
		var parsed map[string]any
		if jsonErr := json.Unmarshal([]byte(input.Command), &parsed); jsonErr == nil && parsed != nil {
			if p, ok := parsed["prompt"].(string); ok {
				prompt = p
			}
			if tools, ok := parsed["tools"].([]any); ok {
				for _, t := range tools {
					if s, ok := t.(string); ok {
						toolNames = append(toolNames, s)
					}
				}
			}
			if mi, ok := parsed["max_iterations"].(float64); ok && mi > 0 {
				maxIter = int(mi)
			}
		} else {
			// Treat the entire Command as the prompt (plain text)
			prompt = input.Command
		}
	}

	if prompt == "" {
		return "", nil, 0, fmt.Errorf("'prompt' is required and must be a non-empty string")
	}

	// Reject notebook-update misuse at the tool boundary. See
	// notebookMisusePromptRe for the why and the rejection rationale.
	if notebookMisusePromptRe.MatchString(prompt) {
		return "", nil, 0, fmt.Errorf("delegate_agent rejected: prompt starts with 'update the notebook' — the sub-agent's job is to investigate a focused sub-question, not to record findings on your behalf. Write to your own notebook with the <update_notebook> XML tag in your next thought; do not delegate the notebook update")
	}

	// Reject degenerate single-iteration delegations (see minDelegateMaxIterations).
	if maxIter < minDelegateMaxIterations {
		return "", nil, 0, fmt.Errorf("'max_iterations' must be at least %d — single-iteration delegation is degenerate. If you don't need iteration, call the tool directly or use the LLM tool; do not delegate", minDelegateMaxIterations)
	}

	// Cap max_iterations
	if maxIter > maxDelegateMaxIterations {
		maxIter = maxDelegateMaxIterations
	}

	return prompt, toolNames, maxIter, nil
}

// flattenableAgent is implemented by "thin" sub-agents whose GetSystemPrompt is fully
// STATIC — no RAG-retrieved context, skills/KB injection, or per-request/ctx-dependent
// composition — and whose whole contribution is that prompt plus their leaf tools (and
// no custom Execute). When such an agent (helm/redis) is named in a delegate_agent call
// we hand the sub-agent its leaf tools directly and carry its static prompt guidance
// into the sub-agent's brief, instead of nesting the whole agent (a redundant ReAct-loop
// hop). Agents with dynamically-composed prompts (postgres/aws/…) must NOT implement this
// (return false / don't implement): flattening would drop the context that makes them
// useful, so they are left nested and run as themselves. It is only an opt-in *signal* —
// the instructions themselves are read from GetSystemPrompt, not duplicated here.
type flattenableAgent interface {
	Flattenable() bool
}

// resolveAgentForDelegate decides how a resolved sub-agent is handed to the delegate
// sub-agent: flattened to its leaf tools + its full static prompt guidance when it opts
// in via flattenableAgent, otherwise nested as an agent-tool (today's default). The
// guidance is read from the agent's own GetSystemPrompt (safe because Flattenable promises
// a static prompt) — instructions, constraints AND examples — so a thin agent whose value
// is its examples (e.g. rabbitmq's curl/jq recipes) is flattened losslessly. Crucially the
// guidance is carried ON-DEMAND into the delegate sub-agent, not folded into the tool
// description (which would bloat every orchestrator that preloads the tool, e.g. v2/delegate).
func resolveAgentForDelegate(ctx *security.RequestContext, agent core.NBAgent) (tools []toolcore.NBTool, instructions []string) {
	if fa, ok := agent.(flattenableAgent); ok && fa.Flattenable() {
		return agent.GetSupportedTools(ctx), flattenAgentGuidance(agent.GetSystemPrompt(ctx, core.NBAgentRequest{}))
	}
	return []toolcore.NBTool{core.NewToolFromAgent(agent)}, nil
}

// flattenAgentGuidance renders a flattenable agent's static prompt (instructions,
// constraints, and examples) into plain lines for injection into the delegate sub-agent's
// brief. Examples carry the agent's real usage recipes — the main value of a thin CLI
// wrapper (e.g. rabbitmq's management-API curl/jq calls) — so they must survive flattening.
func flattenAgentGuidance(p core.NBAgentPrompt) []string {
	guidance := make([]string, 0, len(p.Instructions)+len(p.Constraints)+len(p.Examples))
	guidance = append(guidance, p.Instructions...)
	guidance = append(guidance, p.Constraints...)
	// ToolUsage carries how to drive the leaf tool(s) — including config-gated guidance
	// (e.g. shell pipes / jq when the shell tool is enabled). Sorted for deterministic
	// output since ToolUsage is a map.
	usageKeys := make([]string, 0, len(p.ToolUsage))
	for k := range p.ToolUsage {
		usageKeys = append(usageKeys, k)
	}
	sort.Strings(usageKeys)
	for _, k := range usageKeys {
		guidance = append(guidance, p.ToolUsage[k]...)
	}
	for _, ex := range p.Examples {
		answer := ex.Answer
		if answer == "" && len(ex.AnswerSteps) > 0 {
			steps := make([]string, 0, len(ex.AnswerSteps))
			for _, s := range ex.AnswerSteps {
				steps = append(steps, s.Input)
			}
			answer = strings.Join(steps, " ; ")
		}
		line := "Example — " + ex.Question
		if answer != "" {
			line += ": " + answer
		}
		if ex.Explanation != "" {
			line += " (" + ex.Explanation + ")"
		}
		guidance = append(guidance, line)
	}
	return guidance
}

// resolveToolsForDelegate resolves tool names to NBTool instances, filtering out any
// that cannot be found. This enforces security: the sub-agent can only access tools
// that exist in the account's tool registry. When a name resolves to a flattenable
// agent, its leaf tools are inlined (single hop) and its static instructions are
// returned in flattenInstructions for injection into the sub-agent's prompt.
func resolveToolsForDelegate(ctx *security.RequestContext, accountId string, toolNames []string) (resolved []toolcore.NBTool, flattenInstructions []string, unresolved []string) {
	seen := map[string]bool{}          // input names already processed
	resolvedNames := map[string]bool{} // resolved tool names, for dedup across flatten/direct
	seenAgents := map[string]bool{}    // canonical agent names, so two aliases of one agent don't double its instructions
	add := func(t toolcore.NBTool) {
		if t == nil {
			return
		}
		ln := strings.ToLower(t.Name())
		if resolvedNames[ln] {
			return
		}
		resolvedNames[ln] = true
		resolved = append(resolved, t)
	}

	for _, name := range toolNames {
		lower := strings.ToLower(name)
		if seen[lower] {
			continue
		}
		seen[lower] = true

		if tool, ok := toolcore.GetNBTool(accountId, name); ok && tool != nil {
			add(tool)
			continue
		}
		// Try resolving as an agent — flatten if it opts in, else nest.
		agent, found := core.GetNBAgent(ctx, name, accountId, core.AgentStatusEnabled)
		if !found || agent == nil {
			unresolved = append(unresolved, name)
			if ctx != nil {
				ctx.GetLogger().Warn("delegate_agent: tool not found", "tool", name, "account_id", accountId)
			}
			continue
		}
		// Dedup by canonical agent name: two different aliases resolving to the same
		// agent must not append its instructions (or re-add its tools) twice.
		agentName := strings.ToLower(agent.GetName())
		if seenAgents[agentName] {
			continue
		}
		seenAgents[agentName] = true

		agentTools, instructions := resolveAgentForDelegate(ctx, agent)
		for _, at := range agentTools {
			add(at)
		}
		flattenInstructions = append(flattenInstructions, instructions...)
	}
	return resolved, flattenInstructions, unresolved
}

// filterOutTool removes a tool by name from a slice, used to prevent recursive delegation.
func filterOutTool(tools []toolcore.NBTool, name string) []toolcore.NBTool {
	result := make([]toolcore.NBTool, 0, len(tools))
	for _, t := range tools {
		if !strings.EqualFold(t.Name(), name) {
			result = append(result, t)
		}
	}
	return result
}

// isLLMReasoningTool reports whether the given tool name refers to the internal
// reasoning LLM tool. The ReAct loop emits a dedicated LLM step for thought and
// final-summary generation, which is orthogonal to external tool calls. Treat this
// predicate as the single source of truth when filtering LLM steps out of user-
// facing lists (the system-prompt "available tools" line) or metrics (the budget/
// usage count shown to the parent agent).
func isLLMReasoningTool(name string) bool {
	return strings.EqualFold(name, core.ToolLlm)
}

// collectToolsUsed builds a histogram of tool names the sub-agent actually invoked,
// extracted from the ReAct step history. This is surfaced in the tool response's
// AdditionalDetails so the parent agent can see how the sub-agent spent its budget —
// critical for diagnosing poor-quality summaries from well-executed investigations.
// The LLM reasoning tool is filtered out because every step uses it and it adds no
// signal. Returns nil when the step history is empty or contains only LLM steps.
func collectToolsUsed(steps []core.ToolInvocation) map[string]int {
	if len(steps) == 0 {
		return nil
	}
	usage := map[string]int{}
	for _, step := range steps {
		if step.Call.FunctionCall == nil {
			continue
		}
		name := step.Call.FunctionCall.Name
		if name == "" || isLLMReasoningTool(name) {
			continue
		}
		usage[name]++
	}
	if len(usage) == 0 {
		return nil
	}
	return usage
}

// sumHistogram returns the total count across all buckets in a tool-usage histogram.
// Used to convert the tools_used map into a flat "external tool calls" count that
// aligns with the sub-agent's stated max_iterations budget.
func sumHistogram(h map[string]int) int {
	total := 0
	for _, v := range h {
		total += v
	}
	return total
}

// dynamicReActAgent implements core.NBAgent with runtime-provided prompt, tools, and
// iteration budget. It is created on-the-fly by delegateAgentTool and uses the standard
// ReAct planner for execution.
type dynamicReActAgent struct {
	name          string
	prompt        string
	tools         []toolcore.NBTool
	maxIterations int
	accountId     string
}

func (a *dynamicReActAgent) GetName() string {
	return a.name
}

func (a *dynamicReActAgent) GetNameAliases() []string {
	return nil
}

func (a *dynamicReActAgent) GetDescription() string {
	return fmt.Sprintf("Dynamically composed specialist sub-agent: %s", a.name)
}

func (a *dynamicReActAgent) GetSupportedTools(_ *security.RequestContext) []toolcore.NBTool {
	return a.tools
}

func (a *dynamicReActAgent) GetSystemPrompt(ctx *security.RequestContext, _ core.NBAgentRequest) core.NBAgentPrompt {
	// Build a list of concrete tool names (excluding the reasoning LLM tool) so the
	// sub-agent knows exactly what it can call. Exposing this in the system prompt
	// lets the sub-agent plan its investigation up front instead of discovering its
	// own capabilities mid-loop — a common source of wasted iterations.
	toolNames := make([]string, 0, len(a.tools))
	for _, tool := range a.tools {
		name := tool.Name()
		if isLLMReasoningTool(name) {
			continue
		}
		toolNames = append(toolNames, name)
	}

	// Budget awareness: give the sub-agent its exact iteration count so it can
	// prioritize. Without this the sub-agent flies blind, often burning its budget
	// on low-value checks before reaching the actual root-cause investigation.
	var budgetConstraint string
	if len(toolNames) > 0 {
		budgetConstraint = fmt.Sprintf(
			"You have a budget of %d tool calls total. Available tools: %s. Plan your investigation up front and prioritize the highest-impact checks first — do not waste iterations on exploratory calls when the task is already narrowly scoped.",
			a.maxIterations, strings.Join(toolNames, ", "),
		)
	} else {
		budgetConstraint = fmt.Sprintf(
			"You have a budget of %d reasoning steps total. Plan your analysis carefully since no external tools are provided.",
			a.maxIterations,
		)
	}

	instructions := []string{a.prompt}
	if menu := a.accountSkillListsMenu(ctx); menu != "" {
		instructions = append(instructions, menu)
	}

	return core.NBAgentPrompt{
		Role:         "a specialist investigator dynamically composed by a parent agent",
		Instructions: instructions,
		Constraints: []string{
			"Focus exclusively on the task described in your instructions. Do not expand scope.",
			"Use only the tools provided to you. Do not attempt to call tools not in your tool list.",
			budgetConstraint,
			"Report your findings with specific evidence: timestamps, metric values, log lines, or query results. Never make claims without supporting data.",
		},
	}
}

// accountSkillListsMenu renders a `<skill-lists>` discovery block from all active
// KBs mapped in the delegate's account. Returns "" when the feature flag is off,
// the account has no accessible KBs, or the KB list call fails (fail-open — the
// menu is discovery-only and the delegate stays functional without it).
//
// This is the delegate's only path to skills: the parent agent opts the whole
// sub-agent out of default-tool injection to keep the curated toolset honest,
// so unlike other agents the delegate can't rely on the executor's kb_prestep
// path to attach a menu. Load_skills itself re-injects via
// DefaultSkillsInjectOverride once this menu marker is present.
func (a *dynamicReActAgent) accountSkillListsMenu(ctx *security.RequestContext) string {
	if !config.Config.LlmServerDelegateAccountKBsEnabled {
		return ""
	}
	if a.accountId == "" {
		return ""
	}
	kbs, err := toolcore.ListKnowledgebases(ctx, a.accountId)
	if err != nil {
		ctx.GetLogger().Warn("delegate: unable to list account KBs for skill menu", "error", err, "account_id", a.accountId)
		return ""
	}
	return core.BuildSkillListsMenu(kbs)
}

func (a *dynamicReActAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// GetModelCategory runs the delegated sub-agent on the cheap Retrieval tier, not the
// orchestrator's Reasoning tier. A delegate is scoped grunt work (run a query, grep
// logs, check a config) — the same shape the specialist agents (postgres/helm/redis)
// already run on Retrieval. The expensive Reasoning tier stays with the top-level
// orchestrator that owns the cross-cutting diagnosis; paying pro rates for a delegated
// sub-task was cost with no matching quality need. This also makes delegation (and the
// trimmed orchestrators' on-demand reach-back) cost-competitive with a direct tool call.
func (a *dynamicReActAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierRetrieval
}

// GetMaxIterations implements core.NBAgentIterationProvider to cap the sub-agent's
// ReAct loop, preventing runaway execution.
func (a *dynamicReActAgent) GetMaxIterations() int {
	return a.maxIterations
}

// GetSummaryToolName implements core.NBAgentReActPlannerSummaryToolProvider.
// Use the standard LLM tool for summarization.
func (a *dynamicReActAgent) GetSummaryToolName() string {
	return core.ToolLlm
}

// OptOutDefaultTools implements core.DefaultToolsOptOut. The parent agent has already
// curated this sub-agent's tool list (via the `tools` field on the delegate_agent call);
// the planner must not silently inject shell_execute or watch tools on top of that scope.
// Skills are handled separately via InjectDefaultSkills below.
func (a *dynamicReActAgent) OptOutDefaultTools() bool {
	return true
}

// InjectDefaultSkills implements core.DefaultSkillsInjectOverride: even though
// the delegate opts out of default-tool injection wholesale, load_skills must
// still land when the delegate's synthesized system prompt advertises a
// `<skill-lists>` menu (see accountSkillListsMenu). Without this override the
// menu is visible but unreachable — the LLM sees named runbooks it cannot open.
func (a *dynamicReActAgent) InjectDefaultSkills() bool {
	return true
}
