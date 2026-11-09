package core

import (
	"context"
	"strings"
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
)

func TestFinalIterationsNote(t *testing.T) {
	_, ok := finalIterationsNote(context.Background())
	require.False(t, ok, "no budget set: sub-agents and other callers get nothing")

	_, ok = finalIterationsNote(context.WithValue(context.Background(), contextKeyIterationsLeft, finalIterationsWarning+1))
	require.False(t, ok)

	note, ok := finalIterationsNote(context.WithValue(context.Background(), contextKeyIterationsLeft, 2))
	require.True(t, ok)
	require.Contains(t, note, "You have 2 planning step(s) left")

	note, ok = finalIterationsNote(context.WithValue(context.Background(), contextKeyIterationsLeft, 1))
	require.True(t, ok)
	require.Contains(t, note, "You have 1 planning step(s) left")
}

func TestAppendNoteToLastMessage(t *testing.T) {
	t.Run("human message gets a text part", func(t *testing.T) {
		original := []llms.ContentPart{llms.TextContent{Text: "question"}}
		msgs := []llms.MessageContent{{Role: llms.ChatMessageTypeHuman, Parts: original}}
		got := appendNoteToLastMessage(msgs, "finish now")
		require.Len(t, got, 1)
		require.Len(t, got[0].Parts, 2)
		require.Equal(t, llms.TextContent{Text: "finish now"}, got[0].Parts[1])
		require.Len(t, original, 1, "the caller's parts slice is not modified")
		require.Len(t, msgs[0].Parts, 1, "the caller's message is not modified")
	})
	t.Run("tool result carries the note in its text", func(t *testing.T) {
		msgs := []llms.MessageContent{
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "question"}}},
			{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
				llms.ToolCallResponse{ToolCallID: "a", Name: "metrics", Content: "first"},
				llms.ToolCallResponse{ToolCallID: "b", Name: "logs", Content: "second"},
			}},
		}
		got := appendNoteToLastMessage(msgs, "finish now")
		require.Equal(t, "second", msgs[1].Parts[1].(llms.ToolCallResponse).Content, "the caller's messages are not modified")
		require.Len(t, got, 2, "no new message after tool results")
		require.Len(t, got[1].Parts, 2, "tool message keeps one part per response")
		require.Equal(t, "first", got[1].Parts[0].(llms.ToolCallResponse).Content)
		require.Equal(t, "second\n\nfinish now", got[1].Parts[1].(llms.ToolCallResponse).Content)
	})
	t.Run("assistant last gets a human message", func(t *testing.T) {
		msgs := []llms.MessageContent{{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{llms.TextContent{Text: "answer"}}}}
		got := appendNoteToLastMessage(msgs, "finish now")
		require.Len(t, got, 2)
		require.Equal(t, llms.ChatMessageTypeHuman, got[1].Role)
	})
}

func lastMessageText(msgs []llms.MessageContent) string {
	var b strings.Builder
	for _, part := range msgs[len(msgs)-1].Parts {
		switch p := part.(type) {
		case llms.TextContent:
			b.WriteString(p.Text)
		case llms.ToolCallResponse:
			b.WriteString(p.Content)
		}
	}
	return b.String()
}

// Both planners carry the note on the request that is sent once the executor
// says the budget is nearly spent, and nothing otherwise.
func TestPlannersSendFinalIterationsNote(t *testing.T) {
	steps := []NBAgentPlannerToolActionStep{{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: "CPU high"}}
	for _, left := range []int{10, 1} {
		fake := &fakeLLMModel{response: "<final_answer>done</final_answer>"}
		withFakeLLMModel(t, fake)
		ctx := llmOverrideContext()
		planCtx := context.WithValue(ctx.GetContext(), contextKeyIterationsLeft, left)

		p4 := &NBReActPlanner4{ctx: ctx, request: NBAgentRequest{Query: "list pods"}, nbAgent: notebookOptOutAgent{}}
		_, _, err := p4.Plan(planCtx, steps, "list pods")
		require.NoError(t, err)
		require.Equal(t, left == 1, strings.Contains(lastMessageText(fake.lastMessages), "planning step(s) left"), "react4, %d left", left)

		p3 := &NBReActPlanner3{ctx: ctx, request: NBAgentRequest{Query: "list pods"}, nbAgent: notebookOptOutAgent{}, prompt: prompts.NewPromptTemplate("{{.input}}", []string{"input"})}
		_, _, err = p3.Plan(planCtx, steps, "list pods")
		require.NoError(t, err)
		require.Equal(t, left == 1, strings.Contains(lastMessageText(fake.lastMessages), "planning step(s) left"), "react3, %d left", left)
	}
}

// An answer the fallback summary wrote for a top-level investigation is
// recorded as not critiqued; a sub-agent's is not recorded.
func TestRecordUncritiquedSummary(t *testing.T) {
	previous := config.Config.ClaimCritiqueFeedbackEnabled
	config.Config.ClaimCritiqueFeedbackEnabled = true
	t.Cleanup(func() { config.Config.ClaimCritiqueFeedbackEnabled = previous })
	query := "why is the payments pod not ready"
	require.True(t, IsInvestigationRequestTask(query))
	steps := []NBAgentPlannerToolActionStep{{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: "CPU high"}}

	for _, tc := range []struct {
		name    string
		request NBAgentRequest
		want    int
	}{
		{"top-level investigation", NBAgentRequest{AgentId: "a1", Query: query}, 1},
		{"sub-agent", NBAgentRequest{AgentId: "a2", ParentAgentId: "a1", Query: query}, 0},
		{"top-level plain query", NBAgentRequest{AgentId: "a1", Query: "list pods"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dao := &claimCaptureDAO{}
			installClaimCaptureDAO(t, dao)
			e := &plannerExecutor{ctx: llmOverrideContext(), agent: notebookOptOutAgent{}, agentPlanner: &NBReActPlanner4{},
				agentRequest: tc.request, steps: steps, maxIterations: 50, currentIteration: 49}
			e.recordUncritiquedSummary(query, map[string]any{"output": "summary answer"})
			require.Len(t, dao.rows, tc.want)
			if tc.want == 1 {
				require.Equal(t, claimStatusFallbackSummary, dao.rows[0].Status)
				require.Equal(t, "react4", dao.rows[0].Planner)
				require.Equal(t, "summary answer", dao.rows[0].Answer)
				require.Empty(t, dao.rows[0].Decision, "a fallback summary is not an audit verdict")
			}
		})
	}
}
