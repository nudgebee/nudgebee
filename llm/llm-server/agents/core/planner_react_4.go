package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/llms/googleai"
	nbprompts "nudgebee/llm/prompts"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
)

// NBReActPlanner4 is the provider-native tool-calling planner (ReAct4). It
// implements the same NBAgentPlanner contract as NBReActPlanner3 — the executor
// (executor_planner.go) drives the outer loop, dispatches actions via doAction,
// and feeds accumulated NBAgentPlannerToolActionStep back into Plan(). The only
// divergence from ReAct3 is the model I/O:
//
//   - Tools are advertised to the provider as native tool definitions
//     (llms.WithTools(nbToolsToLlmTools(tools))) instead of rendered as XML in
//     the prompt.
//   - The model's tool calls come back as completion.Choices[0].ToolCalls; a
//     turn with zero tool calls is terminal (assistant text = final answer).
//   - The intermediateSteps are rendered back as native assistant(tool_use) /
//     tool(tool_result) message turns instead of a reconstructed text scratchpad.
//
// Because the neutral NBAgentPlannerToolActionStep is what the executor persists
// and replays, native message history is reconstructed per-call from those steps
// — so step persistence and suspend/resume ride on the existing executor
// machinery unchanged. See docs/planner_react_4.md.
//
// Reconstructed tool_result content is window-gated compressed (renderStepsToMessages,
// reusing the react_3 scratchpad primitives), and final answers pass through the
// same critique/refine gate as react_3 (Plan's refine loop). DB persistence of
// critiques is not yet wired for react_4.
type NBReActPlanner4 struct {
	ctx     *security.RequestContext
	request NBAgentRequest
	nbAgent NBAgent
	tools   []toolcore.NBTool
	// llmTools is the provider-native conversion of tools, computed once at
	// construction. The tool set is static for the planner's lifetime, so caching
	// avoids re-running the NBTool->llms.Tool conversion (map allocs + schema
	// parsing) on every Plan iteration.
	llmTools      []llms.Tool
	systemMessage string
	history       string // rendered prior-conversation turns (from extraMessages)

	// userContextBlock and evidenceIndex mirror reActCreatePrompt3's human-message
	// context. They are resolved ONCE at construction, not per humanText() call:
	// react_3 builds its human message a single time (PartialVariables on the
	// prompt template), whereas react_4 re-renders it on every Plan() iteration —
	// so computing them inline would turn a once-per-turn DB read
	// (fetchEvidenceIndex) into a per-iteration one.
	userContextBlock string
	evidenceIndex    string

	// Notebook is the durable investigation state. It is derived at the start of
	// every Plan() from the most recent update_notebook step in intermediateSteps
	// (see refreshNotebookFromSteps) and injected into the human message so the
	// model always sees current state. It is also serialized in Marshal so it
	// survives a suspend/resume round-trip before the first replay rebuilds it.
	Notebook string
	// notebookAgentID identifies the synthetic llm_conversation_agent row used
	// to expose the evolving notebook in the UI. notebookUpdateCount is carried
	// with it so suspend/resume updates the same row and preserves its breadcrumb.
	notebookAgentID     string
	notebookUpdateCount int
	persistedNotebook   string

	stepCount int

	// enableCritique is the per-request critique override (request.EnableCritique).
	// maxRefinementAttempts bounds the refine loop. Both are derived at
	// construction, not conversation state, so they are re-set on resume and need
	// no serialization.
	enableCritique        bool
	maxRefinementAttempts int

	// refinementData carries every (rejected answer, critique feedback) pair the
	// turn has accumulated so the redirect survives across executor iterations —
	// when a refinement produces a tool call, Plan() returns to the executor and
	// the next call rebuilds messages from intermediateSteps, which do NOT contain
	// the rejected answer or feedback. buildMessages replays these pairs so the
	// model keeps the critique redirect in view (matches react_3's refinementData).
	//
	// It is turn-scoped but persisted: a struct field (so it survives across
	// iterations, since the executor reuses one planner instance per turn) AND
	// marshaled, so a refinement that triggers a write tool call and suspends the
	// turn for approval does not lose the critique redirect on resume. Plan()
	// clears it at the start of a fresh turn (empty intermediateSteps), so a turn
	// that exhausted its refinement budget still cannot disable critique for the
	// rest of the conversation. The attempt count is derived from its length.
	refinementData []refinementRecord
}

// refinementRecord is one rejected-answer / critique-feedback pair (see
// NBReActPlanner4.refinementData). StepIndex is len(intermediateSteps) at the
// moment the refinement was recorded, so buildMessages can splice the redirect
// back into its true chronological position — before any tool step the redirect
// went on to trigger, not after it. Fields are exported so refinementData
// survives a mid-turn suspend/resume via Marshal/Unmarshal.
type refinementRecord struct {
	Answer    string
	Feedback  string
	StepIndex int
}

// NewReActAgent4 initializes the ReAct4 planner. It resolves the agent's tool
// set the same way NewReActAgent3 does (GetSupportedTools + client tools) and
// additionally injects the update_notebook control tool when the agent runs with
// a notebook, so the model can maintain investigation state via a native tool
// call. See docs/planner_react_4.md.
func NewReActAgent4(ctx *security.RequestContext, request NBAgentRequest, nbAgent NBAgent, systemMessage string, extraMessages []prompts.MessageFormatter, initialNotebook string) (*NBReActPlanner4, error) {
	tools, agentAdditionalPrompt := resolveReact4Tools(ctx, request, nbAgent, systemMessage)

	// First-name greeting personalisation, top-level turns only (sub-agents don't
	// greet) — same gate reActCreatePrompt3 applies.
	userContextBlock := ""
	if request.ParentAgentId == "" || request.ParentAgentId == request.AgentId {
		userContextBlock = renderUserContextBlock(ctx)
	}

	return &NBReActPlanner4{
		userContextBlock:      userContextBlock,
		evidenceIndex:         fetchEvidenceIndex(ctx, request),
		ctx:                   ctx,
		request:               request,
		nbAgent:               nbAgent,
		tools:                 tools,
		llmTools:              nbToolsToLlmTools(tools),
		systemMessage:         composeReact4SystemMessage(ctx, request, nbAgent, systemMessage, agentAdditionalPrompt, tools),
		history:               messageFormatterToString(extraMessages),
		Notebook:              initialNotebook,
		stepCount:             0,
		enableCritique:        request.EnableCritique,
		maxRefinementAttempts: 2,
	}, nil
}

// resolveReact4Tools builds the ReAct4 tool set with the SAME resolution steps
// reActCreatePrompt3 applies for ReAct3 — client tools, account-configured
// tools, default-tool injection (load_skills/shell/watch), and capability
// filtering — so a ReAct4 agent runs with the identical tool surface as its
// ReAct3 counterpart. It additionally injects the update_notebook control tool
// (after FilterTools, so capability filtering never drops it). Returns the tool
// list and the account-configured additional agent prompt (surfaced separately
// so the caller can place it in the system message). See docs/planner_react_4.md.
func resolveReact4Tools(ctx *security.RequestContext, request NBAgentRequest, nbAgent NBAgent, systemMessage string) ([]toolcore.NBTool, string) {
	// Defensive copy: GetSupportedTools may return a slice backed by a shared
	// array (e.g. custom_agent's per-instance tool cache). The append operations
	// below would otherwise mutate that shared backing array. reActCreatePrompt3
	// copies for the same reason.
	supported := nbAgent.GetSupportedTools(ctx)
	tools := make([]toolcore.NBTool, len(supported))
	copy(tools, supported)

	// Client tools take priority in the list, mirroring reActCreatePrompt3.
	if len(request.ClientTools) > 0 {
		clientTools := make([]toolcore.NBTool, 0, len(request.ClientTools))
		for _, ct := range request.ClientTools {
			clientTools = append(clientTools, toolcore.NewClientToolWrapper(ct))
		}
		tools = append(clientTools, tools...)
	}

	// Account-configured additional prompt + tools.
	agentAdditionalPrompt, configuredTools, _ := AgentAdditionalInstructionsAndToolsAndConfigs(ctx, request.AccountId, nbAgent.GetName())
	for _, ct := range configuredTools {
		if t, ok := toolcore.GetNBTool(request.AccountId, ct); ok {
			tools = append(tools, t)
		} else {
			ctx.GetLogger().Warn("react4: configured tool not found, skipping", "tool", ct)
		}
	}

	// Default-tool injection (load_skills when the agent has KB mappings, plus
	// shell / watch tools). SkillListsMenu is appended for the <skill-lists>
	// detection that gates load_skills — same call react_3 makes.
	tools = FilterAndInjectDefaultTools(request.AccountId, nbAgent, systemMessage+request.SkillListsMenu, tools, request.Capabilities)

	// Capability-based filtering (parity with reActCreatePrompt3's final FilterTools).
	tools = FilterTools(tools, request.Capabilities)

	// The update_notebook control tool is added AFTER FilterTools so capability
	// filtering never removes it. In ReAct3 the notebook was an inline XML tag;
	// in ReAct4 it is a native tool call.
	if ResolveAgentNotebookEnabled(nbAgent) {
		tools = ensureNotebookTool(ctx, request.AccountId, tools)
	}

	return tools, agentAdditionalPrompt
}

// composeReact4SystemMessage assembles the ReAct4 system prefix: the react_4
// base prompt (native-tool operating instructions, notebook discipline, shared
// rules — no XML action grammar), then the account-configured
// <additional_agent_prompt>, then the agent's domain prompt. reActCreatePrompt3's
// react_3 base is XML-specific and deliberately bypassed. All three parts are
// stable per (agent, account) so the cached system prefix is preserved.
func composeReact4SystemMessage(ctx *security.RequestContext, request NBAgentRequest, nbAgent NBAgent, agentPrompt, additionalAgentPrompt string, tools []toolcore.NBTool) string {
	var parts []string
	if base := renderReact4Base(ctx, request, nbAgent, tools); strings.TrimSpace(base) != "" {
		parts = append(parts, base)
	}
	if strings.TrimSpace(additionalAgentPrompt) != "" {
		parts = append(parts, fmt.Sprintf("<additional_agent_prompt>\n%s\n</additional_agent_prompt>", additionalAgentPrompt))
	}
	if strings.TrimSpace(agentPrompt) != "" {
		parts = append(parts, agentPrompt)
	}
	return strings.Join(parts, "\n\n")
}

// ensureNotebookTool appends the registered update_notebook tool to tools when
// no notebook-family tool is already present. Instantiated via the tool registry
// because agents/core cannot import the tools package (import cycle).
func ensureNotebookTool(ctx *security.RequestContext, accountId string, tools []toolcore.NBTool) []toolcore.NBTool {
	for _, t := range tools {
		if isNotebookToolName(t.Name()) {
			return tools
		}
	}
	if nb, ok := toolcore.GetNBTool(accountId, toolcore.NotebookToolName); ok {
		return append(tools, nb)
	}
	ctx.GetLogger().Warn("react4: update_notebook tool not registered; notebook tool call will be unavailable")
	return tools
}

// renderReact4Base renders the react_4 base system prompt with the same
// role-mode gates and shared-rule fragments react_3 uses (resolveReact3RoleModes,
// resolveHypothesisModeEnabled, the versioned prompt tree's shared-rule
// fragments), so a react_4 agent runs with the same operational guidance as its
// react_3 counterpart — only the response-format section differs (native tools
// instead of XML). Returns "" on any load/render error; the caller falls back to
// the agent prompt alone. Every prompt loaded here is verified at startup by
// MustResolveAll, so an error is a build defect rather than a runtime condition.
func renderReact4Base(ctx *security.RequestContext, request NBAgentRequest, agent NBAgent, tools []toolcore.NBTool) string {
	base, baseErr := nbprompts.GetPromptStrict(ctx.GetContext(), nbprompts.PromptReact4Base, request.AccountId)
	if baseErr != nil || strings.TrimSpace(base) == "" {
		ctx.GetLogger().Error("react4: failed to load base prompt; using agent prompt only", "error", baseErr)
		return ""
	}

	notebookEnabled := ResolveAgentNotebookEnabled(agent)
	hypothesisModeEnabled := resolveHypothesisModeEnabled(request, agent)
	orchestratorMode, executorMode := resolveReact3RoleModes(request)

	// Shared-rule fragments resolve with an empty accountID (include-only, no
	// per-account override), matching reActCreatePrompt3. async_completion_rules is
	// derived per-agent (asyncCompletionRules), not loaded as a fragment.
	contextManagementRules, err1 := nbprompts.GetPromptStrict(ctx.GetContext(), nbprompts.PromptContextContinuity, "")
	timeHandlingRules, err2 := nbprompts.GetPromptStrict(ctx.GetContext(), nbprompts.PromptTimeHandlingRules, "")
	dataProtectionRules, err3 := nbprompts.GetPromptStrict(ctx.GetContext(), nbprompts.PromptDataProtectionRules, "")
	codeAnalysisRules, err4 := nbprompts.GetPromptStrict(ctx.GetContext(), nbprompts.PromptCodeAnalysisRules, "")
	securityRules, err5 := nbprompts.GetPromptStrict(ctx.GetContext(), nbprompts.PromptSecurityRules, "")
	memoryConsumptionRules, err6 := nbprompts.GetPromptStrict(ctx.GetContext(), nbprompts.PromptMemoryConsumptionRules, "")
	if fragErr := errors.Join(err1, err2, err3, err4, err5, err6); fragErr != nil {
		ctx.GetLogger().Error("react4: failed to load shared-rule fragment; using agent prompt only", "error", fragErr)
		return ""
	}

	vars := []string{
		"notebook_enabled", "hypothesis_mode_enabled", "orchestrator_mode", "executor_mode",
		"delegate_agent_enabled",
		"context_management_rules", "time_handling_rules", "data_protection_rules",
		"code_analysis_rules", "security_rules", "memory_consumption_rules", "async_completion_rules",
	}
	tmpl := prompts.NewPromptTemplate(base, vars)
	out, err := tmpl.Format(map[string]any{
		"notebook_enabled":        notebookEnabled,
		"hypothesis_mode_enabled": hypothesisModeEnabled,
		// Gates the DELEGATION section. react_4 shipped with the delegate_agent
		// TOOL available but no guidance on when to use it, so the model kept
		// multi-step discovery inline: on one comparison case the orchestrator made
		// 31 LLM calls under react_4 versus 9 under react_3, and orchestrator calls
		// carry the full accumulated context. Same gate react_3 uses.
		"delegate_agent_enabled":   HasDelegateAgentTool(tools),
		"orchestrator_mode":        orchestratorMode,
		"executor_mode":            executorMode,
		"context_management_rules": contextManagementRules,
		"time_handling_rules":      timeHandlingRules,
		"data_protection_rules":    dataProtectionRules,
		"code_analysis_rules":      codeAnalysisRules,
		"security_rules":           securityRules,
		"memory_consumption_rules": memoryConsumptionRules,
		"async_completion_rules":   asyncCompletionRules(agent),
	})
	if err != nil {
		ctx.GetLogger().Error("react4: failed to render base prompt; using agent prompt only", "error", err)
		return ""
	}
	return out
}

func (o *NBReActPlanner4) GetTools() []toolcore.NBTool { return o.tools }

// agentName is a nil-safe accessor for logging. nbAgent is always set in
// production, but parseCompletion and the critique paths are exercised directly
// by unit tests that build a bare planner — the same reason the ctx accesses are
// guarded. Centralised so call sites cannot drift back to a raw
// o.nbAgent.GetName(), which is how two of these ended up unguarded.
func (o *NBReActPlanner4) agentName() string {
	if o.nbAgent == nil {
		return ""
	}
	return o.nbAgent.GetName()
}

// GetNotebook satisfies NBAgentNotebookProvider.
func (o *NBReActPlanner4) GetNotebook() string { return o.Notebook }

// react4PersistentState is the planner state carried across a suspend/resume
// boundary. stepCount is re-seeded from intermediateSteps in Plan(), and the
// critique budget/override are re-derived at construction, so neither is stored;
// refinementData is stored so a refinement that triggered a write-approval
// suspend keeps its critique redirect on resume.
type react4PersistentState struct {
	Notebook            string             `json:"notebook"`
	NotebookAgentID     string             `json:"notebook_agent_id,omitempty"`
	NotebookUpdateCount int                `json:"notebook_update_count,omitempty"`
	PersistedNotebook   string             `json:"persisted_notebook,omitempty"`
	RefinementData      []refinementRecord `json:"refinement_data,omitempty"`
}

func (o *NBReActPlanner4) Marshal() ([]byte, error) {
	return common.MarshalJson(react4PersistentState{
		Notebook:            o.Notebook,
		NotebookAgentID:     o.notebookAgentID,
		NotebookUpdateCount: o.notebookUpdateCount,
		PersistedNotebook:   o.persistedNotebook,
		RefinementData:      o.refinementData,
	})
}

func (o *NBReActPlanner4) Unmarshal(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	var state react4PersistentState
	if err := common.UnmarshalJson(data, &state); err != nil {
		return fmt.Errorf("react4: unmarshal planner state: %w", err)
	}
	o.Notebook = state.Notebook
	o.notebookAgentID = state.NotebookAgentID
	o.notebookUpdateCount = state.NotebookUpdateCount
	o.persistedNotebook = state.PersistedNotebook
	o.refinementData = state.RefinementData
	return nil
}

// Plan runs one native-tool-calling iteration: refresh the notebook from prior
// steps, build the native message list, call the model with tool definitions,
// and translate the returned tool calls (or the final text) into the executor's
// neutral action/finish contract.
//
// When the turn produces a final answer (no tool calls), the answer is passed
// through the critique gate: if the critiquer rejects it and refinement budget
// remains, the rejected answer + feedback are recorded in refinementData and the
// model re-prompted — the react_4 analog of react_3's critique/refine pass. A
// refine turn that instead calls more tools returns those actions to the
// executor, so refinement can deepen the investigation, not just reword; because
// refinementData is replayed by buildMessages, the redirect survives that
// round-trip (the rejected answer + feedback are not in intermediateSteps). The
// budget (maxRefinementAttempts) is counted across the whole turn via
// len(refinementData), not reset per iteration.
func (o *NBReActPlanner4) Plan(
	ctx context.Context,
	intermediateSteps []NBAgentPlannerToolActionStep,
	input string,
) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	// A fresh turn (no steps yet) must not inherit refinement carry from a prior
	// turn restored by Unmarshal — that would resurrect a stale rejected-answer
	// redirect and consume this turn's critique budget. Mid-turn (steps present,
	// including a resume after write approval) it is preserved.
	if len(intermediateSteps) == 0 {
		o.refinementData = nil
	}
	// Resume safety: stepCount is not marshaled, so a resumed conversation starts
	// a fresh planner with stepCount == 0. Re-seed it from the prior step count so
	// new DisplayIDs (E<n>) and synthesized tool-call IDs continue past the
	// pre-suspension steps instead of colliding with them. Mid-turn this is a
	// no-op: len(intermediateSteps) already equals the running count.
	o.stepCount = len(intermediateSteps)
	o.refreshNotebookFromSteps(intermediateSteps)

	clarificationContinuationPending := needsClarificationContinuation(intermediateSteps)
	plannerInput := input
	if clarificationContinuationPending {
		plannerInput = clarificationContinuationInput(o.request.QueryConfig.OriginalUserQuery, input)
	}
	messages := o.buildMessages(plannerInput, intermediateSteps)
	messages = AppendImagesToLastHumanMessage(messages, o.request.Images)
	clarificationContinuationRetried := false

	for {
		// Only advertise tools when there is at least one: several providers
		// (OpenAI, Anthropic, Gemini) reject a request that carries an empty tools
		// array with a 400, so pass WithTools only when llmTools is non-empty.
		opts := []llms.CallOption{llms.WithTemperature(0.0)}
		if len(o.llmTools) > 0 {
			opts = append(opts, llms.WithTools(o.llmTools))
		}
		// Replay the reasoning signatures for every prior tool call in this
		// turn's reconstructed history. Without them a thinking model rejects
		// the whole request once any tool step exists (see thoughtSignatureOption).
		if sigOpt := thoughtSignatureOption(intermediateSteps); sigOpt != nil {
			opts = append(opts, sigOpt)
		}
		result, err := GenerateAndTrackLLMContent(
			o.ctx, o.request.UserId, o.request.AccountId, o.request.ConversationId,
			o.request.MessageId, o.request.AgentId, false, messages, true,
			opts...,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("react4: llm generate: %w", err)
		}
		if result == nil || len(result.Choices) == 0 {
			// Treated as a parse failure so the executor's failed-step /
			// consecutive-failure handling engages, matching react_3.
			return nil, nil, fmt.Errorf("react4: empty completion (no choices): %w", ErrParseFailure)
		}

		actions, finish, perr := o.parseCompletion(result.Choices[0])
		// Tool calls to dispatch, or a hard parse error: hand straight back to
		// the executor. Only a final answer (finish != nil) is critiqued.
		if perr != nil || finish == nil {
			return actions, finish, perr
		}
		if clarificationContinuationPending && !clarificationContinuationRetried {
			clarificationContinuationRetried = true
			messages = append(messages, clarificationContinuationMessages(finish.Data)...)
			o.ctx.GetLogger().Info("react4: rejected first terminal answer after clarification without subsequent evidence",
				"agent", o.agentName())
			continue
		}

		if !o.shouldCritique() || len(o.refinementData) >= o.maxRefinementAttempts {
			// Log the gate inputs on the skip path, exactly as react_3 does
			// ("reactagent3: skipping critique"). Without this the accept and
			// the never-ran cases are indistinguishable in logs — a silently
			// disabled critiquer looks identical to one that approved every
			// answer, which is how a whole suite of un-critiqued answers can
			// pass unnoticed.
			o.ctx.GetLogger().Info("react4: skipping critique",
				"enableCritique", o.enableCritique,
				"isTopLevel", o.isTopLevel(),
				"isInvestigation", IsInvestigationRequestTask(o.request.Query),
				"autoCritiqueEnabled", config.Config.LlmServerReActCritiqueEnabled,
				"refinementsUsed", len(o.refinementData),
				"maxRefinements", o.maxRefinementAttempts,
				"agent", o.agentName())
			// Answer accepted (or budget exhausted): drop the turn's refinement
			// carry so a stale redirect can't leak into a later answer.
			o.refinementData = nil
			return nil, finish, nil
		}

		decision, feedback := o.runCritique(plannerInput, o.flattenTranscript(intermediateSteps), finish.Data, intermediateSteps)
		if !strings.EqualFold(decision, "refine") || strings.TrimSpace(feedback) == "" {
			o.ctx.GetLogger().Info("react4: critique accepted answer",
				"decision", decision, "attempts", len(o.refinementData), "agent", o.agentName())
			o.refinementData = nil
			return nil, finish, nil
		}

		// Record the rejection so it survives across executor iterations (see
		// refinementData); stepIndex anchors it after the steps that already
		// exist so buildMessages can order it before any tool step the redirect
		// triggers next. Also append it to this call's local messages so an
		// in-loop retry that stays in final-answer territory sees the redirect
		// immediately.
		o.refinementData = append(o.refinementData, refinementRecord{
			Answer:    finish.Data,
			Feedback:  feedback,
			StepIndex: len(intermediateSteps),
		})
		o.ctx.GetLogger().Info("react4: critique requested refinement", "attempt", len(o.refinementData))
		messages = append(messages, o.refinementMessages(finish.Data, feedback)...)
	}
}

// needsClarificationContinuation reports whether the latest substantive step is
// a successful ask_clarification result. On resume that result contains the
// user's answer, but it is not evidence that the requested inspection happened.
// ReAct4 previously accepted an immediate final answer at this point and could
// claim it had inspected resources while executing zero post-clarification
// tools. Notebook control steps are ignored because they add no external
// evidence; any other later step clears the guard.
func needsClarificationContinuation(steps []NBAgentPlannerToolActionStep) bool {
	for i := len(steps) - 1; i >= 0; i-- {
		step := &steps[i]
		if isNotebookToolName(step.Action.Tool) {
			continue
		}
		return strings.EqualFold(step.Action.Tool, "ask_clarification") && step.Status == ToolStatusSuccess
	}
	return false
}

func clarificationContinuationInput(originalQuery, clarificationResponse string) string {
	originalQuery = strings.TrimSpace(originalQuery)
	clarificationResponse = strings.TrimSpace(clarificationResponse)
	if originalQuery == "" || strings.EqualFold(originalQuery, clarificationResponse) {
		return clarificationResponse
	}
	return "Original task:\n" + originalQuery + "\n\nUser's clarification response:\n" + clarificationResponse
}

func clarificationContinuationMessages(rejectedAnswer string) []llms.MessageContent {
	return []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{llms.TextContent{Text: rejectedAnswer}},
		},
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: "The user has answered your clarification, but no investigation tool has run since that answer. " +
				"Do not claim that you inspected or verified anything without evidence. Continue the original task now: call the appropriate native tool(s) if the answer supplied the missing target. " +
				"If no tool is actually needed, answer directly and explicitly avoid claiming an inspection occurred."}},
		},
	}
}

// refinementMessages renders one rejected-answer / feedback pair as the
// assistant(answer) + human(feedback) turns that carry the critique redirect.
func (o *NBReActPlanner4) refinementMessages(answer, feedback string) []llms.MessageContent {
	return []llms.MessageContent{
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{llms.TextContent{Text: answer}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: react4RefinementMessage(feedback)}}},
	}
}

// containsActionGrammar reports whether text carries react_3's XML tool-call
// protocol. Used only when the turn produced NO native tool call, to tell a
// text-protocol attempt apart from a genuine prose answer that merely mentions
// these tags (hence the opening-tag match rather than a substring search).
func containsActionGrammar(text string) bool {
	for _, tag := range []string{"<thought_action>", "<tool_name>", "<tool_input>", "<actions>"} {
		if strings.Contains(text, tag) {
			return true
		}
	}
	return false
}

// extractXMLFinalAnswer unwraps a react_3-style <final_answer> block, mirroring
// NBReActPlanner3.processFinalAnswer: <content> becomes the answer and <thought>
// becomes the Log (never rendered as the answer). Returns nil when the text is
// not XML-wrapped, so a normal plain-text react_4 answer passes through as-is.
func extractXMLFinalAnswer(output string) *NBAgentPlannerFinishAction {
	if !strings.Contains(output, "<final_answer>") {
		return nil
	}
	content := common.XmlExtractTagContent(output, "content")
	thought := common.XmlExtractTagContent(output, "thought")
	if content == "" {
		content = common.XmlExtractTagContent(output, "final_answer")
		if content == "" {
			return nil
		}
	}
	return &NBAgentPlannerFinishAction{
		Data:       content,
		Log:        thought,
		IsTerminal: true,
	}
}

func react4RefinementMessage(feedback string) string {
	return "Your previous answer was reviewed and needs improvement before it can be returned to the user. " +
		"Feedback:\n" + feedback + "\n\n" +
		"Revise your answer to address this feedback. If you need more evidence, call the appropriate tool(s); " +
		"otherwise reply with the improved final answer as plain text."
}

// parseCompletion translates a single provider choice into actions or a finish.
// Zero tool calls with non-empty text is the terminal (final answer); zero tool
// calls with empty text is a blocked/empty turn surfaced as an error so the
// executor's consecutive-failure handling can summarize instead of terminating
// on a blank answer.
// thoughtSignaturesFromChoice reads the provider's per-tool-call reasoning
// signatures off a completion. The slice is POSITIONAL: entry i belongs to
// choice.ToolCalls[i]. Nil for providers that do not emit them (every
// non-Gemini provider today), which is why the value is always treated as
// optional rather than required.
func thoughtSignaturesFromChoice(choice *llms.ContentChoice) [][]byte {
	if choice == nil || len(choice.GenerationInfo) == 0 {
		return nil
	}
	signatures, _ := choice.GenerationInfo[googleai.GenerationInfoThoughtSignatures].([][]byte)
	return signatures
}

// thoughtSignatureOption supplies the reasoning signature for every tool call
// being replayed in this request, keyed by tool-call id. Returns nil when there
// is nothing to send, so the caller can skip adding an option entirely.
//
// It MUTATES CallOptions.Metadata instead of calling llms.WithMetadata, which
// assigns the map wholesale and would silently drop CachedContentName and
// ThinkingLevel set by the caching and thinking layers — the same reason
// llm_cache.go builds its option this way.
func thoughtSignatureOption(steps []NBAgentPlannerToolActionStep) llms.CallOption {
	signatures := make(map[string][]byte, len(steps))
	for i := range steps {
		action := &steps[i].Action
		if action.ToolID != "" && len(action.ThoughtSignature) > 0 {
			signatures[action.ToolID] = action.ThoughtSignature
		}
	}
	if len(signatures) == 0 {
		return nil
	}
	return func(o *llms.CallOptions) {
		if o.Metadata == nil {
			o.Metadata = make(map[string]any)
		}
		o.Metadata[googleai.MetadataThoughtSignatures] = signatures
	}
}

func (o *NBReActPlanner4) parseCompletion(choice *llms.ContentChoice) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	thought := strings.TrimSpace(choice.Content)

	if len(choice.ToolCalls) == 0 {
		if thought == "" {
			// Blocked/empty turn — surface as a parse failure (not a blank final
			// answer) so the executor retries / summarizes, matching react_3.
			return nil, nil, fmt.Errorf("react4: empty completion with no tool calls (stop_reason=%q): %w", choice.StopReason, ErrParseFailure)
		}
		// Defensive XML unwrap. react_4's prompt carries no XML answer grammar, but
		// the model still emits react_3's <final_answer><thought>…</thought>
		// <content>…</content></final_answer> shape in practice — residual pattern
		// from training, prior-turn history, or account/memory-injected examples.
		// react_3 always parsed it (processFinalAnswer: <content> -> Data,
		// <thought> -> Log), so users never saw the wrapper. Taking choice.Content
		// raw leaked the tags AND the internal monologue into the answer, which
		// surfaced in the UI as replies opening with "The user is asking for…".
		// Parsing it here restores react_3's behavior; plain-text answers (the
		// expected react_4 shape) fall through untouched.
		if finish := extractXMLFinalAnswer(choice.Content); finish != nil {
			return nil, finish, nil
		}
		// Action grammar with no native tool call means the model tried to invoke a
		// tool via react_3's TEXT protocol. Nothing was dispatched, so returning this
		// as the final answer would ship raw XML to the user AND silently skip the
		// tool — the worst failure shape available. Surface it as a parse failure so
		// the executor retries, matching how react_3 treats an unusable turn. The
		// risk is real enough to guard: this model already emits react_3 XML
		// unprompted, and FINAL ANSWER FORMAT now teaches it one XML block, which it
		// may over-generalize to actions.
		if containsActionGrammar(choice.Content) {
			return nil, nil, fmt.Errorf("react4: model emitted XML action grammar instead of a native tool call (no tool ran): %w", ErrParseFailure)
		}
		return nil, &NBAgentPlannerFinishAction{
			Data:       choice.Content,
			Log:        choice.Content,
			IsTerminal: true,
		}, nil
	}

	// Positionally aligned with choice.ToolCalls (the provider has no id to key
	// on at that point), so it must be indexed by the RANGE index, not by the
	// index of the action being appended — this loop skips entries.
	signatures := thoughtSignaturesFromChoice(choice)

	// One id per completion: every action below came from the same assistant
	// turn and must be replayed inside a single assistant message (see TurnID).
	turnID := fmt.Sprintf("t%d-%d", o.stepCount, len(choice.ToolCalls))

	// The thought is whatever prose accompanied the tool call, and it is the ONLY
	// record of why a step happened — it lands on the persisted tool_calls row and
	// is shown beside the step in the UI. react_3's XML grammar forced a <thought>
	// on every action; native tool calling does not, and a model that answers with
	// a bare tool_use leaves the whole investigation unexplainable (measured: 100%
	// of react_4 tool calls persisted an empty thought across three A/B sessions,
	// versus 0% for react_3). The prompt now requires a one-line intent; log when
	// it is missing so adherence is measurable rather than assumed. Nothing is
	// synthesized here on purpose — a fabricated rationale would read as the
	// model's reasoning while being ours.
	// nil-guarded: parseCompletion is exercised directly by unit tests that build
	// a bare planner with no request context or agent.
	if thought == "" && o.ctx != nil {
		o.ctx.GetLogger().Warn("react4: tool call turn carried no intent — step will persist with an empty thought",
			"agent", o.agentName(), "tool_calls", len(choice.ToolCalls), "turn_id", turnID)
	}

	actions := make([]NBAgentPlannerToolAction, 0, len(choice.ToolCalls))
	for i, tc := range choice.ToolCalls {
		if tc.FunctionCall == nil {
			continue
		}
		name := tc.FunctionCall.Name
		args := tc.FunctionCall.Arguments
		id := tc.ID
		o.stepCount++
		if id == "" {
			// Some providers omit the id (Gemini always does); synthesize a
			// deterministic one so the tool_use/tool_result pairing in
			// reconstructed history stays intact.
			//
			// A SINGLE-call turn gets a deterministic id (hash of tool+args), so a
			// repeat collides with its earlier self and accumulateSteps — which keys
			// on ToolID — drops it, exactly as react_3 behaves. A running counter
			// here made every repeat unique and silently disabled both that dedup
			// and the duplicate-action brake (which fires only when an iteration
			// adds NO new steps): react_4 showed 11-19% duplicate calls against
			// react_3's ~0%, its step list inflated to 81 reported vs 21 persisted,
			// and the brake never engaged across a whole comparison run.
			//
			// A PARALLEL batch keeps a turn-unique suffix instead, and must NOT be
			// deduped. Gemini signs only the first functionCall part of a turn, so
			// if dedup drops that signature-bearing sibling, the survivors replay as
			// a group whose first part is unsigned and the request is rejected
			// ("...missing a thought_signature..., position 2"). Batches are rare —
			// one in an entire comparison run — so nearly all the dedup benefit
			// comes from the single-call path anyway.
			if len(choice.ToolCalls) == 1 {
				id = generateToolId(name, args)
			} else {
				id = fmt.Sprintf("%s-E%d-%d", generateToolId(name, args), o.stepCount, i)
			}
		}
		var signature []byte
		if i < len(signatures) {
			signature = signatures[i]
		}
		actions = append(actions, NBAgentPlannerToolAction{
			Tool:             name,
			ToolInput:        args,
			ToolID:           id,
			Log:              thought,
			DisplayID:        fmt.Sprintf("E%d", o.stepCount),
			TurnID:           turnID,
			ThoughtSignature: signature,
		})
	}

	if len(actions) == 0 {
		// Every call had a nil FunctionCall — nothing dispatchable. Wrap the
		// parse sentinel so the executor treats it as a retryable failed step
		// rather than a hard abort (react_3 parity).
		return nil, nil, fmt.Errorf("react4: completion had tool calls but no function payloads: %w", ErrParseFailure)
	}
	return actions, nil, nil
}

// buildMessages assembles the native message list: a stable system message, a
// dynamic human message (context + notebook + question), and the reconstructed
// tool-call history with any critique redirects spliced back into chronological
// order. update_notebook steps are omitted from the reconstructed history
// because the notebook is injected into the human message instead.
//
// Refinement redirects (rejected answer + feedback) are not in intermediateSteps,
// so they are replayed here from refinementData. Each is inserted at its recorded
// stepIndex — before the tool step it triggered — so a refine→tool-call turn does
// not render the triggered tool call before the feedback that caused it, which
// would invert cause and effect for the model.
func (o *NBReActPlanner4) buildMessages(input string, steps []NBAgentPlannerToolActionStep) []llms.MessageContent {
	messages := make([]llms.MessageContent, 0, len(steps)*2+len(o.refinementData)*2+2)

	if strings.TrimSpace(o.systemMessage) != "" {
		messages = append(messages, llms.MessageContent{
			Role:  llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{llms.TextContent{Text: o.systemMessage}},
		})
	}

	messages = append(messages, llms.MessageContent{
		Role:  llms.ChatMessageTypeHuman,
		Parts: []llms.ContentPart{llms.TextContent{Text: o.humanText(input)}},
	})

	compressionActive := o.compressionActive(steps)
	totalSteps := len(steps)
	maxObs := getMaxObservationChars()

	// Walk the step indices, flushing any refinement redirects anchored at each
	// index before rendering that step. refinementData is append-ordered, so its
	// stepIndex values are non-decreasing and a single forward cursor suffices.
	// The i == len(steps) iteration flushes redirects recorded after the last
	// step (the common single-turn case, where no tool call followed).
	refIdx := 0
	flushRedirects := func(upTo int) {
		for refIdx < len(o.refinementData) && o.refinementData[refIdx].StepIndex <= upTo {
			rd := o.refinementData[refIdx]
			messages = append(messages, o.refinementMessages(rd.Answer, rd.Feedback)...)
			refIdx++
		}
	}
	// Advance a TURN at a time, not a step at a time: a parallel batch has to stay
	// inside one assistant message (see TurnID). A redirect anchored strictly
	// inside a batch is flushed ahead of it rather than splitting the batch.
	claimed := make([]bool, len(steps))
	for i := range steps {
		if claimed[i] {
			continue
		}
		flushRedirects(i)
		group := turnGroup(steps, i, claimed)
		messages = append(messages, o.renderTurn(steps, group, totalSteps, compressionActive, maxObs)...)
	}
	// Flush redirects recorded after the last step (the common single-turn case,
	// where no tool call followed the rejected answer).
	flushRedirects(len(steps))
	return messages
}

// humanText renders the dynamic per-turn human message. Kept out of the system
// message so the cached system prefix stays stable across turns.
func (o *NBReActPlanner4) humanText(input string) string {
	var b strings.Builder
	// Block order mirrors reActCreatePrompt3's human-message template so react_4
	// presents the same context in the same sequence; only the scratchpad is
	// absent, replaced by the reconstructed native tool turns that follow.
	fmt.Fprintf(&b, "Today is %s.\n", time.Now().Format("January 02, 2006"))
	// KB pre-step content + the skill-lists menu, so KB/skill-driven flows (and
	// the load_skills tool) have the context react_3 provides.
	if kb := strings.TrimSpace(o.request.KBPrestepContent); kb != "" {
		fmt.Fprintf(&b, "\n%s\n", kb)
	}
	if menu := strings.TrimSpace(o.request.SkillListsMenu); menu != "" {
		fmt.Fprintf(&b, "\n%s\n", menu)
	}
	// Account global preferences (rendered from AccountPrompt) live in the human
	// message — not the system prefix — so the event-analysis fragment doesn't
	// bust the Account-scope cache, matching react_3.
	if gp := strings.TrimSpace(renderGlobalPreferencesBlock(o.request.AccountPrompt)); gp != "" {
		fmt.Fprintf(&b, "\n%s\n", gp)
	}
	if uc := strings.TrimSpace(o.userContextBlock); uc != "" {
		fmt.Fprintf(&b, "\n%s\n", uc)
	}
	if ctxStr := strings.TrimSpace(o.request.ConversationContext); ctxStr != "" {
		fmt.Fprintf(&b, "\n<conversation_context>\n%s\n</conversation_context>\n", ctxStr)
	}
	if h := strings.TrimSpace(o.history); h != "" {
		fmt.Fprintf(&b, "\n<history>\n%s\n</history>\n", h)
	}
	if ei := strings.TrimSpace(o.evidenceIndex); ei != "" {
		fmt.Fprintf(&b, "\n%s\n", ei)
	}
	if nb := strings.TrimSpace(o.Notebook); nb != "" {
		fmt.Fprintf(&b, "\n<notebook>\n%s\n</notebook>\n", nb)
	}
	if mc := strings.TrimSpace(renderMemoryContextBlock(o.request.MemoryContext)); mc != "" {
		fmt.Fprintf(&b, "\n%s\n", mc)
	}
	if cc := strings.TrimSpace(renderChannelContextBlock(o.request.ChannelContext)); cc != "" {
		fmt.Fprintf(&b, "\n%s\n", cc)
	}
	fmt.Fprintf(&b, "\n<question>%s</question>", input)
	return b.String()
}

// renderStepsToMessages reconstructs the native tool-calling history from the
// executor's neutral steps. Each non-notebook step becomes one assistant turn
// carrying the tool_use (with its original id/args) followed by one tool turn
// carrying the matching tool_result — preserving the id pairing every provider
// requires. Notebook steps are skipped (state is injected via the human message).
//
// Tool-result content is window-gated compressed with the same policy react_3
// applies to its text scratchpad: once the total observation bytes approach the
// resolved model window, the last recentStepsFullContext steps keep full
// (hard-capped) observations and older ones are summarized/truncated via the
// shared SummarizeObservation (which caches on step.CompressedObservation).
// steps is indexed (not ranged by value) so that cache write-back persists.
func (o *NBReActPlanner4) renderStepsToMessages(steps []NBAgentPlannerToolActionStep) []llms.MessageContent {
	compressionActive := o.compressionActive(steps)
	totalSteps := len(steps)
	maxObs := getMaxObservationChars()

	messages := make([]llms.MessageContent, 0, len(steps)*2)
	claimed := make([]bool, len(steps))
	for i := range steps {
		if claimed[i] {
			continue
		}
		group := turnGroup(steps, i, claimed)
		messages = append(messages, o.renderTurn(steps, group, totalSteps, compressionActive, maxObs)...)
	}
	return messages
}

// turnGroup returns the indices of every step belonging to the same assistant
// turn as steps[i] — the run of steps sharing its TurnID — and marks them
// claimed. A step with no TurnID (persisted before the field existed) is a turn
// of one, preserving the original one-message-per-step rendering.
//
// It scans the WHOLE remaining slice rather than a consecutive run because
// parallel execution appends results in COMPLETION order, so siblings dispatched
// together can land non-adjacent. A consecutive-only scan split them back into
// standalone assistant messages, and Gemini signs only the first functionCall
// part of a turn — which is how the "missing a thought_signature ... position 2"
// rejections came back on the one comparison case that used a parallel batch.
func turnGroup(steps []NBAgentPlannerToolActionStep, i int, claimed []bool) []int {
	claimed[i] = true
	group := []int{i}
	id := steps[i].Action.TurnID
	if id == "" {
		return group
	}
	for j := i + 1; j < len(steps); j++ {
		if !claimed[j] && steps[j].Action.TurnID == id {
			claimed[j] = true
			group = append(group, j)
		}
	}
	return group
}

// renderTurn renders one assistant turn: a single assistant message carrying the
// turn's thought and ALL of its tool calls, followed by one tool message per
// call. Splitting these into separate assistant messages is what made Gemini
// reject replays of parallel batches, since only the first functionCall part of
// a turn carries a thought signature.
//
// Notebook steps contribute nothing (their state rides on the human message);
// a turn consisting only of notebook steps therefore yields no messages at all.
func (o *NBReActPlanner4) renderTurn(steps []NBAgentPlannerToolActionStep, group []int, totalSteps int, compressionActive bool, maxObs int) []llms.MessageContent {
	assistantParts := make([]llms.ContentPart, 0, len(group)+1)
	results := make([]llms.MessageContent, 0, len(group))

	// A MULTI-call turn replays every member, notebook included. Skipping the
	// notebook step is right for a turn of one (its state rides on the human
	// message instead of bloating history), but inside a batch it is unsafe:
	// signatures are assigned positionally, so when Gemini signs part 0 and that
	// part is the update_notebook call — which the prompt explicitly allows
	// alongside an investigation tool — dropping it discards the turn's only
	// signature and the surviving first part replays unsigned, producing the same
	// "...missing a thought_signature..., position N" rejection. Replaying a batch
	// exactly as emitted keeps the signature attached to the call it belongs to.
	replayNotebook := len(group) > 1
	for _, idx := range group {
		step := &steps[idx]
		if isNotebookToolName(step.Action.Tool) && !replayNotebook {
			continue
		}
		// The thought is shared by the whole batch (parseCompletion copies the
		// same Log onto every sibling), so emit it once, ahead of the calls.
		if len(assistantParts) == 0 {
			if thought := strings.TrimSpace(step.Action.Log); thought != "" {
				assistantParts = append(assistantParts, llms.TextContent{Text: step.Action.Log})
			}
		}
		assistantParts = append(assistantParts, llms.ToolCall{
			ID:   step.Action.ToolID,
			Type: "function",
			FunctionCall: &llms.FunctionCall{
				Name:      step.Action.Tool,
				Arguments: step.Action.ToolInput,
			},
		})
		results = append(results, llms.MessageContent{
			Role: llms.ChatMessageTypeTool,
			Parts: []llms.ContentPart{llms.ToolCallResponse{
				ToolCallID: step.Action.ToolID,
				Name:       step.Action.Tool,
				Content:    o.renderObservation(step, idx, totalSteps, compressionActive, maxObs),
			}},
		})
	}

	if len(results) == 0 {
		return nil
	}
	return append([]llms.MessageContent{{Role: llms.ChatMessageTypeAI, Parts: assistantParts}}, results...)
}

// compressionActive reports whether the accumulated observation bytes have grown
// large enough (relative to the resolved model window) to warrant compressing
// older tool results — the same window-gated trigger react_3 uses. Falls back to
// the char budget when the window can't be resolved (scratchpadBudget handles
// that).
func (o *NBReActPlanner4) compressionActive(steps []NBAgentPlannerToolActionStep) bool {
	var totalObsBytes int
	for i := range steps {
		totalObsBytes += len(steps[i].Observation) + len(steps[i].SubAgentEvidence)
	}
	activationChars, _ := scratchpadBudget(resolveMaxContextTokens(o.ctx, o.request.AccountId, o.agentName(), o.request.ConversationId))
	return activationChars > 0 && totalObsBytes > activationChars
}

// isStepRecent decides whether a step keeps its full observation. When
// compression is inactive every step is "recent"; otherwise only the last
// recentStepsFullContext steps are. Pure so it is unit-testable without a live
// model window.
func isStepRecent(stepIndex, totalSteps int, compressionActive bool) bool {
	return !compressionActive || (totalSteps-stepIndex) < recentStepsFullContext
}

// renderObservation returns the tool-result content for a step: recent steps are
// hard-capped (UTF-8-safe) to the per-observation ceiling; older steps (only
// when compression is active) are summarized/truncated via SummarizeObservation,
// which caches the result on the step. SubAgentEvidence is appended uncompressed
// so the distilled manifest always survives, mirroring react_3.
func (o *NBReActPlanner4) renderObservation(step *NBAgentPlannerToolActionStep, stepIndex, totalSteps int, compressionActive bool, maxObs int) string {
	var obs string
	if isStepRecent(stepIndex, totalSteps, compressionActive) {
		obs = TruncateMiddle(step.Observation, maxObs/2, maxObs/2)
	} else {
		obs = SummarizeObservation(o.ctx, step, o.request, step.Observation)
	}
	if ev := strings.TrimSpace(step.SubAgentEvidence); ev != "" {
		obs = obs + "\n\n" + ev
	}
	return obs
}

// refreshNotebookFromSteps sets o.Notebook to the content of the most recent
// update_notebook step, so the human message reflects the latest state the model
// recorded. update_notebook is dispatched like any other tool; its observation
// carries the content the model wrote.
func (o *NBReActPlanner4) refreshNotebookFromSteps(steps []NBAgentPlannerToolActionStep) {
	for i := len(steps) - 1; i >= 0; i-- {
		step := &steps[i]
		if !isNotebookToolName(step.Action.Tool) || step.Status != ToolStatusSuccess {
			continue
		}
		content := extractNotebookContent(step.Action.ToolInput)
		if content == "" {
			return
		}
		if content != o.Notebook {
			o.Notebook = content
			o.notebookUpdateCount++
		}
		if content != o.persistedNotebook && o.persistNotebook(content, i, analyzeNotebook(content)) {
			o.persistedNotebook = content
		}
		return
	}
}

// persistNotebook mirrors ReAct3's notebook visibility contract without
// changing its execution path: create one synthetic notebook agent row on the
// first successful update, then patch that row for later replacements. Errors
// are observability-only and never fail planning.
func (o *NBReActPlanner4) persistNotebook(content string, turnIdx int, stats notebookStats) bool {
	if o.request.ConversationId == "" || o.request.MessageId == "" {
		return false
	}
	conversationDAO := GetConversationDao()
	if conversationDAO == nil {
		return false
	}

	var logger *slog.Logger
	if o.ctx != nil {
		logger = o.ctx.GetLogger()
	}
	breadcrumb := fmt.Sprintf(
		"turn=%d updates=%d done=%d doing=%d next=%d todo=%d blocked=%d skip=%d plan=%t findings=%t",
		turnIdx, o.notebookUpdateCount,
		stats.DoneCount, stats.DoingCount, stats.NextCount, stats.TodoCount,
		stats.BlockedCount, stats.SkipCount,
		stats.HasPlanSection, stats.HasFindings,
	)

	if o.notebookAgentID == "" {
		agentID, err := conversationDAO.SaveCompletedConversationAgentCall(
			uuid.Nil,
			o.request.ConversationId,
			o.request.MessageId,
			o.request.AccountId,
			o.request.UserId,
			notebookDummyAgent,
			o.request.ParentAgentId,
			o.request.Query,
			"",
			content,
			o.request.ConversationContext,
			o.request.QueryConfig,
			AgentExecutionStatusSuccess,
			breadcrumb,
		)
		if err != nil {
			if logger != nil {
				logger.Error("react4: failed to save notebook agent record", "error", err, "turn_idx", turnIdx)
			}
			return false
		}
		o.notebookAgentID = agentID.String()
		if logger != nil {
			logger.Info("react4: notebook record created", "notebook_agent_id", o.notebookAgentID, "turn_idx", turnIdx)
		}
		return true
	}

	if err := updateConversationNotebook(conversationDAO, o.notebookAgentID, content, breadcrumb); err != nil {
		if logger != nil {
			logger.Error("react4: failed to update notebook agent record", "error", err, "notebook_agent_id", o.notebookAgentID, "turn_idx", turnIdx)
		}
		return false
	}
	return true
}

// extractNotebookContent pulls the notebook body from a native tool-call
// arguments payload, which is a JSON string like {"content":"..."}. Falls back
// to the raw string when it is not JSON with a content field.
func extractNotebookContent(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return ""
	}
	parsed := map[string]any{}
	if err := common.UnmarshalJson([]byte(args), &parsed); err == nil {
		if c, ok := parsed["content"].(string); ok && strings.TrimSpace(c) != "" {
			return c
		}
	}
	return args
}

const react4CritiqueMaxRetries = 2

// isTopLevel reports whether this planner runs the top-level agent (not a
// sub-agent). Critique only applies at the top level, mirroring react_3.
func (o *NBReActPlanner4) isTopLevel() bool {
	return o.request.ParentAgentId == "" || o.request.ParentAgentId == o.request.AgentId
}

// shouldCritique mirrors react_3's gate: allowed when explicitly enabled for the
// request, or (config on AND top-level AND an investigation task); a
// CritiqueSupport agent can further veto it.
func (o *NBReActPlanner4) shouldCritique() bool {
	allowed := o.enableCritique ||
		(config.Config.LlmServerReActCritiqueEnabled && o.isTopLevel() && IsInvestigationRequestTask(o.request.Query))
	if agent, ok := o.nbAgent.(NBAgentReActPlannerCritiqueSupport); ok {
		allowed = allowed && agent.CritiqueEnabled()
	}
	return allowed
}

// flattenTranscript renders the native step history into the plain-text
// scratchpad the critiquer prompt expects (react_4 has no XML scratchpad).
// Notebook steps are skipped — notebook state is passed separately via the
// {{.notebook}} var.
func (o *NBReActPlanner4) flattenTranscript(steps []NBAgentPlannerToolActionStep) string {
	var b strings.Builder
	n := 0
	for i := range steps {
		s := &steps[i]
		if isNotebookToolName(s.Action.Tool) {
			continue
		}
		n++
		if thought := strings.TrimSpace(s.Action.Log); thought != "" {
			fmt.Fprintf(&b, "Thought: %s\n", thought)
		}
		fmt.Fprintf(&b, "Step %d — %s(%s)\nObservation: %s\n\n", n, s.Action.Tool, s.Action.ToolInput, s.Observation)
	}
	return b.String()
}

// runCritique asks the critiquer whether the final answer is acceptable,
// returning (decision, feedback); an empty decision means accept. It mirrors
// NBReActPlanner3.runCritique but omits the react_3-specific DB persistence
// (saveCritique / saveCritiqueAsToolCall), which the seam map flagged as
// optional (errors there are swallowed and never affect the decision). Wiring
// analytics persistence for react_4 is a follow-up.
func (o *NBReActPlanner4) runCritique(input, scratchpad, finalAnswer string, intermediateSteps []NBAgentPlannerToolActionStep) (string, string) {
	logger := o.ctx.GetLogger()

	questionType := "query"
	if IsInvestigationRequestTask(o.request.Query) {
		questionType = "investigation"
	}

	// Fail-safe (mirrors reactagent3): critiquing under an empty instruction would
	// accept or reject on no basis at all, so a missing prompt accepts the answer
	// rather than letting it silently decide quality.
	critiquerPrompt, critiquerErr := nbprompts.GetPromptStrict(o.ctx.GetContext(), nbprompts.PromptReactCritiquer, o.request.AccountId)
	if critiquerErr != nil {
		logger.Error("react4: critiquer prompt failed to load, accepting answer", "error", critiquerErr)
		return "", ""
	}
	critiquePrompt := prompts.NewPromptTemplate(
		critiquerPrompt,
		reactCritiquerInputVariables,
	)
	critiquePromptStr, promptErr := critiquePrompt.Format(map[string]any{
		"input":                        input,
		"scratchpad":                   scratchpad,
		"final_answer":                 finalAnswer,
		"today":                        time.Now().Format(time.RFC1123),
		"notebook":                     o.Notebook,
		"question_type":                questionType,
		"tool_names":                   reActPromptToolNames(o.tools),
		"tool_descriptions":            reActPromptToolDescriptions(o.tools),
		"tools_invoked":                extractToolsInvoked(intermediateSteps),
		"hypothesis_mode_enabled":      resolveHypothesisModeEnabled(o.request, o.nbAgent),
		"sdg_grounding_enabled":        config.Config.LlmServerSDGGroundingContractEnabled && HasServiceDependencyGraphTool(o.tools),
		"premise_verification_enabled": config.Config.PremiseVerificationEnabled,
	})
	if promptErr != nil {
		logger.Error("react4: failed to format critique prompt, accepting answer", "error", promptErr)
		return "", ""
	}

	// Critique is reasoning-heavy; tag the context so it resolves the
	// Reasoning-tier model regardless of the host agent's category.
	critiqueCtx := security.NewRequestContext(
		context.WithValue(o.ctx.GetContext(), ContextKeyModelTier, ModelTierReasoning),
		o.ctx.GetSecurityContext(), o.ctx.GetLogger(), o.ctx.GetTracer(), o.ctx.GetMeter(),
	)

	critiqueMessages := []llms.MessageContent{{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: critiquePromptStr}}}}
	critiqueResult, critiqueErr := GenerateAndTrackLLMContent(critiqueCtx, o.request.UserId, o.request.AccountId, o.request.ConversationId, o.request.MessageId, o.request.AgentId, false, critiqueMessages, true, llms.WithTemperature(0.0))
	if critiqueErr != nil {
		logger.Error("react4: critique llm call failed, accepting answer", "error", critiqueErr)
		return "", ""
	}
	if critiqueResult == nil || len(critiqueResult.Choices) == 0 || strings.TrimSpace(critiqueResult.Choices[0].Content) == "" {
		logger.Warn("react4: critique response empty, accepting answer")
		return "", ""
	}

	decision := common.XmlExtractTagContent(critiqueResult.Choices[0].Content, "decision")
	if decision == "" {
		instruction := "The previous critique response was empty or malformed. Please reply with XML containing exactly a <decision> tag with value 'accept' or 'refine', and a <feedback> tag with concise feedback."
		for retry := 0; retry < react4CritiqueMaxRetries; retry++ {
			retryMessages := []llms.MessageContent{
				{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: instruction}}},
				{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: critiquePromptStr}}},
			}
			retryCritique, retryErr := GenerateAndTrackLLMContent(critiqueCtx, o.request.UserId, o.request.AccountId, o.request.ConversationId, o.request.MessageId, o.request.AgentId, false, retryMessages, true, llms.WithTemperature(0.0))
			if retryErr != nil {
				continue
			}
			if retryCritique != nil && len(retryCritique.Choices) > 0 {
				if d := common.XmlExtractTagContent(retryCritique.Choices[0].Content, "decision"); d != "" {
					critiqueResult = retryCritique
					decision = d
					break
				}
			}
		}
	}
	if decision == "" {
		logger.Error("react4: critique failed after retries, accepting answer")
		return "", ""
	}

	return decision, common.XmlExtractTagContent(critiqueResult.Choices[0].Content, "feedback")
}
