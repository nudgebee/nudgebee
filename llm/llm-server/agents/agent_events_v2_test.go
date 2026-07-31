package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAgentEventsV2_ShouldSummarizeNow(t *testing.T) {
	agent := AgentEventsV2{}

	threeEvents := `[{"event_id":"1"},{"event_id":"2"},{"event_id":"3"}]`
	assert.True(t, agent.ShouldSummarizeNow("events_execute", threeEvents))
	assert.True(t, agent.ShouldSummarizeNow("  events_execute  ", threeEvents))
	assert.True(t, agent.ShouldSummarizeNow("EVENTS_EXECUTE", threeEvents))
	assert.True(t, agent.ShouldSummarizeNow("anomaly_execute", threeEvents))

	fourEvents := `[{"event_id":"1"},{"event_id":"2"},{"event_id":"3"},{"event_id":"4"}]`
	assert.False(t, agent.ShouldSummarizeNow("events_execute", fourEvents))

	// get_event_by_id must never trigger direct summarization — it precedes
	// get_triage_explanation in this agent's dominant example, and skipping
	// straight to the summary tool would drop that second call.
	assert.False(t, agent.ShouldSummarizeNow("get_event_by_id", threeEvents))

	assert.False(t, agent.ShouldSummarizeNow("list_events", threeEvents))
	assert.False(t, agent.ShouldSummarizeNow("aggregate_events", threeEvents))
	assert.False(t, agent.ShouldSummarizeNow("events_execute", "[]"))
}
