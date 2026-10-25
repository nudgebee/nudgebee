package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// propose_fix's result says where the person sees the fixes and asks for a one-sentence reply, so Nubi does not
// restate the plan in chat (and in the alert's Slack thread, beside the options message).
func TestProposalTextAsksForAOneSentenceReply(t *testing.T) {
	plan := RemediationPlan{Version: 3, RootCause: "the memory limit is too low", Summary: "raise the limit",
		Actions: []RemediationAction{
			{Title: "Increase Kafka memory", ExecuteCommand: "kubectl set resources deployment/kafka --limits=memory=2Gi"},
			{Title: "Raise the limit in the chart"},
		}}
	out := proposalText(plan)
	assert.Equal(t, "Stored version 3 of the alert's fixes, with 2 fix options. A person must confirm each command before it runs."+
		" The person already sees the fix options, each with its own button, on the event's Remediation tab and in the alert's Slack thread, and the investigation is there too."+
		" Your final answer is one sentence that the fixes were updated (version 3, 2 fix options) and can be picked there."+
		" Do not restate, list or summarise the fixes, their commands, the root cause or the investigation, and use no headings or sections.", out)
	assert.NotContains(t, out, "kubectl", "no command to copy into the answer")
	assert.NotContains(t, out, "raise the limit", "no summary to copy into the answer")

	one := proposalText(RemediationPlan{Version: 1, Actions: plan.Actions[:1]})
	assert.Contains(t, one, "(version 1, 1 fix option)")
	none := proposalText(RemediationPlan{Version: 2})
	assert.Contains(t, none, "with no fix options")
	assert.Contains(t, none, "(version 2, 0 fix options)")
}
