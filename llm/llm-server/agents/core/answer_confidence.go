package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
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
// per investigation turn (hence the AnswerConfidenceEnabled flag) and a score
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
	// head-truncated because the sections the rubric keys on (the Causality
	// Chain / Findings header and the evidence citations) are near the top.
	maxConfidenceAnswerChars = 12000
	// maxConfidenceManifestRows bounds the execution manifest. Ordered
	// most-used-first by the DAO, so the tail we drop is the long thin end.
	maxConfidenceManifestRows = 40
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

	outcomes, err := dao.ListToolCallOutcomesByMessage(messageId)
	if err != nil {
		// A missing manifest would silently become "nothing was gathered" —
		// which the rubric scores as `low`. Refusing to score is correct here:
		// a fabricated `low` is exactly the over-claim we are trying to avoid.
		return fmt.Errorf("confidence: failed to load execution manifest: %w", err)
	}

	confidence, err := generateAnswerConfidence(ctx, userId, accountId, conversationId, messageId, question, answer, outcomes)
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
		"tools_in_manifest", len(outcomes))
	return nil
}

// generateAnswerConfidence makes the grading LLM call and normalizes its output.
func generateAnswerConfidence(
	ctx *security.RequestContext,
	userId, accountId, conversationId, messageId, question, answer string,
	outcomes []ToolCallOutcome,
) (*AnswerConfidence, error) {
	systemPrompt, err := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptAnswerConfidence, accountId)
	if err != nil {
		return nil, fmt.Errorf("failed to load grader prompt: %w", err)
	}

	input := fmt.Sprintf(
		"## User question\n%s\n\n## Final answer\n%s\n\n## Execution manifest\n%s\n",
		strings.TrimSpace(question),
		TruncateHead(strings.TrimSpace(answer), maxConfidenceAnswerChars),
		formatExecutionManifest(outcomes),
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
	//   - Summary model tier: grading fills a mechanical checklist against a
	//     fixed rubric, which is what that tier exists for, and it keeps the one
	//     extra call per investigation off the reasoning-tier model. Orthogonal
	//     to WithThinkingLevel, which sets reasoning effort, not model choice.
	//
	// Temperature 0 for a stable grade on re-runs.
	cacheCtx := security.NewRequestContext(
		context.WithValue(
			context.WithValue(ctx.GetContext(), ContextKeyModelTier, ModelTierSummary),
			ContextKeyCacheScope, CacheScopeAccount,
		),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	result, err := GenerateAndTrackLLMContent(
		cacheCtx, userId, accountId, conversationId, messageId, PromptAnswerConfidenceAgent, true, mcList, true,
		llms.WithTemperature(0.0), WithThinkingLevel(ThinkingLevelFastTask),
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

// formatExecutionManifest renders the tool outcomes as compact lines the grader
// can count distinct sources and failures from. Returns an explicit marker when
// nothing ran — the rubric treats that as `low` rather than letting an empty
// section read as "no problems".
func formatExecutionManifest(outcomes []ToolCallOutcome) string {
	if len(outcomes) == 0 {
		return "(no tools were executed for this answer)"
	}
	if len(outcomes) > maxConfidenceManifestRows {
		outcomes = outcomes[:maxConfidenceManifestRows]
	}
	var b strings.Builder
	for _, o := range outcomes {
		fmt.Fprintf(&b, "- %s: %s (%d call%s)\n", o.ToolName, strings.ToLower(o.Status), o.Count, pluralSuffix(o.Count))
	}
	fmt.Fprintf(&b, "\nDistinct tools used: %d\n", distinctToolCount(outcomes))
	return b.String()
}

// pluralSuffix is "" for 1 and "s" otherwise. Named apart from the package's
// existing `plural` (conversation_optimizer.go), which is the y/ies variant.
func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// distinctToolCount counts unique tool names, since the same tool appearing
// with several statuses is still ONE source under the rubric.
func distinctToolCount(outcomes []ToolCallOutcome) int {
	seen := make(map[string]struct{}, len(outcomes))
	for _, o := range outcomes {
		seen[o.ToolName] = struct{}{}
	}
	return len(seen)
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

// ScoreAnswerConfidenceAsync submits the grader to the shared async task pool.
// It is fire-and-forget by design: the answer has already been persisted and
// returned to the client by the time this runs, so a grading failure must never
// surface to the user or affect the turn's status. The chip simply does not
// appear.
//
// Gated on AnswerConfidenceEnabled (off by default) and on the turn being an
// investigation — a plain retrieval has nothing to grade.
func ScoreAnswerConfidenceAsync(ctx *security.RequestContext, request NBAgentRequest, answer string) {
	if !config.Config.AnswerConfidenceEnabled {
		return
	}
	if !shouldScoreAnswerConfidence(request, answer) {
		return
	}

	question := request.OriginalQuery
	if question == "" {
		question = request.Query
	}

	// Read every request-scoped value on the CALLING goroutine, before the task
	// is queued — the parent RequestContext must not be touched from the worker.
	secCtx := ctx.GetSecurityContext()
	logger := ctx.GetLogger()
	tracer := ctx.GetTracer()
	meter := ctx.GetMeter()

	timeout := time.Duration(config.Config.AsyncOperationTimeoutSeconds) * time.Second
	submissionCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err := conversationAsyncTaskWorkerPool.Submit(submissionCtx, func() {
		// The task body needs its OWN deadline: submissionCtx bounds only the
		// queue wait and is cancelled by the deferred cancel above as soon as
		// Submit returns. Without this, a hung provider would pin a pool worker
		// indefinitely — the LLM client's per-call timeout is the backstop, but
		// this task also does DB work either side of it.
		taskCtx, taskCancel := context.WithTimeout(context.Background(), timeout)
		defer taskCancel()

		bgCtx := security.NewRequestContext(taskCtx, secCtx, logger, tracer, meter)
		if err := ScoreAnswerConfidence(bgCtx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, question, answer); err != nil {
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
