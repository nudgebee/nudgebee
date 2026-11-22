package api

import (
	"context"
	"errors"
	"testing"

	"nudgebee/llm/events"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

// With the remediation loop on, a step runs only through Nubi's run_step approval: the panel's Run and a Slack step
// message's Run (and their undo) are refused before anything is read or recorded; run_step's own decision, and the
// decisions that run nothing, go on. With the loop off nothing changes.
func TestLoopRunRefusal(t *testing.T) {
	cases := []struct {
		name   string
		loop   bool
		slot   string
		source string
		refuse bool
	}{
		{"loop on: panel Run", true, RemediationSlotExecute, "", true},
		{"loop on: Slack Run", true, RemediationSlotExecute, slackSource, true},
		{"loop on: panel undo", true, RemediationSlotRollback, "", true},
		{"loop on: no slot is a Run", true, "", slackSource, true},
		{"loop on: run_step", true, RemediationSlotExecute, chatSource, false},
		{"loop on: run_step undo", true, RemediationSlotRollback, chatSource, false},
		{"loop on: Slack Decline", true, RemediationSlotDecline, slackSource, false},
		{"loop on: I ran this", true, RemediationSlotManual, "", false},
		{"loop off: panel Run", false, RemediationSlotExecute, "", false},
		{"loop off: Slack Run", false, RemediationSlotExecute, slackSource, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stubLoopOn(t, c.loop)
			code, got := loopRunRefusal(RemediationExecuteRequest{AccountId: "acct", Slot: c.slot, Source: c.source})
			if c.refuse {
				assert.Equal(t, 409, code)
				assert.Equal(t, loopRunNeedsApproval, got)
			} else {
				assert.Empty(t, got)
			}
		})
	}
}

// decideStep answers the refusal with 409 before it reads the plan (no database is set up here).
func TestDecideStepRefusesARunOutsideTheApprovalWithTheLoopOn(t *testing.T) {
	stubLoopOn(t, true)
	ctx := security.NewRequestContextForTenantAdminWithUser(context.Background(), "tenant", "user-1")
	answer := decideStep(ctx, RemediationExecuteRequest{AccountId: "acct", EventId: "evt", Slot: RemediationSlotExecute, Source: slackSource, PlanVersion: "v1"}, "user-1")
	assert.Equal(t, 409, answer.Code)
	assert.Equal(t, loopRunNeedsApproval, answer.Message)
}

// A loop flag that cannot be read never lets a Run through without an approval; a decision that runs nothing goes on.
func TestLoopRunRefusal_FlagUnread(t *testing.T) {
	orig := events.RemediationLoopRead
	events.RemediationLoopRead = func(string) (bool, error) { return false, errors.New("db down") }
	t.Cleanup(func() { events.RemediationLoopRead = orig })
	code, got := loopRunRefusal(RemediationExecuteRequest{AccountId: "acct", Slot: RemediationSlotExecute})
	assert.Equal(t, 503, code)
	assert.Equal(t, loopRunFlagUnread, got)
	code, got = loopRunRefusal(RemediationExecuteRequest{AccountId: "acct", Slot: RemediationSlotDecline})
	assert.Zero(t, code)
	assert.Empty(t, got)
}
