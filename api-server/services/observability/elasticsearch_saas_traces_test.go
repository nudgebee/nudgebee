package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEsTraceIndexFor(t *testing.T) {
	cases := map[string]string{
		"":                        esTraceIndex, // unset -> Data Prepper default
		"otel-v1-apm-span-*":      "otel-v1-apm-span-*",
		"acme-otel-v1-apm-span-*": "acme-otel-v1-apm-span-*", // renamed Data Prepper index
	}
	for configured, want := range cases {
		assert.Equal(t, want, esTraceIndexFor(&ElasticsearchConfig{TraceIndex: configured}, ""), "trace_index %q", configured)
	}

	// An explicit per-request override wins over cfg.TraceIndex and the default.
	assert.Equal(t, "picked-*", esTraceIndexFor(&ElasticsearchConfig{TraceIndex: "cfg-*"}, "picked-*"))
	assert.Equal(t, "picked-*", esTraceIndexFor(&ElasticsearchConfig{}, "picked-*"))
	assert.Equal(t, "cfg-*", esTraceIndexFor(&ElasticsearchConfig{TraceIndex: "cfg-*"}, "")) // empty override -> fall back
}

func TestTraceIndexOverride(t *testing.T) {
	assert.Equal(t, "", traceIndexOverride(nil))
	assert.Equal(t, "", traceIndexOverride(map[string]any{}))
	assert.Equal(t, "", traceIndexOverride(map[string]any{"index": "  "})) // whitespace trimmed to ""
	assert.Equal(t, "traces-*", traceIndexOverride(map[string]any{"index": "traces-*"}))
	assert.Equal(t, "traces-*", traceIndexOverride(map[string]any{"index": "  traces-*  "}))
	assert.Equal(t, "", traceIndexOverride(map[string]any{"index": 123})) // non-string ignored
}
