package agents

import (
	"encoding/json"
	"fmt"
	"strings"

	"nudgebee/llm/agents/core"
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
	return `Spawn a dynamically-composed specialist sub-agent with a custom prompt and curated tool list.
USE when no pre-registered agent (aws, gcp, kubectl, postgres, etc.) covers the sub-task and you need a specialist with a specific methodology over a specific tool subset.
Prefer pre-registered agents when one fits — don't reinvent.
DO NOT use as a final-answer preamble (e.g. prompt = "I have enough info, will now generate response").
DO NOT use to format text or do single-LLM-call work — call the LLM tool directly.
DO NOT set max_iterations < 2 — if you don't need iteration, you don't need delegation.
Always specify "tools" for any non-trivial sub-task; omitting it gives the sub-agent LLM-only access.`
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

	// Resolve requested tools with security validation
	resolvedTools, unresolvedNames := resolveToolsForDelegate(ctx.Ctx, t.accountId, toolNames)
	if len(unresolvedNames) > 0 {
		ctx.Ctx.GetLogger().Warn("delegate_agent: some requested tools could not be resolved",
			"unresolved", unresolvedNames, "resolved_count", len(resolvedTools))
	}

	// Prevent recursive delegation — remove delegate_agent from the sub-agent's tool list.
	// Without this, a delegated sub-agent could spawn further delegates, causing unbounded
	// recursion and cost amplification.
	resolvedTools = filterOutTool(resolvedTools, DelegateAgentToolName)

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
	if err != nil {
		ctx.Ctx.GetLogger().Error("delegate_agent: sub-agent execution failed", "error", err)
		return toolcore.NBToolResponse{
			Status: toolcore.NBToolResponseStatusError,
			Data:   fmt.Sprintf("Sub-agent execution failed: %s", err.Error()),
		}, nil
	}

	// Use the canonical "agent_id" key the planner-callback persistence layer reads
	// (see planner_callback_handler.go and factory_agent.go). Writing the previous
	// "delegate_agent_id" key meant child_agent_id was never populated for delegate
	// calls, breaking call-tree reconstruction.
	additionalDetails := map[string]any{
		"agent_id": resp.AgentId,
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
			AdditionalDetails: additionalDetails,
		}, nil
	}

	if len(resp.Response) > 0 {
		return toolcore.NBToolResponse{
			Data:              resp.Response[0],
			Status:            toolcore.NBToolResponseStatusSuccess,
			Type:              toolcore.NBToolResponseTypeText,
			AdditionalDetails: additionalDetails,
		}, nil
	}

	// Fallback: return last non-empty step observation
	for i := len(resp.AgentStepResponse) - 1; i >= 0; i-- {
		if resp.AgentStepResponse[i].Response.Content != "" {
			return toolcore.NBToolResponse{
				Data:              resp.AgentStepResponse[i].Response.Content,
				Status:            toolcore.NBToolResponseStatusSuccess,
				Type:              toolcore.NBToolResponseTypeText,
				AdditionalDetails: additionalDetails,
			}, nil
		}
	}

	return toolcore.NBToolResponse{
		Data:              "Sub-agent completed but produced no output.",
		Status:            toolcore.NBToolResponseStatusError,
		Type:              toolcore.NBToolResponseTypeText,
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

// resolveToolsForDelegate resolves tool names to NBTool instances, filtering out any
// that cannot be found. This enforces security: the sub-agent can only access tools
// that exist in the account's tool registry.
func resolveToolsForDelegate(ctx *security.RequestContext, accountId string, toolNames []string) (resolved []toolcore.NBTool, unresolved []string) {
	seen := map[string]bool{}
	for _, name := range toolNames {
		lower := strings.ToLower(name)
		if seen[lower] {
			continue
		}
		seen[lower] = true

		tool, ok := toolcore.GetNBTool(accountId, name)
		if ok && tool != nil {
			resolved = append(resolved, tool)
		} else {
			// Try resolving as an agent wrapped as tool
			agent, found := core.GetNBAgent(ctx, name, accountId, core.AgentStatusEnabled)
			if found {
				resolved = append(resolved, core.NewToolFromAgent(agent))
			} else {
				unresolved = append(unresolved, name)
				if ctx != nil {
					ctx.GetLogger().Warn("delegate_agent: tool not found", "tool", name, "account_id", accountId)
				}
			}
		}
	}
	return resolved, unresolved
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

func (a *dynamicReActAgent) GetSystemPrompt(_ *security.RequestContext, _ core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{
		Role: "a specialist investigator dynamically composed by a parent agent",
		Instructions: []string{
			a.prompt,
		},
		Constraints: []string{
			"Focus exclusively on the task described in your instructions.",
			"Use only the tools provided to you. Do not attempt to call tools not in your tool list.",
			"Be thorough but stay within your iteration budget. Prioritize the most impactful investigations first.",
			"Report your findings with specific evidence: timestamps, metric values, log lines, or query results.",
		},
	}
}

func (a *dynamicReActAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
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
// the planner must not silently inject shell_execute / load_skills on top of that scope.
func (a *dynamicReActAgent) OptOutDefaultTools() bool {
	return true
}
