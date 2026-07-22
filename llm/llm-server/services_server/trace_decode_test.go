package services_server

import (
	"testing"

	core "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecodeTraceQueryBody covers the version-skew-tolerant decode for the free-form ClickHouse
// trace path: the upgraded services-server returns the {result:{...}} object, while an older one
// (or a non-clickhouse fallthrough) returns the bare typed array. Both must decode without error.
func TestDecodeTraceQueryBody(t *testing.T) {
	t.Run("object shape with raw result", func(t *testing.T) {
		body := []byte(`{"result":{"columns":["service","p95_latency_ns"],"column_types":["String","Float64"],"rows":[["notifications",84213000]]}}`)
		var resp core.ObservabilityTraceResponse
		require.NoError(t, decodeTraceQueryBody(body, true, &resp))

		require.NotNil(t, resp.Result, "raw result must be populated from the object body")
		assert.Equal(t, []string{"service", "p95_latency_ns"}, resp.Result.Columns)
		require.Len(t, resp.Result.Rows, 1)
		assert.EqualValues(t, 84213000, resp.Result.Rows[0][1])
		assert.Empty(t, resp.Traces, "typed array must stay empty on the raw path")
	})

	t.Run("legacy bare array falls back to typed traces", func(t *testing.T) {
		// includeRaw=true but an older services-server returned a bare array (no result object).
		body := []byte(`[{"service_name":"notifications","duration_ns":84213000,"span_name":"GET /notify"}]`)
		var resp core.ObservabilityTraceResponse
		require.NoError(t, decodeTraceQueryBody(body, true, &resp))

		assert.Nil(t, resp.Result, "no raw result in a bare-array body")
		require.Len(t, resp.Traces, 1)
		assert.Equal(t, "notifications", resp.Traces[0].ServiceName)
		assert.EqualValues(t, 84213000, resp.Traces[0].DurationNs)
	})

	t.Run("non-raw path decodes typed array", func(t *testing.T) {
		body := []byte(`[{"service_name":"checkout","duration_ns":41020000}]`)
		var resp core.ObservabilityTraceResponse
		require.NoError(t, decodeTraceQueryBody(body, false, &resp))

		assert.Nil(t, resp.Result)
		require.Len(t, resp.Traces, 1)
		assert.Equal(t, "checkout", resp.Traces[0].ServiceName)
	})
}
