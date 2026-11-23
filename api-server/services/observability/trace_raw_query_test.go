package observability

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Running a raw trace query is the contract for every trace provider, so this asserts the
// DEFAULT rather than a per-provider opt-in: a provider is advertised as supporting one
// unless it declares a hard backend limitation. Only SolarWinds does, and the reason it
// gives is the same string its QueryTraces refuses with.
//
// A new reader therefore needs no declaration here — it needs to build its query through
// the shared helpers like every other reader. What this test catches is the opposite
// mistake: quietly declaring a provider incapable to avoid wiring the query up.
func TestEveryTraceProviderRunsRawQueriesUnlessTheBackendCannot(t *testing.T) {
	const onlyException = "solarwinds"

	for _, p := range traceProviders {
		source, err := getTraceSource(p, "user")
		if err != nil {
			source, err = getTraceSource(p, "agent")
		}
		require.NoError(t, err, "%s is listed in traceProviders but getTraceSource has no reader for it", p)

		reason := rawTraceQueryUnsupportedReason(source)
		if p == onlyException {
			assert.NotEmpty(t, reason, "%s is the known exception and must state why it cannot run a raw query", p)
			continue
		}
		assert.Empty(t, reason,
			"%s declares that it cannot run a raw trace query (%q). Raw query execution is basic behaviour for every trace provider — wire TracesV3Request.Query through its QueryTraces using the shared helpers instead of declaring the provider incapable. Only a backend with no trace read endpoint at all qualifies.",
			p, reason)
	}
}

// The advertised capability follows the same rule, so the API never claims a provider can
// run a raw query when its reader refuses to.
func TestRawTraceQueryCapabilityMatchesTheReader(t *testing.T) {
	for _, p := range traceProviders {
		source, err := getTraceSource(p, "user")
		if err != nil {
			source, err = getTraceSource(p, "agent")
		}
		require.NoError(t, err)
		advertised := rawTraceQueryUnsupportedReason(source) == ""
		assert.Equal(t, p != "solarwinds", advertised,
			"%s advertises raw-query support = %v, which disagrees with its reader", p, advertised)
	}
}

func TestHasRawTraceQuery(t *testing.T) {
	assert.False(t, hasRawTraceQuery(TracesV3Request{}))
	assert.False(t, hasRawTraceQuery(TracesV3Request{Query: "   "}), "whitespace is not a query")
	assert.True(t, hasRawTraceQuery(TracesV3Request{Query: `{"query":{"match_all":{}}}`}))
}

func TestESTraceRawQueryType(t *testing.T) {
	assert.Equal(t, "dsl", esTraceRawQueryType(nil), "no overrides defaults to dsl")
	assert.Equal(t, "dsl", esTraceRawQueryType(map[string]any{}))
	assert.Equal(t, "dsl", esTraceRawQueryType(map[string]any{"query_type": "  "}))
	assert.Equal(t, "kql", esTraceRawQueryType(map[string]any{"query_type": "KQL"}), "case-insensitive")
	assert.Equal(t, "ppl", esTraceRawQueryType(map[string]any{"query_type": "ppl"}), "passed through so the caller gets a named refusal")
}

// The regression guard for the trap that would have broken the reader this issue was filed
// about: a Data Prepper raw query must be bounded on startTime, NOT the logs path's
// @timestamp, which those indices do not have.
func TestFinalizeESTraceQueryBody_UsesTheCallersTimeField(t *testing.T) {
	for _, tc := range []struct {
		name      string
		timeField string
		filter    map[string]any
	}{
		{"data prepper", "startTime", dataPrepperTraceTimeRangeFilter(1000, 2000)},
		{"otel", "@timestamp", otelTraceTimeRangeFilter(1000, 2000)},
		{"apm", "@timestamp", apmTraceTimeRangeFilter(1000, 2000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := finalizeESTraceQueryBody(`{"query":{"term":{"serviceName":"checkout"}}}`, "dsl",
				tc.filter, []map[string]any{{tc.timeField: map[string]any{"order": "desc"}}}, 0, 0)
			require.NoError(t, err)

			raw, err := json.Marshal(body)
			require.NoError(t, err)
			assert.Contains(t, string(raw), tc.timeField,
				"the time bound must use this reader's own time field")
			assert.Contains(t, string(raw), "checkout", "the caller's own condition must survive")
		})
	}

	// Explicit: the Data Prepper body must not mention @timestamp anywhere.
	body, err := finalizeESTraceQueryBody(`{"query":{"match_all":{}}}`, "dsl",
		dataPrepperTraceTimeRangeFilter(1000, 2000), dataPrepperTraceDefaultSort(), 0, 0)
	require.NoError(t, err)
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "@timestamp",
		"Data Prepper indices have no @timestamp; reusing the logs helper here returns zero hits forever")
}

// The caller's query is AND-ed WITH the time bound, never in place of it: a raw query
// cannot widen the window it was asked for.
func TestFinalizeESTraceQueryBody_TimeWindowIsNotNegotiable(t *testing.T) {
	// A caller who writes their own range clause still gets the requested window AND-ed on.
	body, err := finalizeESTraceQueryBody(
		`{"query":{"range":{"startTime":{"gte":"0","lte":"99999999999"}}}}`, "dsl",
		dataPrepperTraceTimeRangeFilter(1000, 2000), dataPrepperTraceDefaultSort(), 0, 0)
	require.NoError(t, err)

	boolQ, ok := body["query"].(map[string]any)["bool"].(map[string]any)
	require.True(t, ok, "the caller's query must be wrapped in a bool")
	filters, ok := boolQ["filter"].([]any)
	require.True(t, ok)
	require.Len(t, filters, 2, "exactly the caller's query plus the layer's time bound")
	assert.Equal(t, dataPrepperTraceTimeRangeFilter(1000, 2000), filters[1],
		"the second filter is the window the layer enforces")
}

func TestFinalizeESTraceQueryBody_DefaultsOnlyWhenAbsent(t *testing.T) {
	t.Run("defaults applied", func(t *testing.T) {
		body, err := finalizeESTraceQueryBody(`{"query":{"match_all":{}}}`, "dsl", nil,
			dataPrepperTraceDefaultSort(), 0, 0)
		require.NoError(t, err)
		assert.Equal(t, defaultESTraceQuerySize, body["size"], "trace default is 100, not the logs path's 1000")
		assert.Equal(t, dataPrepperTraceDefaultSort(), body["sort"])
		assert.NotContains(t, body, "from", "no offset requested")
	})

	t.Run("request paging applied", func(t *testing.T) {
		body, err := finalizeESTraceQueryBody(`{"query":{"match_all":{}}}`, "dsl", nil, nil, 25, 50)
		require.NoError(t, err)
		assert.Equal(t, 25, body["size"])
		assert.Equal(t, 50, body["from"])
	})

	t.Run("the caller's own paging and sort win", func(t *testing.T) {
		body, err := finalizeESTraceQueryBody(
			`{"query":{"match_all":{}},"size":7,"from":3,"sort":[{"durationInNanos":{"order":"asc"}}]}`,
			"dsl", nil, dataPrepperTraceDefaultSort(), 25, 50)
		require.NoError(t, err)
		assert.Equal(t, float64(7), body["size"], "a size the caller wrote is not overridden")
		assert.Equal(t, float64(3), body["from"])
		assert.NotEqual(t, dataPrepperTraceDefaultSort(), body["sort"], "a sort the caller wrote is not overridden")
	})
}

func TestFinalizeESTraceQueryBody_KQL(t *testing.T) {
	body, err := finalizeESTraceQueryBody(`serviceName : "checkout"`, "kql",
		dataPrepperTraceTimeRangeFilter(1000, 2000), dataPrepperTraceDefaultSort(), 0, 0)
	require.NoError(t, err)

	raw, err := json.Marshal(body)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "checkout")
	assert.Contains(t, string(raw), "startTime", "KQL goes through the same trace finalizer")
}

func TestFinalizeESTraceQueryBody_Rejections(t *testing.T) {
	t.Run("malformed dsl errors rather than returning everything", func(t *testing.T) {
		_, err := finalizeESTraceQueryBody(`SELECT * FROM spans WHERE service = 'checkout'`, "dsl",
			dataPrepperTraceTimeRangeFilter(1000, 2000), nil, 0, 0)
		require.Error(t, err, "ClickHouse-era SQL must not be accepted as an ES body")
		assert.Contains(t, err.Error(), "dsl")
	})

	t.Run("ppl is refused by name", func(t *testing.T) {
		_, err := finalizeESTraceQueryBody(`source=spans`, "ppl", nil, nil, 0, 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ppl")
		assert.Contains(t, err.Error(), "kql", "the refusal names what IS supported")
	})

	t.Run("malformed kql errors", func(t *testing.T) {
		_, err := finalizeESTraceQueryBody(`serviceName : (((`, "kql", nil, nil, 0, 0)
		require.Error(t, err)
	})
}

// Each ES reader routes a raw query through the finalizer and a blank one through its
// canonical builder.
func TestESTraceSearchBody_RawVersusCanonical(t *testing.T) {
	req := TracesV3Request{StartTime: 1000, EndTime: 2000}
	rawReq := req
	rawReq.Query = `{"query":{"term":{"serviceName":"checkout"}}}`

	t.Run("data prepper", func(t *testing.T) {
		src := &ElasticSaasTraceSource{}
		canonical, err := src.traceSearchBody(req)
		require.NoError(t, err)
		assert.NotContains(t, mustJSON(t, canonical), "checkout")

		raw, err := src.traceSearchBody(rawReq)
		require.NoError(t, err)
		assert.Contains(t, mustJSON(t, raw), "checkout")
		assert.Contains(t, mustJSON(t, raw), "startTime")
	})

	t.Run("otel", func(t *testing.T) {
		src := &ElasticOtelTraceSource{}
		raw, err := src.traceSearchBody(rawReq)
		require.NoError(t, err)
		assert.Contains(t, mustJSON(t, raw), "checkout")
	})

	t.Run("apm", func(t *testing.T) {
		src := &ElasticAPMTraceSource{}
		raw, err := src.traceSearchBody(rawReq)
		require.NoError(t, err)
		assert.Contains(t, mustJSON(t, raw), "checkout")
	})

	t.Run("a malformed raw query surfaces as an error, not unfiltered traces", func(t *testing.T) {
		bad := req
		bad.Query = "not a dsl body"
		_, err := (&ElasticSaasTraceSource{}).traceSearchBody(bad)
		require.Error(t, err)
	})
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return string(raw)
}

func TestOpenObserveBuildSQL_RawRunsVerbatim(t *testing.T) {
	src := &OpenObserveTraceSource{}
	raw := `SELECT * FROM "traces" WHERE service_name = 'checkout'`

	sql, err := src.buildSQL(TracesV3Request{Query: raw}, "traces")
	require.NoError(t, err)
	assert.Equal(t, raw, sql, "the caller's SQL runs exactly as written")
	assert.NotContains(t, sql, "LIMIT",
		"a raw query is bounded by the request's structured size field, never by appending LIMIT")

	canonical, err := src.buildSQL(TracesV3Request{}, "traces")
	require.NoError(t, err)
	assert.Contains(t, canonical, "LIMIT", "the canonical path still carries its own bound")
}

func TestOpenObserveRawRowLimit(t *testing.T) {
	assert.Equal(t, 10000, openObserveRawRowLimit(TracesV3Request{}), "unbounded defaults to the cap")
	assert.Equal(t, 10000, openObserveRawRowLimit(
		TracesV3Request{QueryRequest: TracesQueryBuilderRequest{Limit: 999999}}), "clamped to the cap")
	assert.Equal(t, 50, openObserveRawRowLimit(
		TracesV3Request{QueryRequest: TracesQueryBuilderRequest{Limit: 50}}))
}

// The "By Traces" views narrow to root spans through the WHERE CLAUSE, which a raw query
// replaces. Rather than losing that narrowing (every span) or refusing, the narrowing is
// composed onto the caller's SQL as an outer SELECT.
func TestOpenObserveComposesRootSpanNarrowingOntoRawSQL(t *testing.T) {
	raw := `SELECT * FROM "traces" WHERE service_name = 'checkout'`

	t.Run("root spans wrap the raw query and keep the narrowing", func(t *testing.T) {
		sql := fmt.Sprintf("SELECT * FROM %s WHERE %s ORDER BY _timestamp DESC",
			openObserveRawSubquery(raw), openObserveRootSpanWhere)
		assert.Contains(t, sql, raw, "the caller's SQL survives verbatim inside the subquery")
		assert.Contains(t, sql, "parent_id IS NULL", "the root narrowing is still applied")
		assert.Contains(t, sql, "AS nb_raw", "DataFusion rejects an unaliased derived table")
	})

	t.Run("count wraps the raw query", func(t *testing.T) {
		sql := openObserveCountOver(raw, "")
		assert.Contains(t, sql, "count(*)")
		assert.Contains(t, sql, raw)
	})

	t.Run("by-traces count keeps the root narrowing", func(t *testing.T) {
		sql := openObserveCountOver(raw, openObserveRootSpanWhere)
		assert.Contains(t, sql, "count(*)")
		assert.Contains(t, sql, "parent_id IS NULL")
	})

	t.Run("a trailing semicolon does not break the wrap", func(t *testing.T) {
		assert.NotContains(t, openObserveRawSubquery(raw+" ;"), ";",
			"a semicolon inside a derived table is a syntax error")
	})
}

// SolarWinds is the one provider that cannot run a raw query at all — it has no span read
// API. It must refuse rather than return its usual empty slice, which would read as
// "nothing matched" for a query that never ran.
func TestSolarWindsRefusesRawQueryRatherThanReturningEmpty(t *testing.T) {
	src := &SolarWindsTraceSource{}

	traces, err := src.QueryTraces(nil, TracesV3Request{AccountId: "acct"})
	require.NoError(t, err, "the canonical path keeps its existing empty-slice behaviour")
	assert.Empty(t, traces)

	_, err = src.QueryTraces(nil, TracesV3Request{AccountId: "acct", Query: "service.name:checkout"})
	require.Error(t, err, "a raw query must not be silently dropped — that is the #41426 defect")
	assert.Contains(t, err.Error(), "no span or trace read API")
}

func TestGcpTraceFilterFor(t *testing.T) {
	assert.Equal(t, `span:checkout-handler`,
		gcpTraceFilterFor(TracesV3Request{Query: `  span:checkout-handler  `}),
		"the caller's Cloud Trace filter is pushed verbatim, trimmed")

	assert.Empty(t, gcpTraceFilterFor(TracesV3Request{}),
		"no query and no where clause renders no filter")
}

// The grouped views own their aggregation, so they take the raw query as one filter clause
// alongside their own time bound rather than being handed the caller's whole body. A raw
// query must narrow a grouped view exactly as it narrows the listing — before #41426 these
// views built their filters from the where clause only and ignored Query entirely.
func TestESTraceFilters_RawQueryReachesTheGroupedViews(t *testing.T) {
	timeFilter := dataPrepperTraceTimeRangeFilter(1000, 2000)

	t.Run("raw dsl becomes a filter clause next to the time bound", func(t *testing.T) {
		req := TracesV3Request{Query: `{"query":{"term":{"serviceName":"checkout"}}}`}
		filters, err := esTraceFilters(req, timeFilter, elasticTraceLabelMapping)
		require.NoError(t, err)
		require.Len(t, filters, 2, "the time bound plus the caller's query")
		assert.Equal(t, timeFilter, filters[0], "the view's own window is still enforced")
		assert.Contains(t, mustJSON(t, filters[1]), "checkout")
	})

	t.Run("raw kql becomes a filter clause too", func(t *testing.T) {
		req := TracesV3Request{
			Query:   `serviceName : "checkout"`,
			Request: map[string]any{"query_type": "kql"},
		}
		filters, err := esTraceFilters(req, timeFilter, elasticTraceLabelMapping)
		require.NoError(t, err)
		require.Len(t, filters, 2)
		assert.Contains(t, mustJSON(t, filters[1]), "checkout")
	})

	t.Run("the raw query REPLACES the where clause, it does not stack with it", func(t *testing.T) {
		req := TracesV3Request{Query: `{"query":{"term":{"serviceName":"checkout"}}}`}
		req.QueryRequest.Where = whereJSON(t, `{"_binary":{"service_name":{"_eq":"other"}}}`)
		filters, err := esTraceFilters(req, timeFilter, elasticTraceLabelMapping)
		require.NoError(t, err)
		rendered := mustJSON(t, filters)
		assert.Contains(t, rendered, "checkout")
		assert.NotContains(t, rendered, "other", "the builder filter must not silently stack onto a raw query")
	})

	t.Run("no raw query keeps the canonical where clause", func(t *testing.T) {
		req := TracesV3Request{}
		req.QueryRequest.Where = whereJSON(t, `{"_binary":{"service_name":{"_eq":"checkout"}}}`)
		filters, err := esTraceFilters(req, timeFilter, elasticTraceLabelMapping)
		require.NoError(t, err)
		assert.Contains(t, mustJSON(t, filters), "checkout")
	})

	t.Run("a malformed raw query errors instead of grouping the whole window", func(t *testing.T) {
		req := TracesV3Request{Query: `SELECT * FROM spans`}
		_, err := esTraceFilters(req, timeFilter, elasticTraceLabelMapping)
		require.Error(t, err)
	})

	t.Run("a dsl body with no query key is match_all, not an error", func(t *testing.T) {
		clause, err := esRawTraceQueryClause(`{"size":5}`, "dsl")
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"match_all": map[string]any{}}, clause)
	})
}

// An empty query object must never reach Elasticsearch inside bool.filter: `{}` there is
// rejected outright with "[bool] failed to parse field [filter]" (verified against a live
// cluster), so `{"query":{}}` would 400 instead of matching the window. Both raw-query
// entry points degrade it to match_all. Raised by review on PR #42028.
func TestEmptyRawQueryObjectBecomesMatchAll(t *testing.T) {
	matchAll := map[string]any{"match_all": map[string]any{}}

	t.Run("clause form, used by the grouped views", func(t *testing.T) {
		for _, raw := range []string{`{"query":{}}`, `{"query":{},"size":10}`, `{}`, `{"size":10}`} {
			clause, err := esRawTraceQueryClause(raw, "dsl")
			require.NoError(t, err, raw)
			assert.Equal(t, matchAll, clause, "%s must degrade to match_all, not an empty clause", raw)
		}
	})

	t.Run("body form, used by the listing", func(t *testing.T) {
		body, err := finalizeESTraceQueryBody(`{"query":{}}`, "dsl",
			dataPrepperTraceTimeRangeFilter(1000, 2000), dataPrepperTraceDefaultSort(), 0, 0)
		require.NoError(t, err)

		boolQ := body["query"].(map[string]any)["bool"].(map[string]any)
		filters := boolQ["filter"].([]any)
		require.Len(t, filters, 2)
		assert.Equal(t, matchAll, filters[0],
			"an empty query object must be replaced, or Elasticsearch rejects the whole search")
		assert.NotEmpty(t, filters[0], "an empty clause in bool.filter is an ES 400")
	})

	t.Run("a real query is still passed through untouched", func(t *testing.T) {
		clause, err := esRawTraceQueryClause(`{"query":{"term":{"serviceName":"checkout"}}}`, "dsl")
		require.NoError(t, err)
		assert.Contains(t, mustJSON(t, clause), "checkout")
	})
}
