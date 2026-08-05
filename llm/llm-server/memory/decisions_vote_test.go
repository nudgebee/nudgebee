package memory_test

import (
	"context"
	"testing"

	"nudgebee/llm/ee/memory"
	memdecisions "nudgebee/llm/ee/memory/stores/decisions"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The RCA thumbs up/down used to append a row per click, keyed on the user's
// question — so eight clicks on "what is 2+2?" left eight decisions all saying
// the user agreed about "what is 2+2?". Votes are now keyed on the message and
// carry the root cause as their subject, so a repeat click changes nothing and
// a flip supersedes rather than leaving both verdicts live.

func castVote(t *testing.T, m memory.Memory, tenantID, userID, messageID, decisionType, subject string) memory.MutateResponse {
	t.Helper()
	resp, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID, Layer: "decisions", Action: "vote",
		Value: map[string]any{
			"decision_type": decisionType,
			"subject":       subject,
			"message_id":    messageID,
			"rationale":     "user thumbs-up on RCA",
		},
		ActorKind: "user", ActorID: userID,
	})
	require.NoError(t, err)
	return resp
}

func TestVoteIsIdempotentPerMessage(t *testing.T) {
	tenantID, userID, m, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	const msgID = "vote-msg-idempotent"
	const subject = "payments-api OOMKilled: memory limit below steady-state usage"

	first := castVote(t, m, tenantID, userID, msgID, memdecisions.TypeRootCauseAgreed, subject)
	assert.False(t, first.Skipped, "the first vote is a real write")

	second := castVote(t, m, tenantID, userID, msgID, memdecisions.TypeRootCauseAgreed, subject)
	assert.True(t, second.Skipped, "clicking the same vote again must not append a second row")

	live, err := memdecisions.FindVoteForMessage(tenantID, userID, msgID)
	require.NoError(t, err)
	require.NotNil(t, live, "the vote is still recorded")
	assert.Equal(t, memdecisions.TypeRootCauseAgreed, live.DecisionType)
	assert.Equal(t, subject, live.Subject, "the subject is the finding, not the question")
}

func TestVoteFlipSupersedesRatherThanDuplicating(t *testing.T) {
	tenantID, userID, m, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	const msgID = "vote-msg-flip"
	const subject = "checkout latency: connection pool exhausted on orders-db"

	castVote(t, m, tenantID, userID, msgID, memdecisions.TypeRootCauseAgreed, subject)
	flip := castVote(t, m, tenantID, userID, msgID, memdecisions.TypeRootCauseDisagreed, subject)
	assert.False(t, flip.Skipped, "changing your mind is a real write")

	live, err := memdecisions.FindVoteForMessage(tenantID, userID, msgID)
	require.NoError(t, err)
	require.NotNil(t, live)
	assert.Equal(t, memdecisions.TypeRootCauseDisagreed, live.DecisionType,
		"the live row must be the latest verdict — both cannot be live at once")
	assert.False(t, live.Superseded)
}

// Without a message there is nothing to key idempotency on, so the write is
// refused rather than silently degrading to append-per-click.
func TestVoteRequiresMessageID(t *testing.T) {
	tenantID, userID, m, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	_, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID, Layer: "decisions", Action: "vote",
		Value: map[string]any{
			"decision_type": memdecisions.TypeRootCauseAgreed,
			"subject":       "some finding",
		},
		ActorKind: "user", ActorID: userID,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "message_id")
}

// A vote on one message must not be mistaken for a vote on another.
func TestVotesAreScopedToTheirMessage(t *testing.T) {
	tenantID, userID, m, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	castVote(t, m, tenantID, userID, "vote-msg-a", memdecisions.TypeRootCauseAgreed, "finding A")
	castVote(t, m, tenantID, userID, "vote-msg-b", memdecisions.TypeRootCauseDisagreed, "finding B")

	a, err := memdecisions.FindVoteForMessage(tenantID, userID, "vote-msg-a")
	require.NoError(t, err)
	require.NotNil(t, a)
	assert.Equal(t, "finding A", a.Subject)

	b, err := memdecisions.FindVoteForMessage(tenantID, userID, "vote-msg-b")
	require.NoError(t, err)
	require.NotNil(t, b)
	assert.Equal(t, "finding B", b.Subject)
}
