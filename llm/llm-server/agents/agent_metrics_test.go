package agents

import (
	"encoding/json"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	toolcore "nudgebee/llm/tools/core"
	"testing"

	"github.com/tmc/langchaingo/llms"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildLargeMetricsMessage asserts the metrics wrapper's truncation notice tells
// the calling orchestrator to grep the saved file instead of re-querying with a
// narrower time range (NB-27743), and applies uniformly regardless of provider — the
// saved file holds the sub-agent's own answer text either way (PostProcessResponse's
// raw-JSON override never fires through this nested ExecuteAgentToolCall path), so
// the guidance is plain-text search, not a provider-specific data shape.
func TestBuildLargeMetricsMessage(t *testing.T) {
	msg := buildLargeMetricsMessage(12345, "metrics_123.txt", "some preview text")

	assert.Contains(t, msg, "Output large (12345 bytes)")
	assert.Contains(t, msg, "metrics_123.txt")
	assert.Contains(t, msg, "shell_execute")
	assert.Contains(t, msg, "instead of re-querying with a narrower time range")
	assert.Contains(t, msg, "some preview text")

	// Must not assume a specific data shape (JSON/jq) — the saved content is the
	// sub-agent's own prose answer, not raw per-datapoint data.
	assert.NotContains(t, msg, "jq")
	assert.NotContains(t, msg, "map_values(fromjson)")
}

// Each query writer predates the metrics_query wrapper and answers in its own shape.
// These cases are the contract the wrapper has to keep honouring; a writer changing
// its output shape should fail here rather than silently deliver an empty editor.
func TestNormaliseGeneratedMetricsQuery(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		raw          string
		wantQuery    string
		wantProvider string
		wantIndex    string
	}{
		{
			name:         "promql_query single query",
			provider:     "prometheus",
			raw:          `{"promql": "sum(rate(container_cpu_usage_seconds_total{namespace=\"nudgebee\"}[5m])) by (pod)"}`,
			wantQuery:    `sum(rate(container_cpu_usage_seconds_total{namespace="nudgebee"}[5m])) by (pod)`,
			wantProvider: "prometheus",
		},
		{
			// promql_query returns an array when asked for several; the editor holds one.
			name:         "promql_query array takes the first",
			provider:     "victoria-metrics",
			raw:          `{"promql": ["up{job=\"a\"}", "up{job=\"b\"}"]}`,
			wantQuery:    `up{job="a"}`,
			wantProvider: "victoria-metrics",
		},
		{
			name:         "promql_query array skips a leading empty entry",
			provider:     "prometheus",
			raw:          `{"promql": ["", "up"]}`,
			wantQuery:    "up",
			wantProvider: "prometheus",
		},
		{
			name:         "es envelope passes through with its index",
			provider:     "ES",
			raw:          `{"query":"{\n  \"query\": {}\n}","provider":"ES","index":"metrics-kubernetes.*"}`,
			wantQuery:    "{\n  \"query\": {}\n}",
			wantProvider: "ES",
			wantIndex:    "metrics-kubernetes.*",
		},
		{
			name:         "datadog bare query string",
			provider:     "datadog",
			raw:          "avg:kubernetes.cpu.usage.total{kube_namespace:nudgebee}",
			wantQuery:    "avg:kubernetes.cpu.usage.total{kube_namespace:nudgebee}",
			wantProvider: "datadog",
		},
		{
			name:         "fenced bare query is unwrapped",
			provider:     "datadog",
			raw:          "```\navg:system.cpu.user{*}\n```",
			wantQuery:    "avg:system.cpu.user{*}",
			wantProvider: "datadog",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normaliseGeneratedMetricsQuery(tc.provider, tc.raw)

			require.NoError(t, err)
			assert.Equal(t, tc.wantQuery, got.Query)
			assert.Equal(t, tc.wantProvider, got.Provider)
			assert.Equal(t, tc.wantIndex, got.Index)
		})
	}
}

// An empty query box after a spinner reads as "the AI is broken" and gives the user
// nothing to act on, so every unusable answer must surface as an error instead.
func TestNormaliseGeneratedMetricsQueryRejectsUnusableAnswers(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{name: "empty response", raw: ""},
		{name: "whitespace only", raw: "   \n  "},
		{name: "json object with neither query nor promql", raw: `{"summary":"cpu looks fine"}`},
		{name: "empty promql string", raw: `{"promql": ""}`},
		{name: "promql array with only empty entries", raw: `{"promql": ["", "  "]}`},
		{name: "empty es query field", raw: `{"query":"","provider":"ES"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normaliseGeneratedMetricsQuery("prometheus", tc.raw)
			require.Error(t, err)
		})
	}
}

// The {"promql": ...} envelope is a transport, not a trust boundary: prose
// smuggled into it must fail the same single-expression parse as bare text, while
// a valid JSON-wrapped query still fills the editor.
func TestNormaliseGeneratedMetricsQueryRejectsProseInPromqlEnvelope(t *testing.T) {
	_, err := normaliseGeneratedMetricsQuery("prometheus", `{"promql":"Here is the query: up"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a runnable query", "prose in the promql envelope must fail with the same error shape as bare prose")

	got, err := normaliseGeneratedMetricsQuery("prometheus", `{"promql":"up"}`)
	require.NoError(t, err)
	assert.Equal(t, "up", got.Query)
}

func TestNormaliseGeneratedMetricsQueryValidatesQueryEnvelope(t *testing.T) {
	_, err := normaliseGeneratedMetricsQuery("prometheus", `{"query":"Here is the query: up"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a runnable query")

	got, err := normaliseGeneratedMetricsQuery("prometheus", `{"query":"up"}`)
	require.NoError(t, err)
	assert.Equal(t, "up", got.Query)

	got, err = normaliseGeneratedMetricsQuery("datadog", `{"query":"avg:system.cpu.user{*}"}`)
	require.NoError(t, err)
	assert.Equal(t, "avg:system.cpu.user{*}", got.Query)
}

func TestNormaliseGeneratedMetricsQueryValidatesDatadogQueries(t *testing.T) {
	valid := "avg:kubernetes.cpu.usage.total{pod_name:my-pod,kube_namespace:default} by {pod_name}"
	got, err := normaliseGeneratedMetricsQuery("datadog", valid)
	require.NoError(t, err)
	assert.Equal(t, valid, got.Query)

	for _, raw := range []string{
		"Here is the query: " + valid,
		"Here are some queries:\n\n### CPU\n```text\n" + valid + "\n```\n\n### Memory\n```text\navg:kubernetes.memory.usage{*}\n```",
		`{"query":"Here is the query: ` + valid + `"}`,
	} {
		_, err := normaliseGeneratedMetricsQuery("datadog", raw)
		require.Error(t, err, "raw response should not reach the query editor: %q", raw)
		assert.Contains(t, err.Error(), "not a runnable query")
	}
}

// Issue #293: on an ambiguous ask, promql_query answered with a markdown menu of
// example queries. That must never land in the editor — for a PromQL provider the
// markdown fails the single-expression parse and surfaces as an error, and a
// {"note": ...}-only answer is the contract for the clarification and its text
// becomes the error message the user can act on.
func TestNormaliseGeneratedMetricsQueryRejectsMarkdownMenuAndSurfacesNote(t *testing.T) {
	markdownMenu := "Here are some queries you can use:\n\n### CPU Usage\n```promql\nsum(rate(container_cpu_usage_seconds_total[5m])) by (pod)\n```\n\n### Memory Usage\n```promql\ncontainer_memory_usage_bytes{namespace=\"default\"}\n```\n"
	_, err := normaliseGeneratedMetricsQuery("prometheus", markdownMenu)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a runnable query", "the markdown menu must be rejected, not stored as the query")

	_, err = normaliseGeneratedMetricsQuery("prometheus", `{"note": "Which metric do you want to query — CPU or memory?"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Which metric do you want to query — CPU or memory?", "the note itself is the clarification message")

	// The ES writer answers only in the JSON envelope; prose can never run in the
	// ES editor either, so it must error rather than fill the bar.
	_, err = normaliseGeneratedMetricsQuery("ES", "some explanatory prose instead of a query")
	require.Error(t, err)
}

// Bare text for a PromQL provider is accepted only when it parses as one PromQL
// expression: a writer regression emitting a bare-but-valid query still fills the
// editor, while prose becomes an error.
func TestNormaliseGeneratedMetricsQueryAcceptsBarePromQL(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		raw      string
		want     string
	}{
		{name: "bare metric selector", provider: "prometheus", raw: "up", want: "up"},
		{
			name:     "bare query with __CLUSTER__ placeholder is kept verbatim",
			provider: "victoria-metrics",
			raw:      `rate(http_requests_total{__CLUSTER__ job="api"}[5m])`,
			want:     `rate(http_requests_total{__CLUSTER__ job="api"}[5m])`,
		},
		{
			name:     "single fenced promql block is unwrapped and parsed",
			provider: "prometheus",
			raw:      "```promql\nsum(rate(node_cpu_seconds_total[5m]))\n```",
			want:     "sum(rate(node_cpu_seconds_total[5m]))",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normaliseGeneratedMetricsQuery(tc.provider, tc.raw)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Query)
			assert.Equal(t, tc.provider, got.Provider)
		})
	}
}

// Regression for issue #293 at the Execute boundary, driven through the injectable
// executor so no LLM is needed: a "give me a query for X" ask with a well-formed
// writer answer fills the editor with one clean query, while the issue's markdown
// writer answer surfaces as a failed response instead of editor garbage.
func TestMetricsQueryAgentExecuteNormalisesWriterAnswer(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	newAgent := func(writerRaw string) *metricsQueryAgent {
		return &metricsQueryAgent{
			accountId: "acct",
			provider:  "prometheus",
			agent:     &PromqlAgent{accountId: "acct"},
			executor: func(_ toolcore.NbToolContext, _ core.NBAgent, _ toolcore.NBToolCallRequest) (core.NBAgentResponse, error) {
				return core.NBAgentResponse{
					Response: []string{writerRaw},
					Status:   core.ConversationStatusCompleted,
				}, nil
			},
		}
	}

	t.Run("well-formed writer answer yields one clean query", func(t *testing.T) {
		resp, err := newAgent(`{"promql": "sum(rate(container_cpu_usage_seconds_total{__CLUSTER__ namespace=\"default\"}[5m])) by (pod)"}`).
			Execute(ctx, core.NBAgentRequest{Query: "give me a query for cpu usage"})

		require.NoError(t, err)
		require.Equal(t, core.ConversationStatusCompleted, resp.Status)

		var result metricsQueryResult
		require.NoError(t, json.Unmarshal([]byte(resp.Response[0]), &result))
		assert.Equal(t, `sum(rate(container_cpu_usage_seconds_total{__CLUSTER__ namespace="default"}[5m])) by (pod)`, result.Query)
		assert.Equal(t, "prometheus", result.Provider)
	})

	t.Run("markdown menu writer answer fails instead of filling the editor", func(t *testing.T) {
		markdownMenu := "Here are some queries:\n### CPU\n```promql\nup\n```\n### Memory\n```promql\nup\n```"
		resp, err := newAgent(markdownMenu).Execute(ctx, core.NBAgentRequest{Query: "give me a query"})

		require.NoError(t, err)
		require.Equal(t, core.ConversationStatusFailed, resp.Status)
		assert.Contains(t, resp.Response[0], "not a runnable query")
	})
}

// The router picks the query writer whose language the account's editor speaks. It
// also proves the registry lookups resolve: promql_query and datadog_metrics_query
// register via RegisterNBAgentFactoryAsTool, and a rename there would otherwise only
// surface as a runtime "not registered" error on a user's click.
func TestNewMetricsQueryAgentSelectsWriterByProvider(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	for _, tc := range []struct {
		provider   string
		wantWriter string
	}{
		{provider: "prometheus", wantWriter: PromqlAgentName},
		{provider: "victoria-metrics", wantWriter: PromqlAgentName},
		{provider: "chronosphere", wantWriter: PromqlAgentName},
		{provider: "", wantWriter: PromqlAgentName},
		{provider: "ES", wantWriter: ESMetricsQueryAgentName},
		{provider: "elasticsearch", wantWriter: ESMetricsQueryAgentName},
		{provider: "opensearch", wantWriter: ESMetricsQueryAgentName},
		{provider: "datadog", wantWriter: DatadogMetricsQueryAgentName},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			agent, err := newMetricsQueryAgent(ctx, "acct", "", services_server.ObservabilityProvider{Provider: tc.provider})

			require.NoError(t, err)
			wrapper, ok := agent.(*metricsQueryAgent)
			require.True(t, ok)
			assert.Equal(t, tc.wantWriter, wrapper.agent.GetName())
			assert.Equal(t, tc.provider, wrapper.provider, "provider is echoed back in the envelope")
		})
	}
}

// Cloud-only accounts read metrics through a CLI agent, so there is no query language
// for the editor. Generating one anyway would hand the user a string Run Query can
// never execute.
func TestNewMetricsQueryAgentRejectsCloudOnlyProviders(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	for _, provider := range []string{"aws", "gcp", "azure"} {
		t.Run(provider, func(t *testing.T) {
			_, err := newMetricsQueryAgent(ctx, "acct", "", services_server.ObservabilityProvider{Provider: provider})

			require.Error(t, err)
			assert.Contains(t, err.Error(), "not supported")
		})
	}
}

// The ES writer resolves its schema against the pinned index, so the pin has to reach
// it rather than being dropped at the router.
func TestNewMetricsQueryAgentPinsESIndex(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	agent, err := newMetricsQueryAgent(ctx, "acct", "metrics-kubernetes.*", services_server.ObservabilityProvider{Provider: "ES"})

	require.NoError(t, err)
	wrapper := agent.(*metricsQueryAgent)
	esWriter, ok := wrapper.agent.(*ESMetricsQueryAgent)
	require.True(t, ok)
	assert.Equal(t, "metrics-kubernetes.*", esWriter.requestedIndex)
}

// TestNewestMetricsStepReportsFailedStepAsError pins #36633: a failed forwarded step
// (relay down, deadline exceeded) must not reach the orchestrator as a metrics success.
func TestNewestMetricsStepReportsFailedStepAsError(t *testing.T) {
	step := func(content string, failed bool) core.ToolInvocation {
		return core.ToolInvocation{Response: llms.ToolCallResponse{Content: content}, Failed: failed}
	}

	content, status, ok := newestMetricsStep([]core.ToolInvocation{
		step(`{"promql":"up"}`, false),
		step("executePromQl: relay execute: relay error: agent not connected", true),
		step("", false),
	})
	require.True(t, ok)
	assert.Equal(t, "executePromQl: relay execute: relay error: agent not connected", content)
	assert.Equal(t, toolcore.NBToolResponseStatusError, status)

	content, status, ok = newestMetricsStep([]core.ToolInvocation{
		step("relay error: agent not connected", true),
		step(`{"data":[1]}`, false),
	})
	require.True(t, ok)
	assert.Equal(t, `{"data":[1]}`, content)
	assert.Equal(t, toolcore.NBToolResponseStatusSuccess, status)

	_, _, ok = newestMetricsStep([]core.ToolInvocation{step("", true)})
	assert.False(t, ok)
}
