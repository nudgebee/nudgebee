package observability

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time proof the source satisfies the full interface. Every method is required —
// getTraceSource returns it as a TraceSource, so a missing one is a build failure here
// rather than a nil-method panic at request time.
var _ TraceSource = (*SplunkEnterpriseTraceSource)(nil)

func TestBuildSplunkEnterpriseTraceSPL(t *testing.T) {
	t.Run("canonical shape", func(t *testing.T) {
		spl, err := buildSplunkEnterpriseTraceSPL(TracesV3Request{}, "otel_traces")
		require.NoError(t, err)
		assert.Equal(t, `search index="otel_traces" | head 100 | fields *`, spl)
		// The shared validator must accept what this builder emits, or every span search
		// is refused before it runs.
		assert.NoError(t, validateSplunkEnterpriseQuery(spl))
	})

	t.Run("fields * is not optional", func(t *testing.T) {
		// Splunk only returns a field the search REFERENCES. Without this every span
		// comes back with the default fields only and each mapped column renders blank —
		// the same defect that was fixed for logs.
		spl, err := buildSplunkEnterpriseTraceSPL(TracesV3Request{}, "otel_traces")
		require.NoError(t, err)
		assert.Contains(t, spl, "| fields *")
	})

	t.Run("canonical filters are mapped onto span fields", func(t *testing.T) {
		req := TracesV3Request{QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{Binary: query.BinaryWhereClause{
				"workload_name": {query.Eq: "checkout"},
			}},
		}}
		spl, err := buildSplunkEnterpriseTraceSPL(req, "otel_traces")
		require.NoError(t, err)
		assert.Contains(t, spl, `service.name="checkout"`)
	})

	t.Run("unsafe index is rejected", func(t *testing.T) {
		_, err := buildSplunkEnterpriseTraceSPL(TracesV3Request{}, `bad" index`)
		assert.Error(t, err)
	})

	t.Run("limit is clamped", func(t *testing.T) {
		req := TracesV3Request{QueryRequest: TracesQueryBuilderRequest{Limit: 999999}}
		spl, err := buildSplunkEnterpriseTraceSPL(req, "otel_traces")
		require.NoError(t, err)
		assert.Contains(t, spl, "| head 10000")
	})
}

// A filter group whose every predicate is inexpressible must FAIL, not render empty.
// An empty render is no restriction at all: harmless inside an OR, but silently matching
// every span inside an AND — a filter that cannot be honoured must never widen the result.
func TestSplunkEnterpriseTraceWhereClauseRefusesFullyDroppedGroup(t *testing.T) {
	where := query.QueryWhereClause{Binary: query.BinaryWhereClause{
		"destination_workload_namespace": {query.Eq: "prod"},
	}}
	_, err := buildSplunkEnterpriseTraceWhereClause(where)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no field this span schema can express")
}

func TestSplunkEnterpriseTraceWhereClauseKeepsExpressibleSiblings(t *testing.T) {
	where := query.QueryWhereClause{Binary: query.BinaryWhereClause{
		"destination_workload_namespace": {query.Eq: "prod"},
		"workload_name":                  {query.Eq: "checkout"},
	}}
	got, err := buildSplunkEnterpriseTraceWhereClause(where)
	require.NoError(t, err)
	assert.Contains(t, got, `service.name="checkout"`)
	assert.NotContains(t, got, "destination_workload_namespace")
}

func TestStripSplunkEnterpriseUnsupportedTraceFieldsDoesNotMutateInput(t *testing.T) {
	// The map belongs to the caller's request, which other providers in a fan-out may
	// still read.
	binary := query.BinaryWhereClause{
		"destination_workload_namespace": {query.Eq: "prod"},
		"workload_name":                  {query.Eq: "checkout"},
	}
	kept, dropped := stripSplunkEnterpriseUnsupportedTraceFields(binary)
	assert.Equal(t, 1, dropped)
	assert.Len(t, kept, 1)
	assert.Len(t, binary, 2, "the caller's clause must be untouched")
}

func TestSplunkEnterpriseSpanNanos(t *testing.T) {
	// Magnitude is what disambiguates the unit. Reading a seconds-scale value as
	// nanoseconds would date every span to 1970 and make every duration nonsense.
	cases := []struct {
		name string
		in   string
		want int64
	}{
		{"nanoseconds pass through", "1787851349000000000", 1787851349000000000},
		{"microseconds are scaled", "1787851349000000", 1787851349000000000},
		{"milliseconds are scaled", "1787851349000", 1787851349000000000},
		{"seconds are scaled", "1787851349", 1787851349000000000},
		{"fractional seconds keep precision", "1787851349.500", 1787851349500000000},
		{"ISO 8601 is accepted", "2026-08-27T17:22:29Z", 1787851349000000000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := splunkEnterpriseSpanNanos(tc.in)
			require.True(t, ok)
			assert.Equal(t, tc.want, got)
		})
	}

	for _, bad := range []string{"", "   ", "not-a-time", "0"} {
		_, ok := splunkEnterpriseSpanNanos(bad)
		assert.False(t, ok, "%q must not parse", bad)
	}
}

func TestSplunkEnterpriseSpanDurationNs(t *testing.T) {
	t.Run("a precomputed duration wins", func(t *testing.T) {
		row := map[string]any{"duration_ns": "1500", "start_time": "1", "end_time": "2"}
		assert.Equal(t, int64(1500), splunkEnterpriseSpanDurationNs(row))
	})

	t.Run("otherwise end minus start", func(t *testing.T) {
		row := map[string]any{
			"start_time": "1787851349000000000",
			"end_time":   "1787851349250000000",
		}
		assert.Equal(t, int64(250000000), splunkEnterpriseSpanDurationNs(row))
	})

	t.Run("a clock-skewed span reports zero rather than a negative duration", func(t *testing.T) {
		row := map[string]any{
			"start_time": "1787851349250000000",
			"end_time":   "1787851349000000000",
		}
		assert.Equal(t, int64(0), splunkEnterpriseSpanDurationNs(row))
	})

	t.Run("missing timestamps report zero", func(t *testing.T) {
		assert.Equal(t, int64(0), splunkEnterpriseSpanDurationNs(map[string]any{}))
	})
}

func TestConvertSplunkEnterpriseSpans(t *testing.T) {
	rows := []map[string]any{{
		"trace_id":                    "abc123",
		"span_id":                     "def456",
		"parent_span_id":              "",
		"name":                        "GET /checkout",
		"kind":                        "SPAN_KIND_SERVER",
		"status.code":                 "STATUS_CODE_ERROR",
		"status.message":              "upstream timeout",
		"service.name":                "checkout",
		"k8s.namespace.name":          "demo",
		"k8s.pod.name":                "checkout-7d9f-abc",
		"start_time":                  "1787851349000000000",
		"end_time":                    "1787851349250000000",
		"attributes.http.target":      "/checkout",
		"attributes.http.status_code": "503",
		"attributes.net.peer.name":    "payments",
		"attributes.custom.thing":     "kept",
		"resource.cloud.region":       "us-east1",
		"_bkt":                        "internal",
	}}

	got := convertSplunkEnterpriseSpans(rows)
	require.Len(t, got, 1)
	span := got[0]

	assert.Equal(t, "abc123", span.TraceID)
	assert.Equal(t, "def456", span.SpanID)
	assert.Equal(t, "GET /checkout", span.SpanName)
	assert.Equal(t, "GET /checkout", span.Operation)
	assert.Equal(t, "SPAN_KIND_SERVER", span.SpanKind)
	assert.Equal(t, "STATUS_CODE_ERROR", span.StatusCode)
	assert.Equal(t, "upstream timeout", span.StatusMessage)
	assert.Equal(t, "checkout", span.ServiceName)
	assert.Equal(t, "checkout", span.WorkloadName, "for OTel spans the service is the workload")
	assert.Equal(t, "demo", span.WorkloadNamespace)
	assert.Equal(t, "/checkout", span.Resource)
	assert.Equal(t, "503", span.HTTPStatusCode)
	assert.Equal(t, "payments", span.DestinationName)
	assert.Equal(t, int64(250000000), span.DurationNs)

	// The span's own start time, not the ingest time: the collector batches, so _time can
	// trail the span by the batch interval and the waterfall would lay out wrong.
	assert.Equal(t, "2026-08-27T17:22:29Z", span.Timestamp)
	assert.Equal(t, "1787851349000000000", span.StartTimeUnixNano)
	assert.Equal(t, "1787851349250000000", span.EndTimeUnixNano)

	// A field this schema does not name is still reachable rather than dropped.
	assert.Equal(t, "kept", span.SpanAttributes["custom.thing"])
	assert.Equal(t, "us-east1", span.ResourceAttributes["cloud.region"])
	assert.Equal(t, "demo", span.ResourceAttributes["k8s.namespace.name"])
	assert.Equal(t, "checkout-7d9f-abc", span.ResourceAttributes["k8s.pod.name"])

	// Splunk internals must not leak into the attribute maps as though they were span data.
	assert.NotContains(t, span.SpanAttributes, "_bkt")
	assert.NotContains(t, span.ResourceAttributes, "_bkt")
}

func TestConvertSplunkEnterpriseSpansFallsBackToIngestTime(t *testing.T) {
	// A pipeline that writes no start_time still has to produce a placeable span.
	rows := []map[string]any{{"trace_id": "a", "_time": "2026-08-27T17:22:29.000+00:00"}}
	got := convertSplunkEnterpriseSpans(rows)
	require.Len(t, got, 1)
	assert.Equal(t, "2026-08-27T17:22:29Z", got[0].Timestamp)
}

// The label dropdown is the one place a wrong quoting choice is silent: a query that
// matches nothing renders as "this field has no values", not as an error.
func TestSplunkEnterpriseTraceLabelValuesLeavesDottedFieldsUnquoted(t *testing.T) {
	spl := fmt.Sprintf(`search index="%s" %s=* | top limit=%d %s | fields %s`,
		"otel_traces", "service.name", splunkEnterpriseLabelValueLimit, "service.name", "service.name")
	assert.NotContains(t, spl, "'service.name'",
		"quoting a dotted field inside search/top/fields makes it match nothing in SPL")
	assert.Contains(t, spl, "top limit=100 service.name")
}

func TestIsSplunkInternalField(t *testing.T) {
	// Offering these as filters builds a predicate on an index-internal value that matches
	// no span, so the result reads as "no traces" rather than as a bad filter.
	for _, name := range []string{"_bkt", "_cd", "_raw", "_time", "punct", "linecount",
		"splunk_server", "eventtype", "sourcetype", "index", "host"} {
		assert.True(t, isSplunkInternalField(name), "%s must be hidden", name)
	}
	for _, name := range []string{"trace_id", "span_id", "service.name", "k8s.namespace.name",
		"attributes.http.target", "status.code"} {
		assert.False(t, isSplunkInternalField(name), "%s is real span data", name)
	}
}

func TestBuildSplunkEnterpriseTraceCountSPL(t *testing.T) {
	spl, err := buildSplunkEnterpriseTraceCountSPL(TracesV3Request{}, "otel_traces", false)
	require.NoError(t, err)
	assert.Equal(t, `search index="otel_traces" | stats count AS nb_tr_count`, spl)

	// dc() over trace_id is exact and cheap in Splunk, so the "By Traces" view gets a real
	// count instead of the -1 estimate providers without one must return.
	distinct, err := buildSplunkEnterpriseTraceCountSPL(TracesV3Request{}, "otel_traces", true)
	require.NoError(t, err)
	assert.Equal(t, `search index="otel_traces" | stats dc(trace_id) AS nb_tr_distinct`, distinct)
}

func TestSplunkEnterpriseCountFromRows(t *testing.T) {
	assert.Equal(t, 7, splunkEnterpriseCountFromRows([]map[string]any{{"nb_tr_count": "7"}}, "nb_tr_count"))
	assert.Equal(t, 0, splunkEnterpriseCountFromRows(nil, "nb_tr_count"))
	assert.Equal(t, 0, splunkEnterpriseCountFromRows([]map[string]any{{"other": "7"}}, "nb_tr_count"))
	assert.Equal(t, 0, splunkEnterpriseCountFromRows([]map[string]any{{"nb_tr_count": "-3"}}, "nb_tr_count"))
}

func TestBuildSplunkEnterpriseTraceGroupSPL(t *testing.T) {
	spl, err := buildSplunkEnterpriseTraceGroupSPL(TracesV3Request{}, "otel_traces", 0)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(spl, `search index="otel_traces" | eval `), spl)
	assert.NoError(t, validateSplunkEnterpriseQuery(spl))

	t.Run("every grouped field is coalesced to a non-null default", func(t *testing.T) {
		// Same load-bearing property as the log-group query: `stats ... BY f` drops every
		// event where f is null, so one absent span field would empty the whole view.
		for _, alias := range []string{
			splunkTraceServiceCol, splunkTraceNamespaceCol, splunkTraceNameCol,
			splunkTraceResourceCol, splunkTraceStatusCol, splunkTraceHTTPCodeCol,
			splunkTraceDestCol,
		} {
			idx := strings.Index(spl, alias+"=coalesce(")
			require.NotEqual(t, -1, idx, "%s must be assigned by coalesce", alias)
			end := strings.Index(spl[idx:], ")")
			require.NotEqual(t, -1, end)
			assert.True(t, strings.HasSuffix(spl[idx:idx+end], `, ""`),
				"%s must fall back to an empty literal, got %q", alias, spl[idx:idx+end])
		}
	})

	t.Run("duration is numeric so the percentiles are arithmetic", func(t *testing.T) {
		// Splunk returns HEC fields as strings; perc99 over strings would compare
		// lexicographically and report a "tail latency" that is simply the longest digits.
		assert.Contains(t, spl, `tonumber('duration_ns')`)
		assert.Contains(t, spl, `tonumber('start_time')`)
		assert.Contains(t, spl, `perc99(nb_tr_duration) AS nb_tr_p99`)
		assert.Contains(t, spl, `perc95(nb_tr_duration) AS nb_tr_p95`)
	})

	t.Run("the error count accepts both status spellings", func(t *testing.T) {
		// OTel writes STATUS_CODE_ERROR; some pipelines write the numeric 2.
		assert.Contains(t, spl, `match(nb_tr_status, "(?i)error")`)
		assert.Contains(t, spl, `nb_tr_status=2`)
	})

	t.Run("limit is clamped", func(t *testing.T) {
		wide, err := buildSplunkEnterpriseTraceGroupSPL(TracesV3Request{}, "otel_traces", 99999)
		require.NoError(t, err)
		assert.Contains(t, wide, "| head 100")
	})
}

func TestConvertSplunkEnterpriseTraceGroups(t *testing.T) {
	rows := []map[string]any{{
		splunkTraceServiceCol:   "checkout",
		splunkTraceNamespaceCol: "demo",
		splunkTraceNameCol:      "GET /checkout",
		splunkTraceResourceCol:  "/checkout",
		splunkTraceDestCol:      "payments",
		splunkTraceHTTPCodeCol:  "503",
		splunkTraceCountCol:     "40",
		splunkTraceErrorsCol:    "6",
		splunkTraceP99Col:       "990000000",
		splunkTraceP95Col:       "450000000",
		splunkTraceMaxCol:       "1200000000",
	}}

	got := convertSplunkEnterpriseTraceGroups(rows)
	require.Len(t, got, 1)
	g := got[0]
	assert.Equal(t, 40, g.Count)
	assert.Equal(t, 6, g.ErrorCount)
	assert.Equal(t, int64(990000000), g.P99Latency)
	assert.Equal(t, int64(450000000), g.P95Latency)
	assert.Equal(t, int64(1200000000), g.MaxLatency)
	assert.Equal(t, "checkout", g.WorkloadName)
	assert.Equal(t, "demo", g.WorkloadNamespace)
	assert.Equal(t, "payments", g.DestinationWorkloadName)
	assert.Equal(t, "GET /checkout", g.SpanName)
	assert.Equal(t, "503", g.HTTPStatusCode)
	// The row's representative latency is the p99, not a mean: the group view exists to
	// surface tail latency, and a mean hides exactly what it is for.
	assert.Equal(t, int64(990000000), g.DurationNS)

	assert.Empty(t, convertSplunkEnterpriseTraceGroups([]map[string]any{{"other": "1"}}),
		"a row with no count is not a group")
}

func TestSplunkEnterpriseHeatmapWindow(t *testing.T) {
	now := time.UnixMilli(1787851349000).UTC()

	t.Run("a supplied window is padded on both sides", func(t *testing.T) {
		// The window comes from the listing row's timestamp, but a trace's earliest span
		// can begin before it and its latest can end after; clipping truncates the
		// waterfall.
		start, end := splunkEnterpriseHeatmapWindow(1000000, 2000000, now)
		assert.Equal(t, int64(1000000-300000), start)
		assert.Equal(t, int64(2000000+300000), end)
	})

	t.Run("no window falls back to a long lookback", func(t *testing.T) {
		// The trace-detail view asks by trace_id alone. A one-hour default returns zero
		// spans for any older trace, which reads as a trace that does not exist.
		start, end := splunkEnterpriseHeatmapWindow(0, 0, now)
		assert.Equal(t, now.UnixMilli(), end)
		assert.Equal(t, now.Add(-splunkEnterpriseHeatmapDefaultLookback).UnixMilli(), start)
	})
}

func TestSplunkEnterpriseTraceLabelMappingIsDisplayable(t *testing.T) {
	// A field that filters on one Splunk field but displays from another silently returns
	// nothing when a user clicks a row. Every mapped target must therefore appear among
	// the candidates the converter reads.
	displayed := map[string][]string{
		"trace_id":                  splunkEnterpriseTraceFields.TraceID,
		"span_id":                   splunkEnterpriseTraceFields.SpanID,
		"parent_id":                 splunkEnterpriseTraceFields.ParentSpanID,
		"span_name":                 splunkEnterpriseTraceFields.SpanName,
		"status_code":               splunkEnterpriseTraceFields.StatusCode,
		"workload_name":             splunkEnterpriseTraceFields.ServiceName,
		"workload_namespace":        splunkEnterpriseTraceFields.Namespace,
		"http_status_code":          splunkEnterpriseTraceFields.HTTPStatusCode,
		"resource":                  splunkEnterpriseTraceFields.Resource,
		"destination_workload_name": splunkEnterpriseTraceFields.Destination,
	}
	for canonical, candidates := range displayed {
		target, ok := splunkEnterpriseTraceLabelMapping[canonical]
		require.True(t, ok, "%s must be mapped", canonical)
		assert.Contains(t, candidates, target,
			"%s filters on %q, so the converter must also display from it", canonical, target)
	}
}
