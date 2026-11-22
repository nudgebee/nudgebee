package api

import (
	"testing"

	"nudgebee/llm/events"

	"github.com/stretchr/testify/assert"
)

func TestEventKnowledgeHints(t *testing.T) {
	got := eventKnowledgeHints(events.Event{
		Title:          "CI failed: example-ci / Go Lint",
		Category:       " ci ",
		FindingType:    "github_workflow_job",
		AggregationKey: "github_ci_failure",
	})
	assert.Equal(t, []string{"ci", "github_workflow_job", "github_ci_failure"}, got)
	assert.Nil(t, eventKnowledgeHints(events.Event{}))
}

// Most alerts carry "issue" as both category and finding type; searching on it finds nothing for some alert names
// (#42793), so only the alert's own name is left.
func TestEventKnowledgeHintsSkipsGenericKinds(t *testing.T) {
	assert.Equal(t, []string{"AgentLatencyHigh"}, eventKnowledgeHints(events.Event{
		Category: "issue", FindingType: "Issue", AggregationKey: "AgentLatencyHigh",
	}))
	assert.Equal(t, []string{"email_alert", "HighCPU"}, eventKnowledgeHints(events.Event{
		Category: "alert", FindingType: "email_alert", AggregationKey: "HighCPU",
	}))
	assert.Equal(t, []string{"Anomaly", "memory_anomaly"}, eventKnowledgeHints(events.Event{
		Category: "Anomaly", FindingType: "anomaly", AggregationKey: "memory_anomaly",
	}))
}
