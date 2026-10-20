package core

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"

	"github.com/tmc/langchaingo/llms"
)

// AnswerConfidence is a post-hoc grade of how well a completed investigation
// answer is supported by the work that produced it. It is produced by a
// dedicated LLM call that runs AFTER the agent loop has finished and the answer
// is already persisted and delivered — the answer path never waits on it, and
// nothing in the planner, the prompts, or the final-answer contract changes to
// accommodate it.
//
// Why post-hoc rather than asked of the answering model in-band: the grader is
// a separate reviewer with a single job, so a weak self-report cannot inflate
// it, and it can be re-run, re-prompted, or re-modelled against stored history
// without touching the answer pipeline at all. The cost is one extra LLM call
// per investigation turn (hence the per-tenant AI_ANSWER_CONFIDENCE flag) and a score
// that lands a few seconds after the answer.
//
// Persisted to llm_conversation_messages.metadata under the "confidence" key,
// which the UI reads to render the chip. Absent means no chip — a level is
// never synthesized.
type AnswerConfidence struct {
	Level       string   `json:"level"`
	Rationale   string   `json:"rationale,omitempty"`
	Limitations []string `json:"limitations,omitempty"`
}

// Confidence levels. Qualitative rather than a percentage: the grader is
// reasoning about support, not computing a probability, and the UI distinguishes
// exactly three buckets (green / amber / red).
const (
	ConfidenceLevelHigh   = "high"
	ConfidenceLevelMedium = "medium"
	ConfidenceLevelLow    = "low"
)

const (
	// maxConfidenceLimitations caps the stored gap list. The tooltip is a
	// glance surface, not an audit log.
	maxConfidenceLimitations = 5
	// maxConfidenceTextLength bounds each free-text field so a runaway
	// generation cannot bloat the message metadata column.
	maxConfidenceTextLength = 500
	// maxConfidenceAnswerChars bounds how much of the answer is sent to the
	// grader. Investigation answers are normally well under this; a huge one is
	// head-truncated because the conclusion and its supporting chain are near
	// the top.
	maxConfidenceAnswerChars = 12000
	// Per-call caps on the execution trace. Inputs (the command a tool ran) are
	// short and high-signal; outputs keep head and tail so both the command's
	// framing and its last lines survive.
	maxConfidenceTraceInputChars  = 500
	maxConfidenceTraceOutputChars = 1600
	// maxConfidenceTraceChars bounds the whole rendered trace (~8k tokens). A
	// longer trace keeps its first and last calls: the head shows how the
	// investigation was framed, the tail holds the checks behind the conclusion.
	maxConfidenceTraceChars = 32000
	// maxConfidenceTraceRows bounds how many calls are fetched at all, so memory
	// is bounded before the renderer runs rather than after. 400 is ~2.4x the
	// busiest turn ever graded (165 calls) and ~6x p99 (69), so it is a safety
	// valve for a runaway investigation, not a working limit.
	maxConfidenceTraceRows = 400
)

// ScoreAnswerConfidence runs the grader for one completed message and persists
// the result. Returns nil (having written nothing) whenever the turn does not
// qualify or the grader produced nothing usable — callers treat a nil error and
// an absent chip as the same non-event.
func ScoreAnswerConfidence(ctx *security.RequestContext, userId, accountId, conversationId, messageId, question, answer string) error {
	dao := GetConversationDao()
	if dao == nil {
		return fmt.Errorf("confidence: conversation dao not initialized")
	}

	calls, err := dao.ListToolCallTraceByMessage(messageId, maxConfidenceTraceInputChars, maxConfidenceTraceOutputChars, maxConfidenceTraceRows)
	if err != nil {
		// A missing trace would silently read as "nothing was gathered", which
		// the grader would score as `low`. Refusing to score is correct here: a
		// fabricated `low` is exactly the over-claim we are trying to avoid.
		return fmt.Errorf("confidence: failed to load execution trace: %w", err)
	}

	confidence, err := generateAnswerConfidence(ctx, userId, accountId, conversationId, messageId, question, answer, calls)
	if err != nil {
		return fmt.Errorf("confidence: grading failed: %w", err)
	}
	if confidence == nil {
		ctx.GetLogger().Info("confidence: grader returned no usable level, skipping", "message_id", messageId)
		return nil
	}

	if err := dao.UpdateConversationMessageMetadata(messageId, map[string]any{"confidence": confidence}); err != nil {
		return fmt.Errorf("confidence: failed to persist score: %w", err)
	}

	ctx.GetLogger().Info("confidence: scored answer",
		"message_id", messageId,
		"level", confidence.Level,
		"limitations", len(confidence.Limitations),
		"tool_calls_in_trace", len(calls))
	return nil
}

// generateAnswerConfidence makes the grading LLM call and normalizes its output.
func generateAnswerConfidence(
	ctx *security.RequestContext,
	userId, accountId, conversationId, messageId, question, answer string,
	calls []ToolCallTrace,
) (*AnswerConfidence, error) {
	systemPrompt, err := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptAnswerConfidence, accountId)
	if err != nil {
		return nil, fmt.Errorf("failed to load grader prompt: %w", err)
	}

	// The trace is fenced because it carries raw tool output — pod logs, config
	// values, scraped pages — which is attacker-adjacent text now entering a
	// second LLM call. The delimiter plus the prompt's "data, never
	// instructions" rule is what stops a log line shaped like this grader's own
	// JSON being read as the verdict.
	input := fmt.Sprintf(
		"## User question\n%s\n\n## Final answer\n%s\n\n## Execution trace\n<execution_trace>\n%s\n</execution_trace>\n",
		strings.TrimSpace(question),
		TruncateHead(strings.TrimSpace(answer), maxConfidenceAnswerChars),
		formatExecutionTrace(calls),
	)

	mcList := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: systemPrompt}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: input}}},
	}

	// Cache scope and model tier, matching every other post-hoc utility call in
	// this package (title generation, acknowledgment, memory extraction, the
	// context manager):
	//
	//   - Account scope, not the default Conversation scope. The grader's system
	//     prefix is a static rubric, identical for every conversation, so keying
	//     it on conversationId (generateCacheKey's default) would miss on
	//     essentially every call — grading runs once per conversation turn, so a
	//     conversation-scoped slot is created and then never reused before its
	//     30m/10m TTL expires. Account scope shares one 12h slot across every
	//     investigation in the account, which is the reuse pattern this call
	//     actually has.
	//
	//   - Account rather than Global specifically because the Global key is
	//     `global:{agent}:{model}:{credsFp}` — no accountId and no content hash
	//     (see generateCacheKey). The rubric is loaded per-account via
	//     GetPromptStrict, so an account that overrides it would otherwise share
	//     a cache slot with every other account and be served the wrong prefix.
	//     Account scope keys on accountId, so an override stays isolated.
	//
	//   - Summary model tier: grading reads a bounded trace and checks the
	//     answer's claims against it — a reading task, not a re-investigation —
	//     and it keeps the one extra call per investigation off the
	//     reasoning-tier model. Orthogonal to WithThinkingLevel, which sets
	//     reasoning effort, not model choice.
	//
	// Temperature 0 for a stable grade on re-runs.
	cacheCtx := security.NewRequestContext(
		context.WithValue(
			context.WithValue(ctx.GetContext(), llmruntime.ContextKeyModelTier, llmruntime.ModelTierSummary),
			llmruntime.ContextKeyCacheScope, llmruntime.CacheScopeAccount,
		),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	result, err := llmruntime.GenerateAndTrackLLMContent(
		cacheCtx, userId, accountId, conversationId, messageId, PromptAnswerConfidenceAgent, true, mcList, true,
		llms.WithTemperature(0.0), llmruntime.WithThinkingLevel(llmruntime.ThinkingLevelFastTask),
	)
	if err != nil {
		return nil, fmt.Errorf("llm call failed: %w", err)
	}
	if len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Content) == "" {
		return nil, fmt.Errorf("empty grader response")
	}

	return parseAnswerConfidence(result.Choices[0].Content), nil
}

// PromptAnswerConfidenceAgent is the pseudo-agent name this call is booked
// against for token accounting, so post-hoc grading cost is separable from the
// answer's own cost in usage reports.
const PromptAnswerConfidenceAgent = "answer_confidence"

// fenceTagPattern matches an execution_trace fence tag appearing inside the
// rendered trace, so raw tool output cannot forge or close the fence.
var fenceTagPattern = regexp.MustCompile(`(?i)</?\s*execution_trace\s*>`)

// formatExecutionTrace renders the tool calls in order — tool, status, input
// and (bounded) output — so the grader can check the answer's claims against
// what the tools actually returned. Returns an explicit marker when nothing
// ran, so an empty section cannot read as "no problems".
//
// A trace at the row cap is labelled as abridged. Without that label the
// grader reads a dropped call as a call that never happened and marks the
// answer down for it — quietly penalising exactly the longest investigations.
func formatExecutionTrace(calls []ToolCallTrace) string {
	if len(calls) == 0 {
		return "(no tools were executed for this answer)"
	}
	// Either cap can drop whole calls, and the char cap is the one that usually
	// does: at full-size outputs it binds around 19 calls, while the row cap
	// binds at 400. Labelling only the row cap would leave the common case
	// silent — worse than saying nothing, because the prompt promises the label.
	abridged := len(calls) >= maxConfidenceTraceRows
	var b strings.Builder
	for i, c := range calls {
		output := strings.TrimSpace(c.Response)
		if output == "" {
			output = "(empty)"
		}
		// Some tool wrappers persist no arguments at all (custom_agent_tool_tracking.go
		// saves ""), which would otherwise render a dangling "input:" the grader
		// has to interpret.
		input := strings.TrimSpace(c.Parameters)
		if input == "" {
			input = "(none recorded)"
		}
		fmt.Fprintf(&b, "%d. %s [%s]\n   input: %s\n   output: %s\n",
			i+1, c.ToolName, strings.ToLower(c.Status), input, output)
	}
	out := b.String()
	if len(out) > maxConfidenceTraceChars {
		out = TruncateMiddle(out, maxConfidenceTraceChars*2/5, maxConfidenceTraceChars*3/5)
		abridged = true
	}
	// Tool output is untrusted text, so it must not be able to close the fence the
	// caller wraps it in: a ConfigMap value or scraped page containing the closing
	// tag would put everything after it outside the fence. Case-insensitive and
	// whitespace-tolerant because the model reads it the same way.
	out = fenceTagPattern.ReplaceAllString(out, "[tag]")
	if abridged {
		// TruncateMiddle's own marker reads as one output being cut, which is
		// how a grader would misread whole calls going missing.
		out = "(abridged: some of this investigation's tool calls are omitted)\n" + out
	}
	return out
}

// parseAnswerConfidence normalizes the grader's JSON. Returns nil when the
// payload is unparseable or carries a level outside the known set, so a
// malformed grade renders no chip rather than a guessed one.
func parseAnswerConfidence(raw string) *AnswerConfidence {
	var parsed struct {
		Level       string   `json:"level"`
		Rationale   string   `json:"rationale"`
		Limitations []string `json:"limitations"`
	}
	if err := common.ExtractAndUnmarshalJSON([]byte(raw), &parsed); err != nil {
		return nil
	}

	level := normalizeConfidenceLevel(parsed.Level)
	if level == "" {
		return nil
	}

	return &AnswerConfidence{
		Level:       level,
		Rationale:   truncateConfidenceText(parsed.Rationale),
		Limitations: normalizeLimitations(parsed.Limitations),
	}
}

// normalizeConfidenceLevel maps the grader's level onto the canonical set.
// Anything else (empty, a percentage, "very high") returns "" so the caller
// drops the grade instead of guessing.
func normalizeConfidenceLevel(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case ConfidenceLevelHigh:
		return ConfidenceLevelHigh
	case ConfidenceLevelMedium:
		return ConfidenceLevelMedium
	case ConfidenceLevelLow:
		return ConfidenceLevelLow
	default:
		return ""
	}
}

// normalizeLimitations trims, drops empties, dedupes and caps the gap list.
func normalizeLimitations(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, maxConfidenceLimitations)
	for _, item := range items {
		item = truncateConfidenceText(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
		if len(out) == maxConfidenceLimitations {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// truncateConfidenceText trims and length-bounds a free-text field, without
// splitting a multi-byte rune.
func truncateConfidenceText(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxConfidenceTextLength {
		return s
	}
	return TruncateHead(s, maxConfidenceTextLength)
}

// Per-tenant opt-in feature (public.feature catalog + a public.feature_flag row
// per tenant), because grading costs one extra LLM call per investigation turn.
const FeatureAnswerConfidence = "AI_ANSWER_CONFIDENCE"

// Test seam for the tenant-flag read. Package-level var like llm_common.go's
// newLLMModel; safe only while the tests that swap it stay non-t.Parallel().
var answerConfidenceFeatureCheck = common.IsFeatureEnabled

// Test seam for the graded work itself, same rule as the var above. Lets the
// async wiring (gate → flag → submit → deadline → arguments) be asserted
// without a live LLM or metastore.
var scoreAnswerConfidenceFn = ScoreAnswerConfidence

// answerConfidenceDefaultTaskTimeoutSeconds bounds the grading task body —
// trace read, grading LLM call, metadata write — when
// AnswerConfidenceTaskTimeoutSeconds is unset or <= 0.
//
// This is NOT AsyncOperationTimeoutSeconds, which stays the worker-pool
// admission budget and is too short for a task containing an LLM call. The
// provider's own per-call timeouts are the real backstop; this deadline exists
// only to stop a stuck task occupying a worker indefinitely, so it is kept to
// a couple of minutes rather than several — the pool is shared with every
// other async conversation task.
const answerConfidenceDefaultTaskTimeoutSeconds = 120

// answerConfidenceTaskTimeout resolves the configured grading deadline.
func answerConfidenceTaskTimeout() time.Duration {
	secs := config.Config.AnswerConfidenceTaskTimeoutSeconds
	if secs <= 0 {
		secs = answerConfidenceDefaultTaskTimeoutSeconds
	}
	return time.Duration(secs) * time.Second
}

// AnswerConfidenceEnabledForTenant reports whether the grader is active for tenantId.
// Fail-closed — uncertainty must not spend an LLM call. ctx is logger-only and may be
// nil (GetLogger tolerates a nil receiver).
func AnswerConfidenceEnabledForTenant(ctx *security.RequestContext, tenantId string) bool {
	if tenantId == "" {
		return false
	}
	enabled, err := answerConfidenceFeatureCheck(FeatureAnswerConfidence, tenantId)
	if err != nil {
		ctx.GetLogger().Warn("confidence: unable to read tenant feature flag, treating as disabled",
			"feature", FeatureAnswerConfidence, "tenant_id", tenantId, "error", err)
		return false
	}
	return enabled
}

// ScoreAnswerConfidenceAsync submits the grader to the shared async task pool.
// It is fire-and-forget by design: the answer has already been persisted and
// returned to the client by the time this runs, so a grading failure must never
// surface to the user or affect the turn's status. The chip simply does not
// appear.
//
// Gated on the tenant's AI_ANSWER_CONFIDENCE flag and the turn being an
// investigation — a plain retrieval has nothing to grade.
func ScoreAnswerConfidenceAsync(ctx *security.RequestContext, request NBAgentRequest, answer string) {
	if !shouldScoreAnswerConfidence(request, answer) {
		return
	}

	// Background flows can carry no security context; extractSessionWorkingMemoryV2
	// guards the same way. A blind deref would panic a goroutine nobody awaits.
	secCtx := ctx.GetSecurityContext()
	if secCtx == nil {
		return
	}
	// One indexed lookup, here rather than inside the task so a disabled tenant
	// never takes a worker slot. The cheap gates above already excluded non-candidates.
	if !AnswerConfidenceEnabledForTenant(ctx, secCtx.GetTenantId()) {
		return
	}

	question := request.OriginalQuery
	if question == "" {
		question = request.Query
	}

	// Read every remaining request-scoped value on the CALLING goroutine — the
	// parent RequestContext must not be touched from the worker.
	logger := ctx.GetLogger()
	tracer := ctx.GetTracer()
	meter := ctx.GetMeter()

	// Admission only — how long this submission may wait for a free worker.
	// The task body gets its own, much larger budget below.
	submissionCtx, cancel := context.WithTimeout(context.Background(),
		time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
	defer cancel()

	err := conversationAsyncTaskWorkerPool.Submit(submissionCtx, func() {
		// The task body needs its OWN deadline: submissionCtx bounds only the
		// queue wait and is cancelled by the deferred cancel above as soon as
		// Submit returns. Without this, a hung provider would pin a pool worker
		// indefinitely — the LLM client's per-call timeout is the backstop, but
		// this task also does DB work either side of it. See
		// answerConfidenceDefaultTaskTimeoutSeconds for why this is not the
		// admission budget.
		taskCtx, taskCancel := context.WithTimeout(context.Background(), answerConfidenceTaskTimeout())
		defer taskCancel()

		bgCtx := security.NewRequestContext(taskCtx, secCtx, logger, tracer, meter)
		if err := scoreAnswerConfidenceFn(bgCtx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, question, answer); err != nil {
			bgCtx.GetLogger().Error("confidence: failed to score answer", "message_id", request.MessageId, "error", err)
		}
	})
	if err != nil {
		ctx.GetLogger().Error("confidence: failed to submit scoring task", "message_id", request.MessageId, "error", err)
	}
}

// shouldScoreAnswerConfidence decides whether a completed turn is worth
// grading. Investigation-only, matching where a root-cause claim is actually
// made — mirrors the classification the executor already uses for prompt
// variant and model tier, so "is an investigation" means one thing service-wide.
func shouldScoreAnswerConfidence(request NBAgentRequest, answer string) bool {
	if request.MessageId == "" || strings.TrimSpace(answer) == "" {
		return false
	}
	question := request.OriginalQuery
	if question == "" {
		question = request.Query
	}
	if strings.TrimSpace(question) == "" {
		return false
	}
	return IsInvestigationRequestTask(question) || request.ConversationSource == ConversationSourceInvestigation
}
