package core

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	nbprompts "nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/security/egressfilter"
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

	// egressfilter blocks are persisted with the user-safe message as the
	// agent's response text (no "error: agent unable to process request"
	// prefix, deliberately — see ErrLlmUnableToGenerate). Without this
	// check, a blocked turn would classify as Success and a parent planner
	// would happily build on a non-answer.
	if strings.Contains(response, egressfilter.BlockedMessageMarker) {
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

// applyAgentModelTier returns a context whose ContextKeyModelTier reflects the
// agent's declared model category — defaulting to Retrieval when the agent
// declares none (see agentModelCategory).
//
// Always stamping the value matters because sub-agents are invoked with the
// parent's context (see factory_agent.go), which may already carry the parent
// investigation's Reasoning tier. A naive "set only when declared" would let a
// category-less tool agent (kubectl, logs, postgres, …) inherit that tier and
// silently run on the expensive Reasoning-tier (pro) model. Stamping every
// agent — Retrieval by default — confines the pro tier to the orchestrators
// that explicitly opt into Reasoning.
func applyAgentModelTier(ctx *security.RequestContext, agent NBAgent, request NBAgentRequest) *security.RequestContext {
	if ctx == nil {
		return nil
	}
	// A zero-value security.RequestContext has a nil internal context.Context
	// (e.g., tests that build planner stubs with `&security.RequestContext{}`).
	// Guard so context.WithValue never gets a nil parent — mirrors the same
	// defensive check in modelTierFromContext (llm_config.go).
	goCtx := ctx.GetContext()
	if goCtx == nil {
		goCtx = context.Background()
	}
	return security.NewRequestContext(
		context.WithValue(goCtx, ContextKeyModelTier, resolveModelTier(agent, request)),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)
}

// resolveModelTier returns the model tier stamped for this turn. By default it is
// the agent's declared category (agentModelCategory). When query model-downshift is
// enabled, a TOP-LEVEL plain-retrieval turn on a Reasoning-tier orchestrator is
// downshifted Reasoning → Summary: a query needs tool orchestration + formatting,
// not deep causal reasoning, so it runs on the cheaper/faster Summary-tier model.
// It keys off isTopLevelPlainRetrievalTurn — the SAME signal as the lean prompt
// variant — so tier + prompt variant + (model-keyed) cache slot stay consistent.
// Investigations and sub-agents (isTopLevelPlainRetrievalTurn == false, or a non-
// Reasoning base) keep their tier, so this only ever shifts the top-level query case.
func resolveModelTier(agent NBAgent, request NBAgentRequest) ModelTier {
	base := agentModelCategory(agent)
	if config.Config.LlmServerReact3QueryModelDownshiftEnabled &&
		base == ModelTierReasoning &&
		isTopLevelPlainRetrievalTurn(request) {
		return ModelTierSummary
	}
	return base
}

// promptVariantForRequest returns the prompt/cache variant for a turn. Only a
// TOP-LEVEL plain-retrieval (query) turn gets a non-default variant; investigations,
// sub-agents, and degenerate queries resolve to "" (full/default prompt + its cache
// slot). Query turns fork the prompt shape AND the cache slot:
//   - lean-prompt flag ON  → promptVariantLean: drops the heavy investigation overlays
//     (notebook / hypothesis / orchestrator contract) AND the RCA answer-format spec.
//   - lean-prompt flag OFF → promptVariantQuery: drops ONLY the RCA answer-format spec,
//     so a simple query is not answered as an investigation, while keeping every other
//     overlay identical to today.
//
// Either way the variant keys a DISTINCT cache slot from investigation turns, so the
// two prompt shapes coexist instead of alternating content under one slot and busting it.
// Classification uses isTopLevelPlainRetrievalTurn — the same canonical signal that
// drives the model-tier downshift — so prompt variant, cache slot, and tier agree.
func promptVariantForRequest(request NBAgentRequest) string {
	if !isTopLevelPlainRetrievalTurn(request) {
		return ""
	}
	if config.Config.LlmServerReact3QueryLeanPromptEnabled {
		return promptVariantLean
	}
	return promptVariantQuery
}

// isTopLevelPlainRetrievalTurn reports whether this is a TOP-LEVEL, non-investigation
// ("query" / plain-retrieval) turn. It is the single classification that drives BOTH
// the lean prompt variant (promptVariantForRequest) and the query model downshift
// (resolveModelTier), each behind its own flag — so tier, prompt variant, and cache
// slot always agree on the same signal. Uses OriginalQuery (the user's verbatim
// top-level question, falling back to Query) so a delegated sub-agent brief never
// drives the shape; sub-agents and investigation turns are false. A degenerate/empty
// query is false (keep the full prompt + Reasoning tier rather than stripping either
// on an unknown query).
func isTopLevelPlainRetrievalTurn(request NBAgentRequest) bool {
	isTopLevel := request.ParentAgentId == "" || request.ParentAgentId == request.AgentId
	if !isTopLevel {
		return false
	}
	query := request.OriginalQuery
	if query == "" {
		query = request.Query
	}
	if query == "" {
		return false
	}
	if IsInvestigationRequestTask(query) || request.ConversationSource == ConversationSourceInvestigation {
		return false
	}
	return true
}

// applyPromptVariant stamps ContextKeyPromptVariant with the turn's prompt shape.
// It ALWAYS (re)stamps — mirroring applyAgentModelTier — so a sub-agent invoked
// with the parent's context does not INHERIT the parent's "lean" variant: a
// top-level plain-retrieval turn resolves to promptVariantLean, and everything
// else (sub-agents, investigations, feature disabled) resolves to "" (full),
// which keeps the prompt and cache key byte-identical to the pre-change behavior.
func applyPromptVariant(ctx *security.RequestContext, request NBAgentRequest) *security.RequestContext {
	if ctx == nil {
		return nil
	}
	goCtx := ctx.GetContext()
	if goCtx == nil {
		goCtx = context.Background()
	}
	return security.NewRequestContext(
		context.WithValue(goCtx, ContextKeyPromptVariant, promptVariantForRequest(request)),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)
}

// classifyTaskType labels a TOP-LEVEL turn as taskTypeQuery (plain retrieval) or
// taskTypeInvestigation (RCA), or "" for a sub-agent / degenerate request. It is
// the flag-free classification behind promptVariantForRequest (same top-level +
// IsInvestigationRequestTask signal), split out so it can be persisted on every
// token-usage row regardless of whether the lean-prompt / tier-downshift flags are
// on — that is what makes query-vs-investigation segmentation and classifier-miss
// audits possible in post-run review.
func classifyTaskType(request NBAgentRequest) string {
	isTopLevel := request.ParentAgentId == "" || request.ParentAgentId == request.AgentId
	if !isTopLevel {
		return ""
	}
	query := request.OriginalQuery
	if query == "" {
		query = request.Query
	}
	if query == "" {
		return ""
	}
	if IsInvestigationRequestTask(query) || request.ConversationSource == ConversationSourceInvestigation {
		return taskTypeInvestigation
	}
	return taskTypeQuery
}

// applyTaskTypeAttribution stamps ContextKeyTaskType so the token-usage writer can
// persist the turn classification. Sibling of applyPromptVariant; ALWAYS (re)stamps
// so a sub-agent invoked with the parent's context resolves to "" (unclassified)
// rather than inheriting the parent's label.
func applyTaskTypeAttribution(ctx *security.RequestContext, request NBAgentRequest) *security.RequestContext {
	if ctx == nil {
		return nil
	}
	goCtx := ctx.GetContext()
	if goCtx == nil {
		goCtx = context.Background()
	}
	return security.NewRequestContext(
		context.WithValue(goCtx, ContextKeyTaskType, classifyTaskType(request)),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)
}

// orchestratorSkillScopeName returns the canonical orchestrator name whose KB
// mappings a mode-variant handle should ALSO resolve, or "" for a non-variant name.
// Orchestrators are registered under one canonical name (<cloud>_orchestrator) and
// several @-invocable mode handles (<cloud>_orchestrator_lean / _direct / _native /
// _delegating). The organic default runs under the canonical name (so KBs mapped to it
// resolve), but a handle's GetName()+aliases do NOT include the canonical name — so a
// skill mapped to <cloud>_orchestrator is invisible when the orchestrator runs/tests
// under any of its handles. Scoping handles back to their canonical base makes skill
// mappings work uniformly across every planner mode and cloud (k8s/aws/gcp/azure/…).
// Additive only: fetchAgentKBs dedups, so returning the base is harmless when unmapped.
func orchestratorSkillScopeName(agentName string) string {
	if i := strings.Index(agentName, "_orchestrator_"); i != -1 {
		return agentName[:i+len("_orchestrator")]
	}
	return ""
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

	// Remove the routed-to @agent mention from the query. By default only the FIRST
	// mention is dropped so a "@a @b q" run keeps "@b q" for the agent; set
	// DropExtraAgentMentions to strip every leading mention ("q").
	if config.Config.DropExtraAgentMentions {
		request.Query = common.StripLeadingAgentMention(request.Query)
	} else {
		request.Query = common.StripFirstAgentMention(request.Query)
	}

	// Stamp the agent's declared model category onto the context so every LLM
	// call it makes resolves the category-specific model. Sub-operations may
	// override per call. See applyAgentModelTier for why a category-less agent
	// must RESET (not inherit) the tier.
	ctx = applyAgentModelTier(ctx, agent, request)

	// Stamp the prompt variant (lean vs full) for this turn so the prompt build
	// (planner) and the cache key read one source of truth and never drift.
	// No-op unless the query-lean prompt is enabled AND this is a top-level
	// plain-retrieval turn — sub-agents and investigations keep the full prompt.
	ctx = applyPromptVariant(ctx, request)

	// Stamp the turn classification (query / investigation / "") so the token-usage
	// writer can persist task_type for post-run tier/quality segmentation. Flag-free
	// and side-effect-free — attribution only, does not change model or prompt.
	ctx = applyTaskTypeAttribution(ctx, request)

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
	// Tell the egressfilter which text is prior-conversation history so a
	// secret already surfaced on an earlier message isn't recounted against
	// this message when the planner re-sends that history on every LLM call.
	// Reporting-only: block / redact still scan the full outbound payload.
	ctx.SetContext(egressfilter.WithReportBaseline(ctx.GetContext(), historyStr))
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

	// Attach the provider that will actually serve this agent's LLM calls, so
	// prompt resolution (provider-specific files, provider-scoped DB config,
	// provider-targeted experiments) matches it. Without this, prompt loads fall
	// back to the deployment-wide LLM_PROVIDER env var, which per-account model
	// configuration, pinned sources, and conversation overrides can all disagree
	// with. Resolution failure keeps the env fallback — same behavior as before.
	// Rebind ctx locally instead of ctx.SetContext: sub-agents in a parallel
	// action batch share the caller's RequestContext pointer, so an in-place
	// mutation would race and leak one agent's provider into its siblings.
	if ctx != nil {
		if res, err := ResolveLLMConfig(ctx, request.AccountId, agent.GetName(), request.ConversationId); err == nil && res != nil && res.Provider != "" {
			ctx = security.NewRequestContext(
				nbprompts.WithRequestProvider(ctx.GetContext(), res.Provider),
				ctx.GetSecurityContext(),
				ctx.GetLogger(),
				ctx.GetTracer(),
				ctx.GetMeter(),
			)
		}
	}

	// Images attached but no configured model (main or lite) can process them:
	// don't let the agent loose on tool-calling investigation driven by a query
	// it can't actually ground in the image content — answer directly instead.
	// Checked once here and reused below; ExtractImageContext would otherwise
	// redundantly reach the same conclusion internally and no-op.
	imagesUnsupportedByModel := false
	if len(request.Images) > 0 {
		provider, model := resolveEffectiveVisionProvider(ctx, request.AccountId, agent.GetName(), request.ConversationId)
		if _, ok := resolveVisionCapableTier(provider, model); !ok {
			imagesUnsupportedByModel = true
		}
	}

	// Extract actionable context from attached images before planning.
	// This enriches vague queries like "can you investigate" with concrete details
	// (service names, error codes, metric values) visible in the screenshots.
	if len(request.Images) > 0 && !imagesUnsupportedByModel {
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
	// prompt overhead is minimal) into the agent's system prompt. ReAct planners
	// read it via the lazy load_skills tool: the LLM picks which skills
	// to actually fetch based on the question.
	//
	// ownSkillNames is the agent's canonical name plus its back-compat aliases.
	// KB mappings are keyed by the name in effect when the mapping was created, so a
	// renamed agent (k8s_debug → k8s_orchestrator) must query its aliases too or it
	// never sees runbooks users mapped under the old name. These are always retained.
	//
	// skillAgentNames additionally appends inherited ancestor names, set by
	// custom-planner delegators (metrics → prometheus, logs → log_default,
	// log_default → query_generator, ...) so a sub-agent's lazy <skill-lists> can
	// also see KBs the user mapped to its custom-planner parent.
	ownSkillNames := append([]string{agent.GetName()}, agent.GetNameAliases()...)
	// Mode-variant orchestrator handles (@k8s_orchestrator_lean, @aws_orchestrator_direct,
	// …) also resolve KBs mapped to their canonical <cloud>_orchestrator, so a skill works
	// under every planner mode/cloud — not only the organic default (which runs under the
	// canonical name). See orchestratorSkillScopeName.
	if base := orchestratorSkillScopeName(agentName); base != "" {
		ownSkillNames = append(ownSkillNames, base)
	}
	skillAgentNames := make([]string, 0, len(ownSkillNames)+len(request.InheritSkillsFromAgents))
	skillAgentNames = append(skillAgentNames, ownSkillNames...)
	skillAgentNames = append(skillAgentNames, request.InheritSkillsFromAgents...)

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
			// retrieval runs uniformly across all agent invocations.
			kbs := fetchAgentKBs(ctx, request.AccountId, ownSkillNames, request.InheritSkillsFromAgents, selected)
			// No zero-KB short-circuit: the retrieval below is account-wide RAG
			// and needs no agent mapping — an agent with no mapped KBs must
			// still surface account knowledge (e.g. a synced Confluence runbook
			// for the alert under investigation, #34779). Only the menu is
			// mapping-dependent; BuildSkillListsMenu returns "" for empty kbs.
			// Per-KB retrieval: references reflect only the KBs whose content
			// actually matched, not every mapped KB. If pre-step content was
			// already populated or retrieval was already executed for this turn
			// (e.g. propagated by the caller), reuse it to avoid redundant RAG calls.
			block := strings.TrimSpace(request.KBPrestepContent)
			kbRefs := request.KBReferences
			if !request.KBPrestepExecuted && block == "" {
				block, kbRefs = retrieveRelevantKB(ctx, request, kbs)
				block = strings.TrimSpace(block)
			}
			menu := BuildSkillListsMenu(kbs, block != "")
			kbChan <- kbAssemblyResult{prompt: prompt, menu: menu, prestepBlock: block, kbRefs: kbRefs}
			return
		}
		// Legacy path: skill-lists injected into the cacheable system prompt.
		kbChan <- kbAssemblyResult{prompt: injectKBContext(ctx, request.AccountId, ownSkillNames, request.InheritSkillsFromAgents, selected, prompt, userQuery)}
	}(basePrompt, request.SelectedSkillIds)

	// When the Memory Module is enabled for this tenant, it is the sole memory
	// source for the prompt. The legacy similarity-based notebook is skipped
	// entirely — no concatenation, no double-injection. When the module is off
	// (or the tenant is not allowlisted) the legacy notebook remains primary.
	tenantID := ctx.GetSecurityContext().GetTenantId()
	memoryModuleActive := isMemoryV2ActiveFn(tenantID)

	memChan := make(chan string, 1)
	memV2Chan := make(chan string, 1)
	// Memory is composed once, at the top-level invocation; sub-agents skip the
	// per-agent recompose (mirrors the eager KB retrieval above, which is already
	// top-level-only) — each recompose re-ran the 3-layer memory Compose (~3-5s).
	// isTopLevelInvocation keys off OriginalQuery, which tool-invoked sub-agents like
	// the async "LLM" title generator never inherit, so also treat any request with a
	// distinct parent agent as a sub-agent (the canonical test in promptVariantForRequest).
	isSubAgentInvocation := !isTopLevelInvocation ||
		(request.ParentAgentId != "" && request.ParentAgentId != request.AgentId)
	if isSubAgentInvocation {
		// Empty sends keep the collectors below unblocked.
		memChan <- ""
		memV2Chan <- ""
	} else if memoryModuleActive {
		memChan <- ""
		go func() {
			memV2Chan <- composeMemoryV2BlockFn(ctx, request, agent)
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
		// Reference context, not working state: seeding the notebook handed
		// every injected memory the authority of the agent's own prior
		// findings. The planner frames it as <user_memory>; the notebook
		// starts empty.
		request.MemoryContext = <-memV2Chan
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
	request.KBReferences = kbResult.kbRefs
	request.KBPrestepExecuted = true

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
		// Eager-load inherited skills only when a selection exists to narrow them.
		// Without a selection (e.g. an account-scoped orchestrator propagating its
		// skills down — SelectedSkillIds is nil there to keep its prompt cache stable)
		// LoadActiveAgentSkillContents would force EVERY inherited runbook body into
		// this mechanical sub-agent (a metrics/logs agent loading unrelated runbooks).
		// Own skills are always loaded; inherited ones stay lazily available to ReAct
		// sub-agents via their <skill-lists> menu rather than being force-fed here.
		eagerSkillNames := ownSkillNames
		if len(request.SelectedSkillIds) > 0 {
			eagerSkillNames = skillAgentNames
		}
		skillsContext, refs, sErr := toolcore.LoadActiveAgentSkillContents(ctx, request.AccountId, eagerSkillNames, request.SelectedSkillIds)
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

	// Compute the effective planner type for prompt rendering. Orchestrating and
	// ReAct agents always run as react_3 at runtime, so their prompt uses react-style formatting.
	effectivePlannerType := resolveEffectivePlannerType(agent.GetPlannerType())
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
	if imagesUnsupportedByModel {
		response = NBAgentPlannerExecutorResponse{
			Status:     AgentExecutionStatusSuccess,
			Response:   "The current model doesn't support image attachments, so I can't view the image(s) you attached. Please describe what's shown — error messages, resource names, metric values, or the issue you're seeing — and I'll help investigate.",
			IsTerminal: true,
		}
	} else if customAgent, ok := agent.(NBCustomAgent); ok && agent.GetPlannerType() == AgentPlannerTypeCustom {
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
		var nbAgentPlanner NBAgentPlanner
		nbAgentPlanner, err = createAgentPlanner(ctx, agent, request, systemMessage, messageHistoryFomatter, initialNotebook)
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
		// CRITICAL: Ensure the followup request points to THIS agent (the parent)
		// so that the next user turn correctly resumes this agent instead of jumping to the child.
		response.Followup.AgentId = agentId
		response.Followup.AgentName = agent.GetName()

		syncAgentFollowupMessage(ctx, GetConversationDao(), request, agent.GetName(), response.Followup)
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
	if len(request.Images) > 0 &&
		agentResponse.Status != ConversationStatusFailed &&
		agentResponse.Status != ConversationStatusTerminated {
		GenerateImageDescriptionsAsync(ctx, request)
	}

	// Run the LLM response formatter only when a turn touched enough distinct
	// agents that step-reference guidance and citation normalization pay off.
	//
	// Threshold raised from >1 (2+ agents) to >4 (5+ agents) on 2026-08-04
	// after a 7d prod review found: the formatter is a fresh LLM call
	// (~500ms + ~$0.005) whose measurable unique contribution is step-ID
	// citation rewriting (~28.6% of runs) and header/prose polish. On 2/3/4-
	// agent turns the citation guide adds little (few tool sources to link),
	// the prose polish has historically drifted into chatty/emoji tone that
	// violates the professional persona (memory item on tone-drift), AND the
	// formatter runs a fresh LLM call with only its own system prompt — it
	// does NOT reference account-level persona/tone customization, so any
	// tenant with a custom voice gets overridden on every formatted response.
	//
	// Raising the threshold to 5+ agents drops ~417 marginal runs/wk
	// (~68% reduction: 2-agent 146 + 3-agent 162 + 4-agent 109) while
	// keeping formatter on the ~199/wk heavy-fanout narratives (5+ distinct
	// tool/agent sources) where the step-ref guide is genuinely
	// load-bearing.
	//
	// Follow-up review 2-3 days after deploy: sample formatted vs
	// un-formatted responses at 5+/4-/1-agent slices — decide whether to
	// raise further, keep, or replace the LLM formatter with a deterministic
	// header/citation normalization pass (which would remove the persona-
	// conflict problem entirely). Companion header-source fix in PR #35608
	// removes the primary reason we needed formatter to run broadly
	// (un-normalized `(5-Whys)` header on single-agent turns).
	if agentResponse.Status == ConversationStatusCompleted && (request.ParentAgentId == "" || request.ParentAgentId == request.AgentId || request.ParentAgentId == uuid.Nil.String()) {
		const formatterMinDistinctAgents = 4 // formatter fires when count > this (i.e. 5+ agents)
		distinctAgents := make(map[string]bool)
		for _, invocation := range agentResponse.AgentStepResponse {
			if invocation.Call.FunctionCall != nil && invocation.Call.FunctionCall.Name != "" && !strings.EqualFold(invocation.Call.FunctionCall.Name, "llm") && !strings.EqualFold(invocation.Call.FunctionCall.Name, "planner") && !strings.Contains(invocation.Call.FunctionCall.Name, "debug") {
				if _, ok := GetNBAgent(ctx, invocation.Call.FunctionCall.Name, request.AccountId, AgentStatusEnabled); ok {
					distinctAgents[strings.ToLower(invocation.Call.FunctionCall.Name)] = true
				}
				if len(distinctAgents) > formatterMinDistinctAgents {
					break
				}
			}
		}

		if len(distinctAgents) > formatterMinDistinctAgents {
			// Pass the effective (runtime) planner type: orchestrating/react agents
			// run as react_3 and assign sequential DisplayIDs, so the formatter must
			// build the step-reference guide for them too — not just declared-react_3 agents.
			agentResponse = FormatAgentResponse(ctx, request, agentResponse, resolveEffectivePlannerType(agent.GetPlannerType()))
		}
	}

	return agentResponse, err
}

// syncAgentFollowupMessage persists followUpRequest for the agent: it updates
// the agent's still-active followup message in place when one exists, and
// otherwise creates a new followup message.
//
// The update branch overwrites a config the planner already wrote, so it must
// pass the whole FollowupRequest and let the DAO serialize it. A hand-rolled
// partial map here dropped confirmationKey: the user's approval was then
// recorded under the bare tool name, which a per-action-scoped tool
// (toolcore.ToolConfirmationScope — the recommendation write tools) can never
// match on resume, so every approved apply died on the "config not resolved"
// fail-fast gate in executeAgentPlanner.
// The dao is a parameter rather than a GetConversationDao() call inside: tests
// covering this would otherwise have to swap the package-global, which races
// with the fire-and-forget goroutines other tests leave running (caught by
// -race in CI).
func syncAgentFollowupMessage(ctx *security.RequestContext, dao IConversationDao, request NBAgentRequest, agentName string, followUpRequest FollowupRequest) {
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
				if updateErr := dao.UpdateConversationMessageFollowupConfig(existingAgent.FollowupMessageID.String(), followUpRequest); updateErr != nil {
					ctx.GetLogger().Error("followup: failed to update followup config", "error", updateErr)
				}
				return
			}
		}
	}

	ctx.GetLogger().Info("agentexecutor: generating new followup message", "agent", agentName, "type", followUpRequest.FollowupType)
	if _, err := GenerateFollowup(ctx, request, followUpRequest); err != nil {
		ctx.GetLogger().Error("agentexecutor: unable to generate followup", "error", err)
	}
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
		confirmationKey := ""
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
			// Per-action confirmations record under the key the doAction gate
			// computed (tool + input scope), carried on the followup; absent —
			// the historical per-tool key (the tool name).
			if key, ok := followupConfig["confirmationKey"].(string); ok && key != "" {
				confirmationKey = key
			}
		}

		updateRequestMessageConfig := false
		if followupMessageType == string(FollowupTypeToolConfig) {
			existingToolConfigs[toolName] = followupMessage.Response
			recordConfigSelectionStrategy(&request.QueryConfig, toolName, "followup")
			request.Query = previousQuery
			updateRequestMessageConfig = true
		} else if followupMessageType == string(FollowupTypeToolConfirmation) {
			if confirmationKey == "" {
				confirmationKey = toolName
			}
			existingToolConfirmations[confirmationKey] = followupMessage.Response
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

// resolveEffectivePlannerType maps an agent's *declared* planner type to the
// planner that actually runs it. Orchestrating and ReAct agents both execute
// via the ReAct3 planner (see createAgentPlanner), so they resolve to
// AgentPlannerTypeReAct3; every other type runs as declared. Callers that need
// to reason about runtime behavior (prompt style, DisplayID assignment, response
// formatting) must use this rather than GetPlannerType() directly.
func resolveEffectivePlannerType(declared AgentPlannerType) AgentPlannerType {
	if declared == AgentPlannerTypeOrchestrating || declared == AgentPlannerTypeReAct {
		return AgentPlannerTypeReAct3
	}
	return declared
}

func createAgentPlanner(ctx *security.RequestContext, agent NBAgent, request NBAgentRequest, systemMessage string, messageHistoryFomatter []prompts.MessageFormatter, initialNotebook string) (NBAgentPlanner, error) {
	var nbAgentPlanner NBAgentPlanner
	var err error

	if agent.GetPlannerType() == AgentPlannerTypeTool {
		nbAgentPlanner, err = NewPromptAgent(ctx, request, agent, systemMessage, messageHistoryFomatter)
	} else if agent.GetPlannerType() == AgentPlannerTypeReAct || agent.GetPlannerType() == AgentPlannerTypeReAct3 || agent.GetPlannerType() == AgentPlannerTypeOrchestrating {
		// Orchestrating, ReAct and ReAct3 all execute as react_3.
		nbAgentPlanner, err = NewReActAgent3(ctx, request, agent, systemMessage, messageHistoryFomatter, initialNotebook)
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
		if tool == nil {
			continue
		}
		nameToTool[strings.ToUpper(tool.Name())] = tool
		// Include aliases so the planner can resolve tools by either their
		// canonical name or any registered alias (e.g., "kubectl" → "kubectl_execute").
		if aliased, ok := tool.(interface{ GetNameAliases() []string }); ok {
			for _, alias := range aliased.GetNameAliases() {
				if alias == "" {
					continue
				}
				nameToTool[strings.ToUpper(alias)] = tool
			}
		}
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
// into the system prompt. ownNames are the agent's own name and back-compat aliases;
// inheritedNames are ancestor names a delegated sub-agent inherits so it can also see
// KBs the user mapped to its custom-planner parent.
//
// selectedIds is the question-aware selection produced once at top-level entry. When
// non-nil it filters KBs inherited from ancestor agents to only those IDs; KBs mapped
// directly to one of the agent's own names are ALWAYS retained — they are scoped to
// that agent's specific job and shouldn't be hidden by an upstream filter.
func injectKBContext(ctx *security.RequestContext, accountId string, ownNames []string, inheritedNames []string, selectedIds []string, prompt NBAgentPrompt, userQuery string) NBAgentPrompt {
	if accountId == "" || len(ownNames) == 0 {
		return prompt
	}

	kbs := fetchAgentKBs(ctx, accountId, ownNames, inheritedNames, selectedIds)
	// Use the primary (first) name for downstream logging.
	agentName := ownNames[0]

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
	guidance := "The following skills are available. If any skill is relevant to the current task, load it using the load_skills tool BEFORE running other tools — skills contain expert guidance that improves your analysis."
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
		if strings.TrimSpace(escapedDesc) != "" {
			skillList = append(skillList, fmt.Sprintf("name: %s - description: %s", escapedName, escapedDesc))
		} else {
			skillList = append(skillList, fmt.Sprintf("name: %s", escapedName))
		}
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
