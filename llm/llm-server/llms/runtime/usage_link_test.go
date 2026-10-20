package runtime

import (
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyUsageLink(t *testing.T) {
	link := UsageLink{ConversationID: "conv-link", MessageID: "msg-link", AgentID: "agent-link", ModelTier: "retrieval", TaskType: "reflection"}
	strPtr := func(s string) *string { return &s }

	tests := []struct {
		name   string
		ctx    *security.RequestContext
		record TokenUsageRecord
		want   TokenUsageRecord
	}{
		{
			name:   "no link leaves the record alone",
			ctx:    llmOverrideContext(),
			record: TokenUsageRecord{},
			want:   TokenUsageRecord{},
		},
		{
			name:   "link fills what the caller left empty",
			ctx:    WithUsageLink(llmOverrideContext(), link),
			record: TokenUsageRecord{},
			want: TokenUsageRecord{ConversationID: "conv-link", MessageID: "msg-link", AgentID: strPtr("agent-link"),
				ModelTier: strPtr("retrieval"), TaskType: strPtr("reflection")},
		},
		{
			// A caller that named any part of the run keeps all of it: mixing its
			// conversation with the link's message would describe no real run.
			name:   "caller's run is never mixed with the link's",
			ctx:    WithUsageLink(llmOverrideContext(), link),
			record: TokenUsageRecord{ConversationID: "conv-caller"},
			want: TokenUsageRecord{ConversationID: "conv-caller",
				ModelTier: strPtr("retrieval"), TaskType: strPtr("reflection")},
		},
		{
			name:   "caller's tier and task type win",
			ctx:    WithUsageLink(llmOverrideContext(), link),
			record: TokenUsageRecord{ModelTier: strPtr("reasoning"), TaskType: strPtr("investigation")},
			want: TokenUsageRecord{ConversationID: "conv-link", MessageID: "msg-link", AgentID: strPtr("agent-link"),
				ModelTier: strPtr("reasoning"), TaskType: strPtr("investigation")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := tt.record
			applyUsageLink(tt.ctx, &record)
			assert.Equal(t, tt.want, record)
		})
	}
}

// The link must label the usage row without becoming a model tier on the
// context: that key selects the model, and a workspace's label must not.
func TestWithUsageLinkDoesNotSelectTheModel(t *testing.T) {
	ctx := WithUsageLink(llmOverrideContext(), UsageLink{ModelTier: "retrieval", TaskType: "reflection"})
	assert.Empty(t, ModelTierFromContext(ctx))
	assert.Empty(t, TaskTypeFromContext(ctx))
}

func TestWithUsageLinkOnZeroValueContext(t *testing.T) {
	ctx := WithUsageLink(&security.RequestContext{}, UsageLink{ConversationID: "conv"})
	link, ok := ctx.GetContext().Value(contextKeyUsageLink).(UsageLink)
	require.True(t, ok)
	assert.Equal(t, "conv", link.ConversationID)
	assert.NotPanics(t, func() { applyUsageLink(&security.RequestContext{}, &TokenUsageRecord{}) })
	assert.NotPanics(t, func() { applyUsageLink(nil, &TokenUsageRecord{}) })
	assert.NotPanics(t, func() { applyUsageLink(ctx, nil) })
}
