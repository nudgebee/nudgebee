package core

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"reflect"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/memory"
	"github.com/tmc/langchaingo/schema"
	"go.opentelemetry.io/otel/trace"
)

var ExecutePlannerWorkerPool *common.WorkerPool

// MessageTerminationCacheNamespace is the namespace used for caching termination status.
const MessageTerminationCacheNamespace = "message_termination"

func init() {
	ExecutePlannerWorkerPool = common.NewWorkerPool("execute_planner", config.Config.AsyncPlanExecutionWorkerCount, 50)

	// Register the termination cache namespace with a default TTL from config
	common.CacheCreateNamespace(MessageTerminationCacheNamespace,
		common.CacheNamespaceWithExpiration(time.Duration(config.Config.LlmServerMessageTerminationCacheTTLSeconds)*time.Second),
		common.CacheNamespaceWithMaxEntries(1000),
	)
}

const plannerDummyTool = "planner"
const plannerToolNoData = "No Data"

const (
	logErrGetMessage          = "plannerexecutor: unable to get conversation message"
	logInfoTerminated         = "plannerexecutor: conversation message terminated, stopping execution"
	logErrSaveAgentCall       = "plannerexecutor: failed to save agent call to DB"
	logErrUpdateAgentCall     = "plannerexecutor: failed to update agent call to DB"
	logErrUnableToGenerateFup = "plannerexecutor: unable to generate followup"
	logRequestType            = "request-type"
	strPlanId                 = "\n#PlanId: "
	strToolName               = "\n#ToolName: "
	strQuestion               = "\n#Question: "
	strAnswer                 = "\n#Answer: "
)

// checkMessageTerminationStatus checks if a conversation message has been terminated,
// using the project's cache abstraction to debounce database lookups.
func checkMessageTerminationStatus(messageId, accountId, conversationId string) (bool, error) {
	// Try to get from cache first
	if val, ok := common.CacheGet(MessageTerminationCacheNamespace, messageId); ok {
		return string(val) == "true", nil
	}

	// Fetch from database on cache miss
	message, err := GetConversationDao().GetConversationMessage(messageId, accountId, conversationId)
	if err != nil {
		return false, err
	}

	isTerminated := message.Status == ConversationStatusTerminated

	// Cache the result.
	// If terminated, we can cache it for longer as it's a final state.
	ttl := time.Duration(config.Config.LlmServerMessageTerminationCacheTTLSeconds) * time.Second
	if isTerminated {
		ttl = time.Duration(config.Config.LlmServerMessageTerminatedCacheTTLMinutes) * time.Minute
	}

	valStr := "false"
	if isTerminated {
		valStr = "true"
	}

	err = common.CacheSet(MessageTerminationCacheNamespace, messageId, []byte(valStr), common.CacheSetWithExpiration(ttl))
	if err != nil {
		slog.Warn("plannerexecutor: failed to update termination cache", "error", err, "messageId", messageId)
	}

	return isTerminated, nil
}

// promptStructuralMarkers are the delimiters used to separate fields in the
// planner context prompt. Tool observations and inputs are untrusted — if they
// contain these markers they can trick the LLM into misinterpreting the
// structure (indirect prompt injection). sanitizeToolOutput escapes them.
var promptStructuralMarkers = strings.NewReplacer(
	"#PlanId:", "#PlanId\u200B:",
	"#ToolName:", "#ToolName\u200B:",
	"#Question:", "#Question\u200B:",
	"#Answer:", "#Answer\u200B:",
)

// sanitizeToolOutput escapes structural markers inside untrusted tool output
// to prevent indirect prompt injection via crafted tool observations.
func sanitizeToolOutput(s string) string {
	return promptStructuralMarkers.Replace(s)
}

// normalizeToolInputForTool attempts to convert non-JSON tool input into JSON
// using the tool's InputSchema. Used by planners so the executor receives
// uniformly structured input regardless of how the LLM framed the action.
//
// If the input is already JSON, empty, or the tool has no schema properties,
// the original input is returned unchanged. Recognized non-JSON shapes:
//   - XML tags:          <id>abc</id><limit>1</limit>
//   - key=value pairs:   id=abc,status=FAILED  or  id=abc status=FAILED
//   - plain text + "command" schema property → {"command": "<input>"}
func normalizeToolInputForTool(tool toolcore.NBTool, input string) string {
	if input == "" || tool == nil {
		return input
	}
	if strings.HasPrefix(strings.TrimSpace(input), "{") {
		return input
	}

	schema := tool.InputSchema()
	if len(schema.Properties) == 0 {
		return input
	}

	args := map[string]any{}

	// Try XML tags: <id>value</id><limit>1</limit>
	if strings.Contains(input, "<") && strings.Contains(input, ">") {
		for propName := range schema.Properties {
			if val := common.XmlExtractTagContent(input, propName); val != "" {
				args[propName] = val
			}
		}
	}

	// Try key=value pairs: id=abc,status=FAILED or id=abc status=FAILED
	if len(args) == 0 && strings.Contains(input, "=") {
		for _, sep := range []string{",", " "} {
			parts := strings.Split(input, sep)
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if eqIdx := strings.Index(part, "="); eqIdx > 0 {
					key := strings.TrimSpace(part[:eqIdx])
					val := strings.TrimSpace(part[eqIdx+1:])
					if _, exists := schema.Properties[key]; exists && val != "" {
						args[key] = val
					}
				}
			}
		}
	}

	if len(args) > 0 {
		if jsonBytes, err := common.MarshalJson(args); err == nil {
			return string(jsonBytes)
		}
	}

	// Fallback: if the schema has a "command" property and the input is plain
	// text that didn't match any structured format, wrap it as {"command": ...}.
	// The LLM sometimes emits plain-text input for ask_clarification instead of
	// JSON. Without this, the tool receives an empty command field.
	if _, hasCommand := schema.Properties["command"]; hasCommand {
		wrapped := map[string]any{"command": input}
		if jsonBytes, err := common.MarshalJson(wrapped); err == nil {
			return string(jsonBytes)
		}
	}

	return input
}

// normalizeToolInputByName locates a tool by name in the provided list and
// delegates to normalizeToolInputForTool. Returns the input unchanged if the
// tool is not found.
func normalizeToolInputByName(tools []toolcore.NBTool, toolName, input string) string {
	if input == "" {
		return input
	}
	for _, t := range tools {
		if t.Name() == toolName {
			return normalizeToolInputForTool(t, input)
		}
	}
	return input
}

// isInvalidToolName returns true for tool names that are clearly not real tools,
// e.g. "..." hallucinated by the LLM from truncated text in the context.
func isInvalidToolName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return true
	}
	for _, r := range trimmed {
		if r != '.' && r != '\u2026' {
			return false
		}
	}
	return true
}

type NBAgentPlannerExecutorResponse struct {
	Status            AgentExecutionStatus
	Response          string
	ResponseSummary   string
	IsTerminal        bool
	Invocations       []ToolInvocation
	Followup          FollowupRequest
	State             string
	AgentStepResponse any
	References        []toolcore.NBToolResponseReference
}

// newPlannerExecutor creates a new agent executor with an agent and the tools the agent can use.
func newPlannerExecutor(ctx *security.RequestContext, agentPlanner NBAgentPlanner, agent NBAgent, request NBAgentRequest, maxIterations int) *plannerExecutor {
	toolCallbackhandler := newPlannerExecutorCallbackHandler(ctx, request, agent)
	summaryToolName := ""
	if summaryToolPRovider, ok := agent.(NBAgentReActPlannerSummaryToolProvider); ok {
		summaryToolName = summaryToolPRovider.GetSummaryToolName()
	}
	// Preserve the original user query so sub-agents can use it for config selection.
	// The first executor (top-level) sets it; sub-agents inherit via QueryConfig propagation.
	if request.QueryConfig.OriginalUserQuery == "" {
		request.QueryConfig.OriginalUserQuery = request.Query
	}

	return &plannerExecutor{
		ctx:                 ctx,
		agentPlanner:        agentPlanner,
		agent:               agent,
		agentRequest:        request,
		maxIterations:       maxIterations,
		memory:              memory.NewSimple(),
		toolCallbackHandler: toolCallbackhandler,
		stepKeys:            map[string]bool{},
		summaryToolName:     summaryToolName,
		toolCallCache:       turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)},
	}
}

type ActionNode struct {
	Action       NBAgentPlannerToolAction
	Dependencies map[string]struct{} // Set of toolIDs this node depends on
	Status       string              // "pending", "running", "completed", "failed"
	Result       NBAgentPlannerToolActionStep
}

type ActionGraph struct {
	Nodes     map[string]*ActionNode // Map of toolID to node
	Ready     chan *ActionNode       // Channel for nodes ready to execute
	Results   chan *ActionNode       // Channel for completed nodes
	ErrorChan chan error             // Channel for errors
}

type plannerExecutor struct {
	ctx                 *security.RequestContext
	agentPlanner        NBAgentPlanner
	agent               NBAgent
	agentRequest        NBAgentRequest
	memory              schema.Memory
	finish              *NBAgentPlannerFinishAction
	toolCallbackHandler NBAgentToolCallback
	maxIterations       int
	currentIteration    int
	steps               []NBAgentPlannerToolActionStep
	currentAction       []NBAgentPlannerToolAction
	stepKeys            map[string]bool
	summaryToolName     string
	semaphore           chan struct{}
	toolCallCache       turnToolCallCache // Normalized cache for tool results, persists across plan regeneration within a turn
}

func (e *plannerExecutor) GetInputKeys() []string {
	return []string{"input"}
}

func (e *plannerExecutor) GetOutputKeys() []string {
	return []string{"output"}
}

func (e *plannerExecutor) GetMemory() schema.Memory {
	return e.memory
}

func (e *plannerExecutor) GetCallbackHandler() callbacks.Handler {
	return nil
}

func (e *plannerExecutor) Call(ctx context.Context, inputValues map[string]any, _ ...chains.ChainCallOption) (map[string]any, error) {
	defer func() {
		hits, misses, entries := e.toolCallCache.Stats()
		if hits > 0 || entries > 0 {
			e.ctx.GetLogger().Info("plannerexecutor: tool call cache summary",
				"hits", hits, "misses", misses, "entries", entries)
		}
	}()

	inputs, err := inputsToString(inputValues)
	if err != nil {
		return nil, err
	}
	nameToTool := getNameToTool(e.agentPlanner.GetTools())

	consecutiveFailedIters := 0
	consecutiveDuplicateIters := 0
	for i := e.currentIteration; i < e.maxIterations; i++ {
		if ctx.Err() != nil {
			e.ctx.GetLogger().Warn("plannerexecutor: context deadline exceeded, breaking loop", "agent", e.agent.GetName(), "iteration", i)
			break
		}
		e.currentIteration = i

		var finish *NBAgentPlannerFinishAction
		iterStart := time.Now()
		prevStepCount := len(e.steps)
		steps, finish, err := e.doIteration(ctx, e.steps, nameToTool, inputs)
		e.ctx.GetLogger().Info("plannerexecutor: iteration complete", "iteration", i, "duration", time.Since(iterStart).String(), "steps", len(steps), "hasFinish", finish != nil)
		if len(steps) > 0 {
			for _, step := range steps {
				if _, ok := e.stepKeys[step.Action.ToolID]; !ok {
					e.stepKeys[step.Action.ToolID] = true
					e.steps = append(e.steps, step)
				}
			}
		}

		// Duplicate-action loop detection: if the LLM returns non-empty steps
		// but none are new (all were reused from history via skip logic) and
		// there's no Finish, the planner is spinning on already-done work.
		// Break after 2 consecutive such iterations and fall through to
		// summarizeConversation. Observed with redis SLOWLOG GET: agent
		// keeps proposing same command despite having the result — wastes
		// 10 iterations × 10s otherwise (#28141).
		if finish == nil && len(steps) > 0 && len(e.steps) == prevStepCount {
			consecutiveDuplicateIters++
			if consecutiveDuplicateIters >= 2 {
				e.ctx.GetLogger().Warn("plannerexecutor: breaking after 2 consecutive duplicate-action iterations (LLM looping on completed work)", "agent", e.agent.GetName(), "iteration", i)
				break
			}
		} else {
			consecutiveDuplicateIters = 0
		}
		e.finish = finish
		if finish != nil {
			e.ctx.GetLogger().Debug("plannerexecutor: finishing execution", "status", finish.Status, "data", finish.Data)
			return map[string]any{
				"output": common.XmlExtractCDATA(finish.Data),
			}, err
		}

		if err != nil {
			// If we have accumulated steps, don't bail out — fall through to
			// summarizeConversation so the user gets a partial answer instead
			// of a raw error. This covers cases like the react loop exhausting
			// its inner iterations or returning ErrAgentNoReturn after max attempts.
			if len(e.steps) > 0 {
				e.ctx.GetLogger().Warn("plannerexecutor: iteration returned error but steps exist, falling through to summarization", "error", err, "stepsCount", len(e.steps))
				break
			}
			return nil, err
		}

		// Fast-fail: if the LLM returned no parseable actions OR only failures,
		// count consecutive bad iterations. Breaking after 2 prevents burning
		// 5+ iterations × 16s on a stuck model.
		allFailed := len(steps) == 0
		for _, s := range steps {
			if s.Status != ToolStatusFailure {
				allFailed = false
				break
			}
		}
		if allFailed {
			consecutiveFailedIters++
			if consecutiveFailedIters >= 2 {
				e.ctx.GetLogger().Warn("plannerexecutor: breaking after 2 consecutive failed iterations (likely zero-output LLM)", "agent", e.agent.GetName(), "iteration", i)
				break
			}
		} else {
			consecutiveFailedIters = 0
		}
	}

	result, err := e.summarizeConversation()
	if err == nil && result != nil && len(result) > 0 {
		return result, err
	} else if err != nil {
		e.ctx.GetLogger().Error("plannerexecutor: unable to summarize conversation", "error", err)
		return nil, err
	}

	return map[string]any{"output": agents.ErrNotFinished.Error()}, agents.ErrNotFinished
}

func (e *plannerExecutor) summarizeConversation() (map[string]any, error) {
	// OTEL: Start Summarize Span
	_, span := e.ctx.GetTracer().Start(e.ctx.GetContext(), "Agent:Summarize")
	defer span.End()

	// Build tool call status summary for accurate summarization.
	var toolSummary strings.Builder
	toolSummary.WriteString("\n\nTOOL CALL SUMMARY:\n")
	hasAnyFailure := false
	for i, step := range e.steps {
		status := "SUCCESS"
		switch step.Status {
		case ToolStatusFailure:
			status = "FAILED"
			hasAnyFailure = true
		case ToolStatusEmptyResult:
			status = "NO_DATA"
			hasAnyFailure = true
		}
		toolName := step.Action.Tool
		if toolName == "" {
			toolName = "(unknown)"
		}
		fmt.Fprintf(&toolSummary, "%d. %s — %s\n", i+1, toolName, status)
	}
	toolSummary.WriteString("\nIMPORTANT: If the last intended action (like updating/applying changes) was NOT executed because iterations ran out, clearly state that in your summary. Do NOT say 'I will do X' if X was not actually done.\n")

	mclist := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: e.agentRequest.Query}},
		},
	}
	for _, toolCall := range e.GetToolInvocations() {
		mc := llms.MessageContent{
			Role:  llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{llms.TextContent{Text: toolCall.Log}},
		}
		mclist = append(mclist, mc)
		mc = llms.MessageContent{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: toolCall.Response.Content}},
		}
		mclist = append(mclist, mc)
	}

	summarizationPrompt := fmt.Sprintf("Summarize all the previous conversation and return a response based on the question asked.\nIMPORTANT: Do not include any internal technical data flows, queries, prompts, architecture paths, or execution plans in your summary.\nIMPORTANT: If you ran out of steps before completing an action, clearly state what was NOT completed.%s", toolSummary.String())

	if hasAnyFailure {
		summarizationPrompt += "\n\nSome tool calls returned NO DATA or FAILED. Review the tool call summary above." +
			"\n- If the gathered data is sufficient to answer the question, provide a confident answer." +
			"\n- If critical data is missing and you cannot provide a reliable answer:" +
			"\n  1. Clearly state what could NOT be investigated and why." +
			"\n  2. Under a '**Recommended Next Steps**' section, provide specific CLI commands or manual steps the user can run to gather the missing information." +
			"\n  3. Do NOT fabricate findings for the missing data."
	}
	mclist = append(mclist, llms.MessageContent{
		Role:  llms.ChatMessageTypeHuman,
		Parts: []llms.ContentPart{llms.TextContent{Text: summarizationPrompt}},
	})

	// Use Lite model for summarization to improve performance.
	// WithoutCancel ensures summarization completes even if the parent context
	// (HTTP request or agent timeout) has expired — this is critical for the
	// deadline-exceeded partial-result path.
	summaryCtx := security.NewRequestContext(
		context.WithValue(context.WithoutCancel(e.ctx.GetContext()), ContextKeyUseLiteModel, true),
		e.ctx.GetSecurityContext(),
		e.ctx.GetLogger(),
		e.ctx.GetTracer(),
		e.ctx.GetMeter(),
	)
	response, err := GenerateAndTrackLLMContent(summaryCtx, e.agentRequest.UserId, e.agentRequest.AccountId, e.agentRequest.ConversationId, e.agentRequest.MessageId, e.agentRequest.ParentAgentId, true, mclist, true, WithThinkingLevel(ThinkingLevelFastTask))
	if err != nil {
		slog.Error("plannerexecutor: unable to generate llm contents", "error", err)
	}
	if response != nil && len(response.Choices) > 0 {
		return map[string]any{
			"output": response.Choices[0].Content,
		}, nil
	}
	return nil, nil
}

// evaluateConditions checks if an action should be executed based on its conditions.
// It returns true if the action should be executed, false otherwise.
func (e *plannerExecutor) evaluateConditions(action NBAgentPlannerToolAction, availableSteps []NBAgentPlannerToolActionStep) (bool, error) {
	if action.Condition.Expression == "" && action.Condition.Prompt == "" && len(action.Condition.AllowedResponses) == 0 {
		return true, nil
	}

	previousOutputs := make(map[string]any)
	for _, depToolID := range action.Dependency {
		found := false
		for _, step := range availableSteps {
			if step.Action.ToolID == depToolID {
				// Attempt to unmarshal observation if it's JSON
				var jsonData any
				if err := common.UnmarshalJson([]byte(step.Observation), &jsonData); err == nil {
					previousOutputs[step.Action.ToolID] = jsonData // Use ToolID as key
				} else {
					previousOutputs[step.Action.ToolID] = step.Observation
				}
				found = true
				break
			}
		}
		if !found {
			// If a dependency's output is not found, it might mean the condition cannot be reliably evaluated.
			// Depending on strictness, this could be an error or treated as the condition not being met.
			// For now, let's log and proceed; govaluate will error if the variable is missing.
			e.ctx.GetLogger().Warn("plannerexecutor: dependency output not found for condition evaluation", "actionToolID", action.ToolID, "dependencyToolID", depToolID)
		}
	}

	// Evaluate ConditionExpression
	if action.Condition.Expression != "" {
		result, err := common.EvaluateExpression(previousOutputs, action.Condition.Expression)
		if err != nil {
			e.ctx.GetLogger().Warn("plannerexecutor: failed to evaluate condition expression, treating as false", "expression", action.Condition.Expression, "contextKeys", slices.Collect(maps.Keys(previousOutputs)), "error", err)
			// If evaluation fails (e.g., missing variable, type error), treat as condition not met.
			return false, err
		}

		boolResult, ok := result.(bool)
		if !ok {
			e.ctx.GetLogger().Error("plannerexecutor: condition expression did not return a boolean", "expression", action.Condition.Expression, "result", result)
			return false, fmt.Errorf("condition expression '%s' did not return a boolean", action.Condition.Expression)
		}
		if !boolResult {
			e.ctx.GetLogger().Info("plannerexecutor: condition expression evaluated to false, skipping action", "toolId", action.ToolID, "expression", action.Condition.Expression)
			return false, nil
		}
	}

	// Evaluate ConditionLLM
	if action.Condition.Prompt != "" || len(action.Condition.AllowedResponses) > 0 {
		var llmInputDataBuilder strings.Builder
		for i, depToolID := range action.Dependency {
			if output, exists := previousOutputs[depToolID]; exists {
				var outputStr string
				if strVal, ok := output.(string); ok {
					outputStr = strVal
				} else {
					jsonBytes, err := common.MarshalJson(output)
					if err == nil {
						outputStr = string(jsonBytes)
					} else {
						outputStr = fmt.Sprintf("%v", output)
					}
				}
				llmInputDataBuilder.WriteString(outputStr)
				if i < len(action.Dependency)-1 {
					llmInputDataBuilder.WriteString("\n---\n")
				}
			}
		}
		llmInputData := llmInputDataBuilder.String()

		if len(action.Condition.AllowedResponses) == 0 {
			action.Condition.AllowedResponses = []string{
				"true",
				"false",
			}
		}

		// Use a default prompt if none is provided but PossibleResponses is used
		promptToUse := action.Condition.Prompt
		if len(action.Condition.AllowedResponses) > 0 {
			promptToUse = promptToUse + "\n\nOutput Format - Analyze the following data and respond with one of the following values, do not include any explanation.: " + strings.Join(action.Condition.AllowedResponses, ", ")
		}

		formattedPrompt := promptToUse
		if strings.Contains(promptToUse, "{{input_data}}") {
			formattedPrompt = strings.ReplaceAll(promptToUse, "{{input_data}}", llmInputData)
		} else if llmInputData != "" {
			formattedPrompt = promptToUse + "\n\nData: " + llmInputData
		}

		// Use lite model for condition evaluation — these are simple boolean checks
		// that don't need full-model reasoning capability
		conditionCtx := security.NewRequestContext(
			context.WithValue(e.ctx.GetContext(), ContextKeyUseLiteModel, true),
			e.ctx.GetSecurityContext(),
			e.ctx.GetLogger(),
			e.ctx.GetTracer(),
			e.ctx.GetMeter(),
		)
		llmResponse, err := GenerateAndTrackLLMContent(conditionCtx, e.agentRequest.UserId, e.agentRequest.AccountId, e.agentRequest.ConversationId, e.agentRequest.MessageId, e.agentRequest.AgentId, true, []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, formattedPrompt)}, true, llms.WithTemperature(0.0), WithThinkingLevel(ThinkingLevelFastTask))
		if err != nil || len(llmResponse.Choices) == 0 || llmResponse.Choices[0].Content == "" {
			e.ctx.GetLogger().Error("plannerexecutor: LLM call for condition failed or returned no content", "prompt", formattedPrompt, "error", err)
			return false, fmt.Errorf("LLM condition call failed or returned no content: %w", err)
		}

		actualResponse := strings.TrimSpace(llmResponse.Choices[0].Content)
		expectedResponse := strings.TrimSpace(action.Condition.ExpectedResponse)
		possibleResponses := action.Condition.AllowedResponses

		// Check against PossibleResponses first if provided
		if len(possibleResponses) > 0 {
			if !slices.Contains(possibleResponses, actualResponse) {
				e.ctx.GetLogger().Info("plannerexecutor: LLM response not in possible responses, skipping action", "toolId", action.ToolID, "response", actualResponse, "possible", possibleResponses)
				return false, nil
			}
			// If response is in possible responses, proceed to check ExpectedResponse if it exists
		}

		// Check against ExpectedResponse if provided (and if PossibleResponses check passed or wasn't applicable)
		if expectedResponse != "" && actualResponse != expectedResponse {
			e.ctx.GetLogger().Info("plannerexecutor: LLM condition not met", "toolId", action.ToolID, "expected", expectedResponse, "actual", actualResponse)
			return false, nil // Condition not met if ExpectedResponse doesn't match
		}
		e.ctx.GetLogger().Info("plannerexecutor: LLM condition met", "toolId", action.ToolID)
	}

	return true, nil
}

func (e *plannerExecutor) doIteration(
	ctx context.Context,
	previousIterationSteps []NBAgentPlannerToolActionStep,
	nameToTool map[string]toolcore.NBTool,
	input map[string]string,
) (steps []NBAgentPlannerToolActionStep, finishAct *NBAgentPlannerFinishAction, err error) {
	// Shared validation and termination check
	if e == nil || e.ctx == nil {
		return nil, nil, fmt.Errorf("invalid executor or context")
	}
	if e.agentRequest.MessageId == "" || e.agentRequest.AccountId == "" || e.agentRequest.ConversationId == "" {
		e.ctx.GetLogger().Error("plannerexecutor: invalid agent request, missing required fields", "agentRequest", slog.AnyValue(e.agentRequest))
		return nil, nil, fmt.Errorf("invalid agent request")
	}
	isTerminated, err := checkMessageTerminationStatus(e.agentRequest.MessageId, e.agentRequest.AccountId, e.agentRequest.ConversationId)
	if err != nil {
		e.ctx.GetLogger().Warn(logErrGetMessage, "message", err.Error(), "messageId", e.agentRequest.MessageId)
		return nil, nil, err
	}
	if isTerminated {
		e.ctx.GetLogger().Info(logInfoTerminated, "messageId", e.agentRequest.MessageId)
		return nil, &NBAgentPlannerFinishAction{
			Data:   "Conversation terminated by user.",
			Status: ConversationStatusTerminated,
		}, nil
	}

	// Plan generation (centralized)
	var actions []NBAgentPlannerToolAction
	var finish *NBAgentPlannerFinishAction
	if len(e.currentAction) == 0 {
		// OTEL: Start Plan Span
		var span trace.Span
		_, span = e.ctx.GetTracer().Start(e.ctx.GetContext(), "Agent:Plan")
		e.ctx.GetLogger().Info("plannerexecutor: generating plan", "agent", e.agent.GetName(), "iteration", e.currentIteration)
		planStart := time.Now()
		actions, finish, err = e.agentPlanner.Plan(ctx, previousIterationSteps, input["input"])
		span.End() // End plan span immediately after planning
		e.ctx.GetLogger().Info("plannerexecutor: plan generation complete", "duration", time.Since(planStart).String(), "actions", len(actions), "hasFinish", finish != nil)

		if errors.Is(err, agents.ErrUnableToParseOutput) {
			formattedObservation := err.Error()
			dummyActionForError := NBAgentPlannerToolAction{ToolID: "parse_error_" + common.GenerateUUID(), Tool: "error_parser", ToolInput: "error_observation"}
			return []NBAgentPlannerToolActionStep{{Action: dummyActionForError, Observation: formattedObservation, Status: ToolStatusFailure}}, nil, nil
		}
		if err != nil {
			return nil, nil, err
		}
		e.ctx.GetLogger().Info("plannerexecutor: plan generated", "agent", e.agent.GetName(), "actionsCount", len(actions), "isFinish", finish != nil)
	} else {
		actions = e.currentAction
		e.currentAction = []NBAgentPlannerToolAction{}
		e.ctx.GetLogger().Info("plannerexecutor: using current actions", "agent", e.agent.GetName(), "actionsCount", len(actions))
	}

	if len(actions) == 0 && finish == nil {
		return nil, nil, agents.ErrAgentNoReturn
	}
	if finish != nil {
		return nil, finish, nil
	}

	// Enable parallel execution when multiple actions are returned by a planner
	// that supports it (ReWOO or ReAct3) and parallel execution is enabled in config.
	// Check the actual planner instance (not the agent's declared type) because
	// react agents may be upgraded to react_3 via LlmServerReAct3Enabled config.
	_, isReAct3Planner := e.agentPlanner.(*NBReActPlanner3)
	if len(actions) > 1 && config.Config.PlannerRewooParallelExecEnabled &&
		(e.IsReWOOPlanner() || isReAct3Planner) {
		// Pre-flight check: detect actions that might trigger followups (write approval
		// or config resolution). Only one followup can be active at a time, so if any
		// action in the batch could trigger one, fall back to sequential execution.
		//
		// Followup triggers (both only fire for NBToolTypeTool, not agent-type):
		//   1. Write approval: tool request classified as create/update/delete
		//   2. Config resolution: tool implements NBToolConfig with unresolved config
		//
		// Agent-type tools (NBToolTypeAgent) run their own sub-executor sequentially,
		// so followup collisions within a single agent can't happen. Safe to parallelize.
		needsSequential := false
		for _, action := range actions {
			tool, ok := nameToTool[strings.ToUpper(action.Tool)]
			if !ok {
				continue
			}
			// Only NBToolTypeTool triggers followups at this executor level.
			// Agent-type tools handle followups inside their own sub-executor (ReAct loop).
			if tool.GetType() != toolcore.NBToolTypeTool {
				continue
			}
			// Check 1: Write approval — static heuristic classification
			if validator, ok := tool.(toolcore.ToolRequestInference); ok {
				reqType, err := validator.InferToolRequestType(e.ctx, action.Tool, action.ToolInput)
				if err == nil && reqType != "" && reqType != toolcore.ToolRequestTypeRead {
					needsSequential = true
					e.ctx.GetLogger().Info("plannerexecutor: pre-flight detected write action", "tool", action.Tool, "requestType", reqType)
					break
				}
			}
			// Check 1b: If tool only has LLM-based classification (no static heuristic),
			// we can't cheaply determine if it's a write — assume it could be.
			if _, hasPromptInference := tool.(toolcore.ToolRequestInferencePrompt); hasPromptInference {
				if validator, ok := tool.(toolcore.ToolRequestInference); ok {
					reqType, _ := validator.InferToolRequestType(e.ctx, action.Tool, action.ToolInput)
					if reqType == "" {
						needsSequential = true
						e.ctx.GetLogger().Info("plannerexecutor: pre-flight detected tool with LLM-only classification, assuming potential write", "tool", action.Tool)
						break
					}
				} else {
					needsSequential = true
					e.ctx.GetLogger().Info("plannerexecutor: pre-flight detected tool with LLM-only classification, assuming potential write", "tool", action.Tool)
					break
				}
			}
			// Check 2: Config resolution — tool needs user to select from multiple configs.
			// If the tool implements NBToolConfig and config isn't already resolved,
			// it may trigger a config selection followup.
			configCheckTool := tool
			if _, hasConfig := tool.(toolcore.NBToolConfig); !hasConfig {
				// Same fallback as doAction: find the agent's configurable tool
				for _, t := range e.agent.GetSupportedTools(e.ctx) {
					if _, ok := t.(toolcore.NBToolConfig); ok {
						configCheckTool = t
						break
					}
				}
			}
			if _, hasConfig := configCheckTool.(toolcore.NBToolConfig); hasConfig {
				configResolved := false
				if e.agentRequest.QueryConfig.ToolConfigs != nil {
					if e.agentRequest.QueryConfig.ToolConfigs[configCheckTool.Name()] != "" {
						configResolved = true
					}
				}
				if !configResolved {
					needsSequential = true
					e.ctx.GetLogger().Info("plannerexecutor: pre-flight detected unresolved tool config", "tool", action.Tool, "configTool", configCheckTool.Name())
					break
				}
			}
		}
		if needsSequential {
			e.ctx.GetLogger().Info("plannerexecutor: falling back to sequential execution — parallel batch may trigger followups", "agent", e.agent.GetName(), "actionsCount", len(actions))
		} else {
			e.ctx.GetLogger().Info("plannerexecutor: executing actions in parallel", "agent", e.agent.GetName(), "actionsCount", len(actions))
			return e.doIterationParallel(ctx, previousIterationSteps, nameToTool, actions)
		}
	}
	e.ctx.GetLogger().Info("plannerexecutor: executing actions sequentially", "agent", e.agent.GetName(), "actionsCount", len(actions))
	return e.doIterationSequential(previousIterationSteps, nameToTool, actions)
}

func (e *plannerExecutor) doIterationSequential(
	previousIterationSteps []NBAgentPlannerToolActionStep,
	nameToTool map[string]toolcore.NBTool,
	actions []NBAgentPlannerToolAction,
) ([]NBAgentPlannerToolActionStep, *NBAgentPlannerFinishAction, error) {
	// Guard clause for empty actions
	if len(actions) == 0 {
		return nil, nil, agents.ErrAgentNoReturn
	}

	newStepsThisIteration := []NBAgentPlannerToolActionStep{}

	// Build success-only context for summary tools.
	// Summary tools parse the first "Answer:" they find, so including failed step
	// observations (e.g. "Tool not found - ...") causes them to unmarshal garbage.
	var successContextBuilder strings.Builder
	for _, s := range previousIterationSteps {
		if s.Status == ToolStatusSuccess || s.Status == ToolStatusEmptyResult {
			successContextBuilder.WriteString(strPlanId)
			successContextBuilder.WriteString(s.Action.ToolID)
			successContextBuilder.WriteString(strToolName)
			successContextBuilder.WriteString(s.Action.Tool)
			successContextBuilder.WriteString(strQuestion)
			successContextBuilder.WriteString(sanitizeToolOutput(s.Action.ToolInput))
			successContextBuilder.WriteString(strAnswer)
			successContextBuilder.WriteString(sanitizeToolOutput(s.Observation))
			successContextBuilder.WriteString("\n")
		}
	}
	successContext := successContextBuilder.String()

	for _, action := range actions {
		// Gather all available steps for condition evaluation
		allAvailableStepsForCondition := append([]NBAgentPlannerToolActionStep{}, previousIterationSteps...)
		allAvailableStepsForCondition = append(allAvailableStepsForCondition, newStepsThisIteration...)

		e.ctx.GetLogger().Info("plannerexecutor: checking skip logic", "actionToolID", action.ToolID, "historyCount", len(allAvailableStepsForCondition))

		// CRITICAL: Check if this action already has a result in history (from a previous iteration or resumption)
		var alreadyCompletedStep *NBAgentPlannerToolActionStep
		for i := range allAvailableStepsForCondition {
			s := allAvailableStepsForCondition[i]
			e.ctx.GetLogger().Info("plannerexecutor: comparing with history", "historyToolID", s.Action.ToolID, "historyStatus", s.Status)
			if s.Action.ToolID == action.ToolID && s.Status == ToolStatusSuccess {
				alreadyCompletedStep = &s
				break
			}
		}

		if alreadyCompletedStep != nil {
			newStepsThisIteration = append(newStepsThisIteration, *alreadyCompletedStep)
			continue
		}

		shouldExecute, errEval := e.evaluateConditions(action, allAvailableStepsForCondition)
		if errEval != nil {
			e.ctx.GetLogger().Error("plannerexecutor: error evaluating conditions for action, stopping iteration", "toolId", action.ToolID, "error", errEval)
			_, err := GetConversationDao().SaveCompletedConversationAgentCall(uuid.Nil, e.agentRequest.ConversationId, e.agentRequest.MessageId, e.agentRequest.AccountId, e.agentRequest.UserId, action.Tool, e.agentRequest.ParentAgentId, action.ToolInput, action.Log, "error evaluating conditions for action: "+errEval.Error(), e.agentRequest.QueryContext, e.agentRequest.QueryConfig, AgentExecutionStatusFail, "unable to evaluate condition")
			if err != nil {
				e.ctx.GetLogger().Error(logErrSaveAgentCall, "error", err.Error())
			}
			return newStepsThisIteration, nil, errEval
		}
		if !shouldExecute {
			e.ctx.GetLogger().Info("plannerexecutor: action skipped due to conditions not met", "toolId", action.ToolID, "tool", action.Tool)
			skippedStep := NBAgentPlannerToolActionStep{
				Action:      action,
				Observation: fmt.Sprintf("Action skipped due to unmet condition. Expression: '%s', LLM Prompt: '%s'", action.Condition.Expression, action.Condition.Prompt),
				Status:      ToolStatusFailure,
			}
			newStepsThisIteration = append(newStepsThisIteration, skippedStep)

			_, err := GetConversationDao().SaveCompletedConversationAgentCall(uuid.Nil, e.agentRequest.ConversationId, e.agentRequest.MessageId, e.agentRequest.AccountId, e.agentRequest.UserId, action.Tool, e.agentRequest.ParentAgentId, action.ToolInput, skippedStep.Action.Log, skippedStep.Observation, e.agentRequest.QueryContext, e.agentRequest.QueryConfig, AgentExecutionStatusSkipped, "skipped due to unmet condition")
			if err != nil {
				e.ctx.GetLogger().Error(logErrSaveAgentCall, "error", err.Error())
			}
			continue
		}

		// Build query context for this action
		var queryContextBuilder strings.Builder

		// Include previousIterationSteps only if its summary tool && deps are 0
		// this edge scenario, should not happen often
		// but in case user configures summary tool as first tool in the plan
		// we want to include previous context
		if action.Tool == e.summaryToolName && len(action.Dependency) == 0 {
			queryContextBuilder.WriteString(successContext)
			queryContextBuilder.WriteString("\n\n")
		}
		for _, d := range action.Dependency {
			foundContext := false
			for _, s := range newStepsThisIteration {
				if s.Action.ToolID == d {
					queryContextBuilder.WriteString(strPlanId)
					queryContextBuilder.WriteString(s.Action.ToolID)
					queryContextBuilder.WriteString(strToolName)
					queryContextBuilder.WriteString(s.Action.Tool)
					queryContextBuilder.WriteString(strQuestion)
					queryContextBuilder.WriteString(sanitizeToolOutput(s.Action.ToolInput))
					queryContextBuilder.WriteString(strAnswer)
					queryContextBuilder.WriteString(sanitizeToolOutput(s.Observation))
					queryContextBuilder.WriteString("\n")
					foundContext = true
					break
				}
			}
			if foundContext {
				continue
			}
			for _, s := range previousIterationSteps {
				if s.Action.ToolID == d {
					queryContextBuilder.WriteString(strPlanId)
					queryContextBuilder.WriteString(s.Action.ToolID)
					queryContextBuilder.WriteString(strToolName)
					queryContextBuilder.WriteString(s.Action.Tool)
					queryContextBuilder.WriteString(strQuestion)
					queryContextBuilder.WriteString(sanitizeToolOutput(s.Action.ToolInput))
					queryContextBuilder.WriteString(strAnswer)
					queryContextBuilder.WriteString(sanitizeToolOutput(s.Observation))
					queryContextBuilder.WriteString("\n")
					break
				}
			}
		}
		queryContext := queryContextBuilder.String()
		stepResponse, finishAct, errAct := e.doAction(nameToTool, action, queryContext)
		if finishAct != nil {
			if finishAct.Status == ConversationStatusWaiting || finishAct.Status == ConversationStatusWaitingForClientTool {
				e.currentAction = actions
			}
			if errAct == nil && stepResponse.Action.ToolID != "" {
				newStepsThisIteration = append(newStepsThisIteration, stepResponse)
			}
			return newStepsThisIteration, finishAct, nil
		}
		if errAct != nil {
			return newStepsThisIteration, nil, errAct
		}
		newStepsThisIteration = append(newStepsThisIteration, stepResponse)

		if stepResponse.IsTerminal {
			e.ctx.GetLogger().Info("plannerexecutor: detected terminal response, returning early", "tool", action.Tool)
			return newStepsThisIteration, &NBAgentPlannerFinishAction{
				Data:        stepResponse.Observation,
				Status:      ConversationStatusCompleted,
				Invocations: e.GetToolInvocations(),
			}, nil
		}
	}

	return newStepsThisIteration, nil, nil
}

// Note: previously this file had a safeResultSend helper that wrapped the
// channel send in defer/recover() because cleanup() could close the channel
// while workers were still alive. That race has been eliminated — cleanup()
// now signals workers via a derived cancel and only closes resultsChan after
// producerWg.Wait() (see doIterationParallel), so plain `resultsChan <- n`
// from any worker is panic-safe.

func (e *plannerExecutor) doIterationParallel(
	ctx context.Context,
	previousIterationSteps []NBAgentPlannerToolActionStep,
	nameToTool map[string]toolcore.NBTool,
	actions []NBAgentPlannerToolAction,
) (steps []NBAgentPlannerToolActionStep, finishAct *NBAgentPlannerFinishAction, err error) {
	// Guard clause for empty actions
	if len(actions) == 0 {
		return nil, nil, agents.ErrAgentNoReturn
	}

	// Build action graph
	nodes := make(map[string]*ActionNode)
	for _, action := range actions {
		node := &ActionNode{
			Action:       action,
			Dependencies: make(map[string]struct{}),
			Status:       "pending",
		}
		nodes[action.ToolID] = node
	}
	// Second pass: add dependencies ONLY if they are in the current batch
	for _, node := range nodes {
		for _, dep := range node.Action.Dependency {
			if _, exists := nodes[dep]; exists && dep != node.Action.ToolID {
				node.Dependencies[dep] = struct{}{}
			}
		}
	}

	var mu sync.Mutex
	newStepsThisIteration := []NBAgentPlannerToolActionStep{}
	e.semaphore = make(chan struct{}, config.Config.LLMServerAgentReWooMaxParallel)
	resultsChan := make(chan *ActionNode, len(nodes))
	var followupFinish *NBAgentPlannerFinishAction     // retained for terminal/client-tool single followup
	var waitingFollowups []*NBAgentPlannerFinishAction // collects ALL waiting followups from parallel goroutines (#28141)
	var waitingActions []NBAgentPlannerToolAction      // actions paused for followup, preserved for resume
	completedSteps := make(map[string]NBAgentPlannerToolActionStep)

	// producerWg tracks every in-flight worker goroutine so cleanup() can wait
	// for them before closing resultsChan. Without this, close-from-receiver
	// races sends from late workers (the previous code papered over this with
	// defer/recover() on every send).
	//
	// workCtx is a child of ctx that cleanup() cancels to signal in-flight
	// workers to exit their entry-point ctx.Err() check early on failure or
	// terminal-response paths. The defer ensures it is always released.
	var producerWg sync.WaitGroup
	workCtx, workCancel := context.WithCancel(ctx)
	defer workCancel()

	// preResolveToolConfigs checks all ready-to-dispatch nodes and resolves tool configs
	// BEFORE any goroutines are spawned. This prevents duplicate followup messages when
	// multiple parallel tool calls need the same tool config (#28127).
	preResolveToolConfigs := func() *NBAgentPlannerFinishAction {
		checkedTools := map[string]bool{}
		for _, node := range nodes {
			if len(node.Dependencies) != 0 || node.Status != "pending" {
				continue
			}
			toolName := node.Action.Tool
			if checkedTools[toolName] {
				continue
			}
			checkedTools[toolName] = true

			// Fix (#28141): nameToTool keys are UPPERCASE (set in getNameToTool,
			// executor.go:918). Previously this lookup used the raw toolName and
			// always missed — the dedup loop became a silent no-op, allowing
			// parallel goroutines to race into GenerateFollowup and create
			// duplicate followup messages for the same tool.
			tool, exists := nameToTool[strings.ToUpper(toolName)]
			if !exists {
				continue
			}
			configCheckTool := tool
			if tool.GetType() == toolcore.NBToolTypeTool {
				if _, hasConfig := tool.(toolcore.NBToolConfig); !hasConfig {
					for _, t := range e.agent.GetSupportedTools(e.ctx) {
						if _, ok := t.(toolcore.NBToolConfig); ok {
							configCheckTool = t
							break
						}
					}
				}
			}
			if configCheckTool.GetType() != toolcore.NBToolTypeTool {
				continue
			}
			if _, hasConfig := configCheckTool.(toolcore.NBToolConfig); !hasConfig {
				continue
			}
			_, finish, err := e.followupForMultipleToolConfigs(configCheckTool, node.Action)
			if err != nil {
				e.ctx.GetLogger().Warn("plannerexecutor: pre-resolve tool config failed", "tool", toolName, "error", err)
				continue
			}
			if finish != nil {
				e.ctx.GetLogger().Info("plannerexecutor: tool config followup needed (pre-resolved)", "tool", toolName)
				// Collect ALL pending actions using this tool name so they're
				// preserved for resume after the user selects a config (#28141).
				// Without this, only the triggering node is saved in currentAction
				// and siblings are re-planned by the LLM on resume, wasting tokens.
				var sameToolActions []NBAgentPlannerToolAction
				for _, n2 := range nodes {
					if n2.Status == "pending" && strings.EqualFold(n2.Action.Tool, toolName) {
						sameToolActions = append(sameToolActions, n2.Action)
					}
				}
				if len(sameToolActions) == 0 {
					sameToolActions = []NBAgentPlannerToolAction{node.Action}
				}
				e.currentAction = sameToolActions
				return finish
			}
		}
		return nil
	}

	// Helper to submit ready nodes
	submitReadyNodes := func() {
		for _, node := range nodes {
			if len(node.Dependencies) == 0 && node.Status == "pending" {
				// Will block if no permits available
				e.semaphore <- struct{}{} // Acquire permit
				node.Status = "ready"
				n := node
				e.ctx.GetLogger().Info("plannerexecutor: submitting tool for parallel execution", "tool", n.Action.Tool, "toolId", n.Action.ToolID)
				// Create a new variable to track whether to release the permit
				releasePermit := true
				// Add to producerWg BEFORE Submit so cleanup() never sees a moment
				// where a goroutine is in flight but not yet counted.
				producerWg.Add(1)
				err := ExecutePlannerWorkerPool.Submit(ctx, func() {
					defer producerWg.Done()
					defer func() {
						if releasePermit {
							<-e.semaphore // Release permit when done
						}
					}()
					// Check for cancellation via workCtx so cleanup() can short-circuit
					// in-flight workers on early-exit paths (failure / terminal / followup).
					// workCtx derives from ctx, so parent cancellation also fires here.
					if workCtx.Err() != nil {
						mu.Lock()
						n.Status = "skipped"
						skippedStep := NBAgentPlannerToolActionStep{
							Action:      n.Action,
							Observation: "Action skipped due to context cancellation.",
							Status:      ToolStatusFailure,
						}
						n.Result = skippedStep
						mu.Unlock()
						resultsChan <- n
						return
					}
					// Build context from dependencies and gather available steps for condition evaluation
					var ctxBuilder strings.Builder
					allAvailableStepsForCondition := make([]NBAgentPlannerToolActionStep, 0, len(previousIterationSteps)+len(n.Action.Dependency))
					mu.Lock()
					// Include previousIterationSteps only if its summary tool && deps are 0
					// this edge scenario, should not happen often
					// but in case user configures summary tool as first tool in the plan
					// we want to include previous context
					if n.Action.Tool == e.summaryToolName && len(n.Action.Dependency) == 0 {
						for _, s := range previousIterationSteps {
							if s.Status == ToolStatusSuccess || s.Status == ToolStatusEmptyResult {
								ctxBuilder.WriteString(strPlanId)
								ctxBuilder.WriteString(s.Action.ToolID)
								ctxBuilder.WriteString(strToolName)
								ctxBuilder.WriteString(s.Action.Tool)
								ctxBuilder.WriteString(strQuestion)
								ctxBuilder.WriteString(sanitizeToolOutput(s.Action.ToolInput))
								ctxBuilder.WriteString(strAnswer)
								ctxBuilder.WriteString(sanitizeToolOutput(s.Observation))
								ctxBuilder.WriteByte('\n')
							}
						}
					}
					allAvailableStepsForCondition = append(allAvailableStepsForCondition, previousIterationSteps...)
					// Include completedSteps from current iteration, including failed/skipped actions
					for _, dep := range n.Action.Dependency {
						if step, ok := completedSteps[dep]; ok {
							statusMarker := ""
							if nodes[dep].Status == "failed" {
								statusMarker = "[FAILED] "
							}
							if nodes[dep].Status == "skipped" {
								statusMarker = "[SKIPPED] "
							}
							ctxBuilder.WriteString(strPlanId)
							ctxBuilder.WriteString(step.Action.ToolID)
							ctxBuilder.WriteString(strToolName)
							ctxBuilder.WriteString(step.Action.Tool)
							ctxBuilder.WriteString(strQuestion)
							ctxBuilder.WriteString(sanitizeToolOutput(step.Action.ToolInput))
							ctxBuilder.WriteString(strAnswer)
							ctxBuilder.WriteString(statusMarker)
							ctxBuilder.WriteString(sanitizeToolOutput(step.Observation))
							ctxBuilder.WriteByte('\n')
							// Always include, even if failed/skipped
							allAvailableStepsForCondition = append(allAvailableStepsForCondition, step)
						} else {
							// If not in current batch, check previous iterations
							for _, s := range previousIterationSteps {
								if s.Action.ToolID == dep {
									ctxBuilder.WriteString(strPlanId)
									ctxBuilder.WriteString(s.Action.ToolID)
									ctxBuilder.WriteString(strToolName)
									ctxBuilder.WriteString(s.Action.Tool)
									ctxBuilder.WriteString(strQuestion)
									ctxBuilder.WriteString(sanitizeToolOutput(s.Action.ToolInput))
									ctxBuilder.WriteString(strAnswer)
									ctxBuilder.WriteString(sanitizeToolOutput(s.Observation))
									ctxBuilder.WriteByte('\n')
									break
								}
							}
						}
					}
					previousContext := ctxBuilder.String()

					// CRITICAL: Check if this action already has a result in history
					var alreadyCompletedStep *NBAgentPlannerToolActionStep
					for i := range allAvailableStepsForCondition {
						s := allAvailableStepsForCondition[i]
						if s.Action.ToolID == n.Action.ToolID && s.Status == ToolStatusSuccess {
							alreadyCompletedStep = &s
							break
						}
					}
					if alreadyCompletedStep != nil {
						e.ctx.GetLogger().Info("plannerexecutor: skipping parallel tool as already completed", "toolId", n.Action.ToolID)
						n.Result = *alreadyCompletedStep
						n.Status = "completed"
						completedSteps[n.Action.ToolID] = n.Result
						mu.Unlock()
						resultsChan <- n
						return
					}

					mu.Unlock()
					shouldExecute, evalErr := e.evaluateConditions(n.Action, allAvailableStepsForCondition)
					if evalErr != nil {
						mu.Lock()
						n.Status = "failed"
						mu.Unlock()
						resultsChan <- n
						return
					}
					if !shouldExecute {
						skippedStep := NBAgentPlannerToolActionStep{
							Action:      n.Action,
							Observation: fmt.Sprintf("Action skipped due to unmet condition. Expression: '%s', LLM Prompt: '%s'", n.Action.Condition.Expression, n.Action.Condition.Prompt),
							Status:      ToolStatusFailure,
						}
						mu.Lock()
						n.Result = skippedStep
						n.Status = "completed"
						mu.Unlock()
						resultsChan <- n
						return
					}
					step, finish, actErr := e.doAction(nameToTool, n.Action, previousContext)
					if actErr != nil {
						mu.Lock()
						n.Status = "failed"
						mu.Unlock()
						resultsChan <- n
						return
					}
					if finish != nil {
						if finish.Status == ConversationStatusWaiting || finish.Status == ConversationStatusWaitingForClientTool {
							// Collect ALL waiting followups from parallel sub-agents
							// instead of overwriting. Previously last-goroutine-wins
							// caused sibling followups to be lost from executor state,
							// breaking resume when multiple sub-agents need inputs (#28141).
							mu.Lock()
							waitingFollowups = append(waitingFollowups, finish)
							waitingActions = append(waitingActions, n.Action)
							mu.Unlock()
						} else if finish.Status != "" {
							// Terminal/completed/failed — single followupFinish for backward compat
							mu.Lock()
							followupFinish = finish
							mu.Unlock()
						}
						mu.Lock()
						n.Result = step
						n.Status = "completed"
						completedSteps[n.Action.ToolID] = step
						mu.Unlock()
						resultsChan <- n
						return
					}
					mu.Lock()
					n.Result = step
					n.Status = "completed"
					completedSteps[n.Action.ToolID] = step
					mu.Unlock()
					resultsChan <- n
				})
				if err != nil {
					// Submit failed: the worker goroutine never ran, so its deferred
					// producerWg.Done() will never fire. Cancel the Add(1) here so
					// producerWg.Wait() can complete.
					producerWg.Done()
					releasePermit = false
					<-e.semaphore // Release permit immediately to avoid leak
					// Worker pool is full or failed, mark as skipped and send to resultsChan
					skippedStep := NBAgentPlannerToolActionStep{
						Action:      n.Action,
						Observation: "Action skipped due to worker pool starvation or failure: " + err.Error(),
						Status:      ToolStatusFailure,
					}
					mu.Lock()
					n.Result = skippedStep
					n.Status = "skipped"
					completedSteps[n.Action.ToolID] = skippedStep
					mu.Unlock()
					resultsChan <- n
				}
			}
		}
	}

	// Pre-resolve tool configs before dispatching any parallel goroutines.
	if finish := preResolveToolConfigs(); finish != nil {
		return nil, finish, nil
	}

	submitReadyNodes()

	completed := 0
	total := len(nodes)
	var cleanupOnce sync.Once
	// cleanup signals in-flight workers to stop early via workCancel(), then
	// waits for all producers to finish in a background goroutine before
	// closing resultsChan. The goroutine pattern allows the receiver loop to
	// return immediately on early-exit paths (failure / terminal / followup)
	// without blocking on slow LLM/relay calls. resultsChan is buffered to
	// len(nodes), so late workers can still send without blocking.
	cleanup := func() {
		cleanupOnce.Do(func() {
			workCancel()
			go func() {
				// Defensive recover: producerWg.Wait() cannot panic and close()
				// is gated by cleanupOnce so double-close is impossible. The
				// recover is here purely to satisfy the repo convention that
				// every background goroutine logs and survives a panic rather
				// than tearing down the process.
				defer func() {
					if r := recover(); r != nil {
						e.ctx.GetLogger().Error("plannerexecutor: cleanup goroutine panicked", "panic", r)
					}
				}()
				producerWg.Wait()
				close(resultsChan)
			}()
		})
	}

	for completed < total {
		select {
		case <-ctx.Done():
			// Context cancelled, mark all remaining as skipped and exit
			mu.Lock()
			for _, node := range nodes {
				if node.Status == "pending" || node.Status == "ready" {
					node.Status = "skipped"
					skippedStep := NBAgentPlannerToolActionStep{
						Action:      node.Action,
						Observation: "Action skipped due to context cancellation.",
						Status:      ToolStatusFailure,
					}
					node.Result = skippedStep
					completedSteps[node.Action.ToolID] = skippedStep
					newStepsThisIteration = append(newStepsThisIteration, skippedStep)
					completed++
				}
			}
			mu.Unlock()
			cleanup()
			return newStepsThisIteration, nil, ctx.Err()
		case n := <-resultsChan:
			completed++
			e.ctx.GetLogger().Info("plannerexecutor: parallel tool result received", "tool", n.Action.Tool, "toolId", n.Action.ToolID, "status", n.Status)
			if n.Status == "failed" {
				cleanup()
				return newStepsThisIteration, nil, fmt.Errorf("action %s failed", n.Action.ToolID)
			}
			newStepsThisIteration = append(newStepsThisIteration, n.Result)

			// CRITICAL: If the tool returned a terminal response, return early
			if n.Result.IsTerminal {
				e.ctx.GetLogger().Info("plannerexecutor: detected terminal response in parallel execution, returning early", "tool", n.Action.Tool)
				mu.Lock()
				followupFinish = &NBAgentPlannerFinishAction{
					Data:        n.Result.Observation,
					Status:      ConversationStatusCompleted,
					Invocations: e.GetToolInvocations(),
				}
				mu.Unlock()
				cleanup()
				return newStepsThisIteration, followupFinish, nil
			}

			mu.Lock()
			completedSteps[n.Action.ToolID] = n.Result
			mu.Unlock()
			// Mark dependencies as resolved and resubmit ready nodes
			for _, depNode := range nodes {
				if _, exists := depNode.Dependencies[n.Action.ToolID]; exists {
					delete(depNode.Dependencies, n.Action.ToolID)
					e.ctx.GetLogger().Debug("plannerexecutor: cleared dependency", "targetToolId", depNode.Action.ToolID, "clearedDepId", n.Action.ToolID)
				}
			}
			// Pre-resolve tool configs for the next wave before dispatching
			if finish := preResolveToolConfigs(); finish != nil {
				cleanup()
				return newStepsThisIteration, finish, nil
			}
			submitReadyNodes()

			// If any goroutine returned WAITING, don't wait for siblings — no
			// progress is possible until the user responds. Siblings that are
			// still running will either complete (their results are preserved)
			// or also return WAITING (captured in waitingFollowups).
			// We let the outer loop finish to drain resultsChan; the post-loop
			// handler at the bottom of the function returns the collected set.
			mu.Lock()
			if followupFinish != nil && followupFinish.Status == ConversationStatusWaiting {
				// Legacy non-parallel callers may still set this directly.
				mu.Unlock()
				cleanup()
				return newStepsThisIteration, followupFinish, nil
			}
			mu.Unlock()

			// Deadlock detection: if no nodes are ready or running, but not all completed
			pendingCount := 0
			skippedNodes := make([]*ActionNode, 0, len(nodes))
			mu.Lock()
			for _, node := range nodes {
				if node.Status == "pending" && len(node.Dependencies) > 0 {
					pendingCount++
					skippedNodes = append(skippedNodes, node)
				}
			}
			if pendingCount > 0 {
				readyOrRunning := false
				for _, node := range nodes {
					if node.Status == "ready" || node.Status == "running" {
						readyOrRunning = true
						break
					}
				}
				if !readyOrRunning {
					// Instead of returning error, mark all as skipped and complete
					for _, node := range skippedNodes {
						skippedStep := NBAgentPlannerToolActionStep{
							Action:      node.Action,
							Observation: "Action skipped due to unsatisfiable dependencies.",
							Status:      ToolStatusFailure,
						}
						node.Result = skippedStep
						node.Status = "skipped"
						completedSteps[node.Action.ToolID] = skippedStep
						newStepsThisIteration = append(newStepsThisIteration, skippedStep)
						completed++
					}
					mu.Unlock()
					cleanup()
					break
				}
			}
			mu.Unlock()
		}
	}

	cleanup()

	// Multi-followup support (#28141): if any parallel sub-agents returned WAITING,
	// persist ALL their actions into e.currentAction so resume can re-enter each
	// one. Return the first followup as the API response; the rest are already
	// written to the DB as followup messages by GenerateFollowup inside each
	// goroutine, so the UI can display them.
	//
	// Client-tool aggregation: when multiple parallel actions are ClientToolWrappers
	// they all return Status=WAITING_FOR_CLIENT_TOOL with single-tool AdditionalDetails.
	// Without aggregation, only `waitingFollowups[0]`'s tool would surface to the
	// caller — the rest would be silently dropped, even though they were dispatched
	// and recorded as DB rows. Walk all `WaitingForClient` nodes and combine them
	// into AdditionalDetails["client_tools"] so the consumer at executor_planner.go
	// (the AgentStepResponse builder) hands the full set to chat_get.
	mu.Lock()
	if len(waitingFollowups) > 0 {
		e.currentAction = append([]NBAgentPlannerToolAction(nil), waitingActions...)
		e.ctx.GetLogger().Info("plannerexecutor: parallel iteration has waiting followups",
			"waiting", len(waitingFollowups),
			"completed", len(completedSteps),
			"agent", e.agent.GetName())
		first := waitingFollowups[0]
		var allClientTools []any
		for _, node := range nodes {
			if node.Result.Action.ToolID != "" && node.Result.Status == ToolStatusWaitingForClient {
				allClientTools = append(allClientTools, map[string]any{
					"tool_name":  node.Result.Action.Tool,
					"tool_input": node.Result.Action.ToolInput,
					"tool_id":    node.Result.Action.ToolID,
				})
			}
		}
		if len(allClientTools) > 1 {
			if first.AdditionalDetails == nil {
				first.AdditionalDetails = map[string]any{}
			}
			first.AdditionalDetails["client_tools"] = allClientTools
		}
		mu.Unlock()
		return newStepsThisIteration, first, nil
	}
	mu.Unlock()

	return newStepsThisIteration, nil, nil
}

// truncateToolResponse caps a tool's observation at a configured byte budget
// before it enters the cache, DB, or scratchpad. Success outputs use
// LlmServerMaxToolOutputLen; failure outputs use the smaller
// LlmServerMaxToolErrorOutputLen (stack traces are repetitive — less data
// captures the useful bits). Empty/sentinel payloads are never touched.
// A limit of 0 disables truncation entirely (opt-out).
func truncateToolResponse(ctx *security.RequestContext, data string, status ToolStatus, toolName string) string {
	if data == "" || data == plannerToolNoData || data == "[]" {
		return data
	}

	var maxLen int
	if status == ToolStatusFailure {
		maxLen = config.Config.LlmServerMaxToolErrorOutputLen
	} else {
		maxLen = config.Config.LlmServerMaxToolOutputLen
	}

	if maxLen <= 0 || len(data) <= maxLen {
		return data
	}

	truncated := SmartTruncateToolOutput(data, maxLen)
	if ctx != nil {
		ctx.GetLogger().Info("plannerexecutor: tool output truncated at source",
			"tool", toolName,
			"status", string(status),
			"original_len", len(data),
			"truncated_len", len(truncated),
			"max_len", maxLen)
	}
	return truncated
}

func (e *plannerExecutor) doAction(nameToTool map[string]toolcore.NBTool, action NBAgentPlannerToolAction, queryContext string) (NBAgentPlannerToolActionStep, *NBAgentPlannerFinishAction, error) {
	// Normalize tool name: trim whitespace that XML parsing may leave
	action.Tool = strings.TrimSpace(action.Tool)

	// --- Guard: Reject tool names that are clearly invalid (LLM hallucination of "..." from truncated context)
	if isInvalidToolName(action.Tool) {
		availableTools := make([]string, 0, len(nameToTool))
		for k := range nameToTool {
			availableTools = append(availableTools, k)
		}
		slices.Sort(availableTools)
		e.ctx.GetLogger().Warn("plannerexecutor: skipping invalid tool name", "tool", action.Tool, "toolId", action.ToolID)
		sanitizedName := strings.ReplaceAll(action.Tool, "]]>", "]] >")
		return NBAgentPlannerToolActionStep{
			Action:      action,
			Observation: fmt.Sprintf("Invalid tool name '%s'. Available tools: %s", sanitizedName, strings.Join(availableTools, ", ")),
			Status:      ToolStatusFailure,
		}, nil, nil
	}

	// --- Safety Check: Ensure we don't re-execute if we already have a success result in our own history
	for _, s := range e.steps {
		if s.Action.ToolID == action.ToolID && s.Status == ToolStatusSuccess {
			return s, nil, nil
		}
	}

	// --- Turn Cache Check: Prevent redundant calls with identical or near-identical inputs.
	// Uses normalized keys (collapsed whitespace, sorted JSON keys) to catch duplicates
	// across plan regeneration and critique cycles within the same conversation turn.
	if cachedStep, found := e.toolCallCache.Get(action.Tool, action.ToolInput); found {
		e.ctx.GetLogger().Info("plannerexecutor: returning cached result for duplicate tool call",
			"tool", action.Tool, "toolId", action.ToolID, "phase", "pre-rewrite")
		cachedStep.Action.ToolID = action.ToolID
		return cachedStep, nil, nil
	}
	originalInput := action.ToolInput

	// --- Metrics: record start time
	start := time.Now()
	toolName := action.Tool
	accountID := e.agentRequest.AccountId
	// do not rewrite input for summary tool to preserve context
	// as summary tool is expected to handle large context and summarize it, rewritting increases risk of large input context, causing issue with output
	// other tools can have their input rewritten to fit within token limits and provide better context
	if !strings.EqualFold(e.summaryToolName, action.Tool) {
		rewrittenToolInput, err := e.rewriteToolInput(action, queryContext)
		if err != nil {
			// Log the error but proceed with the original input
			e.ctx.GetLogger().Error("plannerexecutor: failed to rewrite tool input, using original", "error", err)
			rewrittenToolInput = action.ToolInput
		}
		action.ToolInput = rewrittenToolInput

		// Post-rewrite cache check: different original inputs may produce the same
		// rewritten input. Check again to avoid redundant tool execution.
		if action.ToolInput != originalInput {
			if cachedStep, found := e.toolCallCache.Get(action.Tool, action.ToolInput); found {
				e.ctx.GetLogger().Info("plannerexecutor: returning cached result for duplicate tool call",
					"tool", action.Tool, "toolId", action.ToolID, "phase", "post-rewrite")
				cachedStep.Action.ToolID = action.ToolID
				// Also cache under original input for future pre-rewrite hits
				e.toolCallCache.Put(action.Tool, originalInput, cachedStep)
				return cachedStep, nil, nil
			}
		}
	}

	// special handling for planner tools
	if action.Tool == plannerDummyTool {
		output, err := GetConversationDao().GetConversationAgentOutput(action.ToolID, e.agentRequest.AccountId)
		if err != nil {
			slog.Warn("unable to get agent o/p, returning same data", "error", err.Error(), "toolId", action.ToolID, "accountId", e.agentRequest.AccountId)
		}
		var dummyStatus ToolStatus
		if output == "" || output == plannerToolNoData {
			dummyStatus = ToolStatusEmptyResult
		} else {
			dummyStatus = ToolStatusSuccess
		}
		return NBAgentPlannerToolActionStep{
			Action:      action,
			Observation: output,
			Status:      dummyStatus,
		}, nil, nil
	}

	var tool toolcore.NBTool
	var ok bool

	// check if it's a client tool first
	for _, ct := range e.agentRequest.ClientTools {
		if strings.EqualFold(ct.Name, action.Tool) {
			tool = toolcore.NewClientToolWrapper(ct)
			ok = true
			break
		}
	}

	// Use Lite model for LLM tool calls (summarization) to improve performance
	actionCtx := e.ctx
	if strings.EqualFold(action.Tool, "LLM") {
		actionCtx = security.NewRequestContext(
			context.WithValue(e.ctx.GetContext(), ContextKeyUseLiteModel, true),
			e.ctx.GetSecurityContext(),
			e.ctx.GetLogger(),
			e.ctx.GetTracer(),
			e.ctx.GetMeter(),
		)
	}

	if !ok {
		tool, ok = nameToTool[strings.ToUpper(action.Tool)]
	}

	// Handle common aliases and prioritize system tools over custom agents/tools
	if !ok {
		resolvedToolName := action.Tool
		if strings.EqualFold(resolvedToolName, "shell") {
			resolvedToolName = toolcore.ToolExecuteShellCommand
		}

		// check for registered system/custom tools FIRST
		tool2, found := toolcore.GetNBTool(e.agentRequest.AccountId, resolvedToolName)
		if found && tool2 != nil {
			tool = tool2
			ok = true
		} else {
			// fallback to custom agent with same name
			agent, found := GetCustomNbAgent(e.ctx, e.agentRequest.AccountId, resolvedToolName, AgentStatusEnabled)
			if found {
				tool = NewToolFromAgent(agent)
				ok = true
			}
		}
	}

	if !ok {
		// Notebook tool calls are a known LLM hallucination — the model
		// sometimes wraps <update_notebook> content in a tool call
		// instead of using the inline XML tag. The planner should have
		// already filtered this out; this is a last-resort safety net
		// so the executor doesn't fail with "tool not found".
		if isNotebookToolName(action.Tool) {
			e.ctx.GetLogger().Info("plannerexecutor: notebook tool call intercepted as no-op", "tool", action.Tool)
			common.MetricsToolOperationsTotal(toolName, "success", accountID)
			common.MetricsToolLatencySeconds(toolName, accountID, time.Since(start).Seconds())
			return NBAgentPlannerToolActionStep{
				Action:      action,
				Observation: "Notebook content noted. Use the <update_notebook> XML tag instead of a tool call for notebook updates. Continue with your investigation.",
				Status:      ToolStatusSuccess,
			}, nil, nil
		}

		// Log available client tools for debugging
		clientToolNames := []string{}
		for _, ct := range e.agentRequest.ClientTools {
			clientToolNames = append(clientToolNames, ct.Name)
		}
		// Convert iterator to slice for JSON serialization (Go 1.23+ maps.Keys returns iter.Seq)
		serverToolNames := slices.Collect(maps.Keys(nameToTool))
		e.ctx.GetLogger().Warn("plannerexecutor: tool not found", "tool", action.Tool, "available_client_tools", clientToolNames, "available_server_tools", serverToolNames)

		// update db state as action is skipped
		_, err := GetConversationDao().SaveCompletedConversationAgentCall(uuid.Nil, e.agentRequest.ConversationId, e.agentRequest.MessageId, e.agentRequest.AccountId, e.agentRequest.UserId, action.Tool, e.agentRequest.ParentAgentId, action.ToolInput, action.Log, "tool not found - "+action.Tool, e.agentRequest.QueryContext, e.agentRequest.QueryConfig, AgentExecutionStatusFail, "tool not found - "+action.Tool)
		if err != nil {
			e.ctx.GetLogger().Error("plannerexecutor: failed to save agent call to DB", "error", err.Error())
		}
		// Metrics: record fail
		common.MetricsToolOperationsTotal(toolName, "fail", accountID)
		common.MetricsToolLatencySeconds(toolName, accountID, time.Since(start).Seconds())

		// Provide helpful observation with available tools list
		// Sanitize tool name to prevent CDATA breakout in scratchpad XML
		sanitizedToolName := strings.ReplaceAll(action.Tool, "]]>", "]] >")
		observation := fmt.Sprintf("Tool not found: '%s'\n\nAvailable server tools:\n- %s\n\nAvailable client tools:\n- %s",
			sanitizedToolName,
			strings.Join(serverToolNames, "\n- "),
			strings.Join(clientToolNames, "\n- "))
		if len(serverToolNames) == 0 && len(clientToolNames) == 0 {
			observation = fmt.Sprintf("Tool not found: '%s'. No tools are currently available.", sanitizedToolName)
		}

		return NBAgentPlannerToolActionStep{
			Action:      action,
			Observation: observation,
			Status:      ToolStatusFailure,
		}, nil, nil
	}

	// validate user access
	finish2, requestType, err2 := IsAgentToolAuthorizedToProcessRequest(e.ctx, e.agent, e.agentRequest, action)
	if err2 != nil {
		// Metrics: record fail
		common.MetricsToolOperationsTotal(toolName, "fail", accountID)
		common.MetricsToolLatencySeconds(toolName, accountID, time.Since(start).Seconds())
		return NBAgentPlannerToolActionStep{}, nil, err2
	}
	if finish2 != nil {
		// Metrics: record success (finish2 is a finish action, not an error)
		common.MetricsToolOperationsTotal(toolName, "success", accountID)
		common.MetricsToolLatencySeconds(toolName, accountID, time.Since(start).Seconds())
		return NBAgentPlannerToolActionStep{}, finish2, nil
	}

	toolResolveDur := time.Since(start)
	e.ctx.GetLogger().Info("plannerexecutor: tool resolved", "tool", action.Tool, "resolve_duration", toolResolveDur.String())
	e.ctx.GetLogger().Debug("plannerexecutor: identified request-type", logRequestType, requestType, "request", action.ToolInput, "tool", action.Tool)

	// confirmation if tehre is write operation
	if tool.GetType() == toolcore.NBToolTypeTool {
		if requestType != nil && *requestType != "" && (*requestType == toolcore.ToolRequestTypeCreate || *requestType == toolcore.ToolRequestTypeUpdate || *requestType == toolcore.ToolRequestTypeDelete) {
			isFollowupFound := false
			if e.agentRequest.QueryConfig.ToolConfirmations != nil {
				if previousData, exists := e.agentRequest.QueryConfig.ToolConfirmations[action.Tool]; exists {
					isFollowupFound = true
					previousData = strings.TrimSpace(previousData)
					if !slices.Contains([]string{"ok", "yes", "true"}, strings.ToLower(previousData)) {
						return NBAgentPlannerToolActionStep{}, &NBAgentPlannerFinishAction{
							Data:   fmt.Sprintf("user has rejected approval (response - %s) for this action, stopping", previousData),
							Status: ConversationStatusCompleted,
						}, nil
					}
				}
			}

			if !isFollowupFound {
				e.ctx.GetLogger().Info("plannerexecutor: generating followup for confirmation", logRequestType, requestType, "request", action.ToolInput, "tool", action.Tool)
				followupSteps, followupFinish, err := e.followupForToolOperationConfirmation(action, *requestType)
				if err != nil {
					e.ctx.GetLogger().Info("plannerexecutor: error in generating followup for confirmation", logRequestType, requestType, "request", action.ToolInput, "tool", action.Tool, "error", err)
					return NBAgentPlannerToolActionStep{}, nil, err
				}
				if followupFinish != nil {
					e.ctx.GetLogger().Info("plannerexecutor: generated followup for tool confirmation", "tool", action.Tool, "followup", slog.AnyValue(followupFinish))
					var fupStep NBAgentPlannerToolActionStep
					if len(followupSteps) > 0 {
						fupStep = followupSteps[0]
					}
					return fupStep, followupFinish, nil
				}
			}
		}
	}

	// [Changed for TicketV2] Previously, config checks only ran when the current tool itself
	// implemented NBToolConfig. TicketV2 agent's first tool call is often ask_clarification
	// (non-configurable), but we still need to prompt for integration/project selection before
	// the actual ticket tool runs. This fallback finds the agent's configurable tool so config
	// checks fire proactively regardless of which tool is called first.
	// Backward-compatible: for agents without configurable tools, configCheckTool == tool (no-op).
	configCheckTool := tool
	if tool.GetType() == toolcore.NBToolTypeTool {
		if _, hasConfig := tool.(toolcore.NBToolConfig); !hasConfig {
			for _, t := range e.agent.GetSupportedTools(e.ctx) {
				if _, ok := t.(toolcore.NBToolConfig); ok {
					configCheckTool = t
					break
				}
			}
		}
	}

	if configCheckTool.GetType() == toolcore.NBToolTypeTool {
		if _, hasConfig := configCheckTool.(toolcore.NBToolConfig); hasConfig {
			e.ctx.GetLogger().Debug("plannerexecutor: running config checks", "actionTool", action.Tool, "configTool", configCheckTool.Name())
			followupSteps, followupFinish, err := e.followupForMultipleToolConfigs(configCheckTool, action)
			if err != nil {
				e.ctx.GetLogger().Info("plannerexecutor: error in generating followup for multiple tool configs", "tool", configCheckTool.Name(), "error", err)
				return NBAgentPlannerToolActionStep{}, &NBAgentPlannerFinishAction{
					Data:   "Error in handling request: " + err.Error(),
					Status: ConversationStatusFailed,
				}, nil
			}
			if followupFinish != nil {
				e.ctx.GetLogger().Info("plannerexecutor: generated followup for multiple tool configs", "tool", configCheckTool.Name(), "followup", slog.AnyValue(followupFinish))
				var fupStep NBAgentPlannerToolActionStep
				if len(followupSteps) > 0 {
					fupStep = followupSteps[0]
				}
				return fupStep, followupFinish, nil
			}

		}
	}

	if e.toolCallbackHandler != nil {
		e.toolCallbackHandler.BeforeToolCall(action)
	}

	// Enhanced Task Delegation: when calling agent-type sub-tools without explicit
	// dependency context (e.g. react_3 actions), auto-inject previous step observations
	// so the sub-agent has investigation context from earlier steps.
	if queryContext == "" && tool != nil && tool.GetType() == toolcore.NBToolTypeAgent && len(e.steps) > 0 {
		var ctxBuilder strings.Builder
		for _, s := range e.steps {
			if s.Status != ToolStatusSuccess || s.Observation == "" {
				continue
			}
			ctxBuilder.WriteString(strPlanId)
			ctxBuilder.WriteString(s.Action.ToolID)
			ctxBuilder.WriteString(strToolName)
			ctxBuilder.WriteString(s.Action.Tool)
			ctxBuilder.WriteString(strQuestion)
			ctxBuilder.WriteString(sanitizeToolOutput(s.Action.ToolInput))
			ctxBuilder.WriteString(strAnswer)
			ctxBuilder.WriteString(sanitizeToolOutput(s.Observation))
			ctxBuilder.WriteByte('\n')
		}
		if ctxBuilder.Len() > 0 {
			queryContext = ctxBuilder.String()
			e.ctx.GetLogger().Info("plannerexecutor: injected previous observations as context for agent tool", "tool", action.Tool, "context_len", len(queryContext))
		}
	}

	var observation toolcore.NBToolResponse
	var err error
	toolExecStart := time.Now()

	// Optimization: If the tool is LLM (summarizer), use a direct optimized path
	if strings.EqualFold(action.Tool, "LLM") {
		e.ctx.GetLogger().Info("plannerexecutor: using optimized direct path for LLM tool", "tool", action.Tool)
		summary, summaryErr := e.fastSummarizeTool(action, queryContext)
		if summaryErr == nil {
			observation = toolcore.NBToolResponse{
				Data:   summary,
				Status: toolcore.NBToolResponseStatusSuccess,
			}
		} else {
			err = summaryErr
		}
	} else {
		observation, err = callNbTool(actionCtx, e.agentRequest, tool, action.ToolInput, nil, queryContext, action.ToolID)
	}

	toolExecDur := time.Since(toolExecStart)
	e.ctx.GetLogger().Info("plannerexecutor: tool executed", "tool", action.Tool, "toolId", action.ToolID, "exec_duration", toolExecDur.String(), "total_duration", time.Since(start).String())

	if err != nil {
		// Metrics: record fail
		common.MetricsToolOperationsTotal(toolName, "fail", accountID)
		common.MetricsToolLatencySeconds(toolName, accountID, time.Since(start).Seconds())
		return NBAgentPlannerToolActionStep{}, nil, err
	}

	// Classify status and cap tool output at the source BEFORE the callback
	// handler fires — the callback persists observation.Data to the tool-calls
	// table, so truncating later would leave untruncated rows in the DB.
	// Skip truncation for IsTerminal (final structured payload like workflow JSON)
	// and for Waiting statuses (followup question strings).
	var status ToolStatus
	if observation.Status == toolcore.NBToolResponseStatusError {
		status = ToolStatusFailure
	} else if observation.Data == "" || observation.Data == plannerToolNoData || observation.Data == "[]" {
		status = ToolStatusEmptyResult
	} else {
		status = ToolStatusSuccess
	}
	if observation.Data == "" {
		observation.Data = plannerToolNoData
	}
	if !observation.IsTerminal &&
		observation.Status != toolcore.NBToolResponseStatusWaiting &&
		observation.Status != toolcore.NBToolResponseStatusWaitingForClient {
		observation.Data = truncateToolResponse(e.ctx, observation.Data, status, toolName)
	}

	if e.toolCallbackHandler != nil {
		e.toolCallbackHandler.AfterToolCallResponse(action, observation)
	}

	if observation.Status == toolcore.NBToolResponseStatusWaiting || observation.Status == toolcore.NBToolResponseStatusWaitingForClient {
		// Metrics: record waiting
		common.MetricsToolOperationsTotal(toolName, "waiting", accountID)
		common.MetricsToolLatencySeconds(toolName, accountID, time.Since(start).Seconds())

		// check if it's a client tool and return specific waiting status
		if _, isClientTool := tool.(*toolcore.ClientToolWrapper); isClientTool || observation.Status == toolcore.NBToolResponseStatusWaitingForClient {
			e.ctx.GetLogger().Info("plannerexecutor: client tool call recorded, waiting for client execution", "tool", action.Tool)
			return NBAgentPlannerToolActionStep{
					Action:      action,
					Observation: "Waiting for client execution",
					Status:      ToolStatusWaitingForClient,
				}, &NBAgentPlannerFinishAction{
					Status: ConversationStatusWaitingForClientTool,
					Data:   fmt.Sprintf("Waiting for client to execute tool: %s", action.Tool),
					AdditionalDetails: map[string]any{
						"tool_name":  action.Tool,
						"tool_input": action.ToolInput,
						"tool_id":    action.ToolID,
					},
				}, nil
		}

		followUpRequest := observation.AdditionalDetails[nbToolCallAdditionalDatailsFollowupRequest].(FollowupRequest)
		if followUpRequest.AgentName == "" {
			followUpRequest.AgentName = e.agent.GetName()
		}
		if followUpRequest.AgentId == uuid.Nil {
			agentUUID, err := uuid.Parse(e.agentRequest.AgentId)
			if err == nil {
				followUpRequest.AgentId = agentUUID
			}
		}

		// CRITICAL: Ensure the followup is associated with the PARENT's tool ID (e.g. E1)
		// rather than the sub-agent's internal tool ID. This ensures that when the user
		// responds, the result is saved under the ID the planner is actually waiting for.
		followUpRequest.ToolId = action.ToolID

		return NBAgentPlannerToolActionStep{
				Action:      action,
				Observation: observation.Data,
				Status:      ToolStatusWaiting,
				Followup:    &followUpRequest,
			}, &NBAgentPlannerFinishAction{
				Data:              observation.Data,
				Status:            ConversationStatusWaiting,
				Followup:          followUpRequest,
				AdditionalDetails: observation.AdditionalDetails,
			}, nil
	}

	// Metrics: record success
	common.MetricsToolOperationsTotal(toolName, "success", accountID)
	common.MetricsToolLatencySeconds(toolName, accountID, time.Since(start).Seconds())

	result := NBAgentPlannerToolActionStep{
		Action:      action,
		Observation: observation.Data,
		Status:      status,
		IsTerminal:  observation.IsTerminal,
		References:  observation.References,
	}

	// Cache successful results under both original and rewritten inputs
	if status == ToolStatusSuccess || status == ToolStatusEmptyResult {
		e.toolCallCache.Put(action.Tool, originalInput, result)
		if action.ToolInput != originalInput {
			e.toolCallCache.Put(action.Tool, action.ToolInput, result)
		}
	}

	return result, nil, nil
}

func (e *plannerExecutor) followupForToolOperationConfirmation(action NBAgentPlannerToolAction, toolRequestType toolcore.ToolRequestType) ([]NBAgentPlannerToolActionStep, *NBAgentPlannerFinishAction, error) {
	followupRequest, err := FollowupRequestForToolOperationConfirmation(e.ctx, e.agentRequest, e.agent, action, toolRequestType)
	if err != nil {
		e.ctx.GetLogger().Error(logErrUnableToGenerateFup, "error", err)
		return nil, nil, err
	}
	followupId, err := GenerateFollowup(e.ctx, e.agentRequest, followupRequest)
	if err != nil {
		e.ctx.GetLogger().Error(logErrUnableToGenerateFup, "error", err)
		return nil, nil, err
	}
	return []NBAgentPlannerToolActionStep{
			{
				Action:      action,
				Observation: followupRequest.Question,
				Status:      ToolStatusWaiting,
				Followup:    &followupRequest,
			},
		}, &NBAgentPlannerFinishAction{
			Data:     followupRequest.Question,
			Status:   ConversationStatusWaiting,
			Followup: followupRequest,
			AdditionalDetails: map[string]any{
				"followupId": followupId,
			},
		}, nil
}

// selectConfigUsingLLM uses LLM to intelligently select the most appropriate config
// based on the user's original query and available configurations.
// Returns the selected config name, or empty string if the LLM is uncertain.
func (e *plannerExecutor) selectConfigUsingLLM(userQuery string, configs []toolcore.ToolConfig, toolName string) string {
	if len(configs) == 0 || userQuery == "" {
		return ""
	}

	// Build a description of available configs for the LLM
	// Include tags and non-sensitive config values to help match error logs
	var configDescriptions strings.Builder
	for i, config := range configs {
		fmt.Fprintf(&configDescriptions, "%d. **%s**", i+1, config.Name)

		// Add tags if available (e.g., environment, region, purpose)
		if len(config.Tags) > 0 {
			var tagParts []string
			for k, v := range config.Tags {
				tagParts = append(tagParts, fmt.Sprintf("%s: %s", k, v))
			}
			fmt.Fprintf(&configDescriptions, " (%s)", strings.Join(tagParts, ", "))
		}

		// Add non-sensitive config values (e.g., host, database, region)
		// Skip encrypted values for security
		var nonSensitiveValues []string
		for _, val := range config.Values {
			if !val.IsEncrypted && val.Value != "" {
				// Include values that help identify environment (host, database, region, etc.)
				// Skip sensitive fields like passwords, tokens, secrets, credentials, auth
				lowerName := strings.ToLower(val.Name)
				if !strings.Contains(lowerName, "password") &&
					!strings.Contains(lowerName, "secret") &&
					!strings.Contains(lowerName, "token") &&
					!strings.Contains(lowerName, "key") &&
					!strings.Contains(lowerName, "credential") &&
					!strings.Contains(lowerName, "auth") &&
					!strings.Contains(lowerName, "api_key") &&
					!strings.Contains(lowerName, "access") {
					nonSensitiveValues = append(nonSensitiveValues, fmt.Sprintf("%s=%s", val.Name, val.Value))
				}
			}
		}
		if len(nonSensitiveValues) > 0 {
			configDescriptions.WriteString(" [")
			configDescriptions.WriteString(strings.Join(nonSensitiveValues, ", "))
			configDescriptions.WriteString("]")
		}

		configDescriptions.WriteString("\n")
	}

	// Build context from previous tool executions
	const truncatedSuffix = " [truncated]"

	contextSteps := config.Config.LlmConfigAutoSelectionContextSteps
	maxObservationLength := config.Config.LlmConfigAutoSelectionMaxObservationLen
	var executionContext strings.Builder
	var contextInstruction string
	var headerText string
	var questionText string

	// Check if we have investigation context
	contextPart := ""
	if contextSteps > 0 && len(e.steps) > 0 {
		contextPart = " and investigation context"

		// Build investigation context
		executionContext.WriteString("\nPrevious investigation context:\n")

		stepsToProcess := e.steps
		if len(stepsToProcess) > contextSteps {
			stepsToProcess = stepsToProcess[len(stepsToProcess)-contextSteps:]
		}

		for _, step := range stepsToProcess {
			// Truncate long observations to keep context focused
			observation := step.Observation
			if len(observation) > maxObservationLength {
				observation = observation[:maxObservationLength] + truncatedSuffix
			}
			fmt.Fprintf(&executionContext, "- %s: %s\n", step.Action.Tool, observation)
		}

		contextInstruction = `- **Important**: Also, Use the investigation context to understand which environment/cluster is being investigated
- **Important**: Look for patterns like 'dev' or 'prod' in bot names, repo attempts, or deployment updates, even if indirect.
- If one environment has signals and others have none, prefer the signaled environment config.`
	} else {
		contextInstruction = ""
	}

	headerText = fmt.Sprintf("You are helping to select the appropriate configuration based on the user's input%s. The task is to identify the the correct configuration based on the context below.\n\n", contextPart)
	questionText = fmt.Sprintf("Based on the user's input%s, which configuration should be used?\n\n", contextPart)

	// Use the prompt template
	promptText := prompts_repo.GetPrompt(
		prompts_repo.PromptToolConfigAutoSelection,
		headerText,
		userQuery,
		toolName,
		configDescriptions.String(),
		executionContext.String(),
		questionText,
		contextInstruction,
	)

	// Create LLM messages
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, promptText),
	}

	e.ctx.GetLogger().Debug("selectConfigUsingLLM: attempting LLM-based config selection",
		"tool", toolName,
		"query", userQuery,
		"configCount", len(configs))

	// Call LLM
	resp, err := GenerateAndTrackLLMContent(
		e.ctx,
		e.agentRequest.UserId,
		e.agentRequest.AccountId,
		e.agentRequest.ConversationId,
		e.agentRequest.MessageId,
		e.agentRequest.AgentId,
		true,
		messages,
		true, // cleanup markdown
	)

	if err != nil {
		e.ctx.GetLogger().Warn("selectConfigUsingLLM: LLM call failed", "error", err, "tool", toolName)
		return ""
	}

	if resp == nil || len(resp.Choices) == 0 {
		e.ctx.GetLogger().Warn("selectConfigUsingLLM: empty LLM response", "tool", toolName)
		return ""
	}

	// Extract and validate the response
	selectedConfig := strings.TrimSpace(resp.Choices[0].Content)
	selectedConfig = strings.Trim(selectedConfig, "\"'`") // Remove quotes if present

	// Check if LLM is uncertain
	if strings.ToUpper(selectedConfig) == "UNCERTAIN" {
		e.ctx.GetLogger().Debug("selectConfigUsingLLM: LLM indicated uncertainty", "tool", toolName)
		return ""
	}

	// Validate that the selected config exists
	for _, config := range configs {
		if config.Name == selectedConfig {
			e.ctx.GetLogger().Info("selectConfigUsingLLM: successfully selected config using LLM",
				"tool", toolName,
				"selectedConfig", selectedConfig,
				"query", userQuery)
			return selectedConfig
		}
	}

	// LLM returned a config that doesn't exist - log warning
	e.ctx.GetLogger().Warn("selectConfigUsingLLM: LLM returned non-existent config",
		"tool", toolName,
		"selectedConfig", selectedConfig,
		"availableConfigs", len(configs))
	return ""
}

func (e *plannerExecutor) followupForMultipleToolConfigs(tool toolcore.NBTool, action NBAgentPlannerToolAction) ([]NBAgentPlannerToolActionStep, *NBAgentPlannerFinishAction, error) {
	// Get available configs for the tool
	configs, err := toolcore.ListToolConfigs(e.ctx, e.agentRequest.AccountId, tool)
	if err != nil {
		e.ctx.GetLogger().Error("plannerexecutor: unable to list tool configs", "tool", tool.Name(), "error", err)
		return nil, nil, nil
	}

	// If there are no configs, return an error step so the agent can inform the user
	if len(configs) == 0 {
		errMsg := fmt.Sprintf("Tool %s requires configuration (e.g., an integration) but none has been set up for this account. Please configure the integration first.", tool.Name())
		return []NBAgentPlannerToolActionStep{
			{
				Action:      action,
				Observation: errMsg,
				Status:      ToolStatusFailure,
			},
		}, nil, nil
	}

	// If there's exactly one config, no need to ask the user — it will be auto-selected
	if len(configs) == 1 {
		return nil, nil, nil
	}

	// Config already specified, no need to ask
	if e.agentRequest.QueryConfig.ToolConfigs != nil {
		if val := e.agentRequest.QueryConfig.ToolConfigs[tool.Name()]; val != "" {
			return nil, nil, nil
		}
	}

	// Use the original user query for config selection. Sub-agents receive a
	// planner-rewritten query (e.g. "List all EC2 instances") which strips user
	// hints like "(dev-aws)". The original query is preserved in QueryConfig
	// and propagated automatically to sub-agents.
	originalUserQuery := e.agentRequest.QueryConfig.OriginalUserQuery
	if originalUserQuery == "" {
		originalUserQuery = e.agentRequest.Query
	}
	if originalUserQuery != e.agentRequest.Query {
		e.ctx.GetLogger().Info("plannerexecutor: using original user query for config selection",
			"tool", tool.Name(), "originalQuery", originalUserQuery, "agentQuery", e.agentRequest.Query)
	}

	// persistResolvedConfigAsync writes the resolved config to message_config in the
	// background so it survives mid-execution crashes and is available on resumption.
	// Deep-copies maps to avoid races with subsequent steps modifying the same QueryConfig.
	persistResolvedConfigAsync := func() {
		messageId := e.agentRequest.MessageId
		queryConfig := e.agentRequest.QueryConfig // value copy of struct

		// Deep-copy map fields to prevent concurrent map read/write with later steps
		if len(queryConfig.ToolConfigs) > 0 {
			tc := make(map[string]string, len(queryConfig.ToolConfigs))
			for k, v := range queryConfig.ToolConfigs {
				tc[k] = v
			}
			queryConfig.ToolConfigs = tc
		}
		if len(queryConfig.ToolConfigMetadata) > 0 {
			md := make(map[string]any, len(queryConfig.ToolConfigMetadata))
			for k, v := range queryConfig.ToolConfigMetadata {
				md[k] = v
			}
			queryConfig.ToolConfigMetadata = md
		}

		go func() {
			if err := GetConversationDao().UpdateConversationMessageConfig(messageId, queryConfig); err != nil {
				slog.Warn("plannerexecutor: failed to persist resolved config", "error", err, "message_id", messageId)
			}
		}()
	}

	// Strategy 3.5: Check if the user explicitly named a config or ID in their query
	// This has highest priority because it's the most direct expression of user intent
	// Try with original user query first (has hints like "dev-aws"), then fall back to agent query
	for _, query := range uniqueQueries(originalUserQuery, e.agentRequest.Query) {
		matched := findConfigInQuery(query, configs)
		if matched != nil {
			if e.agentRequest.QueryConfig.ToolConfigs == nil {
				e.agentRequest.QueryConfig.ToolConfigs = make(map[string]string)
			}
			e.agentRequest.QueryConfig.ToolConfigs[tool.Name()] = matched.Name
			recordConfigSelectionStrategy(&e.agentRequest.QueryConfig, tool.Name(), "explicit_name_match")
			e.ctx.GetLogger().Info("plannerexecutor: resolved tool config via explicit match in query",
				"tool", tool.Name(), "config", matched.Name, "matchedQuery", query)
			persistResolvedConfigAsync()
			return nil, nil, nil
		}
	}

	// If the tool can identify its own config, try to resolve it
	if configIdentifier, ok := tool.(toolcore.NBToolConfigIdentifier); ok {
		// Use the original user query for config identification — it contains natural-language
		// hints (e.g. "dev-pg") that IdentifyConfig's keyword matching can pick up.
		// Fall back to action.ToolInput for cases where the LLM-generated tool arguments
		// carry more specific info (e.g. explicit "instance" field in JSON args).
		toolContext := toolcore.NewNbToolContext(e.ctx, tool, e.agentRequest.AccountId, e.agentRequest.UserId, e.agentRequest.ConversationId, e.agentRequest.MessageId, e.agentRequest.AgentId, originalUserQuery, nil, e.agentRequest.QueryContext, e.agentRequest.QueryConfig, action.ToolID)
		toolContext.SessionId = e.agentRequest.SessionId

		request := toolcore.NBToolCallRequest{Command: action.ToolInput}

		identifiedConfig, err := configIdentifier.IdentifyConfig(toolContext, request, configs)
		if err != nil {
			e.ctx.GetLogger().Error("plannerexecutor: IdentifyConfig failed", "tool", tool.Name(), "error", err)
		}

		// Fallback: retry with action.ToolInput if original user query didn't resolve
		if identifiedConfig.Name == "" && action.ToolInput != "" && action.ToolInput != originalUserQuery {
			toolContext.Query = action.ToolInput
			identifiedConfig, err = configIdentifier.IdentifyConfig(toolContext, request, configs)
			if err != nil {
				e.ctx.GetLogger().Error("plannerexecutor: IdentifyConfig failed with tool input fallback", "tool", tool.Name(), "error", err)
			}
		}

		if identifiedConfig.Name != "" {
			if e.agentRequest.QueryConfig.ToolConfigs == nil {
				e.agentRequest.QueryConfig.ToolConfigs = make(map[string]string)
			}
			e.agentRequest.QueryConfig.ToolConfigs[tool.Name()] = identifiedConfig.Name

			recordConfigSelectionStrategy(&e.agentRequest.QueryConfig, tool.Name(), "keyword_matching")

			e.ctx.GetLogger().Info("plannerexecutor: resolved tool config using IdentifyConfig", "tool", tool.Name(), "config", identifiedConfig.Name)
			persistResolvedConfigAsync()
			return nil, nil, nil
		}
	}

	// Strategy 4: Try LLM-based config selection for complex queries (if enabled)
	// This handles scenarios where keyword matching fails but the query still provides enough context
	if config.Config.LlmConfigAutoSelectionEnabled && originalUserQuery != "" {
		selectedConfigName := e.selectConfigUsingLLM(originalUserQuery, configs, tool.Name())
		if selectedConfigName != "" {
			// LLM successfully selected a config
			if e.agentRequest.QueryConfig.ToolConfigs == nil {
				e.agentRequest.QueryConfig.ToolConfigs = make(map[string]string)
			}
			e.agentRequest.QueryConfig.ToolConfigs[tool.Name()] = selectedConfigName

			recordConfigSelectionStrategy(&e.agentRequest.QueryConfig, tool.Name(), "llm_based")

			e.ctx.GetLogger().Info("plannerexecutor: resolved tool config using LLM-based selection",
				"tool", tool.Name(),
				"config", selectedConfigName,
				"strategy", "llm_based")
			persistResolvedConfigAsync()
			return nil, nil, nil
		}
	} else if !config.Config.LlmConfigAutoSelectionEnabled {
		e.ctx.GetLogger().Debug("plannerexecutor: LLM config auto-selection disabled via feature flag",
			"tool", tool.Name())
	}

	// Fallback to existing logic: ask the user to choose
	followupRequest, err := FollowupRequestForMultipleToolConfigs(e.ctx, e.agentRequest, e.agent, action)
	if err != nil {
		return nil, nil, err
	}
	if len(followupRequest.Question) > 0 {
		followupId, err := GenerateFollowup(e.ctx, e.agentRequest, followupRequest)
		if err != nil {
			e.ctx.GetLogger().Error(logErrUnableToGenerateFup, "error", err)
			return nil, nil, err
		}
		return []NBAgentPlannerToolActionStep{
				{
					Action:      action,
					Observation: followupRequest.Question,
					Status:      ToolStatusWaiting,
					Followup:    &followupRequest,
				},
			}, &NBAgentPlannerFinishAction{
				Data:     followupRequest.Question,
				Status:   ConversationStatusWaiting,
				Followup: followupRequest,
				AdditionalDetails: map[string]any{
					"followupId": followupId,
				},
			}, nil
	}
	return nil, nil, nil
}

// isToolConfigResolved checks whether any tool config or project selection was just
// resolved in ToolConfigs. The action.Tool might be different from the configurable tool
// (e.g. action.Tool="ask_clarification" but config stored under "ticket_master_v2"),
// so we check both the action's tool name and all keys in ToolConfigs.
func isToolConfigResolved(toolConfigs map[string]string, toolName string) bool {
	if len(toolConfigs) == 0 {
		return false
	}
	// Direct match: action's tool name
	if toolConfigs[toolName] != "" {
		return true
	}
	// Broader check: any tool_config entry exists (covers cases where
	// the action tool differs from the configurable tool, e.g. ask_clarification
	// was the stored action but the config was resolved for ticket_master_v2).
	for range toolConfigs {
		return true
	}
	return false
}

// isChildAgentCompleted returns true iff the parent's tool_call row for
// (conversationId, parentAgentId, toolId) has a child_agent_id whose own
// llm_conversation_agent row has terminated (SUCCESS or FAILED). A FAILED
// child is also considered "completed" for re-dispatch avoidance — we want
// the parent to see the failure observation, not re-invoke the sub-agent
// which would create a fresh run and potentially a duplicate followup.
// Used during parent resume (#28141).
// childOutcome summarizes a sub-agent's terminal state for the parent's
// WAITING-tool-call fallback path (see the bubble-up site in executeAgentPlanner).
type childOutcome struct {
	// completed is true when the child reached a terminal state (success or fail).
	completed bool
	// status is the lowercase terminal status: "success" or "fail".
	// Empty when the child has no row or is not yet terminal.
	status string
	// response is the observation to surface to the parent. Never empty —
	// an empty response would cause the parent to re-dispatch the
	// sub-agent (observed as the duplicate-followup loop in #28141).
	response string
}

// getChildAgentOutcome consolidates the three previously-separate helpers
// (isChildAgentCompleted, childAgentTerminalStatus, getChildAgentResponse)
// into a single call site. Each helper did the same two DB lookups --
// GetConversationToolCallChildAgentId and ListConversationAgents --
// so invoking all three sequentially produced 6 queries where 2 suffice.
//
// The caller is executeAgentPlanner's WAITING-tool-call fallback that
// handles the case where the parent's tool_call is still WAITING but the
// sub-agent itself has terminated via its own followup resume.
func getChildAgentOutcome(conversationId, parentAgentId, toolId string) childOutcome {
	dao := GetConversationDao()
	childAgentId := dao.GetConversationToolCallChildAgentId(conversationId, parentAgentId, toolId)
	if childAgentId == "" {
		return childOutcome{response: "Sub-agent completed but no response recorded."}
	}
	childAgents, err := dao.ListConversationAgents("", childAgentId)
	if err != nil || len(childAgents) == 0 {
		return childOutcome{response: "Sub-agent completed but record could not be loaded."}
	}
	child := childAgents[0]
	status := strings.ToLower(string(child.Status))
	terminal := status == string(AgentExecutionStatusSuccess) || status == string(AgentExecutionStatusFail)

	// Build the response observation.
	resp := ""
	if child.Response != nil && *child.Response != "" {
		resp = *child.Response
	} else if status == string(AgentExecutionStatusFail) {
		resp = fmt.Sprintf("Sub-agent %q failed without a response.", child.AgentName)
	} else {
		resp = fmt.Sprintf("Sub-agent %q completed without producing a response.", child.AgentName)
	}

	out := childOutcome{completed: terminal, response: resp}
	if terminal {
		out.status = status
	}
	return out
}

// lookupChildOutcomeIfWaiting gates the getChildAgentOutcome DB fetch on the
// tool's WAITING status. Without this gate, the outcome fetch (two queries)
// ran for every tool during resumption — including COMPLETED and fresh tools
// that have no sub-agent to consult — needlessly increasing DB load.
// Returns (outcome, true) only when the tool is WAITING AND the child has
// reached a terminal state. In every other case returns ok=false so callers
// can fall through to normal dispatch.
func lookupChildOutcomeIfWaiting(conversationId, parentAgentId, toolId string, toolStatus toolcore.NBToolResponseStatus) (childOutcome, bool) {
	if !strings.EqualFold(string(toolStatus), string(toolcore.NBToolResponseStatusWaiting)) {
		return childOutcome{}, false
	}
	out := getChildAgentOutcome(conversationId, parentAgentId, toolId)
	if !out.completed {
		return childOutcome{}, false
	}
	return out, true
}

func (e *plannerExecutor) GetToolInvocations() []ToolInvocation {
	invocations := []ToolInvocation{}
	for _, step := range e.steps {
		call := step.Action
		response := step.Observation

		invocations = append(invocations, ToolInvocation{
			Call: llms.ToolCall{
				ID:   call.ToolID,
				Type: "function",
				FunctionCall: &llms.FunctionCall{
					Name:      call.Tool,
					Arguments: call.ToolInput,
				},
			},
			Response: llms.ToolCallResponse{
				ToolCallID: call.ToolID,
				Name:       call.Tool,
				Content:    response,
			},
			Log:        call.Log,
			References: step.References,
		})
	}

	return invocations
}

func (e *plannerExecutor) Marshal() ([]byte, error) {
	plannerState, err := e.agentPlanner.Marshal()
	if err != nil {
		e.ctx.GetLogger().Error("plannerexecutor: failed to marshal agent planner state", "error", err)
	}

	data := map[string]any{
		"steps":            e.steps,
		"maxIterations":    e.maxIterations,
		"currentIteration": e.currentIteration,
		"stepKeys":         e.stepKeys,
		"currentAction":    e.currentAction,
		"plannerState":     plannerState,
	}
	jsonBytes, err := common.MarshalJson(data)
	if err != nil {
		return nil, err
	}
	return jsonBytes, nil
}

func (e *plannerExecutor) Unmarshal(previousState []byte) error {
	var dataMap map[string]any
	err := common.UnmarshalJson(previousState, &dataMap)
	if err != nil {
		return fmt.Errorf("failed to unmarshal planner executor data: %w", err)
	}

	if plannerStateRaw, ok := dataMap["plannerState"]; ok && plannerStateRaw != nil {
		var plannerStateBytes []byte
		if str, ok := plannerStateRaw.(string); ok {
			// common.MarshalJson base64 encodes the []byte
			var err error
			plannerStateBytes, err = base64.StdEncoding.DecodeString(str)
			if err != nil {
				e.ctx.GetLogger().Error("plannerexecutor: failed to decode base64 planner state", "error", err)
				// Fallback to raw bytes cast in case it wasn't actually base64
				plannerStateBytes = []byte(str)
			}
		} else if b, ok := plannerStateRaw.([]byte); ok {
			plannerStateBytes = b
		}

		if len(plannerStateBytes) > 0 {
			err := e.agentPlanner.Unmarshal(plannerStateBytes)
			if err != nil {
				e.ctx.GetLogger().Error("plannerexecutor: failed to unmarshal agent planner state", "error", err)
			}
		}
	}

	// Helper to get value from map with robust key lookup (case-insensitive, snake_case, and planner aliases)
	getVal := func(m map[string]any, key string) any {
		if v, ok := m[key]; ok {
			return v
		}
		lowerKey := strings.ToLower(key)
		if v, ok := m[lowerKey]; ok {
			return v
		}

		// Handle snake_case variants for common fields
		switch lowerKey {
		case "toolid":
			if v, ok := m["tool_id"]; ok {
				return v
			}
			if v, ok := m["id"]; ok { // Planner alias
				return v
			}
		case "toolinput":
			if v, ok := m["tool_input"]; ok {
				return v
			}
			if v, ok := m["query"]; ok { // Planner alias
				return v
			}
		case "log":
			if v, ok := m["reason"]; ok { // Planner alias
				return v
			}
			if v, ok := m["plan"]; ok { // Legacy planner alias
				return v
			}
		case "isterminal":
			if v, ok := m["is_terminal"]; ok {
				return v
			}
		case "expected_response":
			if v, ok := m["expected_response"]; ok {
				return v
			}
		case "allowed_responses":
			if v, ok := m["allowed_responses"]; ok {
				return v
			}
		case "followup":
			if v, ok := m["followup"]; ok {
				return v
			}
		}

		return nil
	}

	// Unmarshal steps
	if stepsData, ok := dataMap["steps"].([]any); ok {
		for _, stepData := range stepsData {
			stepMap, ok := stepData.(map[string]any)
			if !ok {
				return errors.New("invalid step data format")
			}

			// Handle both "Action" (legacy) and "action" (new)
			actionRaw := getVal(stepMap, "Action")
			actionData, ok := actionRaw.(map[string]any)
			if !ok {
				return errors.New("invalid step data format action")
			}

			// Ensure default values for optional fields if missing
			logValue := ""
			if logVal, ok := getVal(actionData, "Log").(string); ok {
				logValue = logVal
			}

			var dependencies []string
			if depVal, ok := getVal(actionData, "Dependency").([]any); ok {
				for _, d := range depVal {
					if depStr, ok := d.(string); ok {
						dependencies = append(dependencies, depStr)
					}
				}
			}

			// Unmarshal Condition
			var actionCondition NBAgentPlannerToolActionCondition
			condRaw := getVal(actionData, "Condition")
			if condRaw != nil {
				if condMap, ok := condRaw.(map[string]any); ok {
					if exprVal, exprOk := getVal(condMap, "Expression").(string); exprOk {
						actionCondition.Expression = exprVal
					}
					if promptVal, promptOk := getVal(condMap, "Prompt").(string); promptOk {
						actionCondition.Prompt = promptVal
					}
					if expectedRespVal, erOk := getVal(condMap, "ExpectedResponse").(string); erOk {
						actionCondition.ExpectedResponse = expectedRespVal
					}
					if allowedRespsVal, arOk := getVal(condMap, "AllowedResponses").([]any); arOk {
						for _, r := range allowedRespsVal {
							if respStr, rStrOk := r.(string); rStrOk {
								actionCondition.AllowedResponses = append(actionCondition.AllowedResponses, respStr)
							}
						}
					}
				}
			}

			action := NBAgentPlannerToolAction{
				ToolID:     toString(getVal(actionData, "ToolID")),
				Tool:       toString(getVal(actionData, "Tool")),
				ToolInput:  toString(getVal(actionData, "ToolInput")),
				Log:        logValue,
				Dependency: dependencies,
				Condition:  actionCondition,
			}

			status := ToolStatusSuccess // default
			if statusVal, ok := getVal(stepMap, "Status").(string); ok {
				status = ToolStatus(statusVal)
			}

			isTerminal := false
			if itVal, ok := getVal(stepMap, "IsTerminal").(bool); ok {
				isTerminal = itVal
			}

			var restoredFollowup *FollowupRequest
			if fupRaw := getVal(stepMap, "Followup"); fupRaw != nil {
				if fupMap, ok := fupRaw.(map[string]any); ok {
					fupBytes, _ := common.MarshalJson(fupMap)
					var fup FollowupRequest
					if err := common.UnmarshalJson(fupBytes, &fup); err == nil {
						restoredFollowup = &fup
					}
				}
			}

			var references []toolcore.NBToolResponseReference
			refsRaw := getVal(stepMap, "References")
			if refsData, ok := refsRaw.([]any); ok {
				for _, rd := range refsData {
					if refMap, ok := rd.(map[string]any); ok {
						references = append(references, toolcore.NBToolResponseReference{
							Text:        toString(getVal(refMap, "text")),
							Url:         toString(getVal(refMap, "url")),
							Type:        toString(getVal(refMap, "type")),
							Description: toString(getVal(refMap, "description")),
							Query:       toString(getVal(refMap, "query")),
						})
					}
				}
			}

			step := NBAgentPlannerToolActionStep{
				Action:      action,
				Observation: toString(getVal(stepMap, "Observation")),
				Status:      status,
				IsTerminal:  isTerminal,
				References:  references,
				Followup:    restoredFollowup,
			}
			e.steps = append(e.steps, step)
		}
	}

	if currentActionData, ok := dataMap["currentAction"].([]any); ok {
		for _, actionData := range currentActionData {
			actionMap, ok := actionData.(map[string]any) // actionMap is an individual action from currentAction
			if !ok {
				return errors.New("invalid currentAction data format")
			}

			// Ensure default values for optional fields if missing
			logValue := ""
			if logVal, ok := getVal(actionMap, "Log").(string); ok {
				logValue = logVal
			}

			var dependencies []string
			if depVal, ok := getVal(actionMap, "Dependency").([]any); ok {
				for _, d := range depVal {
					if depStr, ok := d.(string); ok {
						dependencies = append(dependencies, depStr)
					}
				}
			}

			// Unmarshal Condition
			var actionCondition NBAgentPlannerToolActionCondition
			condRaw := getVal(actionMap, "Condition")
			if condRaw != nil {
				if condMap, ok := condRaw.(map[string]any); ok {
					if exprVal, exprOk := getVal(condMap, "Expression").(string); exprOk {
						actionCondition.Expression = exprVal
					}
					if promptVal, promptOk := getVal(condMap, "Prompt").(string); promptOk {
						actionCondition.Prompt = promptVal
					}
					if expectedRespVal, erOk := getVal(condMap, "ExpectedResponse").(string); erOk {
						actionCondition.ExpectedResponse = expectedRespVal
					}
					if allowedRespsVal, arOk := getVal(condMap, "AllowedResponses").([]any); arOk {
						for _, r := range allowedRespsVal {
							if respStr, rStrOk := r.(string); rStrOk {
								actionCondition.AllowedResponses = append(actionCondition.AllowedResponses, respStr)
							}
						}
					}
				}
			}

			action := NBAgentPlannerToolAction{
				ToolID:     toString(getVal(actionMap, "ToolID")),
				Tool:       toString(getVal(actionMap, "Tool")),
				ToolInput:  toString(getVal(actionMap, "ToolInput")),
				Log:        logValue,
				Dependency: dependencies,
				Condition:  actionCondition,
			}
			e.currentAction = append(e.currentAction, action)
		}
	}

	if v, ok := parseIntFromMap(dataMap, "maxIterations"); ok {
		e.maxIterations = v
	}
	if v, ok := parseIntFromMap(dataMap, "currentIteration"); ok {
		e.currentIteration = v
	}

	// Restore stepKeys from serialized data or rebuild from restored steps.
	// Without this, stepKeys stays nil after unmarshal and any write panics
	// with "assignment to entry in nil map".
	if e.stepKeys == nil {
		e.stepKeys = make(map[string]bool)
	}
	if skData, ok := dataMap["stepKeys"].(map[string]any); ok {
		for k, v := range skData {
			if boolVal, ok := v.(bool); ok {
				e.stepKeys[k] = boolVal
			}
		}
	}
	// Belt-and-suspenders: ensure every restored step is in stepKeys
	for _, step := range e.steps {
		if step.Action.ToolID != "" {
			e.stepKeys[step.Action.ToolID] = true
		}
	}

	return nil
}

func parseIntFromMap(m map[string]any, key string) (int, bool) {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case float64:
			return int(val), true
		case int:
			return val, true
		}
	}
	return 0, false
}

func toString(val any) string {
	if val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func inputsToString(inputValues map[string]any) (map[string]string, error) {
	inputs := make(map[string]string, len(inputValues))
	for key, value := range inputValues {
		valueStr, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %s", agents.ErrExecutorInputNotString, key)
		}

		inputs[key] = valueStr
	}

	return inputs, nil
}

func callNbTool(nbRequestContext *security.RequestContext, agentRequest NBAgentRequest, tool toolcore.NBTool, input string, previousHistory []llms.MessageContent, queryContext string, toolId string) (resp toolcore.NBToolResponse, err error) {
	// Recover from panics and convert to proper error responses
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			err = fmt.Errorf("callNbTool panic recovered: %v", r)
			if nbRequestContext != nil && nbRequestContext.GetLogger() != nil {
				nbRequestContext.GetLogger().Error("plannerexecutor: tool call panic", "error", err, "tool", tool.Name(), "stack", string(stack))
			}
			// Convert panic to NBToolResponse with error status
			resp = toolcore.NBToolResponse{
				Status: toolcore.NBToolResponseStatusError,
				Data:   fmt.Sprintf("Tool execution failed: %v", r),
			}
		}
	}()

	// Validate inputs
	if nbRequestContext == nil {
		return toolcore.NBToolResponse{}, fmt.Errorf("nil request context")
	}

	if tool == nil {
		return toolcore.NBToolResponse{}, fmt.Errorf("nil tool")
	}

	// Validate required agent request fields
	if agentRequest.AccountId == "" || agentRequest.ConversationId == "" || agentRequest.MessageId == "" {
		nbRequestContext.GetLogger().Error("plannerexecutor: invalid agent request, missing required fields", "agentRequest", slog.AnyValue(agentRequest))
		return toolcore.NBToolResponse{}, fmt.Errorf("invalid agent request: missing required fields")
	}

	if queryContext == "" {
		queryContext = agentRequest.QueryContext
	}

	if input == "" {
		nbRequestContext.GetLogger().Error("plannerexecutor: empty tool input", "tool", tool.Name())
		return toolcore.NBToolResponse{
			Status: toolcore.NBToolResponseStatusError,
			Data:   "Empty tool input",
		}, nil
	}

	toolContext := toolcore.NewNbToolContext(nbRequestContext, tool, agentRequest.AccountId, agentRequest.UserId, agentRequest.ConversationId, agentRequest.MessageId, agentRequest.AgentId, input, previousHistory, queryContext, agentRequest.QueryConfig, toolId)
	toolContext.AccountPrompt = agentRequest.AccountPrompt
	toolContext.SessionId = agentRequest.SessionId

	// Check if this tool requires configuration
	if _, ok := tool.(toolcore.NBToolConfig); ok {
		// Check if we have a valid tool config
		config := toolContext.ToolConfig
		if !reflect.ValueOf(config).IsValid() || config.Name == "" {
			return toolcore.NBToolResponse{
				Status: toolcore.NBToolResponseStatusError,
				Data:   fmt.Sprintf("Tool %s requires configuration but none was found", tool.Name()),
			}, nil
		}
	}

	request := toolcore.NBToolCallRequest{
		Command: input,
	}
	// handling around multi-tool support
	if _, ok := tool.(toolcore.NBMultiCommandTool); ok {
		err := common.UnmarshalJson([]byte(input), &request)
		if err != nil {
			nbRequestContext.GetLogger().Error("plannerexecutor: invalid input format for tool", "error", err, "data", input, "tool", tool.Name())
			return toolcore.NBToolResponse{}, err
		}
	} else {
		// request is getting generated in new format of reqeust {"command":""}
		request1 := map[string]any{}
		err := common.UnmarshalJson([]byte(input), &request1)
		if err == nil {
			if cmd, ok := request1["command"].(string); ok && cmd != "" {
				request.Command = cmd
				if request.Arguments == nil {
					request.Arguments = map[string]any{}
				}
				for k, v := range request1 {
					if k == "command" {
						continue
					}
					request.Arguments[k] = v
				}
				// fallback, incase sometimes command is generates as map[string]any instead of serialized string
			} else if cmd, ok := request1["command"].(map[string]any); ok && cmd != nil {
				if commandBytes, err := common.MarshalJson(cmd); err == nil {
					request.Command = string(commandBytes)
					if request.Arguments == nil {
						request.Arguments = map[string]any{}
					}
					for k, v := range request1 {
						if k == "command" {
							continue
						}
						request.Arguments[k] = v
					}
				}
			} else if len(request1) > 0 {
				// Fallback: JSON has no "command" key but has other properties
				// (e.g. {"id": "...", "limit": 1}). Use all properties as Arguments.
				request.Arguments = request1
			}
		}
	}

	t0 := time.Now()
	// OTEL: Start ToolExecution Span
	_, span := nbRequestContext.GetTracer().Start(nbRequestContext.GetContext(), fmt.Sprintf("Agent:ToolExecution:%s", tool.Name()))
	data, err := tool.Call(toolContext, request)
	span.End()
	latency := time.Since(t0).Seconds()
	nbRequestContext.GetLogger().Info("tool execution time", "tool", tool.Name(), "time", latency, "input", input)
	// Record tool latency metric
	// Use context.Background() for metrics, as security.RequestContext does not expose context.Context
	common.MetricsToolLatencySeconds(tool.Name(), agentRequest.AccountId, latency)
	if err != nil {
		nbRequestContext.GetLogger().Error("tool execution error", "error", err, "tool", tool.Name())
		responseData := data.Data
		if responseData == "" {
			responseData = err.Error()
		}
		if data.AdditionalDetails == nil {
			data.AdditionalDetails = map[string]any{}
		}

		// instead of returning error, set response sattus as error, so that llms can retry
		// we need to workout sceanrios where we want LLMs to retry && when we want to bypass retry all togeather
		return toolcore.NBToolResponse{
			Data:              responseData,
			Status:            toolcore.NBToolResponseStatusError,
			AdditionalDetails: data.AdditionalDetails,
		}, nil
	}
	return data, err
}

// resolveMaxIterations determines the iteration budget for an agent execution.
// Priority: agent-provided cap (NBAgentIterationProvider) > sub-agent cap > global config.
// The result is always the minimum of all applicable limits.
func resolveMaxIterations(agent NBAgent, request NBAgentRequest) int {
	maxIter := config.Config.LLMServerAgentReActMaxIterations

	// Agent-specific cap takes priority when it's lower than the global config.
	if iterProvider, ok := agent.(NBAgentIterationProvider); ok {
		if agentMax := iterProvider.GetMaxIterations(); agentMax > 0 && agentMax < maxIter {
			maxIter = agentMax
		}
	}

	// Cap sub-agent iterations separately from the parent planner. When react_3
	// bumps the global limit to 50, sub-agents without their own
	// NBAgentIterationProvider would otherwise inherit that full budget and
	// spiral (e.g. redis retrying 50 times on a dead connection).
	// A sub-agent is identified by ParentAgentId differing from its own AgentId.
	// AgentId may be empty if the DB save failed, but the agent is still a
	// sub-agent as long as ParentAgentId is set and differs.
	if request.ParentAgentId != "" && request.ParentAgentId != request.AgentId {
		subAgentMax := config.Config.LLMServerAgentReActSubAgentMaxIterations
		if subAgentMax > 0 && subAgentMax < maxIter {
			maxIter = subAgentMax
		}
	}

	return maxIter
}

func executeAgentPlanner(ctx *security.RequestContext, nbAgentPlanner NBAgentPlanner, agent NBAgent, request NBAgentRequest, previousState string) (plannerResponse NBAgentPlannerExecutorResponse, err error) {
	// Recover from panics in agent execution
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			ctx.GetLogger().Error("agentexecutor: panic recovered during agent execution", "agent", agent.GetName(), "panic", r, "stack", string(stack))
			plannerResponse = NBAgentPlannerExecutorResponse{
				Status:   AgentExecutionStatusFail,
				Response: fmt.Sprintf("Agent execution failed due to : %v", r),
			}
			err = fmt.Errorf("agent execution recovered: %v", r)
		}
	}()

	ctx.GetLogger().Info("agentexecutor: making Run call to agent", "agent", agent.GetName())
	maxIter := resolveMaxIterations(agent, request)
	ctx.GetLogger().Info("agentexecutor: resolved max iterations", "agent", agent.GetName(), "maxIterations", maxIter)
	executor := newPlannerExecutor(ctx, nbAgentPlanner, agent, request, maxIter)
	if previousState != "" {
		err := executor.Unmarshal([]byte(previousState))
		if err != nil {
			ctx.GetLogger().Error("agentexecutor: unable to unmarshal previous state", "error", err, "previousState", previousState)
			return NBAgentPlannerExecutorResponse{}, err
		}

		// CRITICAL: Propagate any NEW tool configurations or confirmations from the current request
		// into the restored executor state. This ensures that if the parent agent just resolved
		// a followup (e.g. tool config selection), the sub-agent actually uses it.
		executor.agentRequest.QueryConfig.MergeFrom(request.QueryConfig)
		ctx.GetLogger().Info("plannerexecutor: resuming from previous state",
			"currentActionCount", len(executor.currentAction),
			"stepsCount", len(executor.steps),
			"toolConfigs", executor.agentRequest.QueryConfig.ToolConfigs)

		// read tool followup o/p as observation
		if len(executor.currentAction) > 0 {
			// read all current actions as steps
			for _, action := range executor.currentAction {
				toolId := action.ToolID
				response, status, err := GetConversationDao().GetConversationToolResponse(toolId, request.MessageId, request.ConversationId, request.AccountId)
				ctx.GetLogger().Info("plannerexecutor: resumption check", "toolId", toolId, "status", status, "err", err)
				if err == nil && strings.EqualFold(string(status), string(toolcore.NBToolResponseStatusSuccess)) {
					step := NBAgentPlannerToolActionStep{
						Action:      action,
						Observation: response,
						Status:      ToolStatusSuccess,
					}
					executor.steps = append(executor.steps, step)
					executor.stepKeys[action.ToolID] = true
					ctx.GetLogger().Info("plannerexecutor: recovered tool result from DB", "toolId", toolId)
				} else if errors.Is(err, sql.ErrNoRows) && !isToolConfigResolved(executor.agentRequest.QueryConfig.ToolConfigs, action.Tool) {
					// No row found in DB for this tool AND no config was resolved. This means the tool
					// was waiting for a config selection that never arrived, or the config was not
					// propagated into QueryConfig. Proceeding would execute the tool with empty/wrong
					// credentials and silently produce "No Data". Fail fast with a clear error instead.
					// Note: only the "no row" case is treated as a config failure; transient DB errors
					// fall through to the next branch so they remain retriable.
					ctx.GetLogger().Error("plannerexecutor: no tool record found in DB and config not resolved, failing fast to avoid running with wrong credentials",
						"tool", action.Tool, "toolId", toolId, "error", err)
					step := NBAgentPlannerToolActionStep{
						Action:      action,
						Observation: fmt.Sprintf("Error: tool %q could not be executed because its configuration was not resolved. Please select a valid configuration and retry.", action.Tool),
						Status:      ToolStatusFailure,
					}
					executor.steps = append(executor.steps, step)
					executor.stepKeys[action.ToolID] = true
				} else if childOut, ok := lookupChildOutcomeIfWaiting(request.ConversationId, request.AgentId, toolId, status); ok {
					// Child-agent completion fallback (#28141): parent's tool_call for
					// this sub-agent is WAITING, but the sub-agent itself already
					// terminated (SUCCESS or FAILED) via its own followup resume. Use
					// the sub-agent's stored response as the observation; DO NOT
					// re-dispatch the tool. Without this branch, the parent falls
					// through to the executor.doAction path below, creates a new
					// sub-agent invocation, triggers another tool_config check, and
					// produces a duplicate followup message.
					//
					// Single DB fetch (via getChildAgentOutcome) replaces the 6 queries
					// the three split helpers used to issue.
					stepStatus := ToolStatusSuccess
					toolCallStatus := toolcore.NBToolResponseStatusSuccess
					if childOut.status == string(AgentExecutionStatusFail) {
						stepStatus = ToolStatusFailure
						toolCallStatus = toolcore.NBToolResponseStatusError
					}
					step := NBAgentPlannerToolActionStep{
						Action:      action,
						Observation: childOut.response,
						Status:      stepStatus,
					}
					executor.steps = append(executor.steps, step)
					executor.stepKeys[action.ToolID] = true
					// Update parent's tool_call row so future lookups see the terminal state.
					// Log on failure: if this persists, the tool_call stays WAITING in DB
					// and a future parent resume (after restart/crash) may re-dispatch
					// the already-completed sub-agent, producing a duplicate followup.
					// Execution of the current iteration is unaffected since executor.steps
					// has been updated in memory.
					if err := GetConversationDao().UpdateConversationToolResponse(toolId, request.MessageId, request.ConversationId, request.AccountId, childOut.response, toolCallStatus); err != nil {
						ctx.GetLogger().Error("plannerexecutor: failed to persist child response to parent tool_call",
							"error", err, "toolId", toolId, "tool", action.Tool)
					}
					ctx.GetLogger().Info("plannerexecutor: using completed child agent response for WAITING parent tool_call",
						"toolId", toolId, "tool", action.Tool, "childStatus", childOut.status)
				} else if request.Query != "" && (strings.EqualFold(string(status), string(toolcore.NBToolResponseStatusWaiting)) || (errors.Is(err, sql.ErrNoRows) && isToolConfigResolved(executor.agentRequest.QueryConfig.ToolConfigs, action.Tool))) {
					// [Changed for TicketV2] Tool is still waiting — execute it IMMEDIATELY so the planner starts with the result.
					// Previously, the user's response always replaced tool input. But for tool_config followups
					// (e.g., user selecting a Jira integration), the user's response is the config choice, not
					// the actual tool input. We now check if a config was just resolved and preserve the original
					// tool input in that case. For user-input followups (ask_clarification), behavior is unchanged.
					// Backward-compatible: isToolConfigResolved returns false when ToolConfigs is empty.
					//
					// Additional case (err != nil): When doAction returns early for a config followup
					// (integration/project selection), NO tool call is saved to DB. On resumption,
					// GetConversationToolResponse returns error (no rows) and status is empty. Without
					// this check, the action would be silently skipped and re-planning would be unpredictable.
					// By also entering this branch when the DB lookup failed AND a config was just resolved,
					// we ensure doAction runs again with the newly resolved config — triggering the next
					// config step (e.g., project selection after integration selection).
					isConfigResolved := isToolConfigResolved(request.QueryConfig.ToolConfigs, action.Tool)
					if isConfigResolved {
						ctx.GetLogger().Info("plannerexecutor: resuming tool after config selection, keeping original input",
							"tool", action.Tool, "toolId", toolId, "isConfigResolved", true, "dbLookupFailed", err != nil)
					} else {
						ctx.GetLogger().Info("plannerexecutor: immediately resuming waiting tool",
							"tool", action.Tool, "toolId", toolId, "newInput", request.Query)
						action.ToolInput = request.Query
					}

					// We need to call doAction directly. We'll use the existing nameToTool mapping.
					nameToTool := getNameToTool(nbAgentPlanner.GetTools())

					// Build query context (for resumption, we use the request's query context).
					stepResponse, finishAct, errAct := executor.doAction(nameToTool, action, request.QueryContext)
					if errAct == nil && finishAct == nil {
						executor.steps = append(executor.steps, stepResponse)
						executor.stepKeys[action.ToolID] = true
						ctx.GetLogger().Info("plannerexecutor: successfully resumed waiting tool", "toolId", toolId)

						// CRITICAL: If the tool returned a terminal response,
						// we should return it IMMEDIATELY as the final answer. This prevents the parent agent
						// from trying to 'helpfully' summarize the data.
						if stepResponse.IsTerminal {
							ctx.GetLogger().Info("plannerexecutor: detected terminal response, returning early", "tool", action.Tool)
							return NBAgentPlannerExecutorResponse{
								Response:    stepResponse.Observation,
								Status:      AgentExecutionStatusSuccess,
								Invocations: executor.GetToolInvocations(),
							}, nil
						}
					} else if finishAct != nil {
						// If it's still waiting or finished, we'll let the planner handle it or store it
						executor.finish = finishAct
						ctx.GetLogger().Info("plannerexecutor: resumed tool returned finish action", "status", finishAct.Status)

						// CRITICAL: If the status is still WAITING, it means the delegated agent is asking another question.
						// We must return immediately and NOT run the planner, otherwise the parent agent
						// will ignore the child's question and try to 'finish' the task itself.
						if strings.EqualFold(string(finishAct.Status), string(ConversationStatusWaiting)) {
							currentState, _ := executor.Marshal()
							return NBAgentPlannerExecutorResponse{
								Response:    common.XmlExtractCDATAOrDefault(finishAct.Data, finishAct.Data),
								Status:      AgentExecutionStatusWaiting,
								Followup:    finishAct.Followup,
								Invocations: executor.GetToolInvocations(),
								State:       string(currentState),
							}, nil
						}
					}
				}
			}
			// Clear currentAction after processing resumption so the planner loop starts fresh
			executor.currentAction = nil
		}
	}
	runCtx := context.WithoutCancel(ctx.GetContext())
	if tp, ok := agent.(NBAgentTimeoutProvider); ok {
		if d := tp.GetTimeout(); d > 0 {
			var cancel context.CancelFunc
			runCtx, cancel = context.WithTimeout(runCtx, d)
			defer cancel()
			ctx.GetLogger().Info("agentexecutor: applying agent timeout", "agent", agent.GetName(), "timeout", d.String())
		}
	}
	response, err := chains.Run(runCtx, executor, request.Query)
	if err != nil {
		// On timeout with accumulated steps, summarize partial results instead of losing them
		if errors.Is(err, context.DeadlineExceeded) && len(executor.steps) > 0 {
			ctx.GetLogger().Warn("agentexecutor: deadline exceeded, summarizing partial results",
				"agent", agent.GetName(), "steps_completed", len(executor.steps))
			result, sumErr := executor.summarizeConversation()
			if sumErr == nil && result != nil {
				return NBAgentPlannerExecutorResponse{
					Status:      AgentExecutionStatusSuccess,
					Response:    result["output"].(string),
					Invocations: executor.GetToolInvocations(),
				}, nil
			}
			ctx.GetLogger().Error("agentexecutor: failed to summarize partial results after timeout", "error", sumErr)
		}
		return NBAgentPlannerExecutorResponse{
			Status:   AgentExecutionStatusFail,
			Response: err.Error(),
		}, err
	}

	status := AgentExecutionStatusSuccess
	followup := FollowupRequest{}
	isTerminal := false
	if executor.finish != nil {
		isTerminal = executor.finish.IsTerminal
		if executor.finish.Status != "" {
			switch executor.finish.Status {
			case ConversationStatusInProgress, ConversationStatusPending:
				status = AgentExecutionStatusInProgress
			case ConversationStatusCompleted:
				status = AgentExecutionStatusSuccess
			case ConversationStatusFailed, ConversationStatusKilled:
				status = AgentExecutionStatusFail
			case ConversationStatusWaiting:
				status = AgentExecutionStatusWaiting
			case ConversationStatusWaitingForClientTool:
				status = AgentExecutionStatusWaitingForClientTool
			}
		}
		followup = executor.finish.Followup
	} else {
		// If no finish action but we have a response, check if the response looks like a wait
		if response == "" {
			status = AgentExecutionStatusFail
		}
	}

	toolInvocations := executor.GetToolInvocations()
	if executor.finish != nil && len(executor.finish.Invocations) > 0 {
		toolInvocations = append(toolInvocations, executor.finish.Invocations...)
	}

	state := ""
	if status == AgentExecutionStatusWaiting || status == AgentExecutionStatusWaitingForClientTool {
		currentState, _ := executor.Marshal()
		state = string(currentState)
	}

	summaryResponse := ""
	var agentStepResponse any
	if status == AgentExecutionStatusWaitingForClientTool && executor.finish != nil {
		if tools, ok := executor.finish.AdditionalDetails["client_tools"]; ok {
			agentStepResponse = tools
		} else {
			// Single tool fallback, wrap in array
			agentStepResponse = []any{executor.finish.AdditionalDetails}
		}
	}

	// summaryResponse is now generated asynchronously in the caller (executor.go)
	// to avoid blocking the main response and race conditions with DB updates.

	var allReferences []toolcore.NBToolResponseReference
	if executor.finish != nil {
		for _, inv := range executor.finish.Invocations {
			allReferences = append(allReferences, inv.References...)
		}
	}

	plannerResponse = NBAgentPlannerExecutorResponse{
		Response:          response,
		Status:            status,
		Followup:          followup,
		Invocations:       toolInvocations,
		State:             state,
		ResponseSummary:   summaryResponse,
		AgentStepResponse: agentStepResponse,
		IsTerminal:        isTerminal,
		References:        allReferences,
	}

	// Phase 2: Long-Term Memory Extraction
	if status == AgentExecutionStatusSuccess && len(response) > 0 && agent.GetName() != ToolLlm {
		bgCtx := security.NewRequestContext(
			context.Background(),
			ctx.GetSecurityContext(),
			ctx.GetLogger(), ctx.GetTracer(),
			ctx.GetMeter(),
		)
		submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
		defer cancel()

		notebook := ""
		if notebookProvider, ok := executor.agentPlanner.(NBAgentNotebookProvider); ok {
			notebook = notebookProvider.GetNotebook()
		}
		ctx.GetLogger().Info("agentexecutor: triggering long-term memory extraction", "notebook_len", len(notebook), "has_notebook", notebook != "")

		err := conversationAsyncTaskWorkerPool.Submit(submissionCtx, func() {
			_ = extractLongTermMemory(bgCtx, request, response, notebook)
		})
		if err != nil {
			ctx.GetLogger().Error("agentexecutor: failed to submit memory extraction task", "error", err)
		}
	} else {
		ctx.GetLogger().Info("agentexecutor: skipping memory extraction - conditions not met",
			"status", status,
			"is_success", status == AgentExecutionStatusSuccess,
			"response_len", len(response))
	}

	return plannerResponse, nil
}

func (e *plannerExecutor) IsReWOOPlanner() bool {
	return e.agent.GetPlannerType() == AgentPlannerTypeReWoo
}

func (e *plannerExecutor) rewriteToolInput(action NBAgentPlannerToolAction, queryContext string) (string, error) {
	// If there are no dependencies, no need to rewrite.
	if len(action.Dependency) == 0 {
		return action.ToolInput, nil
	}

	// OTEL: Start RewriteInput Span
	_, span := e.ctx.GetTracer().Start(e.ctx.GetContext(), "Agent:RewriteInput")
	defer span.End()

	promptTemplate := prompts_repo.GetPrompt(prompts_repo.PromptPlannerAgentRewriteToolInput)

	// Using a simple string replacement for the template here for clarity.
	// In a real implementation, use a proper template engine if it gets more complex.
	prompt := strings.NewReplacer(
		"{{.toolName}}", action.Tool,
		"{{.queryContext}}", queryContext,
		"{{.toolInput}}", action.ToolInput,
	).Replace(promptTemplate)

	llmResponse, err := GenerateAndTrackLLMContent(e.ctx, e.agentRequest.UserId, e.agentRequest.AccountId, e.agentRequest.ConversationId, e.agentRequest.MessageId, e.agentRequest.AgentId, true, []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, prompt)}, true, llms.WithTemperature(0.0), WithThinkingLevel(ThinkingLevelFastTask))
	if err != nil || len(llmResponse.Choices) == 0 || llmResponse.Choices[0].Content == "" {
		e.ctx.GetLogger().Error("plannerexecutor: LLM call for rewriting tool input failed", "prompt", prompt, "error", err)
		// Fallback to original input if rewriting fails
		return action.ToolInput, err
	}

	rewrittenInput := strings.TrimSpace(llmResponse.Choices[0].Content)
	e.ctx.GetLogger().Info("plannerexecutor: Rewrote tool input", "original", action.ToolInput, "rewritten", rewrittenInput)

	return rewrittenInput, nil
}

func (e *plannerExecutor) fastSummarizeTool(action NBAgentPlannerToolAction, queryContext string) (string, error) {
	// OTEL: Start FastSummarize Span
	_, span := e.ctx.GetTracer().Start(e.ctx.GetContext(), "Agent:FastSummarize")
	defer span.End()

	systemPrompt := "You are a helpful SRE assistant. Provide a concise and accurate summary of the provided context to answer the user's question. Preserve all technical details and data. Use Markdown format."

	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf("<context>%s</context>", queryContext)),
		llms.TextParts(llms.ChatMessageTypeHuman, action.ToolInput),
	}

	// Use Lite model for summarization
	summaryCtx := security.NewRequestContext(
		context.WithValue(context.WithValue(e.ctx.GetContext(), ContextKeyUseLiteModel, true), ContextKeyCacheScope, CacheScopeGlobal),
		e.ctx.GetSecurityContext(),
		e.ctx.GetLogger(),
		e.ctx.GetTracer(),
		e.ctx.GetMeter(),
	)

	// Generate a client-side UUID to pass to LLM content tracking.
	// This allows us to combine the DB Save and Update into a single call at the end.
	agentId := uuid.New()

	start := time.Now()
	completion, err := GenerateAndTrackLLMContent(
		summaryCtx,
		e.agentRequest.UserId,
		e.agentRequest.AccountId,
		e.agentRequest.ConversationId,
		e.agentRequest.MessageId,
		agentId.String(),
		true,
		messageContent,
		true, // cleanup markdown
	)
	latency := time.Since(start).Seconds()

	if err != nil {
		if _, dbErr := GetConversationDao().SaveCompletedConversationAgentCall(
			agentId,
			e.agentRequest.ConversationId,
			e.agentRequest.MessageId,
			e.agentRequest.AccountId,
			e.agentRequest.UserId,
			action.Tool,
			e.agentRequest.ParentAgentId,
			action.ToolInput,
			action.Log,
			err.Error(),
			e.agentRequest.QueryContext,
			e.agentRequest.QueryConfig,
			AgentExecutionStatusFail,
			err.Error(),
		); dbErr != nil {
			e.ctx.GetLogger().Warn("plannerexecutor: failed to save fast-summary error", "error", dbErr)
		}
		return "", err
	}

	content := ""
	if len(completion.Choices) > 0 {
		content = strings.TrimSpace(completion.Choices[0].Content)
	}

	if _, dbErr := GetConversationDao().SaveCompletedConversationAgentCall(
		agentId,
		e.agentRequest.ConversationId,
		e.agentRequest.MessageId,
		e.agentRequest.AccountId,
		e.agentRequest.UserId,
		action.Tool,
		e.agentRequest.ParentAgentId,
		action.ToolInput,
		action.Log,
		content,
		e.agentRequest.QueryContext,
		e.agentRequest.QueryConfig,
		AgentExecutionStatusSuccess,
		"",
	); dbErr != nil {
		e.ctx.GetLogger().Warn("plannerexecutor: failed to save fast-summary completion", "error", dbErr)
	}

	e.ctx.GetLogger().Info("plannerexecutor: fast-summary completed", "latency", latency, "content_len", len(content))
	return content, nil
}

func generateAsyncAgentSummary(ctx *security.RequestContext, request NBAgentRequest, response string, agentId string) {
	if agentId == "" || agentId == uuid.Nil.String() {
		return
	}

	// Generate 1 liner summary in a single sentence with up to 20 words
	summaryPrompt := prompts_repo.GetPrompt(prompts_repo.PromptAgentResponseSummary, request.Query, response)
	summaryMessages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, summaryPrompt),
	}

	// Use Lite model for summarization
	summaryCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), ContextKeyUseLiteModel, true),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	summaryCompletion, err := GenerateAndTrackLLMContent(summaryCtx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, "summary_agent", false, summaryMessages, true, llms.WithTemperature(0.2), WithThinkingLevel(ThinkingLevelFastTask))
	if err != nil {
		ctx.GetLogger().Error("agentexecutor: failed to generate summary for agent response", "error", err)
		return
	}

	if summaryCompletion != nil && len(summaryCompletion.Choices) > 0 && summaryCompletion.Choices[0].Content != "" {
		summaryResponse := strings.TrimSpace(summaryCompletion.Choices[0].Content)
		ctx.GetLogger().Info("agentexecutor: generated response summary", "agentId", agentId, "summary_len", len(summaryResponse))
		err = GetConversationDao().UpdateConversationAgentSummary(agentId, summaryResponse)
		if err != nil {
			ctx.GetLogger().Error("agentexecutor: failed to update agent summary in DB", "error", err)
		}
	} else {
		ctx.GetLogger().Warn("agentexecutor: summary generation returned no content")
	}
}

// findConfigInQuery checks if a config name or identifying value exists verbatim in the user query.
// It prioritizes Name matches over Value matches and enforces length constraints to avoid false positives.
func findConfigInQuery(query string, configs []toolcore.ToolConfig) *toolcore.ToolConfig {
	if query == "" {
		return nil
	}

	normalize := func(s string) string {
		f := func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				return unicode.ToLower(r)
			}
			return ' '
		}
		return strings.Join(strings.Fields(strings.Map(f, s)), " ")
	}

	cleanQuery := normalize(query)
	if cleanQuery == "" {
		return nil
	}

	var nameMatches []int
	var valueMatches []int

	for i := range configs {
		// 1. Check Name (Highest priority)
		cleanName := normalize(configs[i].Name)
		if cleanName != "" && strings.Contains(" "+cleanQuery+" ", " "+cleanName+" ") {
			nameMatches = append(nameMatches, i)
			continue
		}

		// 2. Check identifying Values (ID, Project ID, Cluster ID, etc.)
		for _, v := range configs[i].Values {
			lowName := strings.ToLower(v.Name)
			if lowName == "id" || lowName == "project_id" || lowName == "cluster_id" ||
				lowName == "account_id" || lowName == "account_number" || lowName == "cluster_name" {
				cleanVal := normalize(v.Value)
				// Stricter: ignore very short values (e.g. < 3 chars) to avoid false positives
				if len(cleanVal) >= 3 && strings.Contains(" "+cleanQuery+" ", " "+cleanVal+" ") {
					valueMatches = append(valueMatches, i)
					break
				}
			}
		}
	}

	// Use name matches if available, otherwise fall back to value matches
	var matchingIndices []int
	if len(nameMatches) > 0 {
		matchingIndices = nameMatches
	} else {
		matchingIndices = valueMatches
	}

	if len(matchingIndices) == 0 {
		return nil
	}
	if len(matchingIndices) == 1 {
		return &configs[matchingIndices[0]]
	}

	// Multiple matches: Pick the longest match as the most specific
	bestIndex := -1
	for _, idx := range matchingIndices {
		if bestIndex == -1 || len(configs[idx].Name) > len(configs[bestIndex].Name) {
			bestIndex = idx
		}
	}

	// Verify ambiguity: bestMatch must be a superset of all other matches in this tier
	bestMatch := &configs[bestIndex]
	cleanBestName := normalize(bestMatch.Name)
	for _, idx := range matchingIndices {
		if idx == bestIndex {
			continue
		}
		cleanOtherName := normalize(configs[idx].Name)

		// If names aren't hierarchical, check if bestMatch's name contains the other match's value
		isSuperset := cleanBestName != cleanOtherName && strings.Contains(" "+cleanBestName+" ", " "+cleanOtherName+" ")
		if !isSuperset {
			for _, v := range configs[idx].Values {
				lowName := strings.ToLower(v.Name)
				if lowName == "id" || lowName == "project_id" || lowName == "cluster_id" ||
					lowName == "account_id" || lowName == "account_number" {
					cleanVal := normalize(v.Value)
					if cleanVal != "" && strings.Contains(" "+cleanBestName+" ", " "+cleanVal+" ") {
						isSuperset = true
						break
					}
				}
			}
		}

		if !isSuperset {
			// Unrelated matches — not "absolutely sure"
			return nil
		}
	}

	return bestMatch
}

// uniqueQueries returns a deduplicated slice of non-empty queries, preserving order.
func uniqueQueries(queries ...string) []string {
	seen := make(map[string]struct{}, len(queries))
	result := make([]string, 0, len(queries))
	for _, q := range queries {
		if q == "" {
			continue
		}
		if _, exists := seen[q]; exists {
			continue
		}
		seen[q] = struct{}{}
		result = append(result, q)
	}
	return result
}
