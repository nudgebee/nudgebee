package core

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/memory"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"log/slog"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
)

// Maximum length for database varchar columns (standard is 255)
const maxDBColumnLength = 250 // Using 250 to leave some safety margin

func guessAgentStatusFromResponse(plannerResponse NBAgentPlannerExecutorResponse) AgentExecutionStatus {
	if plannerResponse.Status == AgentExecutionStatusFail || plannerResponse.Status == AgentExecutionStatusWaiting || plannerResponse.Status == AgentExecutionStatusSkipped {
		return plannerResponse.Status
	}

	response := plannerResponse.Response

	if len(response) == 0 {
		return AgentExecutionStatusFail
	}
	response = strings.TrimSpace(response)
	response = strings.ToLower(response)

	if response == "[]" || response == "{}" || response == "null" || response == "none" || response == "no action" {
		return AgentExecutionStatusFail
	}

	if strings.HasSuffix(response, "agent not finished") || strings.HasSuffix(response, "agent:noaction") || strings.HasSuffix(response, "error:") {
		return AgentExecutionStatusFail
	}

	if strings.Contains(response, `"error"`) || strings.Contains(response, "bedrock runtime") || strings.HasSuffix(response, "none is not a valid tool") {
		return AgentExecutionStatusFail
	}

	if strings.Contains(response, "unable to fetch") || strings.Contains(response, "unfortunately") {
		return AgentExecutionStatusFail
	}

	if strings.Contains(response, toolcore.ErrUnableToFetchData.Error()) || strings.Contains(response, errLlmUnableToGenerate.Error()) {
		return AgentExecutionStatusFail
	}

	return AgentExecutionStatusSuccess
}

// sanitizeErrorForUser checks for sensitive or low-level errors and returns a user-friendly message.
func sanitizeErrorForUser(err error) string {
	if err == nil {
		return ""
	}
	errStr := err.Error()
	// Check for common DB connection/timeout errors
	// This list can be expanded based on observed errors
	sensitivePatterns := []string{
		"i/o timeout",
		"connection refused",
		"read tcp",
		"write tcp",
		"dial tcp",
		"sql:",
		"pq:",
	}

	for _, pattern := range sensitivePatterns {
		if strings.Contains(strings.ToLower(errStr), pattern) {
			return "An internal system error occurred while processing your request. Please try again later."
		}
	}

	return errStr
}

func executeAgent(ctx *security.RequestContext, agent NBAgent, request NBAgentRequest) (NBAgentResponse, error) {
	// --- Metrics: record start time
	start := time.Now()
	accountID := request.AccountId
	agentName := agent.GetName()
	// Record agent operation start (status = "start")
	common.MetricsAgentOperationsTotal(agentName, "start", accountID)
	ctx.GetLogger().Info("agentexecutor: executing ExecuteAgent from Agent Executor", "for agent", agentName)
	err := common.ValidateStruct(request)
	if err != nil {
		ctx.GetLogger().Info("agentexecutor: validation failed", "error", err)
		// Metrics: record fail
		common.MetricsAgentOperationsTotal(agentName, "fail", accountID)
		common.MetricsAgentLatencySeconds(agentName, accountID, time.Since(start).Seconds())
		return NBAgentResponse{}, common.ErrorBadRequest("agentexecutor: unable to complete request, Please try again later")
	}
	if len(request.Query) == 0 {
		// Metrics: record fail
		common.MetricsAgentOperationsTotal(agentName, "fail", accountID)
		common.MetricsAgentLatencySeconds(agentName, accountID, time.Since(start).Seconds())
		return NBAgentResponse{}, errors.New("agentexecutor: not enough data")
	}

	// Check if the conversation message has been terminated
	isTerminated, err := checkMessageTerminationStatus(request.MessageId, request.AccountId, request.ConversationId)
	if err != nil {
		ctx.GetLogger().Warn("agentexecutor: unable to get conversation message", "message", request.MessageId)
		// Metrics: record fail
		common.MetricsAgentOperationsTotal(agentName, "fail", accountID)
		common.MetricsAgentLatencySeconds(agentName, accountID, time.Since(start).Seconds())
		return NBAgentResponse{}, err
	}

	if isTerminated {
		ctx.GetLogger().Info("agentexecutor: conversation message terminated, stopping execution", "messageId", request.MessageId)
		// Metrics: record fail
		common.MetricsAgentOperationsTotal(agentName, "fail", accountID)
		common.MetricsAgentLatencySeconds(agentName, accountID, time.Since(start).Seconds())
		return NBAgentResponse{
			Response:       []string{"Conversation terminated by user."},
			AgentName:      agentName,
			ConversationId: request.ConversationId,
			Status:         ConversationStatusTerminated,
			MessageId:      request.MessageId,
		}, errors.New("conversation terminated")
	}

	// remove leading @agent mention from the actual query as its causing
	// confusions to agent. Use the shared helper so cleaning is consistent
	// with title generation (see common.StripLeadingAgentMention).
	request.Query = common.StripLeadingAgentMention(request.Query)

	// get history and use it as context - PARALLELIZED
	var messageHistoryFomatter []prompts.MessageFormatter
	var historyErr error
	var existingAgentId uuid.UUID

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		historyStart := time.Now()
		messageHistoryFomatter, historyErr = getExistingHistory(request, ctx)
		ctx.GetLogger().Info("agentexecutor: history loaded", "duration", time.Since(historyStart).String())
	}()

	go func() {
		defer wg.Done()
		if request.AgentId == "" && request.PreviousState != "" {
			existingAgents, err := GetConversationDao().ListConversationAgents(request.MessageId, "")
			if err == nil {
				for _, a := range existingAgents {
					if strings.EqualFold(a.AgentName, agent.GetName()) && (strings.EqualFold(string(a.Status), string(AgentExecutionStatusWaiting)) || strings.EqualFold(string(a.Status), string(AgentExecutionStatusWaitingForClientTool))) {
						existingAgentId = a.ID
						break
					}
				}
			}
		}
	}()

	wg.Wait()

	if historyErr != nil {
		// Metrics: record fail
		common.MetricsAgentOperationsTotal(agentName, "fail", accountID)
		common.MetricsAgentLatencySeconds(agentName, accountID, time.Since(start).Seconds())
		return NBAgentResponse{}, historyErr
	}

	// Build conversation context for the agent: message history + distilled context.
	// When ConversationContextEnabled, conversation.go sets request.ConversationContext
	// to the distilled context JSON (LlmUnifiedExtraction from redistillation).
	// We parse it, format the human-readable summary, and prepend it to the message
	// history so the planner sees both long-term facts and recent conversation turns.
	historyStr := messageFormatterToString(messageHistoryFomatter)
	// convFacts collects the fact contents already present in the per-conversation
	// context so that the LTM notebook can suppress redundant entries at injection time.
	var convFacts []string
	if config.Config.ConversationContextEnabled && request.ConversationContext != "" {
		var unified LlmUnifiedExtraction
		if err := common.UnmarshalJson([]byte(request.ConversationContext), &unified); err == nil {
			for _, f := range unified.MemoryFacts {
				if f.Content != "" {
					convFacts = append(convFacts, f.Content)
				}
			}
			if summary := unified.String(); summary != "" {
				historyStr = "## Conversation Memory\n" + summary + "\n**When the user's query does not specify a resource or entity, default to the Current Subject above.**\n\n## Previous Messages\n" + historyStr
			}
		}
	}
	request.ConversationContext = historyStr
	// saving the agent to Db
	var agentId uuid.UUID
	var previousAgentState string
	originalAgentId := request.AgentId
	if request.AgentId == "" {
		if existingAgentId != uuid.Nil {
			agentId = existingAgentId
			request.AgentId = existingAgentId.String()
			// For existing agents, we need to resolve parent ID
			parentAgentId, previousState := GetConversationDao().GetConversationAgentParentAgentIdAndPreviousState(agentId.String())
			request.ParentAgentId = parentAgentId
			if parentAgentId == "" || parentAgentId == uuid.Nil.String() {
				request.ParentAgentId = request.AgentId
			}
			request.PreviousState = previousState
			previousAgentState = previousState
		} else {
			agentIdUuid, err := GetConversationDao().SaveConversationAgentCall(request.ConversationId, request.MessageId, request.AccountId, request.UserId, agent.GetName(), request.ParentAgentId, request.Query, "", "", request.QueryContext, request.QueryConfig)
			if err != nil {
				ctx.GetLogger().Error("agentexecutor: failed to save agent call to DB, agent token tracking will be unavailable", "error", err)
				// Keep AgentId empty so downstream code (trackTokenUsage) knows no record exists
				// rather than using uuid.Nil which would cause FK violations
			} else {
				request.AgentId = agentIdUuid.String()
				agentId = agentIdUuid
			}
			if request.ParentAgentId == "" || request.ParentAgentId == uuid.Nil.String() {
				if agentIdUuid != uuid.Nil {
					request.ParentAgentId = agentIdUuid.String()
				}
			}
		}

		// If a previous state was provided via request (resumption), use it
		if request.PreviousState != "" {
			previousAgentState = request.PreviousState
		}
	} else {
		agentId, err = uuid.Parse(request.AgentId)
		if err != nil {
			ctx.GetLogger().Error("agentexecutor: unable to parse agent id", "error", err)
			return NBAgentResponse{}, err
		}
		parentAgentId, previousState := GetConversationDao().GetConversationAgentParentAgentIdAndPreviousState(agentId.String())
		request.ParentAgentId = parentAgentId
		if parentAgentId == "" || parentAgentId == uuid.Nil.String() {
			request.ParentAgentId = request.AgentId
		}
		previousAgentState = previousState
		request.PreviousState = previousState
	}

	// Extract actionable context from attached images before planning.
	// This enriches vague queries like "can you investigate" with concrete details
	// (service names, error codes, metric values) visible in the screenshots.
	if len(request.Images) > 0 && IsImageSupportEnabled() {
		request.Query = ExtractImageContext(ctx, request)
	}

	// setting the parent agent id
	// Get base system prompt (includes GC for k8s_debugger)
	promptStart := time.Now()
	basePrompt := agent.GetSystemPrompt(ctx, request)
	ctx.GetLogger().Info("agentexecutor: system prompt generated", "duration", time.Since(promptStart).String())

	var initialNotebook string
	var kbResult kbAssemblyResult

	// Inject a `<skill-lists>` block (names + descriptions only — no bodies, so the
	// prompt overhead is minimal) into the agent's system prompt. ReAct/ReWoo
	// planners read it via the lazy load_skills tool: the LLM picks which skills
	// to actually fetch based on the question.
	//
	// skillAgentNames is the union of the agent's own name and any inherited
	// ancestor names. Inherited names are set by custom-planner delegators
	// (metrics → prometheus, logs → log_default, log_default → query_generator, ...)
	// so a sub-agent's lazy <skill-lists> can also see KBs the user mapped to its
	// custom-planner parent.
	skillAgentNames := append([]string{agent.GetName()}, request.InheritSkillsFromAgents...)

	// Top-level invocation detection: OriginalQuery is empty until the executor
	// stamps it here. Sub-agents reached via ExecuteAgentToolCall already carry
	// the parent's OriginalQuery and SelectedSkillIds verbatim and must NOT re-run
	// selection — running it against a mechanical sub-agent command (e.g.
	// "fetch CPU for pod foo") would destroy the relevance signal.
	isTopLevelInvocation := request.OriginalQuery == ""
	if isTopLevelInvocation {
		request.OriginalQuery = request.Query

		// Question-aware skill narrowing produces a different `<skill-lists>` block
		// for every distinct user question. That block is injected into the system
		// prompt prefix, so for agents that opt into Account/Global LLM cache scope
		// it would invalidate the cache on every new question — exactly the
		// account-cache-thrash we're trying to avoid.
		//
		// Skip selection for cacheable scopes: `injectKBContext` will then keep
		// all active mapped KBs (selectedIds == nil), giving an account-stable
		// skill-lists block. Conversation-scope agents keep the BM25 narrowing
		// because their cache is already per-conversation.
		cacheScope := CacheScopeConversation
		if cacheProvider, ok := agent.(NBAgentCacheScopeProvider); ok {
			cacheScope = cacheProvider.GetCacheScope()
		}

		topK := config.Config.LlmServerSkillSelectionTopK
		if cacheScope != CacheScopeConversation {
			ctx.GetLogger().Debug("agentexecutor: skipping question-aware skill selection for cacheable scope",
				"agent", agent.GetName(), "scope", cacheScope)
		} else if topK > 0 {
			candidates, cErr := toolcore.ListActiveAgentSkillCandidates(ctx, request.AccountId, skillAgentNames)
			if cErr != nil {
				ctx.GetLogger().Warn("agentexecutor: skill selection candidate fetch failed; falling back to show-all", "error", cErr, "agent", agent.GetName())
			} else if len(candidates) > 0 {
				selected := toolcore.SelectRelevantSkills(request.OriginalQuery, candidates, topK)
				if selected != nil {
					request.SelectedSkillIds = selected
					ctx.GetLogger().Info("agentexecutor: skill selection narrowed mapped skills", "agent", agent.GetName(), "candidate_count", len(candidates), "selected_count", len(selected), "top_k", topK)
				}
			}
		}
	}

	kbChan := make(chan kbAssemblyResult, 1)
	go func(prompt NBAgentPrompt, selected []string) {
		userQuery := request.OriginalQuery
		if userQuery == "" {
			userQuery = request.Query
		}
		if config.Config.LlmServerKBPrestepEnabled {
			// Pre-step path: KB content goes to the human message, not the
			// cacheable system prefix. The `<skill-lists>` menu is built for any
			// agent with KB mappings (so load_skills still works); the eager RAG
			// retrieval runs only for the top-level invocation — sub-agents keep
			// the lazy menu + load_skills flow.
			kbs := fetchAgentKBs(ctx, request.AccountId, skillAgentNames, selected)
			if len(kbs) == 0 {
				kbChan <- kbAssemblyResult{prompt: prompt}
				return
			}
			menu := buildSkillListsMenu(kbs)
			block := ""
			var kbRefs []AgentReference
			if isTopLevelInvocation {
				// Per-KB retrieval: references reflect only the KBs whose
				// content actually matched, not every mapped KB.
				block, kbRefs = retrieveRelevantKB(ctx, request, kbs)
			}
			kbChan <- kbAssemblyResult{prompt: prompt, menu: menu, prestepBlock: block, kbRefs: kbRefs}
			return
		}
		// Legacy path: skill-lists injected into the cacheable system prompt.
		kbChan <- kbAssemblyResult{prompt: injectKBContext(ctx, request.AccountId, skillAgentNames, selected, agent.GetPlannerType(), prompt, userQuery)}
	}(basePrompt, request.SelectedSkillIds)

	// When the Memory Module is enabled for this tenant, it is the sole memory
	// source for the prompt. The legacy similarity-based notebook is skipped
	// entirely — no concatenation, no double-injection. When the module is off
	// (or the tenant is not allowlisted) the legacy notebook remains primary.
	tenantID := ctx.GetSecurityContext().GetTenantId()
	memoryModuleActive := memory.ComposeEnabledFor(tenantID)

	memChan := make(chan string, 1)
	memV2Chan := make(chan string, 1)
	if memoryModuleActive {
		memChan <- ""
		go func() {
			memV2Chan <- composeMemoryV2Block(ctx, request, agent)
		}()
	} else {
		go func(cf []string) {
			memChan <- retrieveAndBuildMemoryNotebook(ctx, request, agent, cf)
		}(convFacts)
		memV2Chan <- ""
	}

	// Collect results
	kbStart := time.Now()
	kbResult = <-kbChan
	if memoryModuleActive {
		initialNotebook = <-memV2Chan
		<-memChan // drain
	} else {
		initialNotebook = <-memChan
		<-memV2Chan // drain
	}
	ctx.GetLogger().Info("agentexecutor: KB and memory retrieval complete", "duration", time.Since(kbStart).String(), "memory_module_active", memoryModuleActive)

	if len(kbResult.prompt.Instructions) > 0 {
		basePrompt = kbResult.prompt
	}
	// Pre-step path: carry the skill-lists menu and retrieved KB content on the
	// request so the planner renders them into the human message (out of the
	// cacheable system prefix). Empty on the legacy path.
	request.SkillListsMenu = kbResult.menu
	request.KBPrestepContent = kbResult.prestepBlock

	// Persist pre-step KB references so the UI's "Skills used" surface shows
	// which KBs the pre-step retrieval pulled in — the same way it shows lazy
	// load_skills calls. SaveAgentReferences de-duplicates, so a KB the planner
	// later loads explicitly is not double-counted.
	if agentId != uuid.Nil && len(kbResult.kbRefs) > 0 {
		if err := GetConversationDao().SaveAgentReferences(request.AccountId, request.ConversationId, request.MessageId, agentId.String(), kbResult.kbRefs); err != nil {
			ctx.GetLogger().Warn("agentexecutor: failed to save KB pre-step references", "error", err)
		}
	}

	// Custom-planner agents (loganalysis, metrics, traces, logs, logs_default,
	// resource_search, websearch) implement their own Execute() and bypass the
	// systemMessage path below, so the lazy `<skill-lists>` + load_skills mechanism
	// injected into basePrompt above never reaches their LLM. Eagerly load the full
	// bodies of the selected mapped KBs into request.SkillsContext so their
	// Execute() can prepend it to its prompt.
	//
	// "Selected" honours request.SelectedSkillIds when LlmServerSkillSelectionTopK
	// is enabled — otherwise every active KB mapped to (own ∪ inherited names) is
	// loaded. Per-KB references for whatever was actually loaded are appended to
	// the final agent response below so the UI can show "Skills used" entries the
	// same way it lists tool references.
	var skillReferences []toolcore.NBToolResponseReference
	if agent.GetPlannerType() == AgentPlannerTypeCustom {
		skillsContext, refs, sErr := toolcore.LoadActiveAgentSkillContents(ctx, request.AccountId, skillAgentNames, request.SelectedSkillIds)
		if sErr != nil {
			ctx.GetLogger().Warn("agentexecutor: failed to load active agent skill contents", "error", sErr, "agent", agent.GetName())
		} else if skillsContext != "" {
			ctx.GetLogger().Info("agentexecutor: injecting eager skills content for custom-planner agent", "agent", agent.GetName(), "size", len(skillsContext), "skill_count", len(refs), "inherited_from", request.InheritSkillsFromAgents, "selection_active", request.SelectedSkillIds != nil)
			request.SkillsContext = skillsContext
			skillReferences = refs

			// Persist skill references to llm_conversation_references so
			// the UI can render them in the "Additional Contexts" tab.
			// This mirrors the lazy path in planner_callback_handler.go
			// which saves them on load_skills tool completion.
			// Every agent in the chain attempts to save — the DAO's
			// WHERE NOT EXISTS deduplicates on (conversation, message,
			// reference_id, reference_type), so inherited skills that
			// were already saved by a parent are silently skipped while
			// skills mapped directly to a sub-agent are still persisted.
			if agentId != uuid.Nil && len(refs) > 0 {
				kbRefs := make([]AgentReference, 0, len(refs))
				for _, ref := range refs {
					if ref.Type == "skill" && ref.Url != "" {
						kbRefs = append(kbRefs, AgentReference{
							Type:        AgentReferenceTypeKB,
							ReferenceID: ref.Url,
							Metadata: map[string]any{
								"name":        ref.Text,
								"description": ref.Description,
							},
						})
					}
				}
				if err := GetConversationDao().SaveAgentReferences(request.AccountId, request.ConversationId, request.MessageId, agentId.String(), kbRefs); err != nil {
					ctx.GetLogger().Warn("agentexecutor: failed to save eager skill KB references", "error", err)
				}
			}
		}
	}

	// Compute the effective planner type for prompt rendering. When a ReWOO agent
	// is upgraded to react_3 via config, the prompt template must use react-style
	// formatting (e.g. FINAL ANSWER REQUIREMENTS, <examples>) instead of ReWOO style.
	effectivePlannerType := agent.GetPlannerType()
	if effectivePlannerType == AgentPlannerTypeReWoo && config.Config.LlmServerRewooToReact3Enabled {
		effectivePlannerType = AgentPlannerTypeReAct3
	} else if effectivePlannerType == AgentPlannerTypeReAct && config.Config.LlmServerReAct3Enabled {
		effectivePlannerType = AgentPlannerTypeReAct3
	}
	systemMessage, sysFmtErr := GetPromptTemplate(basePrompt, request, effectivePlannerType).Format(map[string]any{"history": messageFormatterToString(messageHistoryFomatter)})
	// Surface template-render failures: a Format error here yields an empty system prompt, which Bedrock Converse rejects with a 400 (issue #30120).
	if sysFmtErr != nil {
		ctx.GetLogger().Warn("agentexecutor: system prompt template render failed; system message will be empty", "error", sysFmtErr, "agent", agent.GetName())
	}

	// check if the query needs to be refined or we need to generate initial followup
	// Only handle followups when resuming an existing agent (caller provided AgentId or
	// we found a WAITING agent for this message). For freshly-created agents there is
	// no prior followup to process, and calling HandleFollowupResponse with the new
	// agent's ID causes a spurious "agentid is not found" error.
	isResumingAgent := existingAgentId != uuid.Nil || originalAgentId != ""
	refinementStart := time.Now()
	ctx.GetLogger().Info("agentexecutor: handling refinement/followups", "query", request.Query, "agentId", agentId, "isResuming", isResumingAgent)
	var refinementFollowupResponse NBAgentResponse
	var refinementErr error
	if isResumingAgent {
		request, refinementFollowupResponse, refinementErr = refineAgentQuestionAndHandleFollowups(ctx, request, agent, messageFormatterToString(messageHistoryFomatter), agentId)
		if refinementErr != nil {
			ctx.GetLogger().Error("agentexecutor: unable to do query refinement, using original question", "error", refinementErr)
		}
	}
	if len(refinementFollowupResponse.Response) > 0 {
		ctx.GetLogger().Info("agentexecutor: returning refinement followup response", "response", refinementFollowupResponse.Response[0])
		// Metrics: record success for followup response
		common.MetricsAgentOperationsTotal(agentName, "success", accountID)
		common.MetricsAgentLatencySeconds(agentName, accountID, time.Since(start).Seconds())
		return refinementFollowupResponse, nil
	}

	ctx.GetLogger().Info("agentexecutor: executing agent planner", "agent", agent.GetName(), "query_len", len(request.Query), "hasState", request.PreviousState != "", "refinement_duration", time.Since(refinementStart).String(), "total_setup_duration", time.Since(start).String())

	var response NBAgentPlannerExecutorResponse
	if customAgent, ok := agent.(NBCustomAgent); ok && agent.GetPlannerType() == AgentPlannerTypeCustom {
		// Restore state for stateful custom agents (e.g., WorkflowBuilder multi-stage flow)
		if previousAgentState != "" {
			type statefulAgent interface {
				UnmarshalState([]byte) error
			}
			if stateful, ok := customAgent.(statefulAgent); ok {
				if unmarshalErr := stateful.UnmarshalState([]byte(previousAgentState)); unmarshalErr != nil {
					ctx.GetLogger().Error("agentexecutor: failed to restore custom agent state", "error", unmarshalErr)
				} else {
					ctx.GetLogger().Info("agentexecutor: restored custom agent state", "stateLen", len(previousAgentState))
				}
			}
		}

		// Custom agents embed dynamic content (query, logs, errors) directly in their system
		// messages, so provider-level prompt caching would create wasteful cache entries that
		// are never reused. Disable caching for the entire Execute() subtree. Individual custom
		// agents that have a specific LLM call with a stable system prompt can override this
		// by creating a sub-context with ContextKeyDisableCaching set to false.
		noCacheCtx := security.NewRequestContext(
			context.WithValue(ctx.GetContext(), ContextKeyDisableCaching, true),
			ctx.GetSecurityContext(),
			ctx.GetLogger(),
			ctx.GetTracer(),
			ctx.GetMeter(),
		)
		customResp, err := customAgent.Execute(noCacheCtx, request)
		response = NBAgentPlannerExecutorResponse{
			Response:          strings.Join(customResp.Response, "\n"),
			Status:            AgentExecutionStatus(customResp.Status),
			Invocations:       customResp.AgentStepResponse,
			AgentStepResponse: customResp.AgentStepResponseData,
			References:        customResp.References,
			IsTerminal:        customResp.IsTerminal,
			Followup:          customResp.FollowupRequest,
		}

		// Serialize state for stateful custom agents so it persists across turns
		type marshalableAgent interface {
			MarshalState() ([]byte, error)
		}
		if marshaler, ok := customAgent.(marshalableAgent); ok {
			if stateBytes, marshalErr := marshaler.MarshalState(); marshalErr == nil {
				response.State = string(stateBytes)
			} else {
				ctx.GetLogger().Error("agentexecutor: failed to serialize custom agent state", "error", marshalErr)
			}
		}

		if err != nil {
			// Mark the agent record as failed so it doesn't stay orphaned as in_progress
			if agentId != uuid.Nil {
				dbErr := GetConversationDao().UpdateConversationAgentResponse(agentId.String(), err.Error(), AgentExecutionStatusFail, "", "", "", "")
				if dbErr != nil {
					ctx.GetLogger().Error("agentexecutor: failed to update custom agent status on error", "error", dbErr)
				}
			}
			// Even on error, we might have some response data to return
			if len(customResp.Response) > 0 {
				return customResp, err
			}
			return NBAgentResponse{}, err
		}
	} else {
		nbAgentPlanner, err := createAgentPlanner(ctx, agent, request, systemMessage, messageHistoryFomatter, initialNotebook)
		if err != nil {
			// Try to update DB, but don't let a DB error mask the original error
			dbErr := GetConversationDao().UpdateConversationAgentResponse(agentId.String(), err.Error(), AgentExecutionStatusFail, "", "unable to create plan", "", "")
			if dbErr != nil {
				ctx.GetLogger().Error("agentexecutor: Failed to save agent call", "error", dbErr)
			}
			// Metrics: record fail
			common.MetricsAgentOperationsTotal(agentName, "fail", accountID)
			common.MetricsAgentLatencySeconds(agentName, accountID, time.Since(start).Seconds())

			// Return sanitized error to user
			return NBAgentResponse{}, errors.New(sanitizeErrorForUser(err))
		}

		response, err = executeAgentPlanner(ctx, nbAgentPlanner, agent, request, previousAgentState)
		if err != nil {
			ctx.GetLogger().Info("agentexecutor: run call to agent completed with error", "agent", agent.GetName(), "status", response.Status, "error", err)
		}
	}
	// Metrics: record operation result and latency
	status := response.Status
	if status == "" {
		status = guessAgentStatusFromResponse(response)
	}
	ctx.GetLogger().Info("agentexecutor: operation metrics", "agent", agent.GetName(), "status", status)
	switch status {
	case AgentExecutionStatusFail:
		common.MetricsAgentOperationsTotal(agentName, "fail", accountID)
	case AgentExecutionStatusWaiting:
		common.MetricsAgentOperationsTotal(agentName, "waiting", accountID)
	default:
		common.MetricsAgentOperationsTotal(agentName, "success", accountID)
	}
	common.MetricsAgentLatencySeconds(agentName, accountID, time.Since(start).Seconds())

	agentStatus := response.Status
	if agentStatus == "" {
		agentStatus = guessAgentStatusFromResponse(response)
	}

	// Map statuses to allowed database values (lowercase)
	var finalDbStatus AgentExecutionStatus
	switch {
	case strings.EqualFold(string(agentStatus), string(AgentExecutionStatusWaiting)) || strings.EqualFold(string(agentStatus), string(ConversationStatusWaiting)):
		finalDbStatus = AgentExecutionStatusWaiting
	case strings.EqualFold(string(agentStatus), string(AgentExecutionStatusWaitingForClientTool)) || strings.EqualFold(string(agentStatus), string(ConversationStatusWaitingForClientTool)):
		finalDbStatus = AgentExecutionStatusWaitingForClientTool
	case strings.EqualFold(string(agentStatus), string(AgentExecutionStatusFail)) || strings.EqualFold(string(agentStatus), string(ConversationStatusFailed)):
		finalDbStatus = AgentExecutionStatusFail
	default:
		finalDbStatus = AgentExecutionStatusSuccess
	}

	ctx.GetLogger().Info("agentexecutor: finalized status mapping", "finalStatus", finalDbStatus, "originalStatus", agentStatus)

	// Ensure all fields fit within database column constraints
	limitedSummary := limitStringLength(response.ResponseSummary, maxDBColumnLength)

	agentStepResponseJson := ""
	if response.AgentStepResponse != nil {
		asb, _ := common.MarshalJson(response.AgentStepResponse)
		agentStepResponseJson = string(asb)
	}

	// Merge skill references into the response before the DB save so that the
	// agent record's JSON "references" column includes them alongside tool refs.
	mergedReferences := dedupeSkillReferences(append(response.References, skillReferences...))

	referencesJson := ""
	if mergedReferences != nil {
		rb, _ := common.MarshalJson(mergedReferences)
		referencesJson = string(rb)
	}

	dbErr := GetConversationDao().UpdateConversationAgentResponse(agentId.String(), response.Response, finalDbStatus, response.State, limitedSummary, agentStepResponseJson, referencesJson)
	if dbErr != nil {
		ctx.GetLogger().Error("agentexecutor: unable to save agent call", "error", dbErr)
	}

	// Phase 2: Async Summary Generation for success responses
	// Moved here from executeAgentPlanner to avoid race conditions with the main DB update
	// and to ensure the main response is returned to the user as quickly as possible.
	isSummaryTask := strings.EqualFold(agent.GetName(), ToolLlm) && request.ParentAgentId != "" && request.ParentAgentId != uuid.Nil.String()
	if dbErr == nil && finalDbStatus == AgentExecutionStatusSuccess && !isSummaryTask && (agent.GetPlannerType() == AgentPlannerTypeCustom || isReActStylePlanner(agent.GetPlannerType())) && len(response.Response) > 50 && agentId != uuid.Nil {
		// Generate 1 liner summary asynchronously
		bgCtx := security.NewRequestContext(
			context.Background(),
			ctx.GetSecurityContext(),
			ctx.GetLogger(), ctx.GetTracer(),
			ctx.GetMeter(),
		)

		submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
		defer cancel()
		err := conversationAsyncTaskWorkerPool.Submit(submissionCtx, func() {
			generateAsyncAgentSummary(bgCtx, request, response.Response, agentId.String())
		})
		if err != nil {
			ctx.GetLogger().Error("agentexecutor: failed to submit agent summary task", "error", err)
		}
	}

	// Handle response if it has followup with a question
	if response.Followup.Question != "" && (agentStatus == AgentExecutionStatusWaiting || strings.EqualFold(string(agentStatus), string(AgentExecutionStatusWaiting))) {
		dao := GetConversationDao()

		// CRITICAL: Ensure the followup request points to THIS agent (the parent)
		// so that the next user turn correctly resumes this agent instead of jumping to the child.
		response.Followup.AgentId = agentId
		response.Followup.AgentName = agent.GetName()

		followUpRequest := response.Followup
		// Check if the agent already has a followup message
		followUpExists := false
		agents, err := dao.ListConversationAgents("", followUpRequest.AgentId.String())
		if err == nil && len(agents) > 0 {
			existingAgent := agents[0]
			if existingAgent.FollowupMessageID != uuid.Nil {
				// Agent already has a followup message - check if it's currently waiting
				fmsg, fErr := dao.GetConversationMessage(existingAgent.FollowupMessageID.String(), request.AccountId, request.ConversationId)
				if fErr == nil && fmsg.Status != ConversationStatusCompleted {
					ctx.GetLogger().Info("followup: agent already has an active followup message, updating config",
						"agentId", followUpRequest.AgentId.String(),
						"followupMessageId", existingAgent.FollowupMessageID)
					newConfig := map[string]any{
						"question":        followUpRequest.Question,
						"followupType":    followUpRequest.FollowupType,
						"followupOptions": followUpRequest.FollowupOptions,
						"toolName":        followUpRequest.ToolName,
						"toolId":          followUpRequest.ToolId,
					}
					if updateErr := dao.UpdateConversationMessageFollowupConfig(existingAgent.FollowupMessageID.String(), newConfig); updateErr != nil {
						ctx.GetLogger().Error("followup: failed to update followup config", "error", updateErr)
					}
					followUpExists = true
				}
			}
		}
		if !followUpExists {
			ctx.GetLogger().Info("agentexecutor: generating new followup message", "agent", agent.GetName(), "type", followUpRequest.FollowupType)
			_, err := GenerateFollowup(ctx, request, followUpRequest)
			if err != nil {
				ctx.GetLogger().Error("agentexecutor: unable to generate followup", "error", err)
			}
		}
	}

	var conversationStatus ConversationStatus
	switch {
	case strings.EqualFold(string(agentStatus), string(AgentExecutionStatusWaiting)) ||
		strings.EqualFold(string(agentStatus), string(ConversationStatusWaiting)):
		conversationStatus = ConversationStatusWaiting
	case strings.EqualFold(string(agentStatus), string(AgentExecutionStatusWaitingForClientTool)) ||
		strings.EqualFold(string(agentStatus), string(ConversationStatusWaitingForClientTool)):
		conversationStatus = ConversationStatusWaitingForClientTool
	case strings.EqualFold(string(agentStatus), string(AgentExecutionStatusFail)) ||
		strings.EqualFold(string(agentStatus), string(ConversationStatusFailed)):
		conversationStatus = ConversationStatusFailed
	default:
		conversationStatus = ConversationStatusCompleted
	}
	ctx.GetLogger().Info("agentexecutor: finalized conversation status", "status", conversationStatus)

	agentResponse := NBAgentResponse{
		Response:              []string{response.Response},
		AgentName:             agent.GetName(),
		Query:                 request.Query,
		AgentStepResponse:     response.Invocations,
		AgentStepResponseData: response.AgentStepResponse,
		ConversationId:        request.ConversationId,
		Status:                conversationStatus,
		AgentId:               agentId.String(),
		MessageId:             request.MessageId,
		IsTerminal:            response.IsTerminal,
		FollowupRequest:       response.Followup,
		QueryConfig:           &request.QueryConfig,
		References:            mergedReferences,
	}

	// Generate image descriptions asynchronously for follow-up context.
	// Fires for completed and waiting turns so history replay has image context on resume.
	if len(request.Images) > 0 && IsImageSupportEnabled() &&
		agentResponse.Status != ConversationStatusFailed &&
		agentResponse.Status != ConversationStatusTerminated {
		GenerateImageDescriptionsAsync(ctx, request)
	}

	// use response formatting only when there are multiple agents involved
	if agentResponse.Status == ConversationStatusCompleted && (request.ParentAgentId == "" || request.ParentAgentId == request.AgentId || request.ParentAgentId == uuid.Nil.String()) {
		if agent.GetPlannerType() == AgentPlannerTypeReWoo || agent.GetPlannerType() == AgentPlannerTypeReAct3 || config.Config.LlmServerRewooToReact3Enabled {
			distinctAgents := make(map[string]bool)
			for _, invocation := range agentResponse.AgentStepResponse {
				if invocation.Call.FunctionCall != nil && invocation.Call.FunctionCall.Name != "" && !strings.EqualFold(invocation.Call.FunctionCall.Name, "llm") && !strings.EqualFold(invocation.Call.FunctionCall.Name, "planner") && !strings.Contains(invocation.Call.FunctionCall.Name, "debug") {
					if _, ok := GetNBAgent(ctx, invocation.Call.FunctionCall.Name, request.AccountId, AgentStatusEnabled); ok {
						distinctAgents[strings.ToLower(invocation.Call.FunctionCall.Name)] = true
					}
					if len(distinctAgents) > 1 {
						break
					}
				}
			}

			if len(distinctAgents) > 1 {
				agentResponse = FormatAgentResponse(ctx, request, agentResponse, agent.GetPlannerType())
			}
		}
	}

	return agentResponse, err
}

func refineAgentQuestionAndHandleFollowups(ctx *security.RequestContext, request NBAgentRequest, agent NBAgent, history string, agentId uuid.UUID) (NBAgentRequest, NBAgentResponse, error) {
	ctx.GetLogger().Info("followup: handling followup response", "agentId", request.AgentId, "query", request.Query)
	followupMessage, err := HandleFollowupResponse(ctx, request)
	if err != nil {
		ctx.GetLogger().Error("agentexecutor: unable to handle followup response", "error", err)
		return request, NBAgentResponse{}, err
	}

	if followupMessage.ID != uuid.Nil {
		ctx.GetLogger().Info("followup: identified followup response", "msgId", followupMessage.ID, "userResponse", followupMessage.Response)
		previousQuery := ""

		followupMessageType := string(FollowupTypeSingleSelect)
		toolName := ""
		existingToolConfigs := map[string]string{}
		existingToolConfirmations := map[string]string{}
		if request.QueryConfig.ToolConfigs != nil {
			for k, v := range request.QueryConfig.ToolConfigs {
				existingToolConfigs[k] = v
			}
		}
		if request.QueryConfig.ToolConfirmations != nil {
			for k, v := range request.QueryConfig.ToolConfirmations {
				existingToolConfirmations[k] = v
			}
		}
		if followupMessage.MessageContext != nil {
			previousQuestion := NBAgentRequest{}
			err := common.UnmarshalJson([]byte(*followupMessage.MessageContext), &previousQuestion)
			if err != nil {
				ctx.GetLogger().Error("agentexecutor: unable to unmarshal followup message context", "error", err)
			}
			if previousQuestion.Query != "" {
				previousQuery = previousQuestion.Query
			}
			// Restore state and context for stateful agents
			if request.PreviousState == "" && previousQuestion.PreviousState != "" {
				request.PreviousState = previousQuestion.PreviousState
				ctx.GetLogger().Info("followup: restored previous agent state from context", "stateLen", len(request.PreviousState))
			}
			if request.QueryContext == "" && previousQuestion.QueryContext != "" {
				request.QueryContext = previousQuestion.QueryContext
			}
			if request.ParentAgentId == "" && previousQuestion.ParentAgentId != "" {
				request.ParentAgentId = previousQuestion.ParentAgentId
			}

			for k, v := range previousQuestion.QueryConfig.ToolConfigs {
				existingToolConfigs[k] = v
			}
			for k, v := range previousQuestion.QueryConfig.ToolConfirmations {
				existingToolConfirmations[k] = v
			}
			// Merge other config fields from previous question
			request.QueryConfig.MergeFrom(previousQuestion.QueryConfig)
		}

		if followupMessage.MessageConfig != nil {
			followupConfig := map[string]any{}
			err := common.UnmarshalJson([]byte(*followupMessage.MessageConfig), &followupConfig)
			if err != nil {
				ctx.GetLogger().Error("agentexecutor: unable to unmarshal followup message context", "error", err)
			}
			if followupConfig["followupType"] != nil {
				followupMessageType = followupConfig["followupType"].(string)
			}
			if followupConfig["toolName"] != nil {
				toolName = followupConfig["toolName"].(string)
			}
		}

		updateRequestMessageConfig := false
		if followupMessageType == string(FollowupTypeToolConfig) {
			existingToolConfigs[toolName] = followupMessage.Response
			recordConfigSelectionStrategy(&request.QueryConfig, toolName, "followup")
			request.Query = previousQuery
			updateRequestMessageConfig = true
		} else if followupMessageType == string(FollowupTypeToolConfirmation) {
			existingToolConfirmations[toolName] = followupMessage.Response
			request.Query = previousQuery
			updateRequestMessageConfig = true
		} else {
			// For standard followups (text, select), the followup response IS the new query/intent
			if followupMessage.Response != "" {
				request.Query = followupMessage.Response
			}
		}

		request.QueryConfig.ToolConfigs = existingToolConfigs
		request.QueryConfig.ToolConfirmations = existingToolConfirmations
		if updateRequestMessageConfig {
			err := GetConversationDao().UpdateConversationMessageConfig(request.MessageId, request.QueryConfig)
			if err != nil {
				ctx.GetLogger().Error("agentexecutor: unable to update conversation message config", "error", err)
			}
		}
	}

	return request, NBAgentResponse{}, nil
}

func getExistingHistory(request NBAgentRequest, ctx *security.RequestContext) ([]prompts.MessageFormatter, error) {
	chatHistory, err := GetConversationDao().LoadConversationMessages(request.AccountId, request.ConversationId, "", "", config.Config.ConversationHistoryWindowSize)
	if err != nil {
		ctx.GetLogger().Error("agentexecutor: unable to load chat history", "error", err)
		return nil, err
	}
	slices.Reverse(chatHistory)

	// Collect message IDs from human messages to load attachment descriptions
	var messageIDs []string
	for _, chat := range chatHistory {
		if chat["id"] != request.MessageId && chat["role"] == string(llms.ChatMessageTypeHuman) {
			messageIDs = append(messageIDs, chat["id"])
		}
	}

	// Load attachment descriptions for all history messages (best-effort)
	var attachmentDescs map[string][]AttachmentDescription
	if len(messageIDs) > 0 {
		if dao := GetAttachmentDAO(); dao != nil {
			attachmentDescs, err = dao.LoadAttachmentDescriptions(messageIDs, request.AccountId)
			if err != nil {
				ctx.GetLogger().Warn("agentexecutor: failed to load attachment descriptions for history", "error", err)
				// Non-fatal: continue without image context
			}
		}
	}

	// collect existing history
	messageHistoryFomatter := []prompts.MessageFormatter{}
	// Handle formatting of the message history
	for _, chat := range chatHistory {
		if chat["id"] == request.MessageId {
			continue
		}

		mType := MessageType(chat["message_type"])
		switch mType {
		case MessageTypeGeneration:
			if chat["role"] == string(llms.ChatMessageTypeHuman) {
				escapedContent := escapeTemplateSyntax(chat["content"])

				// Append image context from prior turns
				if descs, ok := attachmentDescs[chat["id"]]; ok && len(descs) > 0 {
					escapedContent += "\n" + escapeTemplateSyntax(formatAttachmentDescriptions(descs))
				}

				messageHistoryFomatter = append(messageHistoryFomatter, prompts.NewHumanMessagePromptTemplate(escapedContent, []string{}))

				if chat["response"] != "" {
					escapedResponse := escapeTemplateSyntax(chat["response"])
					messageHistoryFomatter = append(messageHistoryFomatter, prompts.NewAIMessagePromptTemplate(escapedResponse, []string{}))
				}
			}
		case MessageTypeFollowup:
			// For followups, 'content' is the AI question, 'response' is user answer
			if chat["content"] != "" {
				aiQuestion := escapeTemplateSyntax(chat["content"])
				messageHistoryFomatter = append(messageHistoryFomatter, prompts.NewAIMessagePromptTemplate(aiQuestion, []string{}))
			}
			if chat["response"] != "" {
				userAnswer := escapeTemplateSyntax(chat["response"])
				messageHistoryFomatter = append(messageHistoryFomatter, prompts.NewHumanMessagePromptTemplate(userAnswer, []string{}))
			}
		}
	}
	return messageHistoryFomatter, nil
}

// formatAttachmentDescriptions builds a text summary of image attachments for history replay.
func formatAttachmentDescriptions(descs []AttachmentDescription) string {
	var descriptions []string
	for _, d := range descs {
		if d.Description != nil && *d.Description != "" {
			descriptions = append(descriptions, *d.Description)
		}
	}
	if len(descriptions) > 0 {
		return fmt.Sprintf("[User attached %d image(s): %s]", len(descs), strings.Join(descriptions, "; "))
	}
	return fmt.Sprintf("[User attached %d image(s)]", len(descs))
}

// NBClassificationAgent is an agent that provides options for classification.
type NBClassificationAgent interface {
	NBAgent
	GetOptions() []string
}

func createAgentPlanner(ctx *security.RequestContext, agent NBAgent, request NBAgentRequest, systemMessage string, messageHistoryFomatter []prompts.MessageFormatter, initialNotebook string) (NBAgentPlanner, error) {
	var nbAgentPlanner NBAgentPlanner
	var err error

	if agent.GetPlannerType() == AgentPlannerTypeTool {
		nbAgentPlanner, err = NewPromptAgent(ctx, request, agent, systemMessage, messageHistoryFomatter)
	} else if agent.GetPlannerType() == AgentPlannerTypeReAct || agent.GetPlannerType() == AgentPlannerTypeReAct3 {
		// Upgrade react → react_3 when the config flag is enabled, so agents
		// don't need to be changed individually.  Agents that already declare
		// react_3 always use it regardless of the flag.
		useReAct3 := agent.GetPlannerType() == AgentPlannerTypeReAct3 || config.Config.LlmServerReAct3Enabled
		if useReAct3 {
			nbAgentPlanner, err = NewReActAgent3(ctx, request, agent, systemMessage, messageHistoryFomatter, initialNotebook)
		} else {
			nbAgentPlanner, err = NewReActAgent2(ctx, request, agent, systemMessage, messageHistoryFomatter, initialNotebook)
		}
	} else if agent.GetPlannerType() == AgentPlannerTypeReWoo {
		// Upgrade rewoo → react_3 when the config flag is enabled, so agents
		// don't need to be changed individually.
		if config.Config.LlmServerRewooToReact3Enabled {
			nbAgentPlanner, err = NewReActAgent3(ctx, request, agent, systemMessage, messageHistoryFomatter, initialNotebook)
		} else {
			nbAgentPlanner, err = NewReWooAgent2(ctx, request, agent, systemMessage, messageHistoryFomatter, initialNotebook)
		}
	} else if agent.GetPlannerType() == AgentPlannerTypeClassification {
		classificationAgent, ok := agent.(NBClassificationAgent)
		if !ok {
			return nil, errors.New("agent is not of type NBClassificationAgent for classification planner")
		}
		nbAgentPlanner, err = NewClassificationPlanner(ctx, request, agent, systemMessage, classificationAgent.GetOptions())
	} else if agent.GetPlannerType() == AgentPlannerTypeCustom {
		customAgent, ok := agent.(NBCustomAgent)
		if !ok {
			return nil, errors.New("agentexecutor: agent is not of type NBCustomAgent")
		}
		// Same rationale as the direct execution path: disable caching so that
		// dynamic system-message content inside Execute() does not create stale
		// Google AI / Anthropic cache entries.
		noCacheCtx := security.NewRequestContext(
			context.WithValue(ctx.GetContext(), ContextKeyDisableCaching, true),
			ctx.GetSecurityContext(),
			ctx.GetLogger(),
			ctx.GetTracer(),
			ctx.GetMeter(),
		)
		nbAgentPlanner, err = NewCustomAgent(noCacheCtx, request, customAgent, messageHistoryFomatter)
	}
	return nbAgentPlanner, err
}

// escapeTemplateSyntax replaces Go template delimiters {{ and }}
// with template actions that output the literal delimiters,
// preventing the template engine from parsing them as actions.
func escapeTemplateSyntax(content string) string {
	// Replace "{{" with a template action outputting "{{"
	content = strings.ReplaceAll(content, "{{", `{ {`)
	// Replace "}}" with a template action outputting "}}"
	content = strings.ReplaceAll(content, "}}", `} }`)
	return content
}

func messageFormatterToString(messageFormatters []prompts.MessageFormatter) string {
	var ts strings.Builder
	for _, msg := range messageFormatters {
		messages, err := msg.FormatMessages(map[string]any{})
		if err != nil {
			slog.Error("agentexecutor: unable to format message", "error", err)
			continue
		}
		for _, m := range messages {
			fmt.Fprintf(&ts, "- %s: %s\n", m.GetType(), m.GetContent())
		}
	}
	history := ts.String()

	// Hard safety limit: If history is still massive (e.g. > 256KB), truncate the oldest parts.
	// This complements the per-message preflight cap.
	const maxHistoryBytes = 256 * 1024 // 256 KB
	if len(history) > maxHistoryBytes {
		slog.Warn("agentexecutor: history string exceeds safety limit, truncating oldest parts", "size", len(history), "limit", maxHistoryBytes)
		// Keep the last maxHistoryBytes bytes, ensuring we don't break a UTF-8 character
		startIdx := len(history) - maxHistoryBytes
		for startIdx < len(history) && !utf8.RuneStart(history[startIdx]) {
			startIdx++
		}
		history = "[... older history truncated for stability ...]\n" + history[startIdx:]
	}

	return history
}

func getNameToTool(t []toolcore.NBTool) map[string]toolcore.NBTool {
	if len(t) == 0 {
		return nil
	}

	nameToTool := make(map[string]toolcore.NBTool, len(t))
	for _, tool := range t {
		nameToTool[strings.ToUpper(tool.Name())] = tool
	}
	return nameToTool
}

// recordConfigSelectionStrategy records metadata about how a tool config was selected
func recordConfigSelectionStrategy(queryConfig *toolcore.NBQueryConfig, toolName, strategy string) {
	if queryConfig.IsEmpty() {
		slog.Warn("recordConfigSelectionStrategy: queryConfig is empty", "toolName", toolName, "strategy", strategy)
		return
	}

	if queryConfig.ToolConfigMetadata == nil {
		queryConfig.ToolConfigMetadata = make(map[string]any)
	}

	queryConfig.ToolConfigMetadata[toolName] = map[string]any{
		"strategy":  strategy,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	slog.Info("recordConfigSelectionStrategy: metadata recorded",
		"toolName", toolName,
		"strategy", strategy,
		"metadata", queryConfig.ToolConfigMetadata[toolName])
}

func nbToolsToLlmTools(tools []toolcore.NBTool) []llms.Tool {
	llmTools := []llms.Tool{}
	for _, t := range tools {

		properties := map[string]any{}
		for k, p := range t.InputSchema().Properties {
			prop := map[string]any{}
			prop["type"] = p.Type
			if p.Description != "" {
				prop["description"] = p.Description
			}
			if len(p.Enum) > 0 {
				prop["enum"] = p.Enum
			}
			if len(p.Items) > 0 {
				prop["items"] = p.Items
			}
			properties[k] = prop
		}

		parameters := map[string]any{
			"type":       "object",
			"required":   t.InputSchema().Required,
			"properties": properties,
		}

		llmTools = append(llmTools, llms.Tool{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  parameters,
			},
		})
	}

	return llmTools
}

// dedupeSkillReferences removes duplicate entries from a reference slice for
// Type="skill" rows only, keyed by Url (which carries the KB id). First
// occurrence wins. Non-skill reference types are always preserved verbatim.
//
// This is needed because in a delegation chain (e.g. logs → logs_default), both
// the parent custom-planner agent and the sub-agent's executor independently
// load and emit references for skills inherited through InheritSkillsFromAgents.
// Without dedup the aggregated response would list the same skill twice in the
// UI for every level of the chain.
func dedupeSkillReferences(refs []toolcore.NBToolResponseReference) []toolcore.NBToolResponseReference {
	if len(refs) == 0 {
		return refs
	}
	seen := make(map[string]struct{}, len(refs))
	out := make([]toolcore.NBToolResponseReference, 0, len(refs))
	for _, r := range refs {
		if r.Type == "skill" && r.Url != "" {
			if _, dup := seen[r.Url]; dup {
				continue
			}
			seen[r.Url] = struct{}{}
		}
		out = append(out, r)
	}
	return out
}

func containsSQLQuery(input string) bool {
	regex := `(?i)\b(SELECT\s+.*?\s+FROM|INSERT\s+INTO|UPDATE\s+\w+\s+SET|DELETE\s+FROM|CREATE\s+TABLE|DROP\s+TABLE|ALTER\s+TABLE)\b`
	re := regexp.MustCompile(regex)
	return re.MatchString(input)
}

// limitStringLength ensures a string doesn't exceed the maximum allowed length for DB fields
// If truncation is needed, it adds an ellipsis to indicate text was omitted
func limitStringLength(s string, maxLength int) string {
	if len(s) <= maxLength {
		return s
	}

	const ellipsis = "..."
	// Leave space for the ellipsis
	return s[:maxLength-len(ellipsis)] + ellipsis
}

// injectKBContext checks if agent has KB mappings and injects a `<skill-lists>` block
// into the system prompt. agentNames must contain the agent's own name first followed
// by any inherited ancestor names so a delegated sub-agent can also see KBs the user
// mapped to its custom-planner parent.
//
// selectedIds is the question-aware selection produced once at top-level entry. When
// non-nil it filters KBs inherited from ancestor agents to only those IDs; KBs mapped
// directly to the sub-agent's own name (agentNames[0]) are ALWAYS retained — they are
// scoped to that agent's specific job and shouldn't be hidden by an upstream filter.
func injectKBContext(ctx *security.RequestContext, accountId string, agentNames []string, selectedIds []string, plannerType AgentPlannerType, prompt NBAgentPrompt, userQuery string) NBAgentPrompt {
	if accountId == "" || len(agentNames) == 0 {
		return prompt
	}

	kbs := fetchAgentKBs(ctx, accountId, agentNames, selectedIds)
	// Use the primary (first) name for downstream logging.
	agentName := agentNames[0]

	if len(kbs) == 0 {
		// No KB mappings for this agent
		ctx.GetLogger().Debug("agentexecutor: no KB mappings found for agent", "agent", agentName)
		return prompt
	}

	// Check if any active integration KBs exist — if so, fetch RAG previews
	// (only when the feature flag is enabled).
	hasIntegrationKBs := false
	if config.Config.LlmServerIntegrationKBEnabled {
		for _, kb := range kbs {
			if kb.Status == "active" && kb.KBType == "integration" {
				hasIntegrationKBs = true
				break
			}
		}
	}

	// Fetch RAG previews for integration KBs in the background while we
	// build the manual skill list.
	type ragPreview struct {
		title   string
		preview string
		source  string
	}
	ragCh := make(chan []ragPreview, 1)
	if hasIntegrationKBs && strings.TrimSpace(userQuery) != "" {
		go func() {
			ragStart := time.Now()
			ragDocs := toolcore.QueryRAG("", accountId, userQuery, "knowledge_base",
				3, "", "", "", false)
			ctx.GetLogger().Info("agentexecutor: RAG preview fetch complete",
				"agent", agentName, "duration_ms", time.Since(ragStart).Milliseconds(),
				"result_count", len(ragDocs))
			var previews []ragPreview
			for _, doc := range ragDocs {
				content := doc.Document
				// Extract first 2-3 lines as preview.
				lines := strings.SplitN(content, "\n", 4)
				preview := strings.Join(lines[:min(len(lines), 3)], " ")
				preview = strings.TrimSpace(preview)
				if len(preview) > 300 {
					preview = preview[:300] + "..."
				}
				if preview == "" {
					continue
				}
				// Extract title from metadata or first line.
				title := ""
				if t, ok := doc.Metadata["title"].(string); ok && t != "" {
					title = t
				} else if len(lines) > 0 {
					title = strings.TrimSpace(lines[0])
					if len(title) > 100 {
						title = title[:100]
					}
				}
				source := ""
				if s, ok := doc.Metadata["source"].(string); ok {
					source = s
				}
				previews = append(previews, ragPreview{title: title, preview: preview, source: source})
			}
			ragCh <- previews
		}()
	} else {
		ragCh <- nil
	}

	// Build skill-lists context with planner-type-aware guidance
	var skillList []string
	var guidance string
	if plannerType == AgentPlannerTypeReWoo && !config.Config.LlmServerRewooToReact3Enabled {
		guidance = "The following skills are available. If any skill is relevant to the user's question, include a load_skills step as the FIRST step in your plan (E1, no dependencies) to load it — skills contain expert guidance that improves analysis quality."
	} else {
		guidance = "The following skills are available. If any skill is relevant to the current task, load it using the load_skills tool BEFORE running other tools — skills contain expert guidance that improves your analysis."
	}
	skillList = append(skillList,
		"<skill-lists>",
		guidance,
	)

	activeCount := 0
	for _, kb := range kbs {
		if kb.Status != "active" {
			continue
		}
		activeCount++
		escapedName := escapeTemplateSyntax(kb.Name)
		escapedDesc := escapeTemplateSyntax(kb.Description)
		skillList = append(skillList, fmt.Sprintf("name: %s - description: %s", escapedName, escapedDesc))
	}

	// Wait for RAG previews and append integration skill entries.
	var ragPreviews []ragPreview
	select {
	case ragPreviews = <-ragCh:
	case <-time.After(5 * time.Second):
		ctx.GetLogger().Warn("agentexecutor: RAG preview fetch timed out", "agent", agentName)
	}
	for _, rp := range ragPreviews {
		activeCount++
		entry := fmt.Sprintf("name: %s - source: %s - preview: %s",
			escapeTemplateSyntax(rp.title),
			escapeTemplateSyntax(rp.source),
			escapeTemplateSyntax(rp.preview))
		skillList = append(skillList, entry)
	}

	skillList = append(skillList, "</skill-lists>")

	// Inject KB list into the system prompt if we have any active KBs
	if activeCount > 0 {
		ctx.GetLogger().Info("agentexecutor: injecting skill-lists into system prompt",
			"agent", agentName, "manual_count", activeCount-len(ragPreviews),
			"rag_preview_count", len(ragPreviews))
		// Prepend skill list to existing instructions
		prompt.Instructions = append(skillList, prompt.Instructions...)
	} else {
		ctx.GetLogger().Debug("agentexecutor: found KBs but none are active", "agent", agentName)
	}

	return prompt
}
