package core

import (
	"testing"

	"nudgebee/llm/security"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsResumableAgentStatus locks in the contract for which agent statuses
// V2's idempotency guard treats as eligible for resume vs already-progressed.
//
// Both `waiting` (user-question followup) and `waiting_for_client_tool`
// (shell_execute / custom client tool resume) must qualify. The original
// implementation only accepted `waiting`, which silently swallowed every
// client-tool-result resume — the conversation stayed IN_PROGRESS until
// the client-side task timeout. See conversations on PR #29933 / #29746
// (terminal-bench harness) for the regression observation.
func TestIsResumableAgentStatus(t *testing.T) {
	cases := []struct {
		name   string
		status AgentExecutionStatus
		want   bool
	}{
		{"waiting → resumable (followup question)", AgentExecutionStatusWaiting, true},
		{"waiting_for_client_tool → resumable (client-tool resume)", AgentExecutionStatusWaitingForClientTool, true},
		{"WAITING uppercased → resumable (case-insensitive)", "WAITING", true},
		{"Waiting_For_Client_Tool mixed case → resumable", "Waiting_For_Client_Tool", true},
		{"success → not resumable (already advanced)", AgentExecutionStatusSuccess, false},
		{"fail → not resumable", AgentExecutionStatusFail, false},
		{"in_progress → not resumable (someone else holds the work)", AgentExecutionStatusInProgress, false},
		{"empty → not resumable", AgentExecutionStatus(""), false},
		{"unknown garbage → not resumable", AgentExecutionStatus("flapdoodle"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isResumableAgentStatus(tc.status))
		})
	}
}

// TestIsTrueSibling locks in the level-check the bound-sibling resume loop uses.
//
// Regression: a child agent's tool-confirmation followup gets bound up to its
// parent (both rows share the same followup_message_id). When the followup is
// answered against the CHILD, the loop must NOT treat the parent as a sibling —
// resuming the parent there discards its final answer and clears its state,
// leaving the conversation COMPLETED while the generation message stays
// IN_PROGRESS/empty (observed on a "create pod → decline" decline flow).
func TestIsTrueSibling(t *testing.T) {
	parentID := uuid.New()

	child1 := ConversationAgent{ID: uuid.New(), ParentAgentID: parentID}
	child2 := ConversationAgent{ID: uuid.New(), ParentAgentID: parentID}
	parent := ConversationAgent{ID: parentID, ParentAgentID: uuid.Nil} // the parent itself

	assert.True(t, isTrueSibling(child1, child2),
		"two agents under the same parent are true siblings")
	assert.False(t, isTrueSibling(child1, parent),
		"the parent (bound to a child's followup) must not be treated as a sibling")
	assert.False(t, isTrueSibling(child1, child1),
		"an agent is not its own sibling")

	// Top-level peers (both parentless) are genuine siblings of each other.
	top1 := ConversationAgent{ID: uuid.New(), ParentAgentID: uuid.Nil}
	top2 := ConversationAgent{ID: uuid.New(), ParentAgentID: uuid.Nil}
	assert.True(t, isTrueSibling(top1, top2),
		"two top-level agents share the nil parent and are siblings")
}

// bubbleFakeDao is a minimal IConversationDao that drives the no-saved-state
// branch of bubbleUpIfSiblingsDone. Only the methods that branch reaches are
// implemented; everything else falls through to the embedded nil interface and
// panics if hit (so an unexpected code path is loud, not silent).
type bubbleFakeDao struct {
	IConversationDao

	parentAgent  ConversationAgent
	parentState  string
	waitingCount int

	persistedMsgID     string
	persistedContent   string
	persistedMsgStatus ConversationStatus
	convStatus         ConversationStatus
}

func (f *bubbleFakeDao) CountWaitingSubAgents(parentAgentId, messageId string) (int, error) {
	return f.waitingCount, nil
}

func (f *bubbleFakeDao) GetConversationAgentParentAgentIdAndPreviousState(agentId string) (string, string) {
	return "", f.parentState
}

func (f *bubbleFakeDao) ListConversationAgents(messageId, agentId string) ([]ConversationAgent, error) {
	return []ConversationAgent{f.parentAgent}, nil
}

func (f *bubbleFakeDao) UpdateConversationMessage(id, response string, status ConversationStatus) error {
	f.persistedMsgID = id
	f.persistedContent = response
	f.persistedMsgStatus = status
	return nil
}

func (f *bubbleFakeDao) UpdateConversationStatus(conversationId string, status ConversationStatus) error {
	f.convStatus = status
	return nil
}

// TestBubbleUpIfSiblingsDone_StatelessParentFinalizesMessage covers the actual
// user-facing fix: when all siblings are done and the parent has no saved state
// (it completed out-of-band and its state was cleared), the parent's saved
// answer must be persisted to the generation message — otherwise the
// conversation goes COMPLETED while the message stays IN_PROGRESS/empty.
//
// Two sub-cases:
//   - parent row has a non-empty Response  ⇒ that response is persisted.
//   - parent row Response is nil           ⇒ fall back to the child's response.
//
// Both must leave the message and conversation COMPLETED, and preserve the
// non-Response fields of childResp (e.g. AgentName).
func TestBubbleUpIfSiblingsDone_StatelessParentFinalizesMessage(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)

	ctx := security.NewRequestContextForSuperAdmin()
	parentID := uuid.New()
	childID := uuid.New()
	msgID := uuid.New()
	convID := uuid.New()

	childResp := NBAgentResponse{
		Response:  []string{"user has rejected approval (response - no) for this action, stopping"},
		Status:    ConversationStatusCompleted,
		AgentName: "kubectl",
	}
	childAgent := ConversationAgent{ID: childID, ParentAgentID: parentID, MessageID: msgID}
	req := NBAgentRequest{
		ConversationId: convID.String(),
		MessageId:      msgID.String(),
		AccountId:      uuid.New().String(),
	}

	t.Run("parent has saved response → persisted to message", func(t *testing.T) {
		parentAnswer := "The request to scale ml-k8s-server has been cancelled per your instruction."
		fake := &bubbleFakeDao{
			parentState:  "", // stateless → enters the fix branch
			waitingCount: 0,  // all siblings done
			parentAgent:  ConversationAgent{ID: parentID, AgentName: "k8s_debug", Response: &parentAnswer},
		}
		SetConversationDao(fake)

		resp, err := bubbleUpIfSiblingsDone(ctx, req, childResp, childAgent)
		require.NoError(t, err)

		assert.Equal(t, []string{parentAnswer}, resp.Response, "parent's answer must surface in the response")
		assert.Equal(t, ConversationStatusCompleted, resp.Status)
		assert.Equal(t, "kubectl", resp.AgentName, "non-Response fields preserved from childResp")
		assert.Equal(t, msgID.String(), fake.persistedMsgID)
		assert.Equal(t, parentAnswer, fake.persistedContent, "message must be persisted with the parent answer, not left empty")
		assert.Equal(t, ConversationStatusCompleted, fake.persistedMsgStatus)
		assert.Equal(t, ConversationStatusCompleted, fake.convStatus)
	})

	t.Run("parent response nil → falls back to child response", func(t *testing.T) {
		fake := &bubbleFakeDao{
			parentState:  "",
			waitingCount: 0,
			parentAgent:  ConversationAgent{ID: parentID, AgentName: "k8s_debug", Response: nil},
		}
		SetConversationDao(fake)

		resp, err := bubbleUpIfSiblingsDone(ctx, req, childResp, childAgent)
		require.NoError(t, err)

		assert.Equal(t, childResp.Response, resp.Response, "falls back to child response when parent has none")
		assert.Equal(t, ConversationStatusCompleted, resp.Status, "status is Completed even on fallback")
		assert.Equal(t, childResp.Response[0], fake.persistedContent)
		assert.Equal(t, ConversationStatusCompleted, fake.persistedMsgStatus)
		assert.Equal(t, ConversationStatusCompleted, fake.convStatus)
	})
}

// TestBubbleUpIfSiblingsDone_TerminalChildShortCircuits covers the #31997 fix:
// when a resumed sub-agent completes with IsTerminal (its answer IS the final
// answer — e.g. automation_builder returning the built workflow JSON after
// "Approve and Build"), the bubble-up must finalize from the child and NOT resume
// the parent. Resuming the parent re-runs an ancestor planner which, for a nested
// builder (k8s_debug → automation → automation_builder), re-delegates a fresh
// build and re-prompts for approval in a loop.
//
// The fake deliberately sets a NON-EMPTY parentState: the pre-fix code would then
// proceed to resume the parent (GetAgentNameFromAgentId / GetNBAgent / executeAgent),
// none of which the fake implements — so it would panic via the embedded nil DAO.
// A clean COMPLETED return therefore PROVES the parent-resume path was not taken.
func TestBubbleUpIfSiblingsDone_TerminalChildShortCircuits(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)

	ctx := security.NewRequestContextForSuperAdmin()
	parentID := uuid.New()
	childID := uuid.New()
	msgID := uuid.New()
	convID := uuid.New()

	const builtJSON = `{"name":"k8s-pod-inventory","definition":{"triggers":[{"type":"manual"}]}}`
	terminalChild := NBAgentResponse{
		Response:   []string{builtJSON},
		Status:     ConversationStatusCompleted,
		IsTerminal: true,
		AgentName:  "automation_builder",
	}
	childAgent := ConversationAgent{ID: childID, ParentAgentID: parentID, MessageID: msgID}
	req := NBAgentRequest{
		ConversationId: convID.String(),
		MessageId:      msgID.String(),
		AccountId:      uuid.New().String(),
	}

	t.Run("terminal child finalizes without resuming the (stateful) parent", func(t *testing.T) {
		fake := &bubbleFakeDao{
			parentState:  "non-empty-state-blob-would-trigger-parent-resume",
			waitingCount: 0, // all siblings done
			parentAgent:  ConversationAgent{ID: parentID, AgentName: "k8s_debug"},
		}
		SetConversationDao(fake)

		// Would panic on the embedded nil DAO if the parent-resume path were taken.
		resp, err := bubbleUpIfSiblingsDone(ctx, req, terminalChild, childAgent)
		require.NoError(t, err)

		assert.Equal(t, []string{builtJSON}, resp.Response, "terminal child's response is the final answer")
		assert.Equal(t, ConversationStatusCompleted, resp.Status)
		assert.Equal(t, "automation_builder", resp.AgentName, "non-Response fields preserved from childResp")
		assert.Equal(t, msgID.String(), fake.persistedMsgID)
		assert.Equal(t, builtJSON, fake.persistedContent, "generation message persisted with the built workflow")
		assert.Equal(t, ConversationStatusCompleted, fake.persistedMsgStatus)
		assert.Equal(t, ConversationStatusCompleted, fake.convStatus)
	})

	t.Run("terminal child does NOT strand a still-waiting sibling", func(t *testing.T) {
		// Placement guard: the IsTerminal short-circuit sits AFTER the waitingCount>0
		// check, so a sibling still awaiting user input keeps the conversation WAITING
		// and is never finalized out from under the user.
		fake := &bubbleFakeDao{
			parentState:  "non-empty-state-blob",
			waitingCount: 1, // a sibling still needs the user
			parentAgent:  ConversationAgent{ID: parentID, AgentName: "k8s_debug"},
		}
		SetConversationDao(fake)

		resp, err := bubbleUpIfSiblingsDone(ctx, req, terminalChild, childAgent)
		require.NoError(t, err)

		assert.Equal(t, ConversationStatusWaiting, fake.convStatus, "conversation stays WAITING for the pending sibling")
		assert.Equal(t, []string{builtJSON}, resp.Response)
		assert.Empty(t, fake.persistedMsgID, "no final message persisted while a sibling is still waiting")
	})
}
