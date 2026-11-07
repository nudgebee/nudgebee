package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"

	"nudgebee/llm/config"
)

func TestMetricQueryReturnedNoData(t *testing.T) {
	tests := []struct {
		name        string
		tool        string
		observation string
		wantNoData  bool
		status      ToolStatus
	}{
		{"Prometheus no-data message", "prometheus_execute", "No data found for query: up", true, ToolStatusSuccess},
		{"Datadog empty series", "datadog_metrics_execute", `{"series":[]}`, true, ToolStatusSuccess},
		{"Elasticsearch zero series", "es_metrics_query", `{"results":[{"total_series":0,"payload":[]}]}`, true, ToolStatusSuccess},
		{"Elasticsearch missing total series with empty payload", "es_metrics_query", `{"results":[{"payload":[]}]}`, true, ToolStatusSuccess},
		{"Elasticsearch missing total series with data", "es_metrics_query", `{"results":[{"payload":[{}]}]}`, false, ToolStatusSuccess},
		{"Elasticsearch zero matched documents without note stays no-data", "es_metrics_query", `{"results":[{"payload":[],"total_series":0,"docs_matched":0}]}`, true, ToolStatusSuccess},
		{"Elasticsearch documents matched but no series is not no-data", "es_metrics_query", `{"results":[{"query":"{\"match_all\":{}}","payload":[],"total_series":0,"docs_matched":7,"note":"The query matched 7 document(s) but no metric series could be extracted from them. This is NOT an absence of data."}]}`, false, ToolStatusSuccess},
		{"Elasticsearch note without match count is not no-data", "es_metrics_query", `{"results":[{"query":"{\"term\":{\"missing.field\":\"x\"}}","payload":[],"docs_matched":0,"note":"The query references fields that do not exist: missing.field."}]}`, false, ToolStatusSuccess},
		{"Splunk zero series", "splunk_metrics_query", `{"results":[{"total_series":0,"payload":[]}]}`, true, ToolStatusSuccess},
		{"Splunk missing total series with empty payload", "splunk_metrics_query", `{"results":[{"payload":[]}]}`, true, ToolStatusSuccess},
		{"Splunk missing total series with data", "splunk_metrics_query", `{"results":[{"payload":[{}]}]}`, false, ToolStatusSuccess},
		{"Splunk zero matched documents without note stays no-data", "splunk_metrics_query", `{"results":[{"payload":[],"total_series":0,"docs_matched":0}]}`, true, ToolStatusSuccess},
		{"Splunk mstats rows without numeric value column is not no-data", "splunk_metrics_query", `{"results":[{"query":"| mstats avg(k8s.pod.cpu.usage) AS nb_value","payload":[],"total_series":0,"docs_matched":12,"note":"mstats returned rows but none carried a numeric value column"}]}`, false, ToolStatusSuccess},
		{"unknown tool is not treated as a metric read", "unregistered_metrics_tool", `[]`, false, ToolStatusSuccess},
		{"zero-valued metric is data", "datadog_metrics_execute", `{"series":[{"pointlist":[[1720000000000,0]]}]}`, false, ToolStatusSuccess},
		{"kubectl empty mutation success", "kubectl_execute", plannerToolNoData, false, ToolStatusEmptyResult},
		{"failed metric query is not a successful empty read", "prometheus_execute", plannerToolNoData, false, ToolStatusFailure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := NBAgentPlannerToolActionStep{
				Action:      NBAgentPlannerToolAction{Tool: tt.tool, DisplayID: "E1"},
				Observation: tt.observation,
				Status:      tt.status,
			}
			marker := metricNoDataMarker(step)
			if tt.wantNoData {
				assert.Contains(t, marker, metricNoDataTag)
				assert.Contains(t, marker, "not a measured zero")
				assert.Contains(t, marker, "only once discovery confirms the series does not exist")
			} else {
				assert.Empty(t, marker)
			}
		})
	}
}

// B1 regression: ES/Splunk report "documents matched but no series could be extracted"
// with an explicit "This is NOT an absence of data" note — that guidance must reach the
// model uncontradicted, without the [METRIC-NO-DATA] marker declaring unavailability.
func TestMetricDocsMatchedNoSeriesIsNotMarkedNoData(t *testing.T) {
	observation := `{"results":[{"query":"{\"match_all\":{}}","payload":[],"total_series":0,"docs_matched":7,"note":"The query matched 7 document(s) but no metric series could be extracted from them. This is NOT an absence of data."}]}`
	step := NBAgentPlannerToolActionStep{
		Action:      NBAgentPlannerToolAction{Tool: "es_metrics_query", DisplayID: "E1"},
		Observation: observation,
		Status:      ToolStatusSuccess,
	}

	assert.Empty(t, metricNoDataMarker(step))
	rendered := (&NBReActPlanner3{}).resolveObservation(step)
	assert.NotContains(t, rendered, metricNoDataTag)
	assert.Contains(t, rendered, "NOT an absence of data")
	summary, hasFailure := buildToolCallSummary([]NBAgentPlannerToolActionStep{step})
	assert.False(t, hasFailure)
	assert.NotContains(t, summary, "NO_METRIC_DATA")
}

// B2 regression: the per-turn availability block must gate "unavailable" on discovery
// instead of authorizing unavailable after a single empty read.
func TestMetricNoDataSummaryRequiresDiscoveryBeforeUnavailable(t *testing.T) {
	steps := []NBAgentPlannerToolActionStep{{
		Action:      NBAgentPlannerToolAction{Tool: "prometheus_execute", DisplayID: "E1"},
		Observation: "No data found for query: up",
		Status:      ToolStatusSuccess,
	}}

	summary := metricNoDataSummary(steps)
	assert.Contains(t, summary, "report a metric as unavailable only once discovery confirms the series does not exist")
	assert.NotContains(t, summary, "report the metric as unavailable and cite these empty results")
}

func TestMetricNoDataMarkerKeepsMutationEmptyOutputSemantics(t *testing.T) {
	step := NBAgentPlannerToolActionStep{
		Action:      NBAgentPlannerToolAction{Tool: "kubectl_execute", ToolInput: "apply -f deploy.yaml"},
		Observation: plannerToolNoData,
		Status:      ToolStatusEmptyResult,
	}

	assert.Empty(t, metricNoDataMarker(step))
	observation := (&NBReActPlanner3{}).resolveObservation(step)
	assert.Contains(t, observation, "[SUCCESS-NO-OUTPUT]")
	assert.NotContains(t, observation, metricNoDataTag)
}

func TestMetricEmptyResultIsNotSummarizedAsMutationSuccess(t *testing.T) {
	summary, hasFailure := buildToolCallSummary([]NBAgentPlannerToolActionStep{{
		Action:      NBAgentPlannerToolAction{Tool: "prometheus_execute", DisplayID: "E3"},
		Observation: plannerToolNoData,
		Status:      ToolStatusEmptyResult,
	}})

	assert.False(t, hasFailure)
	assert.Contains(t, summary, "NO_METRIC_DATA")
	assert.NotContains(t, summary, "SUCCESS_NO_OUTPUT")
}

func TestSuccessfulMetricNoDataTextIsNotSummarizedAsMeasuredData(t *testing.T) {
	summary, hasFailure := buildToolCallSummary([]NBAgentPlannerToolActionStep{{
		Action:      NBAgentPlannerToolAction{Tool: "prometheus_execute", DisplayID: "E3"},
		Observation: "No data found for query: up",
		Status:      ToolStatusSuccess,
	}})

	assert.False(t, hasFailure)
	assert.Contains(t, summary, "NO_METRIC_DATA")
}

func TestReAct3MetricNoDataSummarySurvivesScratchpadCompression(t *testing.T) {
	originalMax := config.Config.LlmServerAgentMaxScratchpadChars
	config.Config.LlmServerAgentMaxScratchpadChars = 4096
	defer func() { config.Config.LlmServerAgentMaxScratchpadChars = originalMax }()

	planner := &NBReActPlanner3{
		compressionTracker:       NewCompressionTracker(),
		llmSummarizationDisabled: true,
	}
	steps := []NBAgentPlannerToolActionStep{{
		Action: NBAgentPlannerToolAction{
			Tool:      "prometheus_execute",
			ToolID:    "prometheus_execute-1",
			DisplayID: "E4",
			ToolInput: "up",
		},
		Observation:      "No data found for query: up",
		SubAgentEvidence: strings.Repeat("old evidence ", 1000),
		Status:           ToolStatusSuccess,
	}}

	scratchpad := planner.buildScratchpad(steps)
	assert.Contains(t, scratchpad, "<metric_data_availability>")
	assert.Contains(t, scratchpad, metricNoDataTag)
	assert.Contains(t, scratchpad, "step E4")
}

func TestReAct3MetricEvidenceIsInsertedBeforeLastScratchpadClose(t *testing.T) {
	scratchpad := "<scratchpad>older</scratchpad> recent</scratchpad> \n"
	evidence := "<metric_data_availability>\n" + metricNoDataTag + "\n</metric_data_availability>"

	got := appendMetricEvidenceToScratchpad(scratchpad, evidence)
	lastClosingTag := strings.LastIndex(got, "</scratchpad>")
	evidenceIndex := strings.Index(got, metricNoDataTag)
	require.Greater(t, lastClosingTag, evidenceIndex)
	assert.Equal(t, " \n", got[lastClosingTag+len("</scratchpad>"):])
	assert.Contains(t, got[:lastClosingTag], evidence)
}

func TestReAct4MetricNoDataMarkerAndSummaryReachModel(t *testing.T) {
	planner := &NBReActPlanner4{}
	steps := []NBAgentPlannerToolActionStep{{
		Action: NBAgentPlannerToolAction{
			Tool:      "datadog_metrics_execute",
			ToolID:    "datadog_metrics_execute-1",
			DisplayID: "E2",
			ToolInput: "avg:system.cpu.user{service:web}",
		},
		Observation: `{"series":[]}`,
		Status:      ToolStatusSuccess,
	}}

	messages := planner.buildMessages("show CPU", steps)
	var transcript strings.Builder
	for _, message := range messages {
		for _, part := range message.Parts {
			if text, ok := part.(llms.TextContent); ok {
				transcript.WriteString(text.Text)
			}
			if result, ok := part.(llms.ToolCallResponse); ok {
				transcript.WriteString(result.Content)
			}
		}
	}
	assert.Contains(t, transcript.String(), metricNoDataTag)
	assert.Contains(t, transcript.String(), "<metric_data_availability>")
	assert.Contains(t, transcript.String(), "step E2")
}

func TestReAct4MetricEvidenceMergesIntoTrailingHumanMessage(t *testing.T) {
	planner := &NBReActPlanner4{
		refinementData: []refinementRecord{{
			Answer:    "An earlier conclusion.",
			Feedback:  "Recheck the empty metric result.",
			StepIndex: 1,
		}},
	}
	steps := []NBAgentPlannerToolActionStep{{
		Action: NBAgentPlannerToolAction{
			Tool:      "prometheus_execute",
			ToolID:    "prometheus_execute-1",
			DisplayID: "E1",
			ToolInput: "up",
		},
		Observation: "No data found for query: up",
		Status:      ToolStatusSuccess,
	}}

	messages := planner.buildMessages("show CPU", steps)
	require.Len(t, messages, 5)
	require.Equal(t, llms.ChatMessageTypeHuman, messages[len(messages)-1].Role)
	require.Len(t, messages[len(messages)-1].Parts, 1)
	var trailingHuman strings.Builder
	for _, part := range messages[len(messages)-1].Parts {
		if text, ok := part.(llms.TextContent); ok {
			trailingHuman.WriteString(text.Text)
		}
	}
	assert.Contains(t, trailingHuman.String(), "Recheck the empty metric result.")
	assert.Contains(t, trailingHuman.String(), metricNoDataTag)
	for i := 1; i < len(messages); i++ {
		assert.False(t, messages[i-1].Role == llms.ChatMessageTypeHuman && messages[i].Role == llms.ChatMessageTypeHuman)
	}
}

func TestSummarizeConversationIncludesMetricNoDataEvidence(t *testing.T) {
	fake := &fakeLLMModel{response: "P95 245ms"}
	withFakeLLMModel(t, fake)
	e := &plannerExecutor{
		ctx: llmOverrideContext(),
		agentRequest: NBAgentRequest{
			Query:          "What was the P95 latency?",
			AccountId:      "acct-1",
			UserId:         "user-1",
			ConversationId: "conv-1",
			MessageId:      "msg-1",
		},
		steps: []NBAgentPlannerToolActionStep{{
			Action: NBAgentPlannerToolAction{
				Tool:      "prometheus_execute",
				ToolID:    "prometheus_execute-1",
				DisplayID: "E1",
				ToolInput: "histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))",
			},
			Observation: "No data found for query: histogram_quantile(...) ",
			Status:      ToolStatusSuccess,
		}},
	}

	_, err := e.summarizeConversation()
	require.NoError(t, err)
	var prompt strings.Builder
	for _, message := range fake.lastSent() {
		for _, part := range message.Parts {
			if text, ok := part.(llms.TextContent); ok {
				prompt.WriteString(text.Text)
			}
		}
	}
	assert.Contains(t, prompt.String(), "<metric_data_availability>")
	assert.Contains(t, prompt.String(), "step E1")
	assert.Contains(t, prompt.String(), "empty metric result means unavailable, never zero")
}
