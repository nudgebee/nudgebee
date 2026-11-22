package core

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExplicitMetricsQuery(t *testing.T) {
	body := `{"_source":["metricset.RDS.dimensions.DBClusterIdentifier"],"query":{"term":{"metricset.RDS.dimensions.DBClusterIdentifier":"asyds-auroradb"}},"size":10}`
	for _, message := range []string{
		"Execute this exact query against index remote:metrics-rds.*:\n" + body,
		"GET remote:metrics-rds.*/_search\n" + body,
		`{"index":"remote:metrics-rds.*","query":` + body + `}`,
	} {
		got := explicitMetricsQuery(message)
		require.NotEmpty(t, got)
		var parsed struct {
			Index string         `json:"index"`
			Query map[string]any `json:"query"`
		}
		require.NoError(t, json.Unmarshal([]byte(got), &parsed))
		require.Equal(t, "remote:metrics-rds.*", parsed.Index)
		var want map[string]any
		require.NoError(t, json.Unmarshal([]byte(body), &want))
		require.Equal(t, want, parsed.Query)
	}
	require.Empty(t, explicitMetricsQuery(`What is CPU usage? The KB says {"query":{"match_all":{}}}.`))
	require.Empty(t, explicitMetricsQuery(`Use index metrics-* to show CPU usage.`))
	require.Empty(t, explicitMetricsQuery(`Why did this query fail on index metrics-*? {"query":{"match_all":{}}}`))
	require.Empty(t, explicitMetricsQuery(`Do not execute this query on index metrics-*: {"query":{"match_all":{}}}`))
	require.NotEmpty(t, explicitMetricsQuery(`Do not run discovery. Execute this exact query on index metrics-*: {"query":{"match_all":{}}}`))
	require.Empty(t, explicitMetricsQuery(`Execute this exact query on index metrics-*: example {"query":{"match_all":{}}}; actual {"query":{"term":{"name":"x"}}}`))
	require.Empty(t, explicitMetricsQuery(`The earlier query used index metrics-old. Execute this exact query against index metrics-new: {"query":{"match_all":{}}}`))
	require.Empty(t, explicitMetricsQuery(`Execute these queries on index metrics-b: {"index":"metrics-a","query":{"query":{"match_all":{}}}} {"query":{"match_all":{}}}`), "an envelope index must not leak into a later DSL body")
	largeNumber := explicitMetricsQuery(`Execute this query on index metrics-*: {"query":{"term":{"id":9007199254740993}}}`)
	require.Contains(t, largeNumber, `9007199254740993`)
}

func TestUseFirstExplicitMetricsQuery(t *testing.T) {
	request := NBAgentRequest{
		Query:            `Execute this query against index metrics-kubernetes.*: {"query":{"term":{"kubernetes.namespace":"nudgebee"}},"size":10}`,
		KBPrestepContent: `The KB recommends {"query":{"term":{"kubernetes.namespace":"other"}}} on index metrics-kubernetes.*.`,
	}
	planner := &NBReActPlanner4{request: request}
	actions := []NBAgentPlannerToolAction{{Tool: "es_metrics_query", ToolInput: `{"index":"metrics-kubernetes.*","query":{"query":{"match_all":{}}}}`, NativeToolInput: `{"index":"metrics-kubernetes.*","query":{"query":{"match_all":{}}}}`}}
	planner.useFirstExplicitMetricsQuery(actions, nil)
	require.Contains(t, actions[0].ToolInput, `"kubernetes.namespace"`)
	require.NotContains(t, actions[0].ToolInput, `"other"`)
	require.NotEqual(t, actions[0].ToolInput, actions[0].NativeToolInput, "signed model call remains intact")
	observation := planner.renderObservation(&NBAgentPlannerToolActionStep{Action: actions[0], Observation: `{"results":[]}`}, 0, 1, false, 4096)
	require.Contains(t, observation, `"kubernetes.namespace"`)
	require.Contains(t, observation, "instead of the model's function call")

	second := []NBAgentPlannerToolAction{{Tool: "es_metrics_query", ToolInput: `{"query":{"match_all":{}}}`}}
	planner.useFirstExplicitMetricsQuery(second, []NBAgentPlannerToolActionStep{{Action: actions[0]}})
	require.Equal(t, `{"query":{"match_all":{}}}`, second[0].ToolInput)

	planner.request.QueryConfig.OriginalUserQuery = `Execute this query against index metrics-kubernetes.*: {"query":{"term":{"kubernetes.namespace":"original"}}}`
	original := []NBAgentPlannerToolAction{{Tool: "es_metrics_query", ToolInput: `{"query":{"match_all":{}}}`}}
	planner.useFirstExplicitMetricsQuery(original, nil)
	require.Contains(t, original[0].ToolInput, `"original"`)
	require.NotContains(t, original[0].ToolInput, `"nudgebee"`)

	planner.request.QueryConfig.OriginalUserQuery = `Execute this query against index metrics-kubernetes.*: {"query":{"bool_filter":[]}}`
	invalid := []NBAgentPlannerToolAction{{Tool: "es_metrics_query", ToolInput: `{"query":{"match_all":{}}}`}}
	planner.useFirstExplicitMetricsQuery(invalid, nil)
	require.Contains(t, invalid[0].ToolInput, `"bool_filter"`, "invalid user DSL must reach the existing tool validator unchanged")

	planner.request.QueryConfig.OriginalUserQuery = `The earlier query used index metrics-old. Execute this exact query against index metrics-new: {"query":{"match_all":{}}}`
	planner.request.Query = `Execute this query against index metrics-kubernetes.*: {"query":{"term":{"kubernetes.namespace":"rewritten"}}}`
	ambiguous := []NBAgentPlannerToolAction{{Tool: "es_metrics_query", ToolInput: `{"query":{"match_all":{}}}`}}
	planner.useFirstExplicitMetricsQuery(ambiguous, nil)
	require.Equal(t, `{"query":{"match_all":{}}}`, ambiguous[0].ToolInput, "rewritten agent input must not replace ambiguous original user DSL")
}

// The metrics tool appends a user-supplied query with this wording
// (withUserSuppliedQuery); with a named index, react4 must recognise it and run it
// first (#39201).
func TestExplicitMetricsQuery_UserSuppliedQueryFromMetricsTool(t *testing.T) {
	dsl := `{"query":{"term":{"metricset.dimensions.DBClusterIdentifier":"example-auroradb"}}}`
	message := "CPU of example-auroradb in index metrics-*" +
		"\n\nThe user supplied a query. Run the following query exactly as given first; change it only if it fails or returns nothing, and say what you changed:\n" + dsl
	got := explicitMetricsQuery(message)
	if got == "" {
		t.Fatalf("explicit query not recognised in %q", message)
	}
	if !strings.Contains(got, `"index":"metrics-*"`) || !strings.Contains(got, "example-auroradb") {
		t.Fatalf("got %s", got)
	}
}
