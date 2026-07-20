package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCombineChronosphereResponses_MultiResponse exercises the multi-response
// combine path, which previously panicked on a chained, unchecked type
// assertion (agentData.(map[string]any)["data"]) when a response's "data"
// field was not a map.
func TestCombineChronosphereResponses_MultiResponse(t *testing.T) {
	c := &ChronosphereTraceSource{}

	t.Run("non-map data field does not panic and is skipped", func(t *testing.T) {
		responses := []map[string]any{
			{"data": "not-a-map"}, // previously panicked here
			{"data": map[string]any{"data": map[string]any{"traces": []any{"t1", "t2"}}}},
		}
		var got map[string]any
		assert.NotPanics(t, func() {
			got = c.combineChronosphereResponses(responses)
		})
		traces, ok := got["traces"].([]any)
		assert.True(t, ok)
		// only the well-formed response contributes traces
		assert.Equal(t, []any{"t1", "t2"}, traces)
	})

	t.Run("nil and missing-data responses are skipped", func(t *testing.T) {
		responses := []map[string]any{
			nil,
			{"other": "x"}, // no "data" key
			{"data": map[string]any{"data": map[string]any{"traces": []any{"only"}}}},
		}
		var got map[string]any
		assert.NotPanics(t, func() {
			got = c.combineChronosphereResponses(responses)
		})
		assert.Equal(t, []any{"only"}, got["traces"])
	})

	t.Run("combines traces across well-formed responses", func(t *testing.T) {
		responses := []map[string]any{
			{"data": map[string]any{"data": map[string]any{"traces": []any{"a"}}}},
			{"data": map[string]any{"data": map[string]any{"traces": []any{"b", "c"}}}},
		}
		got := c.combineChronosphereResponses(responses)
		assert.ElementsMatch(t, []any{"a", "b", "c"}, got["traces"].([]any))
	})
}

func TestCombineChronosphereResponses_Empty(t *testing.T) {
	c := &ChronosphereTraceSource{}
	got := c.combineChronosphereResponses([]map[string]any{})
	assert.Equal(t, map[string]any{"traces": []any{}}, got)
}

// TestChronosphereTraceLabelMapping_ExposesVocabulary guards the Phase-2 §0 change:
// both Chronosphere trace sources must expose the field vocabulary (via GetLabelMapping)
// so the integration-agnostic (v2) trace agent advertises Chronosphere's real fields
// through provider capabilities instead of falling back to the generic schema.
func TestChronosphereTraceLabelMapping_ExposesVocabulary(t *testing.T) {
	want := []string{
		"service_name", "trace_id", "span_id", "component", "controller",
		"controller.action", "deployment.environment", "k8s_cluster",
		"http.host", "http.method", "http.status_code", "http.url",
		"request.format", "process.command",
	}
	for _, m := range []map[string]string{
		(&ChronosphereTraceSource{}).GetLabelMapping(),
		(&ChronosphereTraceSaasSource{}).GetLabelMapping(),
	} {
		assert.NotEmpty(t, m, "chronosphere trace label mapping must not be empty")
		for _, k := range want {
			assert.Contains(t, m, k, "label mapping must expose %q", k)
		}
	}
}
