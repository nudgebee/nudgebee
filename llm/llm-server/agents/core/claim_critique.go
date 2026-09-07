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

func (a claimAudit) decision() (string, string) {
	var gaps []string
	for _, c := range a.Claims {
		if *c.Material && (c.Support == "contradicted" ||
			(*c.AssertedAsFact && (c.Support == "unsupported" || c.Support == "inferred"))) {
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

// Shadow-only: never returns a decision to the planner or writes a synthetic
// tool call. Existing critique retains all timing, formatting and intent checks.
func runClaimCritiqueShadow(ctx *security.RequestContext, request NBAgentRequest, planner, agentName, baseline, baselineFeedback, input, answer, notebook string, steps []NBAgentPlannerToolActionStep) {
	if !config.Config.ClaimCritiqueShadowEnabled || ctx == nil ||
		(request.ParentAgentId != "" && request.ParentAgentId != request.AgentId) {
		return
	}
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
	if err := persistClaimCapture(auditCtx, record); err != nil {
		MetricsClaimCritiqueShadow(planner, baseline, "unknown", "capture_error", time.Since(started).Seconds())
		ctx.GetLogger().Error("claim critique capture failed before inference", "audit_id", record.ID)
		return
	}
	result := claimShadowResult{Status: record.Status}
	if record.Status == "started" {
		auditCtx = security.NewRequestContext(context.WithValue(bounded, contextKeyClaimUsageID, record.TokenUsageID),
			ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
		result = generateClaimShadow(auditCtx, request, record.Detail)
	}
	record.Status, record.Decision = result.Status, result.Decision
	if result.ErrorStage != "" {
		record.Detail.ErrorStage, record.Detail.ErrorDetail = result.ErrorStage, result.ErrorDetail
	}
	record.Detail.Audit, record.Detail.Feedback = result.Audit, result.Feedback
	record.Detail.RawResponse, record.Detail.ResponseTruncated = result.RawResponse, result.ResponseTruncated
	record.LatencySeconds = time.Since(started).Seconds()
	record.Completed = true
	if err := persistClaimCapture(auditCtx, record); err != nil {
		result.Status, result.Decision = "capture_error", ""
		ctx.GetLogger().Error("claim critique capture completion failed", "audit_id", record.ID)
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
}
