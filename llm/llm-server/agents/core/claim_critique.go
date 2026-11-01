package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

const claimCritiqueInputLimit = 256 * 1024
const claimCritiqueTimeout = 20 * time.Second

type critiqueClaim struct {
	Claim          string `json:"claim"`
	Support        string `json:"support"`
	AssertedAsFact *bool  `json:"asserted_as_fact"`
	Material       *bool  `json:"material"`
	EvidenceRefs   []int  `json:"evidence_refs"`
}

type claimAudit struct {
	Claims          []critiqueClaim `json:"claims"`
	MissingRequired []string        `json:"missing_required"`
}

type claimObservation struct {
	ID     int    `json:"id"`
	Tool   string `json:"tool"`
	Input  string `json:"input"`
	Output string `json:"output"`
	// Keep the existing bounded child manifest with its parent observation.
	// It may contain truncation markers and must not be treated as a full trace.
	SubAgentEvidence string `json:"sub_agent_evidence,omitempty"`
}

// Deliberately omit planner Thought text. Tool outputs may still include
// subagent interpretations; the prompt must distinguish those from observations.
// Notebook state is retained separately as notes, never as authoritative evidence.
func claimCritiquePacket(input, answer, notebook string, steps []NBAgentPlannerToolActionStep) ([]byte, error) {
	observations := make([]claimObservation, 0, len(steps))
	for _, step := range steps {
		if isNotebookToolName(step.Action.Tool) {
			continue
		}
		observations = append(observations, claimObservation{
			ID: len(observations) + 1, Tool: step.Action.Tool, Input: step.Action.ToolInput, Output: step.Observation,
			SubAgentEvidence: step.SubAgentEvidence,
		})
	}
	packet, err := json.Marshal(struct {
		Request      string             `json:"request"`
		Observations []claimObservation `json:"observations"`
		Notes        string             `json:"investigator_notes"`
		Answer       string             `json:"proposed_answer"`
	}{input, observations, notebook, answer})
	if err != nil {
		return nil, fmt.Errorf("encode claim critique input: %w", err)
	}
	if len(packet) > claimCritiqueInputLimit {
		return packet, fmt.Errorf("claim critique input exceeds limit")
	}
	return packet, nil
}

// Validate duplicate keys before decoding: encoding/json otherwise silently
// uses the last value, including conflicting material/asserted_as_fact flags.
func claimJSONValue(dec *json.Decoder, depth int) error {
	if depth > 8 {
		return fmt.Errorf("claim audit nesting exceeds limit")
	}
	token, err := dec.Token()
	if err != nil {
		return fmt.Errorf("read JSON value: %w", err)
	}
	if token == nil {
		return fmt.Errorf("null is not valid in a claim audit")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return fmt.Errorf("read JSON key: %w", err)
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("invalid or duplicate JSON key")
			}
			switch name {
			case "claims", "missing_required", "claim", "support", "asserted_as_fact", "material", "evidence_refs":
			default:
				return fmt.Errorf("unknown claim audit field")
			}
			seen[name] = true
			if err := claimJSONValue(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := claimJSONValue(dec, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("close JSON value: %w", err)
	}
	return nil
}

// Errors have no verdict. Never repair malformed JSON or treat missing bools
// as false: either would turn an evaluator failure into an acceptance.
func parseClaimAudit(content string, observationCount int) (claimAudit, error) {
	var audit claimAudit
	if len(content) > 128*1024 {
		return audit, fmt.Errorf("claim audit output exceeds limit")
	}
	if err := claimJSONValue(json.NewDecoder(strings.NewReader(content)), 0); err != nil {
		return audit, fmt.Errorf("invalid claim audit JSON: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&audit); err != nil {
		return audit, fmt.Errorf("decode claim audit: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return audit, fmt.Errorf("claim audit contains trailing content")
	}
	if len(audit.Claims) == 0 || len(audit.Claims) > 32 || audit.MissingRequired == nil || len(audit.MissingRequired) > 16 {
		return audit, fmt.Errorf("invalid claim audit collection")
	}
	for _, missing := range audit.MissingRequired {
		if strings.TrimSpace(missing) == "" || len(missing) > 1000 {
			return audit, fmt.Errorf("invalid missing requirement")
		}
	}
	for _, claim := range audit.Claims {
		if strings.TrimSpace(claim.Claim) == "" || len(claim.Claim) > 2000 ||
			claim.Material == nil || claim.AssertedAsFact == nil || claim.EvidenceRefs == nil {
			return audit, fmt.Errorf("incomplete claim audit entry")
		}
		switch claim.Support {
		case "observed", "inferred", "unsupported", "contradicted":
		default:
			return audit, fmt.Errorf("invalid claim support")
		}
		if (claim.Support == "observed" || claim.Support == "contradicted") && len(claim.EvidenceRefs) == 0 {
			return audit, fmt.Errorf("claim missing evidence references")
		}
		seen := map[int]bool{}
		for _, ref := range claim.EvidenceRefs {
			// 0 references facts supplied in the original request, not a tool result.
			if ref < 0 || ref > observationCount || seen[ref] {
				return audit, fmt.Errorf("invalid evidence reference")
			}
			seen[ref] = true
		}
	}
	return audit, nil
}

// isClaimGap is the rejection rule: a material claim that is contradicted, or
// asserted as fact without being observed. Step-2 guidance is written for
// exactly these claims, so both steps share this predicate.
func isClaimGap(c critiqueClaim) bool {
	return *c.Material && (c.Support == "contradicted" ||
		(*c.AssertedAsFact && (c.Support == "unsupported" || c.Support == "inferred")))
}

func (a claimAudit) decision() (string, string) {
	var gaps []string
	for _, c := range a.Claims {
		if isClaimGap(c) {
			gaps = append(gaps, c.Support+": "+c.Claim)
		}
	}
	gaps = append(gaps, a.MissingRequired...)
	if len(gaps) == 0 {
		return "accept", ""
	}
	return "refine", "Correct these material evidence gaps using existing observations, or one permitted discriminating read. Clearly label hypotheses; do not repeat denied evidence paths.\n- " + strings.Join(gaps, "\n- ")
}

type claimShadowResult struct {
	ErrorStage        string
	ErrorDetail       string
	Status            string
	Decision          string
	Feedback          string
	Audit             *claimAudit
	RawResponse       string
	ResponseTruncated bool
	// Instruction and Content are the continuation audit's final user turn and
	// the model's reply exactly as returned, so guidance continues the same request.
	Instruction string
	Content     string
}

func evaluateClaimShadow(ctx *security.RequestContext, request NBAgentRequest, input, answer, notebook string, steps []NBAgentPlannerToolActionStep) claimShadowResult {
	detail, status := prepareClaimCapture(ctx, request.AccountId, input, answer, notebook, steps)
	if status != "" {
		return claimShadowResult{Status: status}
	}
	return generateClaimShadow(ctx, request, detail)
}

func generateClaimShadow(ctx *security.RequestContext, request NBAgentRequest, detail claimCaptureDetail) claimShadowResult {
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, detail.Prompt),
		llms.TextParts(llms.ChatMessageTypeHuman, string(detail.InputPacket)),
	}
	response, err := GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId,
		request.MessageId, request.AgentId, false, messages, false, llms.WithTemperature(0), llms.WithJSONMode())
	if err != nil {
		if errors.Is(err, errPreservedPromptChanged) {
			return claimShadowResult{Status: "input_limit", ErrorStage: "preflight", ErrorDetail: TruncateHead(err.Error(), 2048)}
		}
		return claimShadowResult{Status: "generation_error", ErrorStage: "generation", ErrorDetail: TruncateHead(err.Error(), 2048)}
	}
	return evaluateClaimResponse(response, detail.ObservationCount)
}

// generateClaimFallback runs the standalone packet audit after a failed
// continuation, on a fresh budget (a slow continuation may have spent the first)
// and a fresh usage id (an explicit id belongs to a single attempt), which the
// record then points at because its verdict is the one used.
func generateClaimFallback(ctx *security.RequestContext, request NBAgentRequest, record *ClaimCritiqueAuditRecord) claimShadowResult {
	bounded, cancel := context.WithTimeout(ctx.GetContext(), claimCritiqueTimeout)
	defer cancel()
	record.TokenUsageID = uuid.NewString()
	bounded = context.WithValue(bounded, ContextKeyModelTier, ModelTierReasoning)
	bounded = context.WithValue(bounded, contextKeyPreserveInput, true)
	bounded = context.WithValue(bounded, contextKeyClaimUsageID, record.TokenUsageID)
	fallbackCtx := security.NewRequestContext(bounded, ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
	return generateClaimShadow(fallbackCtx, request, record.Detail)
}

// generateClaimContinuation runs the audit as a continuation of the planner's
// request: same messages, tools and model, so the provider cache covers them.
// Evidence references are the planner's E-numbers rather than packet ids.
func generateClaimContinuation(conv *critiqueConversation, usageID string, request NBAgentRequest, detail claimCaptureDetail) claimShadowResult {
	bounded, cancel := context.WithTimeout(conv.ctx.GetContext(), claimCritiqueTimeout)
	defer cancel()
	ctx := conv.derive(context.WithValue(bounded, contextKeyClaimUsageID, usageID))
	instruction := conv.instruction(detail.Prompt)
	response, err := conv.generate(ctx, request, llms.TextParts(llms.ChatMessageTypeHuman, instruction))
	switch {
	case errors.Is(err, errCritiqueToolCall):
		return claimShadowResult{Status: "tool_call", ErrorStage: "generation", ErrorDetail: "Continuation audit called a tool"}
	case errors.Is(err, errPreservedPromptChanged):
		return claimShadowResult{Status: "input_limit", ErrorStage: "preflight", ErrorDetail: TruncateHead(err.Error(), 2048)}
	case err != nil:
		return claimShadowResult{Status: "generation_error", ErrorStage: "generation", ErrorDetail: TruncateHead(err.Error(), 2048)}
	}
	content := ""
	if response != nil && len(response.Choices) == 1 && response.Choices[0] != nil {
		content = response.Choices[0].Content
		response.Choices[0].Content = stripJSONFence(content)
	}
	result := evaluateClaimResponse(response, conv.maxEvidence)
	result.Instruction, result.Content = instruction, content
	return result
}

func evaluateClaimResponse(response *llms.ContentResponse, count int) (result claimShadowResult) {
	defer func() { result.RawResponse, result.ResponseTruncated = captureClaimResponse(response) }()
	if response == nil || len(response.Choices) != 1 || response.Choices[0] == nil {
		return claimShadowResult{Status: "empty_response", ErrorStage: "response", ErrorDetail: "Expected exactly one non-nil response choice"}
	}
	choice := response.Choices[0]
	// Do not certify a partial or blocked generation, even if its prefix is JSON.
	switch strings.ToLower(choice.StopReason) {
	case "stop", "end_turn", "finishreasonstop":
	default:
		return claimShadowResult{Status: "incomplete_response", ErrorStage: "response", ErrorDetail: "Provider did not report a completed response"}
	}
	audit, err := parseClaimAudit(choice.Content, count)
	if err != nil {
		return claimShadowResult{Status: "invalid_output", ErrorStage: "parse", ErrorDetail: TruncateHead(err.Error(), 2048)}
	}
	decision, feedback := audit.decision()
	return claimShadowResult{Status: "ok", Decision: decision, Feedback: feedback, Audit: &audit}
}

// The enabled audit captures its result and can request refinement,
// but can never clear an incumbent rejection or create a separate retry budget.
// finalRound marks the critique whose rejection spends the planner's last
// refinement: the answer that follows is accepted without another review.
// conv, when set, runs the audit and guidance as continuations of the planner's
// request; any failure there falls back to the standalone packet audit.
func runClaimCritiqueShadow(ctx *security.RequestContext, request NBAgentRequest, planner, agentName, baseline, baselineFeedback, input, answer, notebook string, steps []NBAgentPlannerToolActionStep, finalRound bool, conv *critiqueConversation) (effectiveDecision, effectiveFeedback string) {
	effectiveDecision, effectiveFeedback = baseline, baselineFeedback
	if !config.Config.ClaimCritiqueFeedbackEnabled || ctx == nil ||
		(request.ParentAgentId != "" && request.ParentAgentId != request.AgentId) {
		return
	}
	baseline = strings.ToLower(strings.TrimSpace(baseline))
	if baseline != "accept" && baseline != "refine" {
		baseline = "unknown"
	}
	started := time.Now()
	bounded, cancel := context.WithTimeout(ctx.GetContext(), claimCritiqueTimeout)
	defer cancel()
	bounded = context.WithValue(bounded, ContextKeyModelTier, ModelTierReasoning)
	bounded = context.WithValue(bounded, contextKeyPreserveInput, true)
	auditCtx := security.NewRequestContext(bounded, ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
	record := newClaimCapture(auditCtx, request, planner, agentName, baseline, baselineFeedback, input, answer, notebook, steps)
	record.Detail.FeedbackEnabled = config.Config.ClaimCritiqueFeedbackEnabled
	if err := persistClaimCapture(auditCtx, record); err != nil {
		MetricsClaimCritiqueShadow(planner, baseline, "unknown", "capture_error", time.Since(started).Seconds())
		ctx.GetLogger().Error("claim critique capture failed before inference", "audit_id", record.ID)
		return
	}
	result := claimShadowResult{Status: record.Status}
	// An oversized packet only rules out the standalone audit: the continuation
	// sends the planner's own request, which the model has already accepted.
	oversizedContinuation := record.Status == "input_limit" && conv != nil && record.Detail.Prompt != "" && len(record.Detail.InputPacket) == 0
	if record.Status == "started" || oversizedContinuation {
		if oversizedContinuation {
			record.Detail.ErrorStage, record.Detail.ErrorDetail = "", ""
		}
		auditCtx = security.NewRequestContext(context.WithValue(bounded, contextKeyClaimUsageID, record.TokenUsageID),
			ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
		if conv != nil {
			result = generateClaimContinuation(conv, record.TokenUsageID, request, record.Detail)
			record.Detail.Mode = claimModeContinuation
			if result.Status != "ok" {
				record.Detail.ContinuationError = TruncateHead(strings.TrimSpace(result.Status+": "+result.ErrorDetail), 2048)
				// No standalone fallback for an oversized packet: keep the
				// continuation's own failure as the outcome.
				if !oversizedContinuation {
					record.Detail.Mode = claimModeFallback
					result = generateClaimFallback(ctx, request, record)
				}
			}
		} else {
			result = generateClaimShadow(auditCtx, request, record.Detail)
		}
	}
	// Step 2: explain the rejected claims. The verdict is already fixed; guidance
	// only replaces the feedback text, and any failure keeps the legacy text.
	if config.Config.ClaimCritiqueActionableFeedbackEnabled && result.Status == "ok" && result.Decision == "refine" && result.Audit != nil {
		var guidanceConv *claimGuidanceContinuation
		if record.Detail.Mode == claimModeContinuation {
			guidanceConv = &claimGuidanceContinuation{conv: conv, auditInstruction: result.Instruction, auditResponse: result.Content}
		}
		guided, capture := claimGuidanceFeedback(ctx, request, record.Detail, *result.Audit, finalRound, guidanceConv)
		record.Detail.Guidance = capture
		if guided != "" {
			result.Feedback = guided
		}
	}
	record.Status, record.Decision = result.Status, result.Decision
	if result.ErrorStage != "" {
		record.Detail.ErrorStage, record.Detail.ErrorDetail = result.ErrorStage, result.ErrorDetail
	}
	record.Detail.Audit, record.Detail.Feedback = result.Audit, result.Feedback
	record.Detail.RawResponse, record.Detail.ResponseTruncated = result.RawResponse, result.ResponseTruncated
	record.LatencySeconds = time.Since(started).Seconds()
	record.Completed = true
	decisionForPlanner, feedbackForPlanner := claimFeedbackDecision(record.Detail.FeedbackEnabled, effectiveDecision, effectiveFeedback, result)
	record.Detail.EffectiveDecision, record.Detail.EffectiveFeedback = decisionForPlanner, feedbackForPlanner
	record.Detail.FeedbackSelected = decisionForPlanner != effectiveDecision || feedbackForPlanner != effectiveFeedback
	if err := persistClaimCapture(auditCtx, record); err != nil {
		result.Status, result.Decision = "capture_error", ""
		ctx.GetLogger().Error("claim critique capture completion failed", "audit_id", record.ID)
	} else {
		effectiveDecision, effectiveFeedback = decisionForPlanner, feedbackForPlanner
	}

	decision := result.Decision
	if decision == "" {
		decision = "unknown"
	}
	latency := time.Since(started).Seconds()
	MetricsClaimCritiqueShadow(planner, baseline, decision, result.Status, latency)
	// Content and exact model usage are in existing token telemetry. Keep free-
	// form claims, evidence, and model error bodies out of operational logs.
	ctx.GetLogger().Info("claim critique shadow", "audit_id", record.ID, "planner", planner, "status", result.Status,
		"baseline", baseline, "decision", decision, "latency_seconds", latency,
		"conversation_id", request.ConversationId, "message_id", request.MessageId, "agent_id", request.AgentId)
	return
}

// Only a valid audit may add feedback. Unknown incumbent outcomes retain their
// existing fallback; a successful claim audit is not a replacement critic.
func claimFeedbackDecision(enabled bool, baseline, feedback string, result claimShadowResult) (string, string) {
	if !enabled || result.Status != "ok" || result.Decision != "refine" || strings.TrimSpace(result.Feedback) == "" {
		return baseline, feedback
	}
	switch strings.ToLower(strings.TrimSpace(baseline)) {
	case "accept":
		return "refine", result.Feedback
	case "refine":
		if strings.TrimSpace(feedback) == "" {
			return "refine", result.Feedback
		}
		return "refine", feedback + "\n\nAdditional claim audit feedback:\n" + result.Feedback
	default:
		return baseline, feedback
	}
}
