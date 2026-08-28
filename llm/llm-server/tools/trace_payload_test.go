package tools

import (
	"testing"

	"nudgebee/llm/common"
	core "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// An empty trace result must carry the diagnosis to the agent rather than a bare "[]" it can only
// guess at — that guessing is what drives it to re-issue near-identical queries.
func TestTracePayloadForResponse(t *testing.T) {
	t.Run("empty with a suggestion hands back the whole object", func(t *testing.T) {
		resp := core.ObservabilityTraceResponse{
			Traces:     []core.ObservabilityTrace{},
			Suggestion: `no traces matched: unknown label name(s) [namespace] for this trace provider; closest valid label(s): [workload_namespace]`,
		}
		payload, ok := tracePayloadForResponse(resp).(core.ObservabilityTraceResponse)
		assert.True(t, ok, "the full response must travel so the suggestion is not dropped")
		assert.Contains(t, payload.Suggestion, "workload_namespace")
	})

	t.Run("empty without a suggestion stays the bare array", func(t *testing.T) {
		resp := core.ObservabilityTraceResponse{Traces: []core.ObservabilityTrace{}}
		_, isArray := tracePayloadForResponse(resp).([]core.ObservabilityTrace)
		assert.True(t, isArray, "a genuinely empty result keeps the existing shape")
	})

	t.Run("nil traces marshal as [] rather than null", func(t *testing.T) {
		// A provider returning a nil slice must not reach the agent as "null" — that reads as a
		// broken tool rather than a window with no spans in it.
		resp := core.ObservabilityTraceResponse{Traces: nil}
		payload := tracePayloadForResponse(resp)
		spans, isArray := payload.([]core.ObservabilityTrace)
		assert.True(t, isArray)
		assert.NotNil(t, spans, "nil would serialize to null")

		encoded, err := common.MarshalJson(payload)
		assert.NoError(t, err)
		assert.Equal(t, "[]", string(encoded))
	})

	t.Run("spans present stay the bare array", func(t *testing.T) {
		resp := core.ObservabilityTraceResponse{
			Traces:     []core.ObservabilityTrace{{ServiceName: "checkout"}},
			Suggestion: "ignored when there are spans",
		}
		spans, isArray := tracePayloadForResponse(resp).([]core.ObservabilityTrace)
		assert.True(t, isArray)
		assert.Len(t, spans, 1)
	})
}
