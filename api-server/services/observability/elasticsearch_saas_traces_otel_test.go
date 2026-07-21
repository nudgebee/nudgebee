package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMapOtelHitToTrace(t *testing.T) {
	src := map[string]any{
		"@timestamp":     "1781559826466.265767",
		"trace_id":       "abc",
		"span_id":        "def",
		"parent_span_id": "",
		"name":           "GET /x",
		"kind":           float64(2),
		"duration":       float64(1500000),
		"status":         map[string]any{"code": float64(2)},
		"resource": map[string]any{
			"attributes": map[string]any{
				"service.name":        "api",
				"k8s.deployment.name": "api",
				"k8s.namespace.name":  "prod",
			},
		},
		"attributes": map[string]any{
			"http.status_code": "500",
			"http.url":         "http://x/y",
			"net.peer.name":    "db",
		},
	}

	trace := mapOtelHitToTrace(src)

	assert.Equal(t, "abc", trace.TraceID)
	assert.Equal(t, "def", trace.SpanID)
	assert.Equal(t, "GET /x", trace.SpanName)
	assert.Equal(t, "api", trace.ServiceName)
	assert.Equal(t, "api", trace.WorkloadName)
	assert.Equal(t, "prod", trace.WorkloadNamespace)
	assert.Equal(t, "Server", trace.SpanKind)
	assert.Equal(t, "Error", trace.StatusCode)
	assert.Equal(t, int64(1500000), trace.DurationNs)
	assert.Equal(t, "500", trace.HTTPStatusCode)
	assert.Equal(t, "http://x/y", trace.Resource)
	assert.Equal(t, "db", trace.DestinationName)
}

func TestMapOtelHitToTraceStringVariants(t *testing.T) {
	src := map[string]any{
		"@timestamp": "1781559826466",
		"trace_id":   "abc",
		"name":       "GET /x",
		"kind":       "SPAN_KIND_CLIENT",
		"duration":   float64(10),
		"status":     map[string]any{"code": "Error"},
		"resource": map[string]any{
			"attributes": map[string]any{"service.name": "api"},
		},
	}

	trace := mapOtelHitToTrace(src)

	assert.Equal(t, "Client", trace.SpanKind)
	assert.Equal(t, "Error", trace.StatusCode)
}

func TestMapOtelSpanKind(t *testing.T) {
	assert.Equal(t, "Internal", mapOtelSpanKind(float64(1)))
	assert.Equal(t, "Server", mapOtelSpanKind(float64(2)))
	assert.Equal(t, "Client", mapOtelSpanKind(float64(3)))
	assert.Equal(t, "Producer", mapOtelSpanKind(float64(4)))
	assert.Equal(t, "Consumer", mapOtelSpanKind(float64(5)))
	assert.Equal(t, "Server", mapOtelSpanKind("SPAN_KIND_SERVER"))
	assert.Equal(t, "Client", mapOtelSpanKind("Client"))
	assert.Equal(t, "", mapOtelSpanKind(float64(0)))
	assert.Equal(t, "", mapOtelSpanKind("bogus"))
	assert.Equal(t, "", mapOtelSpanKind(nil))
}

func TestMapOtelStatusCode(t *testing.T) {
	assert.Equal(t, "Unset", mapOtelStatusCode(float64(0)))
	assert.Equal(t, "Ok", mapOtelStatusCode(float64(1)))
	assert.Equal(t, "Error", mapOtelStatusCode(float64(2)))
	assert.Equal(t, "Error", mapOtelStatusCode("Error"))
	assert.Equal(t, "Ok", mapOtelStatusCode("STATUS_CODE_OK"))
	assert.Equal(t, "", mapOtelStatusCode("bogus"))
	assert.Equal(t, "", mapOtelStatusCode(nil))
}

func TestIsOtelNativeTraceIndex(t *testing.T) {
	assert.True(t, isOtelNativeTraceIndex("traces-generic.otel-default"))
	assert.True(t, isOtelNativeTraceIndex("logs-generic.otel-default")) // any .otel index
	assert.True(t, isOtelNativeTraceIndex("traces-my"))                 // traces-* prefix
	assert.False(t, isOtelNativeTraceIndex("otel-v1-apm-span-*"))       // Data Prepper schema
	assert.False(t, isOtelNativeTraceIndex(""))
	assert.False(t, isOtelNativeTraceIndex("  "))
}
