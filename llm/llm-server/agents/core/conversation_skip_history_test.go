package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

func TestResolveSessionRequestConfigs_WithoutHistorySurvivesWithConfig(t *testing.T) {
	labels := map[string]any{"subject": "k8s-collector-worker"}

	before := resolveSessionRequestConfigs(
		ConversationSessionRequestWithoutConversationHistory(),
		ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{Labels: labels}),
	)
	after := resolveSessionRequestConfigs(
		ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{Labels: labels}),
		ConversationSessionRequestWithoutConversationHistory(),
	)

	for name, cfg := range map[string]additionalConversationSessionRequestConfig{"before": before, "after": after} {
		assert.True(t, cfg.config.SkipConversationHistory, "option placed %s WithConfig was dropped", name)
		assert.Equal(t, labels, cfg.config.Labels, "option placed %s WithConfig", name)
	}
	assert.False(t, resolveSessionRequestConfigs().config.SkipConversationHistory)
}

// panicHistoryDao fails the test if history is read at all.
type panicHistoryDao struct {
	IConversationDao
	t *testing.T
}

func (d *panicHistoryDao) LoadConversationMessages(_, _, _ string, _ MessageType, _ int) ([]map[string]string, error) {
	d.t.Fatal("history loaded for a turn that asked to skip it")
	return nil, nil
}

func TestGetExistingHistory_SkipsLoadWhenRequested(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)
	SetConversationDao(&panicHistoryDao{t: t})

	history, err := getExistingHistory(NBAgentRequest{
		ConversationId: "conv",
		QueryConfig:    toolcore.NBQueryConfig{SkipConversationHistory: true},
	}, security.NewRequestContextForSuperAdmin())

	require.NoError(t, err)
	assert.Empty(t, history)
}

// storedConfigDao returns one stored message whose message_config is set.
type storedConfigDao struct {
	IConversationDao
	config string
}

func (d *storedConfigDao) GetConversationMessage(_, _, _ string) (ConversationMessage, error) {
	return ConversationMessage{MessageConfig: &d.config}, nil
}

func TestMergeLatestMessageConfig_ResumeKeepsSkipHistory(t *testing.T) {
	// An approval resume rebuilds the request with a non-empty config and
	// merges the stored message_config into it; the flag must come back.
	dao := &storedConfigDao{config: `{"skip_conversation_history":true,"event_id":"e1"}`}

	merged := mergeLatestMessageConfig(dao, "m", "a", "c",
		toolcore.NBQueryConfig{Namespace: "nudgebee"}, security.NewRequestContextForSuperAdmin().GetLogger(), "test")

	assert.True(t, merged.SkipConversationHistory)
	assert.Equal(t, "nudgebee", merged.Namespace)
}

// historyRowsDao returns fixed history rows, newest first, like the real query.
type historyRowsDao struct {
	IConversationDao
	rows []map[string]string
}

func (d *historyRowsDao) LoadConversationMessages(_, _, _ string, _ MessageType, _ int) ([]map[string]string, error) {
	out := make([]map[string]string, len(d.rows))
	for i, r := range d.rows {
		out[len(d.rows)-1-i] = r
	}
	return out, nil
}

// This turn's approval exchange must not be replayed as history: it changed
// the opening human message after every approval and lost its prompt cache
// (#40085). Earlier turns' approvals and this turn's other followups stay.
func TestGetExistingHistory_SkipsThisTurnsApproval(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)
	const approval = `{"followupType":"tool_confirmation","toolName":"kubectl"}`
	SetConversationDao(&historyRowsDao{rows: []map[string]string{ // oldest first
		{"id": "g0", "role": "human", "message_type": "generation", "content": "earlier question", "response": "earlier answer"},
		{"id": "f0", "role": "ai", "message_type": "followup", "content": "Approve restart in an earlier turn?", "response": "yes", "message_config": approval},
		{"id": "g1", "role": "human", "message_type": "generation", "content": "why is vmsingle restarting? fix it"},
		{"id": "f1", "role": "ai", "message_type": "followup", "content": "Approve rollout restart of vmsingle?", "response": "yes", "message_config": approval},
		{"id": "f2", "role": "ai", "message_type": "followup", "content": "Which namespace?", "response": "monitoring", "message_config": `{"followupType":"text"}`},
	}})

	history, err := getExistingHistory(NBAgentRequest{ConversationId: "conv", MessageId: "g1"}, security.NewRequestContextForSuperAdmin())
	require.NoError(t, err)

	var texts []string
	for _, f := range history {
		msgs, err := f.FormatMessages(map[string]any{})
		require.NoError(t, err)
		for _, m := range msgs {
			texts = append(texts, m.GetContent())
		}
	}
	assert.Equal(t, []string{
		"earlier question", "earlier answer",
		"Approve restart in an earlier turn?", "yes",
		"Which namespace?", "monitoring",
	}, texts)
}

func TestFollowupTypeOf(t *testing.T) {
	assert.Equal(t, FollowupTypeToolConfirmation, followupTypeOf(`{"followupType":"tool_confirmation"}`))
	assert.Equal(t, FollowupType(""), followupTypeOf(""))
	assert.Equal(t, FollowupType(""), followupTypeOf("not json"))
}
