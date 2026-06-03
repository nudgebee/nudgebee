package core

import (
	"sync"
	"testing"

	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShouldSkipResumeForTerminalConversation locks the recovery / resume
// guard predicate. The matrix exists because the previous implementation in
// HandleConversationMessageRequest (pre-fix) flipped IN_PROGRESS conversations
// to FAILED unconditionally, terminating healthy dead-worker recoveries and
// client-tool resumes. The helper here is the single source of truth — both
// resume paths use it, so the table covers every product-relevant
// (conv_status, msg_status, msg_type) combination.
func TestShouldSkipResumeForTerminalConversation(t *testing.T) {
	tests := []struct {
		name       string
		convStatus ConversationStatus
		msgStatus  ConversationStatus
		msgType    MessageType
		want       bool
	}{
		// Active conversations are always resumable, regardless of message state.
		{"InProgress conv, InProgress msg, generation", ConversationStatusInProgress, ConversationStatusInProgress, MessageTypeGeneration, false},
		{"InProgress conv, Waiting msg", ConversationStatusInProgress, ConversationStatusWaiting, MessageTypeGeneration, false},
		{"Pending conv", ConversationStatusPending, ConversationStatusInProgress, MessageTypeGeneration, false},
		{"Waiting conv", ConversationStatusWaiting, ConversationStatusInProgress, MessageTypeGeneration, false},

		// Terminal conversations: skip unless message is in a legitimately resumable state.
		{"Completed conv, InProgress msg, generation", ConversationStatusCompleted, ConversationStatusInProgress, MessageTypeGeneration, true},
		{"Failed conv, InProgress msg, generation", ConversationStatusFailed, ConversationStatusInProgress, MessageTypeGeneration, true},
		{"Killed conv, InProgress msg, generation", ConversationStatusKilled, ConversationStatusInProgress, MessageTypeGeneration, true},
		{"Terminated conv, InProgress msg, generation", ConversationStatusTerminated, ConversationStatusInProgress, MessageTypeGeneration, true},

		// Terminal conversations + WAITING/WAITING_FOR_CLIENT_TOOL message → still resumable.
		// User may complete a tool result after the conversation timed out; that completion
		// must not be silently dropped.
		{"Completed conv, Waiting msg", ConversationStatusCompleted, ConversationStatusWaiting, MessageTypeGeneration, false},
		{"Failed conv, WaitingForClientTool msg", ConversationStatusFailed, ConversationStatusWaitingForClientTool, MessageTypeGeneration, false},

		// Followup messages are always resumable — they represent a UX commitment
		// to deliver an answer to a question the agent asked the user.
		{"Completed conv, followup msg", ConversationStatusCompleted, ConversationStatusInProgress, MessageTypeFollowup, false},
		{"Killed conv, followup msg", ConversationStatusKilled, ConversationStatusCompleted, MessageTypeFollowup, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldSkipResumeForTerminalConversation(tc.convStatus, tc.msgStatus, tc.msgType)
			assert.Equal(t, tc.want, got)
		})
	}
}

// recordingDao is a fake IConversationDao that satisfies the interface via
// embedding (so unimplemented methods panic if hit) and records the calls
// HandleConversationMessageRequest is expected to make on the
// IN_PROGRESS-conversation path.
type recordingDao struct {
	IConversationDao

	mu                       sync.Mutex
	conv                     Conversation
	msg                      ConversationMessage
	statusUpdates            []ConversationStatus
	messageUpdateStatusCalls []ConversationStatus
}

func (r *recordingDao) GetConversationMessage(id, accountId, conversationId string) (ConversationMessage, error) {
	return r.msg, nil
}

func (r *recordingDao) GetConversation(conversationId string) (Conversation, error) {
	return r.conv, nil
}

func (r *recordingDao) UpdateConversationStatus(conversationId string, status ConversationStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statusUpdates = append(r.statusUpdates, status)
	r.conv.Status = status
	return nil
}

func (r *recordingDao) UpdateConversationMessage(id, response string, status ConversationStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messageUpdateStatusCalls = append(r.messageUpdateStatusCalls, status)
	return nil
}

func (r *recordingDao) ListConversationAgents(messageId, agentId string) ([]ConversationAgent, error) {
	return nil, nil
}

// Stubs hit only by edge paths the test cases below don't reach; left as
// no-ops so the embedded-interface panic doesn't fire for any incidental call.
func (r *recordingDao) GetAgentNameFromAgentId(agentId string) (string, error) {
	return "", nil
}

func (r *recordingDao) UpdateConversationMessageAsync(id, response string, status ConversationStatus) {
}

func (r *recordingDao) UpdateConversationMessageConfig(id string, config toolcore.NBQueryConfig) error {
	return nil
}

// TestHandleConversationMessageRequest_InProgressConversationNotFlippedToFailed
// is the core regression test: when dead-worker recovery (or a client-tool
// resume) calls HandleConversationMessageRequest with an IN_PROGRESS
// conversation, the function must NOT flip the conversation to FAILED before
// doing recovery work. Pre-fix, lines 1148-1154 unconditionally flipped it,
// which terminated healthy recoveries (see the failed run analysis on
// conversation f537b8bb-84aa-49bf-81fa-f98cc8d140d5).
//
// The test installs a fake DAO that records every UpdateConversationStatus
// call. The function will continue past our fix and enter the agent-not-found
// path (no agents registered for "" agent name) which legitimately writes a
// single UpdateConversationStatus(FAILED). Asserting exactly one such call
// proves no premature flip happened upstream.
func TestHandleConversationMessageRequest_InProgressConversationNotFlippedToFailed(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)

	convID := uuid.New()
	msgID := uuid.New()
	tenantID := uuid.New()
	accountID := uuid.New()
	userID := uuid.New()

	dao := &recordingDao{
		conv: Conversation{
			ID:        convID,
			Status:    ConversationStatusInProgress,
			TenantID:  tenantID,
			AccountID: accountID,
			UserID:    userID,
		},
		msg: ConversationMessage{
			ID:             msgID,
			ConversationID: convID,
			AccountID:      accountID,
			UserID:         userID,
			Status:         ConversationStatusInProgress,
			MessageType:    string(MessageTypeGeneration),
		},
	}
	SetConversationDao(dao)

	// Function returns through the agent-not-found path (no agents registered);
	// we don't care about the response here, only the side-effect record.
	_, _ = HandleConversationMessageRequest(accountID.String(), convID.String(), msgID.String())

	failedFlips := 0
	for _, s := range dao.statusUpdates {
		if s == ConversationStatusFailed {
			failedFlips++
		}
	}
	// Pre-fix: 2 (premature + agent-not-found). Post-fix: 1 (agent-not-found only).
	require.Equal(t, 1, failedFlips,
		"conversation must not be flipped to FAILED before recovery work runs; got status updates: %v", dao.statusUpdates)
}

// TestHandleConversationMessageRequest_TerminalConversationReturnsEarly
// covers the symmetric guard: if the conversation is already terminal and the
// message is not in a wait state, recovery must return early without further
// processing (no agent dispatch, no state churn).
func TestHandleConversationMessageRequest_TerminalConversationReturnsEarly(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)

	convID := uuid.New()
	msgID := uuid.New()
	tenantID := uuid.New()
	accountID := uuid.New()
	userID := uuid.New()

	dao := &recordingDao{
		conv: Conversation{
			ID:        convID,
			Status:    ConversationStatusCompleted, // terminal
			TenantID:  tenantID,
			AccountID: accountID,
			UserID:    userID,
		},
		msg: ConversationMessage{
			ID:             msgID,
			ConversationID: convID,
			AccountID:      accountID,
			UserID:         userID,
			Status:         ConversationStatusInProgress,
			MessageType:    string(MessageTypeGeneration),
		},
	}
	SetConversationDao(dao)

	resp, err := HandleConversationMessageRequest(accountID.String(), convID.String(), msgID.String())
	require.NoError(t, err)
	assert.Equal(t, ConversationStatusCompleted, resp.Status, "early-return must echo conversation status")
	assert.Equal(t, convID.String(), resp.ConversationId)
	assert.Equal(t, msgID.String(), resp.MessageId)

	// No conversation status flips at all — the conversation is terminal and
	// we must not touch it.
	assert.Empty(t, dao.statusUpdates, "terminal conversations must not be re-flipped")

	// The orphan-cleanup path may sync the message status to the conversation
	// status (one UpdateConversationMessage call expected, with status=Completed).
	require.Len(t, dao.messageUpdateStatusCalls, 1, "expected one message-status sync to terminal conversation status")
	assert.Equal(t, ConversationStatusCompleted, dao.messageUpdateStatusCalls[0])
}
