package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatusPollBudget_UnreadableRepliesExhaustIt(t *testing.T) {
	var b statusPollBudget
	for i := 1; i < maxStatusPollErrors; i++ {
		assert.False(t, b.unusable(), "reply %d", i)
	}
	assert.True(t, b.unusable(), "the %dth consecutive unusable reply ends the poll", maxStatusPollErrors)
}

func TestStatusPollBudget_UsableReplyResetsIt(t *testing.T) {
	var b statusPollBudget
	for i := 1; i < maxStatusPollErrors; i++ {
		b.unusable()
	}
	b.usable()
	assert.Equal(t, 0, b.consecutive)
	assert.False(t, b.unusable(), "the count starts over after a usable reply")
}

// The bug: the budget was reset as soon as the HTTP call succeeded, before the body
// was parsed, so a reply that was not JSON never counted.
func TestReadStatusReply_UnreadableBodiesSpendTheBudget(t *testing.T) {
	var b statusPollBudget
	for i := 1; i < maxStatusPollErrors; i++ {
		reply, exhausted, err := readStatusReply([]byte(""), &b)
		assert.Error(t, err)
		assert.Nil(t, reply)
		assert.False(t, exhausted, "reply %d", i)
	}
	_, exhausted, err := readStatusReply([]byte(`{"status":"runn`), &b)
	assert.Error(t, err)
	assert.True(t, exhausted, "the %dth consecutive unreadable reply ends the poll", maxStatusPollErrors)
}

func TestReadStatusReply_ParsedReplyResetsTheBudget(t *testing.T) {
	var b statusPollBudget
	for i := 1; i < maxStatusPollErrors; i++ {
		_, _, _ = readStatusReply([]byte("not json"), &b)
	}
	reply, exhausted, err := readStatusReply([]byte(`{"status":"running","progress":"cloning"}`), &b)
	assert.NoError(t, err)
	assert.False(t, exhausted)
	assert.Equal(t, "running", reply["status"])
	assert.Equal(t, 0, b.consecutive)
}

// A JSON null parses without error but has no status, so it would never end the poll.
func TestReadStatusReply_NullIsUnusable(t *testing.T) {
	var b statusPollBudget
	reply, exhausted, err := readStatusReply([]byte("null"), &b)
	assert.Error(t, err)
	assert.Nil(t, reply)
	assert.False(t, exhausted)
	assert.Equal(t, 1, b.consecutive)
}
