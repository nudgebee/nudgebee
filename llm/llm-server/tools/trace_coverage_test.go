package tools

import (
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEffectiveTraceLimit(t *testing.T) {
	cases := []struct {
		name           string
		configLimit    int
		configHasLimit bool
		builderLimit   int
		want           int
	}{
		{name: "no limit anywhere: the ceiling", configLimit: maxTraceQueryLimit, want: maxTraceQueryLimit},
		{name: "the model's own limit in the query counts", configLimit: maxTraceQueryLimit, builderLimit: 50, want: 50},
		{name: "a config limit wins over the query's", configLimit: 20, configHasLimit: true, builderLimit: 50, want: 20},
		{name: "above the ceiling: lowered to it", configLimit: maxTraceQueryLimit, builderLimit: 5000, want: maxTraceQueryLimit},
		{name: "zero from config: the ceiling", configLimit: 0, configHasLimit: true, want: maxTraceQueryLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, effectiveTraceLimit(tc.configLimit, tc.configHasLimit, tc.builderLimit))
		})
	}
}

func traces(n int) []core.ObservabilityTrace {
	return make([]core.ObservabilityTrace, n)
}

func TestCapTraceResponse(t *testing.T) {
	meta := core.ObservabilityTraceMetadata{StartTime: 1768451400000, EndTime: 1768455000000, Limit: 1000}

	t.Run("more than the tool keeps: cut and said", func(t *testing.T) {
		resp := capTraceResponse(core.ObservabilityTraceResponse{Traces: traces(250), Metadata: meta}, 100)
		assert.Len(t, resp.Traces, 100)
		require.NotNil(t, resp.Coverage)
		assert.Equal(t, 250, resp.Coverage.Received)
		assert.Equal(t, 100, resp.Coverage.Returned)
		assert.Contains(t, resp.Coverage.Note, "the backend returned 250 traces and only the first 100 are shown")
		assert.Contains(t, resp.Coverage.Note, "window 2026-01-15T04:30:00Z to 2026-01-15T05:30:00Z")
	})

	t.Run("exactly the query's limit came back: may hold more", func(t *testing.T) {
		m := meta
		m.Limit = 50
		resp := capTraceResponse(core.ObservabilityTraceResponse{Traces: traces(50), Metadata: m}, 100)
		assert.Len(t, resp.Traces, 50)
		require.NotNil(t, resp.Coverage)
		assert.Contains(t, resp.Coverage.Note, "the query stopped at its limit of 50 traces")
	})

	t.Run("under both caps: nothing to say", func(t *testing.T) {
		resp := capTraceResponse(core.ObservabilityTraceResponse{Traces: traces(12), Metadata: meta}, 100)
		assert.Len(t, resp.Traces, 12)
		assert.Nil(t, resp.Coverage)
	})
}

func TestCapClickhouseRows(t *testing.T) {
	rows := func(n int) [][]any {
		out := make([][]any, n)
		for i := range out {
			out[i] = []any{i}
		}
		return out
	}

	t.Run("one row past the cap: the result was cut", func(t *testing.T) {
		resp := capClickhouseRows(core.ObservabilityTraceResponse{Result: &core.ObservabilityTraceRawTable{Rows: rows(101)}}, 100)
		assert.Len(t, resp.Result.Rows, 100)
		require.NotNil(t, resp.Coverage)
		assert.Contains(t, resp.Coverage.Note, "returned more than 100 rows")

		out, err := serializeClickhouseTraceResponse(resp)
		require.NoError(t, err)
		assert.Contains(t, string(out), `"coverage":{"limit":100`, "the agent sees the coverage")
	})

	t.Run("an older services-server's typed array is cut too", func(t *testing.T) {
		resp := capClickhouseRows(core.ObservabilityTraceResponse{Traces: traces(400)}, 100)
		assert.Len(t, resp.Traces, 100)
		require.NotNil(t, resp.Coverage)
		assert.Equal(t, 400, resp.Coverage.Received, "the count that actually came back")

		out, err := serializeClickhouseTraceResponse(resp)
		require.NoError(t, err)
		assert.Contains(t, string(out), `"coverage":{"limit":100`)
	})

	t.Run("at or under the cap: untouched", func(t *testing.T) {
		resp := capClickhouseRows(core.ObservabilityTraceResponse{Result: &core.ObservabilityTraceRawTable{Rows: rows(100)}}, 100)
		assert.Len(t, resp.Result.Rows, 100)
		assert.Nil(t, resp.Coverage)

		out, err := serializeClickhouseTraceResponse(resp)
		require.NoError(t, err)
		assert.NotContains(t, string(out), "coverage")
	})
}
