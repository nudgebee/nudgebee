package core

import (
	"context"
	"errors"
	"fmt"

	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	nbprompts "nudgebee/llm/prompts"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
)

func generateToolId(tool string, input string) string {
	// Normalize input for hashing: remove whitespace and use lower case
	normalized := strings.ToLower(strings.TrimSpace(input))
	hash := common.HashString(normalized)
	// Return a deterministic but unique-ish ID (first 8 chars of hash)
	return fmt.Sprintf("%s-%s", strings.ToLower(tool), hash[:8])
}

type NBAgentReActPlannerCritiqueSupport interface {
	CritiqueEnabled() bool
}

type NBAgentReActPlannerSummaryToolProvider interface {
	GetSummaryToolName() string
}

// RetryConfig holds the configuration for the retry mechanism.
type RetryConfig struct {
	MaxRetries int
	Prompts    []string
}

// NBAgent is an Agent driven by Tools.
type NBReActPlanner2 struct {
	ctx                   *security.RequestContext
	llm                   llms.Model
	prompt                prompts.FormatPrompter
	request               NBAgentRequest
	nbAgent               NBAgent
	summaryToolName       string
	retryConfig           RetryConfig
	refinementAttempts    int
	maxRefinementAttempts int
	refinementData        []struct {
		feedback string
		answer   string
	}
	postRefinementToolIndex int
	tools                   []toolcore.NBTool
	enableCritique          bool
	Notebook                string
	compressionTracker      *CompressionTracker
	// llmSummarizationDisabled is set to true after the first LLM summarization
	// failure. Once set, prewarmSummaries skips LLM calls entirely.
	llmSummarizationDisabled bool
}

// ErrParseFailure indicates the LLM response could not be parsed into a
// valid action or final answer and is eligible for retrying with a
// reformat prompt.
var ErrParseFailure = errors.New("unable to parse LLM response: no action, final answer, or clarification")

// saveCritique persists the results of a critique cycle to the database.
func (o *NBReActPlanner2) saveCritique(critiqueType, input, critiquedContent, feedback, decision string) error {
	conversationDAO := GetConversationDao()
	var feedbackPtr *string
	if feedback != "" {
		feedbackPtr = &feedback
	}
	return conversationDAO.SaveCritique(&ConversationCritique{
		ConversationID:   o.request.ConversationId,
		MessageID:        o.request.MessageId,
		AccountID:        o.request.AccountId,
		AgentName:        o.nbAgent.GetName(),
		CritiqueType:     critiqueType,
		Input:            input,
		CritiquedContent: critiquedContent,
		Feedback:         feedbackPtr,
		Decision:         decision,
	})
}

// buildScratchpad constructs the XML-formatted history of tool calls and observations
// for the LLM, applying semantic compression to older steps and strict budget capping.
func (o *NBReActPlanner2) buildScratchpad(intermediateSteps []NBAgentPlannerToolActionStep) string {
	// Count effective steps for semantic compression
	effectiveSteps := 0
	for i := range intermediateSteps {
		if i >= o.postRefinementToolIndex {
			effectiveSteps++
		}
	}

	// Parallel prewarm: fire LLM summarizations for non-recent steps concurrently
	// to avoid a sequential latency spike when compression first kicks in.
	o.prewarmSummaries(intermediateSteps, effectiveSteps)

	var history strings.Builder
	stepIndex := 0
	for i, step := range intermediateSteps {
		if i < o.postRefinementToolIndex {
			continue
		}
		stepIndex++
		isRecent := (effectiveSteps - stepIndex) < recentStepsFullContext

		// Reconstruct the full ReAct block for historical turns to maintain formatting continuity
		history.WriteString("<thought_action>\n")
		fmt.Fprintf(&history, "<thought>%s</thought>\n", step.Action.Log)
		history.WriteString("<action>\n")
		fmt.Fprintf(&history, "    <tool_name>%s</tool_name>\n", step.Action.Tool)
		fmt.Fprintf(&history, "    <tool_input>%s</tool_input>\n", step.Action.ToolInput)
		history.WriteString("</action>\n")
		history.WriteString("</thought_action>\n")

		toolResponse := o.resolveToolResponse(step)

		// Semantic compression: older steps get preview-only observations,
		// recent steps keep full observations (capped at maxObservationChars).
		// When summarization is enabled, older steps get an LLM-generated summary
		// instead of a blind 100-byte truncation.
		if isRecent {
			if len(toolResponse) > getMaxObservationChars() {
				toolResponse = TruncateMiddle(toolResponse, 2048, getMaxObservationChars()-2048)
			}
		} else {
			toolResponse = SummarizeObservation(o.ctx, &intermediateSteps[i], o.request, toolResponse)
		}

		observationBlock := fmt.Sprintf(
			"<observation tool=%q>\n%s\n</observation>",
			step.Action.Tool,
			toolResponse,
		)

		if len(step.References) > 0 {
			var refsBuilder strings.Builder
			refsBuilder.WriteString("<references>\n")
			for _, ref := range step.References {
				fmt.Fprintf(&refsBuilder, "  <reference text=%q url=%q type=%q description=%q />\n", ref.Text, ref.Url, ref.Type, ref.Description)
			}
			refsBuilder.WriteString("</references>\n")
			observationBlock = fmt.Sprintf(
				"<observation tool=%q>\n%s\n%s</observation>",
				step.Action.Tool,
				toolResponse,
				refsBuilder.String(),
			)
		}

		history.WriteString(observationBlock)
		history.WriteString("\n")
	}
	scratchpad := ""
	if history.Len() > 0 {
		scratchpad = fmt.Sprintf("<scratchpad>\n%s</scratchpad>", history.String())
	}

	// Aggregate budget: cap total scratchpad size.
	// Leaves room for wrapper tags and truncation notes (approx 500 chars).
	maxChars := config.Config.LlmServerAgentMaxScratchpadChars
	if maxChars > 0 && len(scratchpad) > maxChars {
		const tagOverhead = 500
		effectiveLimit := maxChars - tagOverhead
		if effectiveLimit < 0 {
			effectiveLimit = 0
		}
		scratchpad = "<scratchpad>\n[earlier turns truncated]\n" + truncateTail(scratchpad, effectiveLimit)
		if !strings.HasSuffix(scratchpad, "</scratchpad>") {
			scratchpad += "</scratchpad>"
		}
	}

	return scratchpad
}

// resolveToolResponse applies the agent's optional response handler and returns
// the observation text as it will be rendered into the scratchpad. The result
// matches what getToolResponse would produce before semantic compression.
func (o *NBReActPlanner2) resolveToolResponse(step NBAgentPlannerToolActionStep) string {
	toolResponse := step.Observation
	if responseHandler, ok := o.nbAgent.(NBAgentExecutorToolResponseHandler); ok {
		toolResponse = responseHandler.UpdateToolResponseForPlanner(step.Action, step.Observation)
	}
	if toolResponse == "" {
		toolResponse = step.Observation
	}
	if toolResponse == "" {
		toolResponse = "No Data Found"
	}
	return toolResponse
}

// prewarmSummaries fires LLM summarizations in parallel for all non-recent steps
// whose observation is large enough to benefit. This avoids a sequential latency
// spike on iterations where multiple older steps need compression at once.
// The result is cached in each step's CompressedObservation field.
func (o *NBReActPlanner2) prewarmSummaries(intermediateSteps []NBAgentPlannerToolActionStep, effectiveSteps int) {
	if !config.Config.LlmServerScratchpadSummarizationEnabled || o.ctx == nil {
		return
	}

	// Circuit breaker: skip LLM calls if a previous iteration's calls all failed.
	if o.llmSummarizationDisabled {
		return
	}

	minBytes := getScratchpadSummaryMinBytes()

	// Shared cancel context: on the first LLM failure, cancel remaining goroutines.
	prewarmCtx, cancelPrewarm := context.WithCancel(o.ctx.GetContext())
	defer cancelPrewarm()

	wrappedCtx := security.NewRequestContext(
		prewarmCtx,
		o.ctx.GetSecurityContext(),
		o.ctx.GetLogger(),
		o.ctx.GetTracer(),
		o.ctx.GetMeter(),
	)

	var failOnce sync.Once
	var wg sync.WaitGroup
	stepIdx := 0
	for i := range intermediateSteps {
		if i < o.postRefinementToolIndex {
			continue
		}
		stepIdx++
		isRecent := (effectiveSteps - stepIdx) < recentStepsFullContext
		if isRecent {
			continue
		}
		step := &intermediateSteps[i]
		if step.CompressedObservation != "" {
			continue
		}
		obs := o.resolveToolResponse(*step)
		if len(obs) < minBytes {
			continue
		}
		wg.Add(1)
		go func(s *NBAgentPlannerToolActionStep, processedObs string) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					o.ctx.GetLogger().Error("scratchpad: prewarm summarization panic", "recover", r)
				}
			}()
			if prewarmCtx.Err() != nil {
				result := compressObservation(processedObs)
				s.CompressedObservation = result
				return
			}
			result := SummarizeObservation(wrappedCtx, s, o.request, processedObs)
			if !strings.HasPrefix(result, "[summarized from") {
				failOnce.Do(func() {
					o.ctx.GetLogger().Info("scratchpad: LLM summarization failed, cancelling remaining prewarm goroutines")
					cancelPrewarm()
				})
			}
		}(step, obs)
	}
	wg.Wait()

	if prewarmCtx.Err() != nil {
		o.llmSummarizationDisabled = true
		o.ctx.GetLogger().Info("scratchpad: disabling LLM summarization for remaining iterations (circuit breaker tripped)")
	}
}

// Marshal serializes the ReAct planner state into JSON.
func (o *NBReActPlanner2) Marshal() ([]byte, error) {
	state := map[string]any{
		"notebook":                o.Notebook,
		"refinementAttempts":      o.refinementAttempts,
		"postRefinementToolIndex": o.postRefinementToolIndex,
	}
	return common.MarshalJson(state)
}

// Unmarshal restores the ReAct planner state from JSON.
func (o *NBReActPlanner2) Unmarshal(data []byte) error {
	var state struct {
		Notebook                string `json:"notebook"`
		RefinementAttempts      int    `json:"refinementAttempts"`
		PostRefinementToolIndex int    `json:"postRefinementToolIndex"`
	}
	err := common.UnmarshalJson(data, &state)
	if err != nil {
		return err
	}
	o.Notebook = state.Notebook
	o.refinementAttempts = state.RefinementAttempts
	o.postRefinementToolIndex = state.PostRefinementToolIndex
	return nil
}

// Plan executes the ReAct logic to determine the next action or final answer.
// It handles LLM calls, output parsing, and optional critique/refinement cycles.
func (o *NBReActPlanner2) Plan(
	ctx context.Context,
	intermediateSteps []NBAgentPlannerToolActionStep,
	input string,
) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	agentName := o.nbAgent.GetName()
	requestID := o.request.AgentId
	logger := o.ctx.GetLogger().With("agent", agentName, "agent_id", requestID)

	// Optimization: Check for direct summarization after a tool call.
	if len(intermediateSteps) > 0 && o.summaryToolName != "" {
		lastStep := intermediateSteps[len(intermediateSteps)-1]
		// Ensure we are not in a critique/refinement loop and the last tool wasn't the summary tool.
		if lastStep.Status == ToolStatusSuccess && len(o.refinementData) == 0 && !strings.EqualFold(lastStep.Action.Tool, o.summaryToolName) {
			if summarizer, ok := o.nbAgent.(NBAgentDirectSummarization); ok {
				if summarizer.ShouldSummarizeNow(lastStep.Action.Tool, lastStep.Observation) {
					logger.Info("reactagent2: direct summarization triggered, invoking summary tool", "tool", lastStep.Action.Tool, "summary_tool", o.summaryToolName)
					return []NBAgentPlannerToolAction{
						{
							Tool:      o.summaryToolName,
							ToolInput: input,
							Log:       fmt.Sprintf("The previous tool %s succeeded, and the agent is configured to summarize its output directly. I will now call the summary tool %s.", lastStep.Action.Tool, o.summaryToolName),
							ToolID:    generateToolId(o.summaryToolName, input),
						},
					}, nil, nil
				}
			}
		}
	}

	o.refinementAttempts = 0

	var lastErr error
	isSummaryToolUsed := len(intermediateSteps) > 0 && strings.EqualFold(intermediateSteps[len(intermediateSteps)-1].Action.Tool, o.summaryToolName)

	// Determine cache scope and create context once per plan
	cacheScope := CacheScopeConversation
	if cacheProvider, ok := o.nbAgent.(NBAgentCacheScopeProvider); ok {
		cacheScope = cacheProvider.GetCacheScope()
	}
	// ClientTools (Local Tools) vary per chat session and are injected into
	// the cacheable tool list + system instructions, so they cannot share an
	// Account-scope cache across sessions. Downgrade to Conversation scope.
	if len(o.request.ClientTools) > 0 && cacheScope != CacheScopeConversation {
		o.ctx.GetLogger().Debug("reactagent2: downgrading cache scope to conversation due to client tools",
			"agent", o.nbAgent.GetName(), "from", cacheScope)
		cacheScope = CacheScopeConversation
	}

	agentCtx := security.NewRequestContext(
		context.WithValue(
			context.WithValue(o.ctx.GetContext(), ContextKeyCacheScope, cacheScope),
			ContextKeyCapabilities, o.request.Capabilities,
		),
		o.ctx.GetSecurityContext(),
		o.ctx.GetLogger(),
		o.ctx.GetTracer(),
		o.ctx.GetMeter(),
	)

	// This loop handles the refinement process based on critique feedback.
	// Bound the number of outer iterations to avoid unexpected infinite loops
	// in pathological cases. Normal termination happens via returns or breaks
	// when errors occur or when actions/finish are returned.
	const maxOuterIterations = 5
	reactPlanStart := time.Now()
	for range maxOuterIterations {
		var finish *NBAgentPlannerFinishAction
		// Build the scratchpad from previous steps
		scratchpadStart := time.Now()
		scratchpad := o.buildScratchpad(intermediateSteps)
		SaveCompressionVisibility(o.ctx, o.request, intermediateSteps, o.compressionTracker)
		logger.Info("reactagent2: scratchpad built", "duration", time.Since(scratchpadStart).String(), "length", len(scratchpad))

		if isSummaryToolUsed {
			finish = &NBAgentPlannerFinishAction{
				Data: intermediateSteps[len(intermediateSteps)-1].Observation,
			}
		} else {
			mcList := []llms.MessageContent{}
			if len(o.refinementData) > 0 {
				// Format the main prompt.
				// today and query_context are set once in PartialVariables at construction
				// (stable per-request) so the system message stays byte-for-byte identical
				// across iterations, enabling LLM provider caching (Google AI / Anthropic).
				// notebook is per-iteration because o.Notebook is mutated when the LLM
				// emits <update_notebook>; passing the current value ensures the LLM sees
				// the latest state. A notebook update invalidates the cache for that turn,
				// but subsequent iterations with the same notebook hit the cache again.
				fullInputs := map[string]any{
					"input":      input,
					"scratchpad": "",
					"notebook":   o.Notebook,
				}
				prompt, err := o.prompt.FormatPrompt(fullInputs)
				if err != nil {
					return nil, nil, err
				}
				for _, msg := range prompt.Messages() {
					mcList = append(mcList, llms.MessageContent{
						Role:  msg.GetType(),
						Parts: []llms.ContentPart{llms.TextContent{Text: msg.GetContent()}},
					})
				}
				for _, rd := range o.refinementData {
					mcList = append(mcList, llms.MessageContent{
						Role:  llms.ChatMessageTypeAI,
						Parts: []llms.ContentPart{llms.TextContent{Text: rd.answer}},
					})
					feedbackMsg := fmt.Sprintf("The previous final answer was: %s\nThe critique feedback was: %s\nBased on this feedback, please provide a refined final answer or choose to use a tool if needed. Refer to previous conversation/messages for better context", rd.answer, rd.feedback)
					mcList = append(mcList, llms.MessageContent{
						Role:  llms.ChatMessageTypeHuman,
						Parts: []llms.ContentPart{llms.TextContent{Text: feedbackMsg}},
					})
				}
				if scratchpad != "" {
					mcList = append(mcList, llms.MessageContent{
						Role:  llms.ChatMessageTypeHuman,
						Parts: []llms.ContentPart{llms.TextContent{Text: fmt.Sprintf("Here is the latest scratchpad for reference:\n\n%s", scratchpad)}},
					})
				}

			} else {
				// Format the main prompt.
				// today and query_context are set once in PartialVariables at construction
				// (stable per-request) so the system message stays byte-for-byte identical
				// across iterations, enabling LLM provider caching (Google AI / Anthropic).
				// notebook is per-iteration because o.Notebook is mutated when the LLM
				// emits <update_notebook>; passing the current value ensures the LLM sees
				// the latest state. A notebook update invalidates the cache for that turn,
				// but subsequent iterations with the same notebook hit the cache again.
				fullInputs := map[string]any{
					"input":      input,
					"scratchpad": scratchpad,
					"notebook":   o.Notebook,
				}
				prompt, err := o.prompt.FormatPrompt(fullInputs)
				if err != nil {
					return nil, nil, err
				}
				for _, msg := range prompt.Messages() {
					mcList = append(mcList, llms.MessageContent{
						Role:  msg.GetType(),
						Parts: []llms.ContentPart{llms.TextContent{Text: msg.GetContent()}},
					})
				}
			}

			// Main LLM call to get the next action or a final answer
			// Build call options - exclude stop words for OpenAI models that don't support it
			callOptions := []llms.CallOption{llms.WithTemperature(0.0)}

			// Get provider and model to check if stop words are supported
			provider := GetLLMProvider(o.ctx, o.request.AccountId, o.nbAgent.GetName(), true, o.request.ConversationId)
			model := GetLLMModelName(o.ctx, o.request.AccountId, provider, o.nbAgent.GetName(), true, o.request.ConversationId)

			// Only add stop words if the model supports them
			if !IsOpenAIModelWithoutStopSupport(provider, model) {
				callOptions = append(callOptions, llms.WithStopWords([]string{"<observation>"}))
			} else {
				logger.Debug("reactagent2: skipping stop words for model without support", "provider", provider, "model", model)
			}

			llmCallStart := time.Now()
			result, err := GenerateAndTrackLLMContent(agentCtx, o.request.UserId, o.request.AccountId, o.request.ConversationId, o.request.MessageId, o.request.AgentId, false, mcList, true, callOptions...)
			logger.Info("reactagent2: LLM call complete", "duration", time.Since(llmCallStart).String(), "total_plan_duration", time.Since(reactPlanStart).String())
			if err != nil {
				logger.Error("reactagent2: main llm call failed", "error", err)
				lastErr = err
				break
			}

			parseStart := time.Now()
			actions, finish1, err := o.parseOutput(result, intermediateSteps)
			logger.Info("reactagent2: output parsed", "duration", time.Since(parseStart).String())
			finish = finish1
			if err != nil {
				logger.Warn("reactagent2: failed to parse llm output", "error", err, "data", result.Choices[0].Content)
				// Only attempt to retry for parse failures (ErrParseFailure). For other errors, break.
				if !errors.Is(err, ErrParseFailure) {
					lastErr = err
					break
				}
				// Try to recover by asking the system to reformat the response up to MaxRetries
				retryPrompts := o.retryConfig.Prompts
				if len(retryPrompts) == 0 {
					logger.Warn("reactagent2: no retry prompts configured; skipping parse retries")
					lastErr = err
					break
				}
				priorContent := ""
				if len(result.Choices) > 0 {
					priorContent = result.Choices[0].Content
				}
				retryCount := 0
				for retryCount < o.retryConfig.MaxRetries {
					retryPrompt := retryPrompts[retryCount%len(retryPrompts)]

					// Inject tool names and format reinforcement into retry prompt
					toolNames := reActPromptToolNames(o.tools)

					var recoveryInstruction string
					if priorContent == "" {
						recoveryInstruction = "Your previous response was empty. You MUST provide a valid tool call or final answer now."
					} else {
						parseReason := ""
						if strings.Contains(err.Error(), ": ") {
							parseReason = " Reason for failure: " + strings.SplitN(err.Error(), ": ", 2)[1]
						}
						recoveryInstruction = fmt.Sprintf("Your previous response was: %q. It was malformed or incomplete.%s Please provide the FULL and CORRECTED XML response now.", priorContent, parseReason)
					}

					retryPromptRefined := fmt.Sprintf(`%s

%s

STRICT CONSTRAINTS:
1. RESPONSE FORMAT: You MUST use <thought_action> block for tool calls or <final_answer> block for the final result.
2. TOOL CALL STRUCTURE: A tool call MUST be: <thought_action><thought>...</thought><action><tool_name>...</tool_name><tool_input>...</tool_input></action></thought_action>
3. AVAILABLE TOOLS: You are strictly limited to these tools: [%s].
4. NO HALLUCINATIONS: Do not use tools like 'list_tools'. They do not exist.
5. ONE TURN: Provide exactly one XML block.`, retryPrompt, recoveryInstruction, toolNames)

					// Preserve original context by copying mcList
					retryMessages := make([]llms.MessageContent, len(mcList))
					copy(retryMessages, mcList)

					// Append the failed attempt (only if not empty, to keep the conversation clean)
					if priorContent != "" {
						retryMessages = append(retryMessages, llms.MessageContent{
							Role:  llms.ChatMessageTypeAI,
							Parts: []llms.ContentPart{llms.TextContent{Text: priorContent}},
						})
					}

					// Append the correction instruction
					retryMessages = append(retryMessages, llms.MessageContent{
						Role:  llms.ChatMessageTypeHuman,
						Parts: []llms.ContentPart{llms.TextContent{Text: retryPromptRefined}},
					})

					retryResult, retryErr := GenerateAndTrackLLMContent(o.ctx, o.request.UserId, o.request.AccountId, o.request.ConversationId, o.request.MessageId, o.request.ParentAgentId, false, retryMessages, true, llms.WithTemperature(0.0))
					if retryErr != nil {
						logger.Warn("reactagent2: retry llm call failed", "error", retryErr, "attempt", retryCount+1)
						retryCount++
						time.Sleep(time.Duration(retryCount) * time.Second)
						continue
					}

					// Optimization: If response is empty, don't keep retrying as consecutive empty responses are likely to persist
					if len(retryResult.Choices) == 0 || retryResult.Choices[0].Content == "" {
						logger.Warn("reactagent2: received empty response on retry, breaking early", "attempt", retryCount+1)
						err = ErrParseFailure
						break
					}

					actions, finish, err = o.parseOutput(retryResult, intermediateSteps)
					if err == nil {
						break // success on retry
					}
					logger.Info("reactagent2: retry parsing failed, will retry again if attempts remain", "attempt", retryCount+1, "error", err, "data", retryResult.Choices[0].Content)
					retryCount++
					time.Sleep(time.Duration(retryCount) * time.Second)
				}
				if err != nil {
					// After retries exhausted, treat as parsing failure and fall back to summarization
					logger.Warn("reactagent2: parsing failed after retries", "error", err)
					lastErr = err
					break
				}
			}

			// If the agent returns an action, return it for execution.
			if finish == nil {
				// Defensive: if actions is nil or empty, treat this as a parsing failure
				// instead of returning an ambiguous (nil, nil, nil) result to the caller.
				if len(actions) == 0 {
					logger.Warn("reactagent2: parsed no action and no final answer from LLM")
					lastErr = fmt.Errorf("no action or final answer parsed from LLM response")
					break
				}
				return actions, nil, nil
			}

			// If the agent returns a final answer, CRITIQUE it.
			// If all refinement attempts have been exhausted, accept the answer as-is.
			// Otherwise, enter the critique-and-refine loop below.
			if o.refinementAttempts >= o.maxRefinementAttempts {
				logger.Warn("reactagent2: max refinement attempts reached, accepting current answer", "attempts", o.refinementAttempts)
				return nil, finish, nil
			}
		}

		// incase of errors finish can be nil
		if finish != nil {

			// Auto-critique only fires for top-level agents to avoid
			// slowing down sub-agents like kubectl, logs, etc.
			// Top-level agents have ParentAgentId == AgentId (self-referencing) or empty.
			isTopLevel := o.request.ParentAgentId == "" || o.request.ParentAgentId == o.request.AgentId
			critiqueAllowed := o.enableCritique || (config.Config.LlmServerReActCritiqueEnabled && isTopLevel && IsInvestigationRequestTask(o.request.Query))
			if agent, ok := o.nbAgent.(NBAgentReActPlannerCritiqueSupport); ok {
				// agent supports critique, so combine both flags
				critiqueAllowed = critiqueAllowed && agent.CritiqueEnabled()
			}

			if !critiqueAllowed {
				logger.Info("reactagent2: skipping critique", "enableCritique", o.enableCritique, "isTopLevel", isTopLevel, "isInvestigation", IsInvestigationRequestTask(o.request.Query), "autoCritiqueEnabled", config.Config.LlmServerReActCritiqueEnabled)
				return nil, finish, nil
			}

			logger.Debug("reactagent2: critiquing final answer")
			critiquePrompt := prompts.NewPromptTemplate(
				prompts_repo.GetPrompt(prompts_repo.PromptPlannerReactCritiquer),
				[]string{"input", "scratchpad", "final_answer", "question_type"},
			)
			critiquePromptStr, promptErr := critiquePrompt.Format(map[string]any{
				"input":         input,
				"scratchpad":    scratchpad,
				"final_answer":  finish.Data,
				"today":         time.Now().Format(time.RFC1123),
				"notebook":      o.Notebook,
				"question_type": lo.Ternary(IsInvestigationRequestTask(o.request.Query), "investigation", "query"),
			})
			if promptErr != nil {
				logger.Error("reactagent2: failed to format critique prompt, accepting answer", "error", promptErr)
				return nil, finish, nil
			}

			critiqueMessages := []llms.MessageContent{{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: critiquePromptStr}}}}
			critiqueResult, critiqueErr := GenerateAndTrackLLMContent(o.ctx, o.request.UserId, o.request.AccountId, o.request.ConversationId, o.request.MessageId, "react_answer", false, critiqueMessages, true, llms.WithTemperature(0.0))

			if critiqueErr != nil {
				logger.Error("reactagent2: critique llm call failed, accepting answer", "error", critiqueErr)
				return nil, finish, nil
			}

			// If the critique result is empty or missing the required <decision> tag,
			// attempt to ask the critique LLM to retry formatting the response.
			critiqueContentEmpty := len(critiqueResult.Choices) == 0 || strings.TrimSpace(critiqueResult.Choices[0].Content) == ""
			critiqueDecision := ""
			if !critiqueContentEmpty {
				critiqueDecision = common.XmlExtractTagContent(critiqueResult.Choices[0].Content, "decision")
			}

			// critiquedecision
			if critiqueContentEmpty || critiqueDecision == "" {
				logger.Warn("reactagent2: critique response empty or missing <decision>, attempting retries")
				// Use a concise critique-specific instruction as the system message for retries.
				// Planner retry prompts are designed for XML planner outputs (<scratchpad>/<final_answer})
				// and are not suitable for critique responses (<decision>/<feedback>), so we avoid
				// using them here to prevent contradictory instructions.
				instruction := "The previous critique response was empty or malformed. Please reply with XML containing exactly a <decision> tag with value 'accept' or 'reject', and a <feedback> tag with concise feedback. Do not write any other text outside these tags."
				retryCount := 0
				for retryCount < o.retryConfig.MaxRetries {
					retryMessages := []llms.MessageContent{
						{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: instruction}}},
						{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: critiquePromptStr}}},
					}
					retryCritique, retryErr := GenerateAndTrackLLMContent(o.ctx, o.request.UserId, o.request.AccountId, o.request.ConversationId, o.request.MessageId, "react_answer", false, retryMessages, true, llms.WithTemperature(0.0))
					if retryErr != nil {
						logger.Warn("reactagent2: critique retry llm call failed", "error", retryErr, "attempt", retryCount+1)
						retryCount++
						continue
					}
					if len(retryCritique.Choices) > 0 && strings.TrimSpace(retryCritique.Choices[0].Content) != "" {
						// Check if this retry produced a decision
						if common.XmlExtractTagContent(retryCritique.Choices[0].Content, "decision") != "" {
							critiqueResult = retryCritique
							break
						}
					}
					logger.Info("reactagent2: critique retry did not produce valid decision, will retry if attempts remain", "attempt", retryCount+1)
					retryCount++
				}
				// After retries, if still no usable critique, accept the answer (same behavior as previous critiqueErr handling)
				if len(critiqueResult.Choices) == 0 || common.XmlExtractTagContent(critiqueResult.Choices[0].Content, "decision") == "" {
					logger.Error("reactagent2: critique failed after retries, accepting answer")
					return nil, finish, nil
				}
			}

			// Final sanity check before extracting decision: ensure there's at least one choice with content.
			if len(critiqueResult.Choices) == 0 || strings.TrimSpace(critiqueResult.Choices[0].Content) == "" {
				logger.Warn("reactagent2: final critique check failed (empty choices), accepting answer")
				return nil, finish, nil
			}
			decision := common.XmlExtractTagContent(critiqueResult.Choices[0].Content, "decision")
			logger.Debug("reactagent2: critique decision", "decision", decision)

			err := o.saveCritique("react_answer", input, finish.Data, critiqueResult.Choices[0].Content, decision)
			if err != nil {
				logger.Error("reactagent2: failed to save critique", "error", err)
			}

			if strings.EqualFold(decision, "accept") {
				return nil, finish, nil
			} else {
				// Answer is bad, add feedback to history and loop again to refine.
				o.refinementAttempts++
				o.postRefinementToolIndex = len(intermediateSteps)
				feedback := common.XmlExtractTagContent(critiqueResult.Choices[0].Content, "feedback")
				logger.Info("reactagent2: answer rejected by critique, refining", "feedback", feedback, "attempt", o.refinementAttempts)
				o.refinementData = append(o.refinementData, struct {
					feedback string
					answer   string
				}{
					feedback: feedback,
					answer:   finish.Data,
				})
				isSummaryToolUsed = false
			}
		}
	}

	// If we exit the loop because the outer iteration cap was reached and
	// no other error was set, set lastErr so the summarization fallback will run.
	if lastErr == nil {
		lastErr = fmt.Errorf("reactagent2: exceeded max outer iterations (%d)", maxOuterIterations)
	}

	// This part is reached if the loop is broken by an error.
	if lastErr != nil && len(intermediateSteps) > 0 {
		logger.Info("reactagent2: loop broken, attempting to summarize last observation", "error", lastErr)
		scratchpad := o.buildScratchpad(intermediateSteps)
		SaveCompressionVisibility(o.ctx, o.request, intermediateSteps, o.compressionTracker)
		prompt := fmt.Sprintf(`<question>%s</question> \n\n%s`, input, scratchpad)

		summaryMessages := []llms.MessageContent{
			{
				Role:  llms.ChatMessageTypeSystem,
				Parts: []llms.ContentPart{llms.TextContent{Text: "You are an expert assistant. Your task is to synthesize the following technical data into a clear, human-readable summary for a user. Present the key information in an easy-to-understand way."}},
			},
			{
				Role:  llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{llms.TextContent{Text: prompt}},
			},
		}

		summaryResult, summaryErr := GenerateAndTrackLLMContent(o.ctx, o.request.UserId, o.request.AccountId, o.request.ConversationId, o.request.MessageId, o.request.ParentAgentId, false, summaryMessages, true, llms.WithTemperature(0.0), WithThinkingLevel(ThinkingLevelFastTask))
		if summaryErr != nil || len(summaryResult.Choices) == 0 {
			logger.Error("reactagent2: summarization call failed, returning raw observation", "error", summaryErr)
			return nil, &NBAgentPlannerFinishAction{
				Data: scratchpad,
			}, nil
		} else {
			return nil, &NBAgentPlannerFinishAction{
				Data: summaryResult.Choices[0].Content,
			}, nil
		}
	}

	// No tools were called and parsing failed — return a clear error to the user
	if lastErr != nil && len(intermediateSteps) == 0 {
		logger.Error("reactagent2: agent failed without making any tool calls", "error", lastErr, "agent", o.nbAgent.GetName())
		return nil, &NBAgentPlannerFinishAction{
			Data:   "I was unable to process your request due to an internal error. Please try again or rephrase your question.",
			Status: ConversationStatusFailed,
		}, nil
	}

	return nil, nil, lastErr
}

// GetTools returns the list of tools available to the planner.
func (o *NBReActPlanner2) GetTools() []toolcore.NBTool {
	return o.tools
}

// GetNotebook returns the current content of the agent's notebook.
func (o *NBReActPlanner2) GetNotebook() string {
	return o.Notebook
}

// parseOutput delegates output parsing to the internal parser and applies
// optional custom response handlers from the agent.
func (o *NBReActPlanner2) parseOutput(contentResp *llms.ContentResponse, intermediateSteps []NBAgentPlannerToolActionStep) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	action, finish, err := o.parseOutputInternal(contentResp, intermediateSteps)
	if _, ok := o.nbAgent.(NBAgentExecutorLlmResponseHandler); ok {
		handler := o.nbAgent.(NBAgentExecutorLlmResponseHandler)
		action1 := []NBAgentPlannerToolAction{}
		for _, a := range action {
			action1 = append(action1, NBAgentPlannerToolAction{
				Tool:      a.Tool,
				ToolInput: a.ToolInput,
				ToolID:    a.ToolID,
				Log:       a.Log,
			})
		}
		action1, finish, err = handler.UpdateExecutorLlmResponse(action1, finish, err)
		if action1 != nil {
			action = []NBAgentPlannerToolAction{}
			for _, a := range action1 {
				action = append(action, NBAgentPlannerToolAction{
					Tool:      a.Tool,
					ToolInput: a.ToolInput,
					ToolID:    a.ToolID,
					Log:       a.Log,
				})
			}
		}
		// Defensive: if the handler returned no action, no finish and no error,
		// avoid propagating a silent nil-nil-nil which the caller may treat as a valid response.
		if action == nil && finish == nil && err == nil {
			return nil, nil, ErrParseFailure
		}
	}
	return action, finish, err
}

// parseOutputInternal parses the raw LLM string into structured actions,
// final answers, or clarifications.
func (o *NBReActPlanner2) parseOutputInternal(contentResp *llms.ContentResponse, intermediateSteps []NBAgentPlannerToolActionStep) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	if len(contentResp.Choices) == 0 {
		return nil, nil, ErrParseFailure
	}
	if contentResp.Choices[0].Content == "" {
		// Gemini models can return MALFORMED_FUNCTION_CALL when the prompt contains
		// XML-like tags and the model incorrectly attempts a native function call.
		// Wrap the error with a specific reason so the retry loop can inject a
		// targeted recovery instruction rather than a generic "response was empty".
		stopReason := contentResp.Choices[0].StopReason
		if strings.EqualFold(stopReason, "MALFORMED_FUNCTION_CALL") {
			return nil, nil, fmt.Errorf("%w: model returned MALFORMED_FUNCTION_CALL — do NOT use native function calls; wrap your response in <thought_action> XML tags instead", ErrParseFailure)
		}
		return nil, nil, ErrParseFailure
	}

	output := contentResp.Choices[0].Content

	// Update notebook if tag is present
	o.processNotebookUpdate(output)

	// Check for each response type and process accordingly
	// Prioritize Tool Actions over Final Answers. If the model generates a tool call,
	// we must execute it, even if it also hallucinated a final answer block.
	if action := o.processToolAction(output); action != nil {
		return action, nil, nil
	}

	if finish := o.processFinalAnswer(output); finish != nil {
		return nil, finish, nil
	}

	if clarification := o.processClarification(output); clarification != nil {
		return nil, clarification, nil
	}

	// If everything failed, try to diagnose WHY it failed to provide better feedback for retry
	reason := ""
	if strings.Contains(output, "<thought_action>") {
		if !strings.Contains(output, "</thought_action>") {
			reason = "missing closing </thought_action> tag"
		} else if !strings.Contains(output, "<tool_name>") {
			reason = "missing <tool_name> tag inside <thought_action>"
		} else if !strings.Contains(output, "<tool_input>") {
			reason = "missing <tool_input> tag inside <thought_action>"
		} else if !strings.Contains(output, "</tool_name>") {
			reason = "missing closing </tool_name> tag"
		} else if !strings.Contains(output, "</tool_input>") {
			reason = "missing closing </tool_input> tag"
		}
	} else if strings.Contains(output, "<final_answer>") {
		if !strings.Contains(output, "</final_answer>") {
			reason = "missing closing </final_answer> tag"
		} else if !strings.Contains(output, "<content>") {
			reason = "missing <content> tag inside <final_answer>"
		}
	} else {
		reason = "no <thought_action> or <final_answer> block found"
	}

	if reason != "" {
		return nil, nil, fmt.Errorf("%w: %s", ErrParseFailure, reason)
	}

	return nil, nil, ErrParseFailure
}

// processNotebookUpdate extracts content for notebook update
func (o *NBReActPlanner2) processNotebookUpdate(output string) {
	hasTag := strings.Contains(output, "<update_notebook>")
	var notebookContent string
	if hasTag {
		notebookContent = common.XmlExtractTagContent(output, "update_notebook")
	}

	// Fallback: the LLM may mistakenly emit the notebook update as a tool
	// call (<tool_name>update_notebook</tool_name><tool_input>...</tool_input>)
	// instead of the inline <update_notebook> tag. Detect this pattern and
	// extract the content from tool_input so it is still processed.
	if !hasTag || notebookContent == "" {
		toolNameTag := common.XmlExtractTagContent(output, "tool_name")
		if isNotebookToolName(toolNameTag) {
			candidate := common.XmlExtractTagContent(output, "tool_input")
			if candidate != "" {
				notebookContent = candidate
			}
		}
	}

	if notebookContent != "" {
		o.Notebook = notebookContent
		if o.ctx != nil && o.ctx.GetLogger() != nil {
			o.ctx.GetLogger().Info("reactagent2: notebook updated", "content_length", len(notebookContent))
		}
	}
}

// processToolAction extracts tool name, input and thought from the LLM output
// to create a structured tool action for execution.
func (o *NBReActPlanner2) processToolAction(output string) []NBAgentPlannerToolAction {
	if !strings.Contains(output, "<thought_action>") && !strings.Contains(output, "<tool_name>") && !strings.Contains(output, "<action>") {
		return nil
	}

	toolName := common.XmlExtractTagContent(output, "tool_name")
	toolInput := ""

	if toolName == "" {
		// Robust check: LLM used tool name as tag directly inside <action> or <thought_action>
		actionBlock := common.XmlExtractTagContent(output, "action")
		if actionBlock == "" {
			actionBlock = common.XmlExtractTagContent(output, "thought_action")
		}

		if actionBlock != "" {
			for _, t := range o.tools {
				tName := t.Name()
				if strings.Contains(actionBlock, "<"+tName+">") {
					toolName = tName
					toolInput = common.XmlExtractTagContent(actionBlock, tName)
					break
				}
			}
		}
	} else {
		toolInput = common.XmlExtractTagContent(output, "tool_input")
	}

	if toolName == "" {
		return nil
	}

	// Notebook tool call: capture content and skip execution.
	if isNotebookToolName(toolName) {
		if toolInput != "" && toolInput != o.Notebook {
			o.Notebook = toolInput
			if o.ctx != nil && o.ctx.GetLogger() != nil {
				o.ctx.GetLogger().Info("reactagent2: notebook updated from tool call", "content_length", len(toolInput))
			}
		}
		return nil
	}

	// Detect truncated tool_input: opening tag exists but closing tag is missing.

	if toolInput == "" && (strings.EqualFold(toolName, o.summaryToolName) || strings.EqualFold(toolName, "llm")) {
		toolInput = o.request.Query
	}

	// Apply macro substitution to the tool input
	toolInput = common.SubstituteDateMacros(toolInput)

	// Normalize non-JSON tool input to JSON so the executor can parse it uniformly.
	// The LLM may produce XML tags (<id>value</id>) or key=value pairs instead of JSON.
	toolInput = o.normalizeToolInput(toolName, toolInput)

	thought := common.XmlExtractTagContent(output, "thought")
	if thought == "" {
		thought = common.XmlExtractTagContent(output, "thought_action")
		// Clean up common tags from thought if falling back to thought_action
		if thought != "" {
			thought = strings.ReplaceAll(thought, "<action>", "")
			thought = strings.ReplaceAll(thought, "</action>", "")
		}
	}

	// If no thought extracted, fallback to raw output or a default message
	if thought == "" {
		thought = output
	}

	return []NBAgentPlannerToolAction{
		{
			Tool:      toolName,
			ToolInput: toolInput,
			Log:       thought,
			ToolID:    generateToolId(toolName, toolInput),
		},
	}
}

// normalizeToolInput delegates to the package-level normalizer so all planners
// share a single implementation. See normalizeToolInputForTool in executor_planner.go.
func (o *NBReActPlanner2) normalizeToolInput(toolName, input string) string {
	return normalizeToolInputByName(o.tools, toolName, input)
}

// processFinalAnswer extracts content and thought for a final answer
// from the LLM output.
func (o *NBReActPlanner2) processFinalAnswer(output string) *NBAgentPlannerFinishAction {
	if !strings.Contains(output, "<final_answer>") {
		return nil
	}
	content := common.XmlExtractTagContent(output, "content")
	thought := common.XmlExtractTagContent(output, "thought")

	if content == "" {
		content = common.XmlExtractTagContent(output, "final_answer")
		if content == "" {
			// Defensive: if no content found, treat as parse failure
			// instead of returning an empty final answer.
			return nil
		}
	}

	return &NBAgentPlannerFinishAction{
		Data: content,
		Log:  thought,
	}
}

// processClarification handles clarification requests. It is not currently
// used in this model but kept for interface compatibility.
func (o *NBReActPlanner2) processClarification(output string) *NBAgentPlannerFinishAction {
	return nil
}

// reActCreatePrompt2 builds the initial chat prompt template for the ReAct planner,
// incorporating system messages, tool descriptions, history, and conversation context.
func reActCreatePrompt2(ctx *security.RequestContext, agentPrompt string, toolsIn []toolcore.NBTool, conversationContext string, previousMessages []prompts.MessageFormatter, request NBAgentRequest, agent NBAgent) (prompts.ChatPromptTemplate, []toolcore.NBTool) {
	// Create a copy of tools to avoid modifying the original slice
	tools := make([]toolcore.NBTool, len(toolsIn))
	copy(tools, toolsIn)

	reactBasePrompt := nbprompts.GetPrompt(ctx.GetContext(), nbprompts.PromptReactBase, request.AccountId)
	if reactBasePrompt == "" {
		reactBasePrompt = prompts_repo.GetPrompt(prompts_repo.PromptPlannerReactBase2)
	}

	// Only declare template variables actually referenced in planner_react_base_2.txt.
	// Dynamic vars (history, conversation_context, input, scratchpad) have been moved
	// to the human message to keep the system prefix stable for caching.
	// Note: today is still in the base prompt (daily rotation is acceptable for caching).
	messageFormatters := []prompts.MessageFormatter{
		prompts.NewSystemMessagePromptTemplate(
			reactBasePrompt,
			[]string{
				"tool_names",
				"tool_descriptions",
				"today",
				"workspace_enabled",
				"shell_tool_enabled",
				"context_management_rules",
				"time_handling_rules",
				"data_protection_rules",
				"code_analysis_rules",
				"security_rules",
			},
		),
	}

	if len(request.ClientTools) > 0 {
		// Inject client tools at the beginning
		var clientTools []toolcore.NBTool
		clientToolNames := []string{}
		for _, ct := range request.ClientTools {
			clientTools = append(clientTools, toolcore.NewClientToolWrapper(ct))
			clientToolNames = append(clientToolNames, fmt.Sprintf("'%s'", ct.Name))
		}
		tools = append(clientTools, tools...)

		priorityInstruction := fmt.Sprintf(
			`IMPORTANT: The user has provided "Local Tools" which execute directly on their machine: %s. 
			If the user's request involves their local environment (files, shell, local processes), you MUST prioritize these Local Tools over server-side counterparts.`,
			strings.Join(clientToolNames, ", "))

		messageFormatters = append(messageFormatters, prompts.NewSystemMessagePromptTemplate(priorityInstruction, []string{}))
	}

	// AccountPrompt is intentionally NOT added as a system message: it is only
	// populated by the event-analysis path, so injecting it here would alternate
	// the cacheable prefix per entry-point and bust the Account-scope cache.
	// It is rendered into the human-message <global_preferences> block below.

	agentAdditionalPrompt, configuredTools, _ := AgentAdditionalInstructionsAndToolsAndConfigs(ctx, request.AccountId, agent.GetName())
	if agentAdditionalPrompt != "" {
		additionalAdditionalPrompt := fmt.Sprintf(`
		<additional_agent_prompt>
		%s
		</additional_agent_prompt>`, agentAdditionalPrompt)
		messageFormatters = append(messageFormatters, prompts.NewSystemMessagePromptTemplate(additionalAdditionalPrompt, []string{}))
	}

	if len(configuredTools) > 0 {
		for _, ct := range configuredTools {
			if t, ok := toolcore.GetNBTool(request.AccountId, ct); ok {
				tools = append(tools, t)
			} else {
				ctx.GetLogger().Warn("reactagent2: configured tool not found, skipping", "tool", ct)
			}
		}
	}

	// Inject and filter tools
	tools = FilterAndInjectDefaultTools(request.AccountId, agent, agentPrompt, tools, request.Capabilities)

	// Agent prompt as system message so it falls within the cacheable prefix
	// for Global/Account cache scopes. Dynamic parts (history, context, input, scratchpad) stay as human message.
	messageFormatters = append(messageFormatters, prompts.NewSystemMessagePromptTemplate(agentPrompt, []string{}))

	// Move all dynamic context to the final Human message so the system prefix is stable.
	// global_preferences_block carries AccountPrompt (set only by event-analysis path).
	// Rendering it here keeps the cacheable system prefix identical between event-driven
	// and interactive-chat invocations of the same agent.
	dynamicPrompt := `
{{.global_preferences_block}}
<task_context>
**Previous Conversation Context:** {{.conversation_context}}
**Previous Messages (History):**
{{.history}}
</task_context>

<question>{{.input}}</question>

{{.scratchpad}}`

	messageFormatters = append(messageFormatters, prompts.NewHumanMessagePromptTemplate(dynamicPrompt, []string{
		"conversation_context",
		"history",
		"input",
		"scratchpad",
		"global_preferences_block",
	}))

	// Filter tools based on capabilities
	tools = FilterTools(tools, request.Capabilities)

	tmpl := prompts.NewChatPromptTemplate(messageFormatters)

	previousMessageStr := messageFormatterToString(previousMessages)
	tmpl.PartialVariables = map[string]any{
		// System message template vars (stable — cached across conversations)
		"tool_names":               reActPromptToolNames(tools),
		"tool_descriptions":        reActPromptToolDescriptions(tools),
		"today":                    time.Now().Format("January 02, 2006"),
		"workspace_enabled":        config.Config.LlmServerWorkspaceEnabled,
		"shell_tool_enabled":       config.Config.LlmServerShellToolEnabled && HasShellTool(tools),
		"context_management_rules": prompts_repo.GetPrompt(prompts_repo.PromptContextContinuity),
		"time_handling_rules":      prompts_repo.GetPrompt(prompts_repo.PromptSharedTimeHandlingRules),
		"data_protection_rules":    prompts_repo.GetPrompt(prompts_repo.PromptSharedDataProtectionRules),
		"code_analysis_rules":      prompts_repo.GetPrompt(prompts_repo.PromptSharedCodeAnalysisRules),
		"security_rules":           prompts_repo.GetPrompt(prompts_repo.PromptSharedSecurityRules),
		// Kept for backward-compat: the DB-loaded v1 react_base prompt still uses this conditional.
		"conversation_context_enabled": config.Config.ConversationContextEnabled,
		// Human message template vars (dynamic — change per conversation/iteration)
		"history":                  previousMessageStr,
		"conversation_context":     conversationContext,
		"scratchpad":               "", // default; overridden per-iteration in fullInputs
		"global_preferences_block": renderGlobalPreferencesBlock(request.AccountPrompt),
	}
	return tmpl, tools
}

// NewReActAgent2 initializes a new instance of the ReAct planner with the
// provided configuration and LLM model.
func NewReActAgent2(ctx *security.RequestContext, request NBAgentRequest, nbAgent NBAgent, systemMessage string, extraMessages []prompts.MessageFormatter, initialNotebook string) (*NBReActPlanner2, error) {
	model, err := GetLlmModel(ctx, nbAgent.GetName(), request.AccountId, request.ConversationId)
	if err != nil {
		return &NBReActPlanner2{}, err
	}
	summaryToolName := ""
	if tool, ok := nbAgent.(NBAgentReActPlannerSummaryToolProvider); ok {
		summaryToolName = tool.GetSummaryToolName()
	}

	// Default retry configuration
	retryConfig := RetryConfig{
		MaxRetries: 2,
		Prompts: []string{
			"The previous response was not in the expected format. You MUST use one of these two XML formats:\n\nTo call a tool:\n<thought_action>\n<thought>Reasoning about why this tool is needed</thought>\n<action>\n    <tool_name>tool_name_here</tool_name>\n    <tool_input>input for the tool</tool_input>\n</action>\n</thought_action>\n\nTo give a final answer:\n<final_answer>\n<thought>Brief summary of how you arrived at the answer</thought>\n<content>The final, comprehensive answer for the user</content>\n</final_answer>\n\nDo not write any text outside these XML tags. Re-read the user's question and respond using one of the formats above.",
			"Your previous response was still not in the correct format. YOU MUST output ONLY XML. Pick a tool from your available tools and call it, or provide a final answer. Example tool call:\n<thought_action>\n<thought>I need to check deployment status</thought>\n<action>\n    <tool_name>kubectl_execute</tool_name>\n    <tool_input>kubectl get deploy -n default</tool_input>\n</action>\n</thought_action>\n\nDo NOT apologize or explain. Just output the XML.",
		},
	}

	// Normalize retry config: ensure there is at least one prompt and clamp MaxRetries
	if len(retryConfig.Prompts) == 0 {
		retryConfig.Prompts = []string{"Please reformat your previous response into the required XML format. The root tag must be either <scratchpad> or <final_answer>."}
	}
	if retryConfig.MaxRetries < 0 {
		retryConfig.MaxRetries = 0
	}
	if retryConfig.MaxRetries > 5 {
		retryConfig.MaxRetries = 5
	}

	prompt, tools := reActCreatePrompt2(ctx, systemMessage, nbAgent.GetSupportedTools(ctx), request.ConversationContext, extraMessages, request, nbAgent)

	return &NBReActPlanner2{
		ctx:                   ctx,
		llm:                   model,
		prompt:                prompt,
		request:               request,
		nbAgent:               nbAgent,
		summaryToolName:       summaryToolName,
		retryConfig:           retryConfig,
		refinementAttempts:    0,
		maxRefinementAttempts: 2,
		tools:                 tools,
		enableCritique:        request.EnableCritique,
		Notebook:              initialNotebook,
		compressionTracker:    NewCompressionTracker(),
	}, nil
}

func reActPromptToolNames(tools []toolcore.NBTool) string {
	var tn strings.Builder
	for i, tool := range tools {
		if i > 0 {
			tn.WriteString(", ")
		}
		tn.WriteString(tool.Name())
	}

	return tn.String()
}
