package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCountEventsInResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{"empty", "", 0},
		{"no events", `{"foo":"bar"}`, 0},
		{"single event", `[{"event_id":"abc-123","title":"test"}]`, 1},
		{"five events", `[{"event_id":"1"},{"event_id":"2"},{"event_id":"3"},{"event_id":"4"},{"event_id":"5"}]`, 5},
		{"six events", `[{"event_id":"1"},{"event_id":"2"},{"event_id":"3"},{"event_id":"4"},{"event_id":"5"},{"event_id":"6"}]`, 6},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := countEventsInResponse(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestShouldSummarizeNow_ThresholdBoundary(t *testing.T) {
	agent := AgentEvents{}

	// ≤5 events should trigger auto-summarize
	fiveEvents := `[{"event_id":"1"},{"event_id":"2"},{"event_id":"3"},{"event_id":"4"},{"event_id":"5"}]`
	assert.True(t, agent.ShouldSummarizeNow("events_execute", fiveEvents))

	// >5 events should NOT auto-summarize (let ReAct loop handle)
	sixEvents := `[{"event_id":"1"},{"event_id":"2"},{"event_id":"3"},{"event_id":"4"},{"event_id":"5"},{"event_id":"6"}]`
	assert.False(t, agent.ShouldSummarizeNow("events_execute", sixEvents))

	// anomaly_execute should also work
	assert.True(t, agent.ShouldSummarizeNow("anomaly_execute", fiveEvents))
	assert.False(t, agent.ShouldSummarizeNow("anomaly_execute", sixEvents))

	// Non-event tools should never trigger summarize
	assert.False(t, agent.ShouldSummarizeNow("some_other_tool", fiveEvents))

	// Aggregated results (COUNT/GROUP BY) have no event_id — should NOT summarize
	aggregatedResult := `[{"aggregation_key":"pod_oom_killer","event_count":77},{"aggregation_key":"crash_loop","event_count":60}]`
	assert.False(t, agent.ShouldSummarizeNow("events_execute", aggregatedResult))

	// Empty result should NOT summarize
	assert.False(t, agent.ShouldSummarizeNow("events_execute", "[]"))
	assert.False(t, agent.ShouldSummarizeNow("events_execute", ""))
}

// TestSummarizeTraceSpan pins the trace-span projection used to feed spans to
// the LLM. Before this refactor the projection stripped span_attributes and
// resource_attributes (and had a CamelCase-vs-snake_case key mismatch), so
// error spans reached the LLM with nulls where the RCA signal lives
// (rpc.grpc.status_code, rpc.method, exception info).
//
// The tests lock in the new contract:
//  1. Error spans carry the full span_attributes, a compacted resource subset,
//     and any span-event exception info.
//  2. Non-error context spans (from trace-tree expansion) keep only a
//     call-graph slice of span_attributes to bound prompt size.
//  3. Error detection works for grpc non-zero and http 4xx/5xx even when the
//     span's status_code is Unset.
func TestSummarizeTraceSpan(t *testing.T) {
	errSpan := map[string]any{
		"trace_id":           "t1",
		"span_id":            "s1",
		"parent_span_id":     "p1",
		"service_name":       "checkout",
		"span_name":          "oteldemo.CartService/EmptyCart",
		"span_kind":          "SPAN_KIND_CLIENT",
		"status_code":        "STATUS_CODE_ERROR",
		"timestamp":          "2026-04-22T12:04:27Z",
		"duration_ns":        int64(547763325),
		"workload_name":      "checkout",
		"workload_namespace": "demo",
		"span_attributes": map[string]any{
			"rpc.grpc.status_code": "9",
			"rpc.method":           "EmptyCart",
			"rpc.service":          "oteldemo.CartService",
			"server.address":       "34.118.235.116",
		},
		"resource_attributes": map[string]any{
			"k8s.deployment.name":   "checkout",
			"k8s.namespace.name":    "demo",
			"k8s.pod.name":          "checkout-86db459f8-9hshw",
			"service.name":          "checkout",
			"process.command_args":  "[checkout]", // noisy — must be dropped
			"telemetry.sdk.version": "1.39.0",     // noisy — must be dropped
		},
		"events_attributes": []any{map[string]any{"exception.type": "CartError"}},
		"events_name":       []any{"exception"},
	}

	t.Run("error span keeps full span+event attrs and compacts resource attrs", func(t *testing.T) {
		got := summarizeTraceSpan(errSpan)
		assert.NotNil(t, got)
		assert.Equal(t, true, got["is_error"])
		sa, ok := got["span_attributes"].(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, "9", sa["rpc.grpc.status_code"])
		assert.Equal(t, "EmptyCart", sa["rpc.method"])
		ra, ok := got["resource_attributes"].(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, "checkout", ra["k8s.deployment.name"])
		_, hasProc := ra["process.command_args"]
		_, hasSdk := ra["telemetry.sdk.version"]
		assert.False(t, hasProc, "noisy process args must be dropped")
		assert.False(t, hasSdk, "noisy sdk version must be dropped")
		assert.NotNil(t, got["events_attributes"])
	})

	t.Run("context span keeps only call-graph slice of span_attributes", func(t *testing.T) {
		ctxSpan := map[string]any{
			"trace_id":     "t1",
			"span_id":      "s2",
			"service_name": "cart",
			"span_name":    "oteldemo.CartService/EmptyCart",
			"status_code":  "STATUS_CODE_UNSET",
			"span_attributes": map[string]any{
				"rpc.service":                    "oteldemo.CartService",
				"rpc.method":                     "EmptyCart",
				"rpc.grpc.status_code":           "0",
				"messaging.kafka.consumer.group": "cart", // must be dropped
			},
			"resource_attributes": map[string]any{"k8s.deployment.name": "cart"},
		}
		got := summarizeTraceSpan(ctxSpan)
		assert.NotNil(t, got)
		assert.Equal(t, false, got["is_error"])
		sa, _ := got["span_attributes"].(map[string]any)
		assert.Equal(t, "EmptyCart", sa["rpc.method"])
		_, hasKafka := sa["messaging.kafka.consumer.group"]
		assert.False(t, hasKafka, "non-rpc/http/db attr dropped for context spans")
		_, hasRA := got["resource_attributes"]
		assert.False(t, hasRA, "context spans skip resource_attributes")
	})

	t.Run("grpc non-zero classified as error with UNSET status_code", func(t *testing.T) {
		got := summarizeTraceSpan(map[string]any{
			"trace_id":        "t1",
			"span_id":         "s3",
			"status_code":     "STATUS_CODE_UNSET",
			"span_attributes": map[string]any{"rpc.grpc.status_code": "14"},
		})
		assert.Equal(t, true, got["is_error"])
	})

	t.Run("http 5xx classified as error with UNSET status_code", func(t *testing.T) {
		got := summarizeTraceSpan(map[string]any{
			"trace_id":        "t1",
			"span_id":         "s4",
			"status_code":     "STATUS_CODE_UNSET",
			"span_attributes": map[string]any{"http.status_code": "503"},
		})
		assert.Equal(t, true, got["is_error"])
	})

	t.Run("drops spans with no signal", func(t *testing.T) {
		assert.Nil(t, summarizeTraceSpan(map[string]any{}))
	})

	t.Run("nil input", func(t *testing.T) {
		assert.Nil(t, summarizeTraceSpan(nil))
	})

	// Regression: upstream producers sometimes deliver span_attributes as
	// map[string]string (the struct type on common.OpenTelemetryTrace). Without
	// the type coercion in attrMapAsAny, every attribute lookup silently missed
	// and RCA-critical fields (rpc.grpc.status_code, rpc.method) were nil in
	// the LLM prompt. Gemini review on PR #29235 flagged this class of bug.
	t.Run("map[string]string span_attributes are coerced", func(t *testing.T) {
		got := summarizeTraceSpan(map[string]any{
			"trace_id":    "t1",
			"span_id":     "s5",
			"status_code": "STATUS_CODE_UNSET",
			"span_attributes": map[string]string{
				"rpc.grpc.status_code": "9",
				"rpc.method":           "EmptyCart",
				"rpc.service":          "oteldemo.CartService",
			},
			"resource_attributes": map[string]string{
				"k8s.deployment.name": "cart",
				"service.name":        "cart",
			},
		})
		assert.NotNil(t, got)
		assert.Equal(t, true, got["is_error"], "grpc non-zero must be detected from map[string]string")
		sa, ok := got["span_attributes"].(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, "9", sa["rpc.grpc.status_code"])
		assert.Equal(t, "EmptyCart", sa["rpc.method"])
		ra, ok := got["resource_attributes"].(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, "cart", ra["k8s.deployment.name"])
	})

	// Regression: OTel attribute values can arrive as numeric primitives
	// (int64/float64) when the producer uses a typed map, e.g.
	// rpc.grpc.status_code = 14. A plain .(string) type assertion drops them.
	t.Run("numeric attribute values are coerced to strings", func(t *testing.T) {
		got := summarizeTraceSpan(map[string]any{
			"trace_id":    "t1",
			"span_id":     "s6",
			"status_code": "STATUS_CODE_UNSET",
			"span_attributes": map[string]any{
				"rpc.grpc.status_code": float64(14), // JSON-decoded numeric
				"http.status_code":     int64(503),  // struct-typed numeric
			},
		})
		assert.NotNil(t, got)
		assert.Equal(t, true, got["is_error"], "numeric grpc/http codes must be detected")
	})
}
