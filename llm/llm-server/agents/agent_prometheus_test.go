package agents

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPrometheusDiscoveryWiring asserts the metrics_series_match tool is registered and
// its prompt guidance is present, so the agent can resolve a workload's real metric
// families on an empty templated query instead of reporting N/A. It is unconditional —
// there is no flag gating this discovery.
func TestPrometheusDiscoveryWiring(t *testing.T) {
	agent := PrometheusAgent{accountId: "test"}

	// Tool is registered on the agent.
	registered := false
	for _, tl := range agent.GetSupportedTools(nil) {
		if tl.Name() == "metrics_series_match" {
			registered = true
			break
		}
	}
	assert.True(t, registered, "metrics_series_match tool must be registered on the prometheus agent")

	prompt := agent.GetSystemPrompt(nil, core.NBAgentRequest{})

	// Prompt instructions tell the agent to discover families on an empty result.
	joined := strings.Join(prompt.Instructions, "\n")
	assert.Contains(t, joined, "discover, don't assume", "discovery guidance must be in the system prompt")
	assert.Contains(t, joined, "metrics_series_match", "prompt must point the agent at metrics_series_match")

	// ToolUsage documents the tool.
	if _, ok := prompt.ToolUsage["metrics_series_match"]; !ok {
		t.Error("metrics_series_match must have a ToolUsage entry")
	}

	// Discovery must not hardcode this environment's metric families.
	assert.NotContains(t, joined, "rpc_server", "must not hardcode a specific metric family")
	assert.NotContains(t, joined, "http_server_", "must not hardcode a specific metric family")
}

// #40909: the planner view of a prometheus_execute result keeps the chart note verbatim
// instead of formatting it as a query.
func TestPrometheusPlannerViewKeepsChartNote(t *testing.T) {
	note := toolcore.MetricChartNote("chart_ab12", "node_cpu (UTC)")
	raw, err := json.Marshal(map[string]string{
		"node_cpu":     `[{"metric":{"node":"a"},"timestamps":[1,2],"values":[0.1,0.2],"stats":{"max":0.2}}]`,
		"__chart_note": note,
	})
	require.NoError(t, err)

	got := PrometheusAgent{accountId: "test"}.UpdateToolResponseForPlanner(core.NBAgentPlannerToolAction{Tool: tools.ToolQueryPrometheus}, string(raw))

	assert.True(t, strings.HasSuffix(got, note), got)
	assert.NotContains(t, got, "**Query** - __chart_note")
}

// The planner view keeps what an oversized result says instead of reporting no data:
// the saved-file note verbatim, and a per-query "not shown inline" note as that
// query's response.
func TestPrometheusPlannerViewKeepsDataNotes(t *testing.T) {
	dataNote := `Inline results carry only stats (min/max/avg/p99) for the 1 of 40 matched series with the largest |max|. The complete results are saved to file_ref "metrics_prometheus_1.json".`
	raw, err := json.Marshal(map[string]string{
		"node_cpu":    `[{"metric":{"node":"a"},"stats":{"max":0.2}}]`,
		"pod_cpu":     "39 series, not shown inline (inline limit reached); see the saved file",
		"__data_note": dataNote,
	})
	require.NoError(t, err)

	got := PrometheusAgent{accountId: "test"}.UpdateToolResponseForPlanner(core.NBAgentPlannerToolAction{Tool: tools.ToolQueryPrometheus}, string(raw))

	assert.True(t, strings.HasSuffix(got, dataNote), got)
	assert.NotContains(t, got, "**Query** - __data_note")
	assert.Contains(t, got, "**Query** - pod_cpu \n **Response** - 39 series, not shown inline")
	assert.NotContains(t, got, "No data found")
}

// Each query's response lists only its own series, not every earlier query's too.
func TestPrometheusPlannerViewSeparatesQueries(t *testing.T) {
	raw, err := json.Marshal(map[string]string{
		"q_a": `[{"metric":{"node":"only-a"},"timestamps":[1],"values":[1],"stats":{"max":1}}]`,
		"q_b": `[{"metric":{"node":"only-b"},"timestamps":[1],"values":[2],"stats":{"max":2}}]`,
	})
	require.NoError(t, err)

	got := PrometheusAgent{accountId: "test"}.UpdateToolResponseForPlanner(core.NBAgentPlannerToolAction{Tool: tools.ToolQueryPrometheus}, string(raw))

	assert.Equal(t, 1, strings.Count(got, "only-a"), got)
	assert.Equal(t, 1, strings.Count(got, "only-b"), got)
}
