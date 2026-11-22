package agents

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseESMetricsQueryResponse(t *testing.T) {
	t.Run("full search body passes through and is pretty-printed", func(t *testing.T) {
		raw := `{"index":"metrics-kubernetes.*","query":{"query":{"bool":{"filter":[{"term":{"kubernetes.namespace":"nudgebee"}}]}}}}`

		dsl, index, err := parseESMetricsQueryResponse(raw)

		require.NoError(t, err)
		assert.Equal(t, "metrics-kubernetes.*", index)
		assert.Contains(t, dsl, "\n", "query should be indented for the editor")

		var body map[string]any
		require.NoError(t, json.Unmarshal([]byte(dsl), &body))
		assert.Contains(t, body, "query")
	})

	// The execution path wraps a bare clause too (esWrapBareQueryClause in
	// api-server), so wrapping here keeps the editor showing what will actually run.
	t.Run("bare filter clause is wrapped into a search body", func(t *testing.T) {
		raw := `{"index":"metrics-*","query":{"bool":{"filter":[{"exists":{"field":"kubernetes.container.cpu.usage.nanocores"}}]}}}`

		dsl, _, err := parseESMetricsQueryResponse(raw)

		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, json.Unmarshal([]byte(dsl), &body))
		inner, ok := body["query"].(map[string]any)
		require.True(t, ok, "bare clause should be nested under a `query` key")
		assert.Contains(t, inner, "bool")
	})

	t.Run("fenced response is unwrapped", func(t *testing.T) {
		raw := "```json\n{\"index\":\"metrics-*\",\"query\":{\"query\":{\"match_all\":{}}}}\n```"

		dsl, index, err := parseESMetricsQueryResponse(raw)

		require.NoError(t, err)
		assert.Equal(t, "metrics-*", index)
		assert.Contains(t, dsl, "match_all")
	})

	t.Run("missing index is not an error - the caller falls back", func(t *testing.T) {
		dsl, index, err := parseESMetricsQueryResponse(`{"query":{"query":{"match_all":{}}}}`)

		require.NoError(t, err)
		assert.Empty(t, index)
		assert.Contains(t, dsl, "match_all")
	})

	// Every rejection below would otherwise put unusable text in the user's editor.
	t.Run("rejects prose", func(t *testing.T) {
		_, _, err := parseESMetricsQueryResponse("Here is the query you asked for.")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "did not return JSON")
	})

	t.Run("rejects a response with no query key", func(t *testing.T) {
		_, _, err := parseESMetricsQueryResponse(`{"index":"metrics-*"}`)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no `query` object")
	})

	t.Run("rejects a query that is not an object", func(t *testing.T) {
		_, _, err := parseESMetricsQueryResponse(`{"index":"metrics-*","query":"bool filter"}`)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a JSON object")
	})

	t.Run("rejects a null query", func(t *testing.T) {
		_, _, err := parseESMetricsQueryResponse(`{"index":"metrics-*","query":null}`)

		require.Error(t, err)
	})
}

// The generator gets one shot with no retry loop, so the rules that decide whether its
// single query returns data have to be in the prompt. Asserting them here keeps them
// from being dropped in a future edit without an LLM round trip.
func TestBuildESMetricsQueryPrompt(t *testing.T) {
	schemas := map[esMetricSchema]bool{schemaElasticK8s: true}
	targets := map[esMetricSchema]schemaTarget{
		schemaElasticK8s: {index: "metrics-kubernetes.*", timestamp: esTimestampECS},
	}

	prompt := buildESMetricsQueryPrompt(schemas, []string{"metrics-kubernetes.*"}, esTimestampECS, targets, "metrics-*")

	t.Run("states the JSON output contract", func(t *testing.T) {
		assert.Contains(t, prompt, "OUTPUT CONTRACT")
		assert.Contains(t, prompt, `"index"`)
		assert.Contains(t, prompt, "not a string")
	})

	// A `term` on a workload name matches zero documents because pod names carry a
	// generated suffix — the confirmed cause of an empty result in a traced run.
	t.Run("carries the workload-name rule", func(t *testing.T) {
		assert.Contains(t, prompt, "Workload names are NOT pod names")
		assert.Contains(t, prompt, "prefix")
	})

	t.Run("carries the account's own schema facts", func(t *testing.T) {
		assert.Contains(t, prompt, "metrics-kubernetes.*")
		assert.Contains(t, prompt, esTimestampECS)
		assert.Contains(t, prompt, "kubernetes.container.cpu.usage.nanocores",
			"the shared field catalogue should reach the generator")
	})

	t.Run("allows aggregations for window answers, forbids log indices", func(t *testing.T) {
		assert.Contains(t, prompt, "use an aggregation with `size: 0`")
		assert.NotContains(t, prompt, "NEVER use `aggs`")
		assert.Contains(t, prompt, "fluentk8-*")
	})
}

func TestStripCodeFence(t *testing.T) {
	assert.Equal(t, `{"a":1}`, stripQueryCodeFence("```json\n{\"a\":1}\n```"))
	assert.Equal(t, `{"a":1}`, stripQueryCodeFence("```\n{\"a\":1}\n```"))
	assert.Equal(t, `{"a":1}`, stripQueryCodeFence(`{"a":1}`), "unfenced input is unchanged")
}
