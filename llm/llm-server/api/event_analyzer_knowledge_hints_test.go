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
