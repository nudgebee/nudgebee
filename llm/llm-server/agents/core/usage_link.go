package core

import (
	"context"

	"nudgebee/llm/security"
)

// UsageLink names the agent run an LLM call was made for, when the caller
// cannot say so through GenerateAndTrackLLMContent's own arguments.
//
// The workspace bridge is that caller. A conversation id passed as an argument
// also switches on per-conversation prompt caching and conversation model pins,
// and a model tier on the context selects the model, so the bridge generates
// with neither and carries the attribution here: it reaches the usage row and
// nothing else.
type UsageLink struct {
	ConversationID string
	MessageID      string
	AgentID        string
	ModelTier      string
	TaskType       string
}

const contextKeyUsageLink LLMContextKey = "usage_link"

// WithUsageLink returns a copy of ctx whose LLM calls record their usage
// against link.
func WithUsageLink(ctx *security.RequestContext, link UsageLink) *security.RequestContext {
	return ctx.WithContext(context.WithValue(ctx.GetContext(), contextKeyUsageLink, link))
}

// applyUsageLink fills the attribution the caller left empty on record from the
// context's UsageLink. What the caller passed always wins, and the run is
// applied whole or not at all, so a link can add to a call's attribution but
// never contradict it.
func applyUsageLink(ctx *security.RequestContext, record *TokenUsageRecord) {
	if record == nil {
		return
	}
	link, ok := ctx.GetContext().Value(contextKeyUsageLink).(UsageLink)
	if !ok {
		return
	}
	if record.ConversationID == "" && record.MessageID == "" && record.AgentID == nil {
		record.ConversationID = link.ConversationID
		record.MessageID = link.MessageID
		if link.AgentID != "" {
			agentID := link.AgentID
			record.AgentID = &agentID
		}
	}
	if record.ModelTier == nil && link.ModelTier != "" {
		tier := link.ModelTier
		record.ModelTier = &tier
	}
	if record.TaskType == nil && link.TaskType != "" {
		taskType := link.TaskType
		record.TaskType = &taskType
	}
}
