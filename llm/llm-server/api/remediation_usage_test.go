package api

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	agentcore "nudgebee/llm/agents/core"
	"nudgebee/llm/security"
)

func TestRemediationConversationID(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"account-1"})
	conversationID := uuid.New()
	origFingerprint, origConversation := remediationEventFingerprint, remediationConversationBySession
	t.Cleanup(func() {
		remediationEventFingerprint, remediationConversationBySession = origFingerprint, origConversation
	})

	var gotSession, gotAccount string
	remediationConversationBySession = func(accountId, sessionId string) (agentcore.Conversation, error) {
		gotAccount, gotSession = accountId, sessionId
		if sessionId == "event-fp-1" || sessionId == "event-event-no-fp" {
			return agentcore.Conversation{ID: conversationID}, nil
		}
		return agentcore.Conversation{}, nil // no investigation for this event
	}
	setEvent := func(fingerprint string, found bool, err error) {
		remediationEventFingerprint = func(*security.RequestContext, string, string) (string, bool, error) {
			return fingerprint, found, err
		}
	}

	// The investigation session is keyed on the event fingerprint.
	setEvent("fp-1", true, nil)
	assert.Equal(t, conversationID.String(), remediationConversationID(ctx, "account-1", "event-a"))
	assert.Equal(t, "event-fp-1", gotSession)
	assert.Equal(t, "account-1", gotAccount, "lookup must be scoped to the request's account")

	// No fingerprint: the analyzer falls back to the event id, and so must we.
	setEvent("", true, nil)
	assert.Equal(t, conversationID.String(), remediationConversationID(ctx, "account-1", "event-no-fp"))
	assert.Equal(t, "event-event-no-fp", gotSession)

	// Event never investigated, event missing, lookup failures, no event id:
	// the call is recorded unattributed, as before.
	setEvent("fp-uninvestigated", true, nil)
	assert.Empty(t, remediationConversationID(ctx, "account-1", "event-b"))
	setEvent("", false, nil)
	assert.Empty(t, remediationConversationID(ctx, "account-1", "event-c"))
	setEvent("", false, errors.New("db down"))
	assert.Empty(t, remediationConversationID(ctx, "account-1", "event-d"))
	setEvent("fp-1", true, nil)
	remediationConversationBySession = func(string, string) (agentcore.Conversation, error) {
		return agentcore.Conversation{}, errors.New("db down")
	}
	assert.Empty(t, remediationConversationID(ctx, "account-1", "event-e"))
	assert.Empty(t, remediationConversationID(ctx, "account-1", ""))
}
