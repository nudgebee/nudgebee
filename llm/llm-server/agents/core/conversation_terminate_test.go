package core

import (
	"sync"
	"testing"

	"nudgebee/llm/security"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// TestShouldSkipSaveBack locks the per-message guard introduced for #30137.
// The original guard was conversation-scoped (`conv.Status != KILLED && conv.Status != TERMINATED`)
// and silently dropped every fresh turn on a previously-terminated
// conversation. The refactored helper scopes the TERMINATED check to the
// *message* so the in-flight callback for the just-stopped message still
// gets blocked, but a brand-new MessageId in the same conversation saves.
func TestShouldSkipSaveBack(t *testing.T) {
	tests := []struct {
		name       string
		convStatus ConversationStatus
		msgStatus  ConversationStatus
		wantSkip   bool
		wantReason string
	}{
		// KILLED conversations: system-initiated, always skip regardless of msg state.
		{"killed conv + in-progress msg", ConversationStatusKilled, ConversationStatusInProgress, true, "conversation_killed"},
		{"killed conv + terminated msg", ConversationStatusKilled, ConversationStatusTerminated, true, "conversation_killed"},
		{"killed conv + completed msg", ConversationStatusKilled, ConversationStatusCompleted, true, "conversation_killed"},

		// Q1 late-return after Stop: message row was flipped to TERMINATED by
		// TerminateConversation, so even if a concurrent Q2 has revived the
		// conversation row to IN_PROGRESS, Q1's late save must still be blocked.
		{"in-progress conv + terminated msg (Q1 late return after concurrent Q2 revive)", ConversationStatusInProgress, ConversationStatusTerminated, true, "message_terminated"},
		{"terminated conv + terminated msg (Q1 late return, no concurrent Q2)", ConversationStatusTerminated, ConversationStatusTerminated, true, "message_terminated"},
		{"completed conv + terminated msg (defensive — shouldn't occur in practice)", ConversationStatusCompleted, ConversationStatusTerminated, true, "message_terminated"},

		// Q2 fresh turn on a previously-terminated conversation: the new message
		// starts as IN_PROGRESS and must save, even though the conversation row
		// briefly carried TERMINATED before markConversationActive flipped it.
		{"terminated conv + in-progress msg (Q2 fresh turn after Stop)", ConversationStatusTerminated, ConversationStatusInProgress, false, ""},

		// Normal happy paths.
		{"in-progress conv + in-progress msg (normal turn)", ConversationStatusInProgress, ConversationStatusInProgress, false, ""},
		{"in-progress conv + completed msg", ConversationStatusInProgress, ConversationStatusCompleted, false, ""},
		{"completed conv + completed msg (idempotent re-save)", ConversationStatusCompleted, ConversationStatusCompleted, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotSkip, gotReason := shouldSkipSaveBack(tc.convStatus, tc.msgStatus)
			assert.Equal(t, tc.wantSkip, gotSkip)
			assert.Equal(t, tc.wantReason, gotReason)
		})
	}
}

// statusUpdateRecorder is a fake IConversationDao that records every
// UpdateConversationStatus call. The embedded interface ensures any
// unintended DAO call panics loudly so the tests can't drift into
// unrelated code paths.
type statusUpdateRecorder struct {
	IConversationDao

	mu      sync.Mutex
	updates []struct {
		conversationId string
		status         ConversationStatus
	}
}

func (r *statusUpdateRecorder) UpdateConversationStatus(conversationId string, status ConversationStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, struct {
		conversationId string
		status         ConversationStatus
	}{conversationId, status})
	return nil
}

// TestMarkConversationActive locks the truth table for the IN_PROGRESS flip
// helper. KILLED is unconditionally sticky. TERMINATED is sticky for resume /
// recovery callers (allowReviveTerminated=false) but not for the new-message
// branch (allowReviveTerminated=true) — a brand-new MessageId can't be the
// racing callback the original guard was protecting against.
func TestMarkConversationActive(t *testing.T) {
	tests := []struct {
		name                  string
		currentStatus         ConversationStatus
		allowReviveTerminated bool
		wantUpdate            bool
	}{
		// KILLED: always sticky (system-initiated; budget exhaustion / hard shutdown).
		{"killed + allowRevive=false", ConversationStatusKilled, false, false},
		{"killed + allowRevive=true", ConversationStatusKilled, true, false},

		// TERMINATED: sticky for resume / recovery, revivable for new-turn.
		{"terminated + allowRevive=false (resume/recovery)", ConversationStatusTerminated, false, false},
		{"terminated + allowRevive=true (new-turn)", ConversationStatusTerminated, true, true},

		// IN_PROGRESS: no-op (already active).
		{"in-progress + allowRevive=false", ConversationStatusInProgress, false, false},
		{"in-progress + allowRevive=true", ConversationStatusInProgress, true, false},

		// COMPLETED / FAILED / PENDING / WAITING: flip to IN_PROGRESS regardless of allowRevive.
		// This covers the pre-existing #29364 fix (new turn on a completed conversation
		// must not leave the conversation row stuck at COMPLETED).
		{"completed + allowRevive=false", ConversationStatusCompleted, false, true},
		{"completed + allowRevive=true", ConversationStatusCompleted, true, true},
		{"failed + allowRevive=false", ConversationStatusFailed, false, true},
		{"pending + allowRevive=false", ConversationStatusPending, false, true},
		{"waiting + allowRevive=false", ConversationStatusWaiting, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := GetConversationDao()
			defer SetConversationDao(original)

			rec := &statusUpdateRecorder{}
			SetConversationDao(rec)

			ctx := security.NewRequestContextForSuperAdmin()
			convID := uuid.New().String()
			markConversationActive(ctx, convID, tc.currentStatus, "unit-test", tc.allowReviveTerminated)

			if tc.wantUpdate {
				if assert.Len(t, rec.updates, 1, "expected exactly one UpdateConversationStatus call") {
					assert.Equal(t, convID, rec.updates[0].conversationId)
					assert.Equal(t, ConversationStatusInProgress, rec.updates[0].status, "flip target must be IN_PROGRESS")
				}
			} else {
				assert.Empty(t, rec.updates, "expected no UpdateConversationStatus calls")
			}
		})
	}
}
