package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	nbprompts "nudgebee/llm/prompts"
	"nudgebee/llm/security"

	"github.com/tmc/langchaingo/llms"
)

// critiqueConversation lets the post-answer critiques continue the ReAct4
// planner's own request instead of sending a standalone prompt. The planner's
// messages, tools and options are reused unchanged, so the provider's prompt
// cache covers them, and the critiques see earlier refinement rounds. Every
// caller falls back to its standalone path when a continuation call fails.
type critiqueConversation struct {
	ctx  *security.RequestContext
	base []llms.MessageContent // the planner request as sent, then its final answer
	opts []llms.CallOption
	// adapter tells the model it now reviews rather than investigates.
	adapter string
	// index lists the evidence ids (E<n>) the critiques cite, one line per step.
	index string
	// maxEvidence bounds evidence_refs: an E-number above it does not exist.
	maxEvidence int
}

var errCritiqueToolCall = errors.New("critique continuation: the model called a tool")

const critiqueIndexInputLimit = 160

// newCritiqueConversation returns nil when the continuation cannot judge the same
// evidence the standalone critiques would: with compression active the planner's
// messages hold summaries of older results instead of the results themselves.
func newCritiqueConversation(ctx *security.RequestContext, accountID string, messages []llms.MessageContent, answer string,
	opts []llms.CallOption, steps []NBAgentPlannerToolActionStep, compressed bool) *critiqueConversation {
	if compressed || ctx == nil || len(messages) == 0 || strings.TrimSpace(answer) == "" {
		return nil
	}
	adapter, err := nbprompts.GetPromptStrict(ctx.GetContext(), nbprompts.PromptReactCritiqueContinuation, accountID)
	if err != nil {
		ctx.GetLogger().Warn("critique continuation: adapter prompt failed to load, using standalone critiques", "error", err)
		return nil
	}
	base := make([]llms.MessageContent, 0, len(messages)+1)
	base = append(base, messages...)
	base = append(base, llms.TextParts(llms.ChatMessageTypeAI, answer))
	index, maxEvidence := critiqueEvidenceIndex(steps)
	return &critiqueConversation{
		ctx: ctx, base: base, opts: append([]llms.CallOption(nil), opts...),
		adapter: adapter, index: index, maxEvidence: maxEvidence,
	}
}

// critiqueEvidenceIndex maps each step's evidence id to its tool call, so the
// critiques can cite tool results that carry no visible id in the conversation.
func critiqueEvidenceIndex(steps []NBAgentPlannerToolActionStep) (string, int) {
	var b strings.Builder
	maxEvidence := 0
	for i, step := range steps {
		if isNotebookToolName(step.Action.Tool) {
			continue
		}
		n := i + 1
		var parsed int
		if _, err := fmt.Sscanf(step.Action.DisplayID, "E%d", &parsed); err == nil && parsed > 0 {
			n = parsed
		}
		if n > maxEvidence {
			maxEvidence = n
		}
		fmt.Fprintf(&b, "E%d = %s(%s)", n, step.Action.Tool,
			strings.ReplaceAll(TruncateHead(step.Action.ToolInput, critiqueIndexInputLimit), "\n", " "))
		if isRequestEcho(step.Action.Tool, step.Action.ToolInput) {
			b.WriteString("  [request_echo]")
		}
		b.WriteString("\n")
	}
	return b.String(), maxEvidence
}

// isRequestEcho reports whether a step only read the alert's own record or labels
// back. Such a result can show what the alert reported, not how the affected
// system behaved (#40236). Collector-gathered evidence (the event's evidence file,
// its captured logs) is a real observation of the system and is not tagged.
// Tool and file names mirror tools.ToolGetEventById, get_event_evidence's
// alert_labels type and api's event_labels_overflow_<id>.txt.
func isRequestEcho(tool, input string) bool {
	switch {
	case tool == "get_event_by_id", strings.Contains(input, "event_labels_overflow_"):
		return true
	default:
		return tool == "get_event_evidence" && strings.Contains(input, "alert_labels")
	}
}

// instruction wraps a critique's own prompt with the reviewer adapter and the
// evidence index.
func (c *critiqueConversation) instruction(prompt string) string {
	return c.adapter + "\n\n" + prompt + "\n\nEvidence index:\n" + c.index
}

// generate sends the planner's request plus tail on ctx (which must derive from
// c.ctx so the planner's model and cache scope apply). Input is preserved: a
// request that would have to be trimmed fails instead, and the caller falls back.
func (c *critiqueConversation) generate(ctx *security.RequestContext, request NBAgentRequest, tail ...llms.MessageContent) (*llms.ContentResponse, error) {
	messages := make([]llms.MessageContent, 0, len(c.base)+len(tail))
	messages = append(messages, c.base...)
	messages = append(messages, tail...)
	ctx = security.NewRequestContext(context.WithValue(ctx.GetContext(), contextKeyPreserveInput, true),
		ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
	response, err := GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId,
		request.MessageId, request.AgentId, false, messages, false, c.opts...)
	if err != nil {
		return nil, fmt.Errorf("critique continuation: generate: %w", err)
	}
	if response != nil {
		for _, choice := range response.Choices {
			if choice != nil && len(choice.ToolCalls) > 0 {
				return response, errCritiqueToolCall
			}
		}
	}
	return response, nil
}

// derive wraps ctx (derived from c.ctx's context) with c.ctx's security
// context, logger and tracing.
func (c *critiqueConversation) derive(ctx context.Context) *security.RequestContext {
	return security.NewRequestContext(ctx, c.ctx.GetSecurityContext(), c.ctx.GetLogger(), c.ctx.GetTracer(), c.ctx.GetMeter())
}

// stripJSONFence removes a Markdown code fence around a JSON reply. Continuation
// calls cannot use JSON mode: providers reject it alongside declared tools.
func stripJSONFence(content string) string {
	s := strings.TrimSpace(content)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimPrefix(s, "json")
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}
