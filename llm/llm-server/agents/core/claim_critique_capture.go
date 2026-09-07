package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	nbprompts "nudgebee/llm/prompts"
	"nudgebee/llm/security"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

const claimCaptureVersion = "1"
const claimShadowCritiqueType = "claim_shadow"

// ClaimCritiqueAuditRecord is a shadow observation, never a planner decision.
// A started row survives a crash or final-write failure, exposing missing data.
type ClaimCritiqueAuditRecord struct {
	AgentName, Input, Answer                      string
	ID, TokenUsageID                              string
	AccountID, ConversationID, MessageID, AgentID string
	Planner, BaselineDecision, BaselineFeedback   string
	Status, Decision                              string
	Detail                                        claimCaptureDetail
	LatencySeconds                                float64
	Completed                                     bool
}

type claimCaptureDetail struct {
	ErrorStage        string          `json:"error_stage,omitempty"`
	ErrorDetail       string          `json:"error_detail,omitempty"`
	Version           string          `json:"version"`
	PromptModule      string          `json:"prompt_module"`
	Prompt            string          `json:"prompt,omitempty"`
	PromptSHA256      string          `json:"prompt_sha256,omitempty"`
	InputPacket       json.RawMessage `json:"input_packet,omitempty"`
	InputBytes        int             `json:"input_bytes"`
	InputSHA256       string          `json:"input_sha256"`
	ObservationCount  int             `json:"observation_count"`
	RawResponse       string          `json:"raw_response,omitempty"`
	ResponseTruncated bool            `json:"response_truncated"`
	Audit             *claimAudit     `json:"audit,omitempty"`
	Feedback          string          `json:"feedback,omitempty"`
}

func claimHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func prepareClaimCapture(ctx *security.RequestContext, accountID, input, answer, notebook string, steps []NBAgentPlannerToolActionStep) (claimCaptureDetail, string) {
	packet, err := claimCritiquePacket(input, answer, notebook, steps)
	detail := claimCaptureDetail{Version: claimCaptureVersion, PromptModule: nbprompts.PromptReactClaimCritiquer,
		InputBytes: len(packet), InputSHA256: claimHash(packet)}
	for _, step := range steps {
		if !isNotebookToolName(step.Action.Tool) {
			detail.ObservationCount++
		}
	}
	if err != nil {
		detail.ErrorStage, detail.ErrorDetail = "prepare", "Evidence packet exceeds byte limit"
		return detail, "input_limit"
	}
	detail.InputPacket = packet
	prompt, err := nbprompts.GetPromptStrict(ctx.GetContext(), nbprompts.PromptReactClaimCritiquer, accountID)
	if err != nil {
		detail.ErrorStage, detail.ErrorDetail = "prompt", TruncateHead(err.Error(), 2048)
		return detail, "prompt_error"
	}
	detail.PromptSHA256 = claimHash([]byte(prompt))
	if len(prompt) > 32*1024 {
		detail.ErrorStage, detail.ErrorDetail = "prepare", "Resolved prompt exceeds byte limit"
		return detail, "input_limit"
	}
	detail.Prompt = prompt
	return detail, ""
}

func newClaimCapture(ctx *security.RequestContext, request NBAgentRequest, planner, agentName, baseline, feedback, input, answer, notebook string, steps []NBAgentPlannerToolActionStep) *ClaimCritiqueAuditRecord {
	detail, status := prepareClaimCapture(ctx, request.AccountId, input, answer, notebook, steps)
	if status == "" {
		status = "started"
	}
	var content struct {
		Request string `json:"request"`
		Answer  string `json:"proposed_answer"`
	}
	// The packet is bounded and produced by our own encoder. Oversized evidence
	// retains only its size/hash; do not reintroduce it through legacy columns.
	if len(detail.InputPacket) > 0 {
		_ = json.Unmarshal(detail.InputPacket, &content)
	}
	return &ClaimCritiqueAuditRecord{AgentName: agentName, Input: content.Request, Answer: content.Answer, ID: uuid.NewString(), TokenUsageID: uuid.NewString(),
		AccountID: request.AccountId, ConversationID: request.ConversationId, MessageID: request.MessageId, AgentID: request.AgentId,
		Planner: planner, BaselineDecision: baseline, BaselineFeedback: feedback,
		Status: status, Detail: detail}
}

// Content capture is intrinsic to the opt-in audit, independent of global tracing.
// Keep output bounded, including malformed/multiple provider choices, and mark truncation.
func captureClaimResponse(response *llms.ContentResponse) (string, bool) {
	if response == nil {
		return "", false
	}
	type choice struct {
		Content    string `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	choices := make([]*choice, 0, len(response.Choices))
	for _, c := range response.Choices {
		if c == nil {
			choices = append(choices, nil)
		} else {
			choices = append(choices, &choice{c.Content, c.StopReason})
		}
	}
	data, _ := json.Marshal(choices) // Only strings and slices; cannot fail.
	const limit = 128 * 1024
	if len(data) > limit {
		return TruncateHead(string(data), limit), true
	}
	return string(data), false
}

func claimUsageID(ctx *security.RequestContext) string {
	// An explicit ID is valid only for the single-attempt audit call.
	preserved, _ := ctx.GetContext().Value(contextKeyPreserveInput).(bool)
	if !preserved {
		return ""
	}
	id, _ := ctx.GetContext().Value(contextKeyClaimUsageID).(string)
	return id
}

func persistClaimCapture(ctx *security.RequestContext, record *ClaimCritiqueAuditRecord) error {
	dao := GetConversationDao()
	if dao == nil {
		return fmt.Errorf("claim capture DAO unavailable")
	}
	// Completion must survive the audit's timeout or user cancellation, but cannot
	// hold the response indefinitely when storage is unavailable.
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), 2*time.Second)
	defer cancel()
	return dao.SaveClaimCritiqueAudit(bounded, record)
}

func (chat *ConversationDao) SaveClaimCritiqueAudit(ctx context.Context, record *ClaimCritiqueAuditRecord) error {
	var completedAt *time.Time
	if record.Completed {
		now := time.Now().UTC()
		completedAt = &now
	}
	detail, err := json.Marshal(struct {
		claimCaptureDetail
		AgentID          string     `json:"agent_id,omitempty"`
		Planner          string     `json:"planner"`
		BaselineDecision string     `json:"baseline_decision"`
		BaselineFeedback string     `json:"baseline_feedback"`
		Status           string     `json:"status"`
		LatencySeconds   float64    `json:"latency_seconds"`
		CompletedAt      *time.Time `json:"completed_at"`
	}{record.Detail, record.AgentID, record.Planner, record.BaselineDecision, record.BaselineFeedback,
		record.Status, record.LatencySeconds, completedAt})
	if err != nil {
		return fmt.Errorf("encode claim capture: %w", err)
	}
	decision := record.Decision
	if decision == "" {
		decision = "skipped"
	}
	_, err = chat.dbManager.Db.ExecContext(ctx, `
        INSERT INTO llm_conversation_agent_critiques
          (id, token_usage_id, account_id, conversation_id, message_id, agent_name,
           input, critiqued_content, critique_type, decision, feedback, audit_metadata)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb)
        ON CONFLICT (id) DO UPDATE SET decision=EXCLUDED.decision, feedback=EXCLUDED.feedback,
          audit_metadata=EXCLUDED.audit_metadata
        WHERE llm_conversation_agent_critiques.critique_type = 'claim_shadow'`,
		record.ID, record.TokenUsageID, record.AccountID, record.ConversationID, record.MessageID, record.AgentName,
		record.Input, record.Answer, claimShadowCritiqueType, decision, record.Detail.Feedback, string(detail))
	if err != nil {
		return fmt.Errorf("save claim capture: %w", err)
	}
	return nil
}
