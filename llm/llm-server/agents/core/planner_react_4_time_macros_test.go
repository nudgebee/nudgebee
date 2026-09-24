package core

import (
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

func TestReAct4_TimeMacrosInExecutableInput(t *testing.T) {
	tests := []struct {
		name  string
		tool  string
		input string
	}{
		{
			name:  "Elasticsearch DSL",
			tool:  "es_metrics_query",
			input: `{"index":"metrics-*","query":{"query":{"range":{"@timestamp":{"gte":"[[Time:-24h]]","lte":"[[Time:Now]]"}}}}}`,
		},
		{
			name:  "cloud CLI",
			tool:  "aws_execute",
			input: `{"command":"aws cloudwatch get-metric-statistics --start-time [[Time:-24h]] --end-time [[Time:Now]]"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			planner := &NBReActPlanner4{}
			call := &llms.ContentChoice{ToolCalls: []llms.ToolCall{toolCall("", tc.tool, tc.input)}}
			before := time.Now().UTC()
			actions, _, err := planner.parseCompletion(call)
			after := time.Now().UTC()
			require.NoError(t, err)
			require.Len(t, actions, 1)
			action := actions[0]
			require.Equal(t, tc.input, action.NativeToolInput)
			require.Equal(t, generateToolId(tc.tool, tc.input), action.ToolID)
			require.NotContains(t, action.ToolInput, "[[Time:")
			var input map[string]any
			require.NoError(t, json.Unmarshal([]byte(action.ToolInput), &input))
			require.WithinDuration(t, before.Add(-24*time.Hour), parseFirstTime(t, action.ToolInput), after.Sub(before)+time.Second)
		})
	}
}

func parseFirstTime(t *testing.T, input string) time.Time {
	t.Helper()
	start := regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z`).FindString(input)
	require.NotEmpty(t, start)
	parsed, err := time.Parse(time.RFC3339, start)
	require.NoError(t, err)
	return parsed
}
