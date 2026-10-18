package core

import (
	"errors"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/config"

	"github.com/stretchr/testify/require"
)

// cardDao records tool-call writes and has no stored conversations, so the
// per-conversation model lookup falls back to defaults instead of hitting a DB.
type cardDao struct{ *trackingFakeDao }

func (cardDao) GetConversation(string) (Conversation, error) {
	return Conversation{}, errors.New("not found")
}

// A ReAct4 answer the critique sends back leaves one "Critique Feedback" step
// in the conversation, as ReAct3 always did (#41546); an accepted answer
// leaves none.
func TestReAct4RefineWritesCritiqueCard(t *testing.T) {
	previous := config.Config.ClaimCritiqueFeedbackEnabled
	config.Config.ClaimCritiqueFeedbackEnabled = false // legacy decision only, no claim audit call
	t.Cleanup(func() { config.Config.ClaimCritiqueFeedbackEnabled = previous })

	for _, tc := range []struct {
		name  string
		turns []fakeTurn
		cards []string
	}{
		{"refined once", []fakeTurn{
			{content: "candidate one"},
			{content: "<decision>refine</decision><feedback>check the pod restarts</feedback>"},
			{content: "candidate two"},
			{content: "<decision>accept</decision><feedback></feedback>"},
		}, []string{"check the pod restarts"}},
		{"accepted", []fakeTurn{
			{content: "candidate one"},
			{content: "<decision>accept</decision><feedback></feedback>"},
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prev := GetConversationDao()
			dao := &trackingFakeDao{IConversationDao: prev}
			SetConversationDao(cardDao{dao})
			t.Cleanup(func() { SetConversationDao(prev) })
			// Route background token-usage writes through a pool that cleanup
			// drains before the DAO above is restored (cleanups run LIFO).
			prevPool := GetMetricsWorkerPool()
			pool := common.NewWorkerPool("critique-card-test-metrics", 1, 16)
			SetMetricsWorkerPool(pool)
			t.Cleanup(func() { pool.Stop(); SetMetricsWorkerPool(prevPool) })
			withFakeLLMModel(t, &fakeLLMModel{turns: tc.turns})

			ctx := llmOverrideContext()
			p := &NBReActPlanner4{ctx: ctx, nbAgent: notebookOptOutAgent{}, maxRefinementAttempts: 2, enableCritique: true,
				request: NBAgentRequest{Query: "why is CPU high", AgentId: "agent-1", ConversationId: "conv-1", MessageId: "msg-1"}}
			_, finish, err := p.Plan(ctx.GetContext(), []NBAgentPlannerToolActionStep{{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: "CPU high"}}, "why is CPU high")
			require.NoError(t, err)
			require.NotNil(t, finish)

			var cards []string
			for _, c := range dao.calls {
				if c.toolName == "react_critique" {
					require.Equal(t, "conv-1", c.conversationId)
					require.Equal(t, "agent-1", c.agentId)
					cards = append(cards, c.result)
				}
			}
			require.Equal(t, tc.cards, cards)
		})
	}
}

// Blank feedback writes no step, whichever planner calls it.
func TestSaveCritiqueCardSkipsBlankFeedback(t *testing.T) {
	prev := GetConversationDao()
	dao := &trackingFakeDao{IConversationDao: prev}
	SetConversationDao(dao)
	t.Cleanup(func() { SetConversationDao(prev) })
	req := NBAgentRequest{AgentId: "agent-1", ConversationId: "conv-1"}
	require.NoError(t, saveCritiqueCard(req, "  \n\t "))
	require.Empty(t, dao.calls)
	require.NoError(t, saveCritiqueCard(req, "check the pod restarts"))
	require.Len(t, dao.calls, 1)
}
