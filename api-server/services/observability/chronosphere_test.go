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
