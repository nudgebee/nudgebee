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
		assert.Equal(t, want, esTraceIndexFor(&ElasticsearchConfig{TraceIndex: configured}), "trace_index %q", configured)
	}
}
