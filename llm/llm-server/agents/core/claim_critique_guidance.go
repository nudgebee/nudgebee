package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	nbprompts "nudgebee/llm/prompts"
	"nudgebee/llm/security"

	"github.com/tmc/langchaingo/llms"
)

// Step 2 runs after the audit, on its own budget: the audit alone takes most of
// claimCritiqueTimeout, and guidance p90 was ~12s in the offline replay (#39803).
const claimGuidanceTimeout = 20 * time.Second
const claimGuidanceLimit = 2000

const claimGuidanceHeader = "Review of your root-cause analysis. For each weak point: why it is weak, the competing explanation, and the one check that tells them apart. Run the most decisive of these checks (one permitted discriminating read), or revise the answer to what the evidence supports; do not repeat denied evidence paths. " +
	"A check that fails or is denied is not evidence for the alternative: keep the original claim and label it unverified."

// On the last refinement the next answer is accepted without review, so an
// alternative adopted then is never checked (#39803 live data: 3/32 refines).
const claimGuidanceFinalRound = "This is the last review: your next answer will not be reviewed. Change the root cause only if a new observation proves the alternative."

// claimGuidanceCapture records step 2 on the audit row so its effect can be
// measured. Status is "ok", or the stage that fell back to legacy feedback.
type claimGuidanceCapture struct {
	Status         string  `json:"status"`
	FinalRound     bool    `json:"final_round"`
	Mode           string  `json:"mode,omitempty"`
	LatencySeconds float64 `json:"latency_seconds"`
	ErrorDetail    string  `json:"error_detail,omitempty"`
	RawResponse    string  `json:"raw_response,omitempty"`
}

type claimGuidanceItem struct {
	Index               int    `json:"index"`
	WhyWeak             string `json:"why_weak"`
	Alternative         string `json:"alternative"`
	DiscriminatingCheck string `json:"discriminating_check"`
}

// claimGuidanceContinuation carries a continuation audit's request so guidance
// continues it: by then the planner's conversation, the audit instruction and
// the audit's reply are a prefix the provider has just cached.
type claimGuidanceContinuation struct {
	conv             *critiqueConversation
	auditInstruction string
	auditResponse    string
}

// claimGuidanceFeedback asks a second call to explain the claims the audit
// already rejected. It never changes the verdict: on any failure it returns an
// error and the caller keeps the audit's legacy feedback.
func claimGuidanceFeedback(ctx *security.RequestContext, request NBAgentRequest, detail claimCaptureDetail, audit claimAudit, finalRound bool, cont *claimGuidanceContinuation) (string, *claimGuidanceCapture) {
	started := time.Now()
	capture := &claimGuidanceCapture{FinalRound: finalRound}
	finish := func(status string, err error) (string, *claimGuidanceCapture) {
		capture.Status, capture.LatencySeconds = status, time.Since(started).Seconds()
		if err != nil {
			capture.ErrorDetail = TruncateHead(err.Error(), 2048)
		}
		return "", capture
	}

	var flagged []critiqueClaim
	for _, c := range audit.Claims {
		if isClaimGap(c) {
			flagged = append(flagged, c)
		}
	}
	if len(flagged) == 0 {
		// Rejected only for missing_required items: there is no claim to explain.
		return finish("no_flagged_claims", nil)
	}

	type flaggedClaim struct {
		Index   int    `json:"index"`
		Support string `json:"support"`
		Claim   string `json:"claim"`
	}
	items := make([]flaggedClaim, len(flagged))
	for i, c := range flagged {
		items[i] = flaggedClaim{Index: i, Support: c.Support, Claim: c.Claim}
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return finish("prepare_error", fmt.Errorf("encode flagged claims: %w", err))
	}

	prompt, err := nbprompts.GetPromptStrict(ctx.GetContext(), nbprompts.PromptReactClaimCritiqueGuidance, request.AccountId)
	if err != nil {
		return finish("prompt_error", fmt.Errorf("load claim guidance prompt: %w", err))
	}

	var response *llms.ContentResponse
	if cont != nil && cont.conv != nil {
		// Continue the audit's own request: same conversation, the audit
		// instruction and its reply, then the guidance instruction.
		capture.Mode = claimModeContinuation
		bounded, cancel := context.WithTimeout(cont.conv.ctx.GetContext(), claimGuidanceTimeout)
		defer cancel()
		response, err = cont.conv.generate(cont.conv.derive(bounded), request,
			llms.TextParts(llms.ChatMessageTypeHuman, cont.auditInstruction),
			llms.TextParts(llms.ChatMessageTypeAI, cont.auditResponse),
			llms.TextParts(llms.ChatMessageTypeHuman, prompt+"\n\nflagged_claims: "+string(encoded)))
		if errors.Is(err, errCritiqueToolCall) {
			return finish("tool_call", err)
		}
		if err != nil {
			return finish("generation_error", fmt.Errorf("generate claim guidance: %w", err))
		}
		if response != nil && len(response.Choices) == 1 && response.Choices[0] != nil {
			response.Choices[0].Content = stripJSONFence(response.Choices[0].Content)
		}
	} else {
		var packet map[string]json.RawMessage
		if err := json.Unmarshal(detail.InputPacket, &packet); err != nil {
			return finish("prepare_error", fmt.Errorf("decode claim critique packet: %w", err))
		}
		if packet == nil {
			// A JSON null packet carries no evidence to explain the claims against.
			return finish("prepare_error", fmt.Errorf("claim critique packet is empty"))
		}
		packet["flagged_claims"] = encoded
		input, err := json.Marshal(packet)
		if err != nil {
			return finish("prepare_error", fmt.Errorf("encode guidance packet: %w", err))
		}

		bounded, cancel := context.WithTimeout(ctx.GetContext(), claimGuidanceTimeout)
		defer cancel()
		bounded = context.WithValue(bounded, ContextKeyModelTier, ModelTierReasoning)
		// Like the audit, send the evidence exactly as captured: a pre-flight trim
		// would let guidance cite observations the model never saw. If the input
		// cannot be sent unaltered, the call fails and legacy feedback stays.
		bounded = context.WithValue(bounded, contextKeyPreserveInput, true)
		guidanceCtx := security.NewRequestContext(bounded, ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
		messages := []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, prompt),
			llms.TextParts(llms.ChatMessageTypeHuman, string(input)),
		}
		response, err = GenerateAndTrackLLMContent(guidanceCtx, request.UserId, request.AccountId, request.ConversationId,
			request.MessageId, request.AgentId, false, messages, false, llms.WithTemperature(0), llms.WithJSONMode())
		if err != nil {
			return finish("generation_error", fmt.Errorf("generate claim guidance: %w", err))
		}
	}
	capture.RawResponse, _ = captureClaimResponse(response)
	if response == nil || len(response.Choices) != 1 || response.Choices[0] == nil {
		return finish("empty_response", nil)
	}
	switch strings.ToLower(response.Choices[0].StopReason) {
	case "stop", "end_turn", "finishreasonstop":
	default:
		return finish("incomplete_response", nil)
	}
	var parsed struct {
		Guidance []claimGuidanceItem `json:"guidance"`
	}
	if err := json.Unmarshal([]byte(response.Choices[0].Content), &parsed); err != nil {
		return finish("invalid_output", fmt.Errorf("decode claim guidance: %w", err))
	}
	byIndex := make(map[int]claimGuidanceItem, len(parsed.Guidance))
	for _, g := range parsed.Guidance {
		if g.Index >= 0 && g.Index < len(flagged) {
			byIndex[g.Index] = g
		}
	}
	if len(byIndex) == 0 {
		return finish("invalid_output", fmt.Errorf("claim guidance covers no flagged claim"))
	}

	lines := make([]string, 0, len(flagged)+len(audit.MissingRequired))
	for i, c := range flagged {
		line := c.Support + ": " + c.Claim
		g := byIndex[i]
		for _, part := range []struct{ label, text string }{
			{"why", g.WhyWeak}, {"alternative", g.Alternative}, {"check", g.DiscriminatingCheck},
		} {
			if text := strings.TrimSpace(part.text); text != "" {
				line += "\n  " + part.label + ": " + TruncateHead(text, claimGuidanceLimit)
			}
		}
		lines = append(lines, line)
	}
	lines = append(lines, audit.MissingRequired...)
	header := claimGuidanceHeader
	if finalRound {
		header += " " + claimGuidanceFinalRound
	}
	capture.Status, capture.LatencySeconds = "ok", time.Since(started).Seconds()
	return header + "\n- " + strings.Join(lines, "\n- "), capture
}
