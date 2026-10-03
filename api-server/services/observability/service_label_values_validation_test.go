package observability

import (
	"errors"
	"fmt"
	"nudgebee/services/query"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"prod", "prod", 0},
		{"prodd", "prod", 1}, // extra char
		{"prd", "prod", 1},   // missing char
		{"prood", "prod", 1}, // substitution-ish
		{"paymets", "payments", 1},
		{"", "prod", 4},
		{"prod", "", 4},
		{"abc", "xyz", 3},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, levenshtein(c.a, c.b), "levenshtein(%q,%q)", c.a, c.b)
		assert.Equalf(t, c.want, levenshtein(c.b, c.a), "levenshtein is symmetric %q,%q", c.a, c.b)
	}
}

func TestClosestValues(t *testing.T) {
	values := []string{"prod", "production", "staging", "dev", "prod-canary"}

	t.Run("typo surfaces closest by edit distance", func(t *testing.T) {
		got := closestValues("prodd", values)
		require.NotEmpty(t, got)
		assert.Equal(t, "prod", got[0]) // distance 1 ranks first
	})

	t.Run("substring fallback catches partials", func(t *testing.T) {
		// "produ" is edit-distance 4 from "production" (> threshold 2) but a substring.
		got := closestValues("produ", values)
		assert.Contains(t, got, "production")
	})

	t.Run("token overlap ranks hyphenated service name first", func(t *testing.T) {
		// "ml-k8s-server" is 4 edits from "ml-server" (loses on pure edit distance) but
		// shares two tokens (ml, server), so token-aware ranking must surface it first.
		services := []string{"llm-server", "rag-server", "apiserver", "ml-k8s-server"}
		got := closestValues("ml-server", services)
		require.NotEmpty(t, got)
		assert.Equal(t, "ml-k8s-server", got[0])
	})

	t.Run("nothing close returns empty", func(t *testing.T) {
		assert.Empty(t, closestValues("zzzzzz", []string{"prod", "dev"}))
	})

	t.Run("caps suggestions at five", func(t *testing.T) {
		many := []string{"aaa0", "aaa1", "aaa2", "aaa3", "aaa4", "aaa5", "aaa6"}
		assert.LessOrEqual(t, len(closestValues("aaa", many)), 5)
	})

	t.Run("case-insensitive duplicates collapse to one suggestion", func(t *testing.T) {
		// "Prod" and "prod" are the same value under the function's documented
		// case-insensitive comparison; deduping on the original-case string would let
		// both through and waste a suggestion slot on a duplicate.
		got := closestValues("prodd", []string{"Prod", "prod", "production"})
		count := 0
		for _, v := range got {
			if strings.EqualFold(v, "prod") {
				count++
			}
		}
		assert.Equal(t, 1, count)
	})
}

func TestCollectWhereFieldValues(t *testing.T) {
	where := query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"namespace": {query.Eq: "prod"}}},
			{Or: []query.QueryWhereClause{
				{Binary: query.BinaryWhereClause{"pod": {query.In: []interface{}{"pod-a", "pod-b"}}}},
			}},
			{Not: &query.QueryWhereClause{Binary: query.BinaryWhereClause{"container": {query.Nq: "sidecar"}}}},
			// Patterns ARE collected now — as segments, not as discrete values.
			{Binary: query.BinaryWhereClause{"content": {query.Contains: "error"}}},
			{Binary: query.BinaryWhereClause{"app": {query.ILike: "%auth%"}}},
			{Binary: query.BinaryWhereClause{"host": {query.Like: "api-server%"}}},
			{Binary: query.BinaryWhereClause{"exact": {query.Like: "no-wildcards-here"}}},
			{Binary: query.BinaryWhereClause{"everything": {query.ILike: "%"}}},
			// Still ignored: dialect-specific, field-vs-field, or not set membership.
			{Binary: query.BinaryWhereClause{"service": {query.Regex: "api.*"}}},
			{Binary: query.BinaryWhereClause{"other": {query.EqF: "some_field"}}},
			{Binary: query.BinaryWhereClause{"never": {query.NLike: "%x%"}}},
		},
	}
	got := map[string][]whereFieldValue{}
	collectWhereFieldValues(where, got)

	assert.Equal(t, []whereFieldValue{{Raw: "prod"}}, got["namespace"])
	assert.ElementsMatch(t, []whereFieldValue{{Raw: "pod-a"}, {Raw: "pod-b"}}, got["pod"])

	assert.Equal(t, []whereFieldValue{{Raw: "error", Segments: []string{"error"}}}, got["content"],
		"_contains carries bare text, so it is one literal segment")
	assert.Equal(t, []whereFieldValue{{Raw: "%auth%", Segments: []string{"auth"}, Fold: true}}, got["app"])
	assert.Equal(t, []whereFieldValue{{Raw: "api-server%", Segments: []string{"api-server"}}}, got["host"])
	assert.Equal(t, []whereFieldValue{{Raw: "no-wildcards-here"}}, got["exact"],
		"a wildcard-free LIKE is an equality, so it takes the exact path")

	assert.NotContains(t, got, "container")  // _neq: an absent value makes it trivially true
	assert.NotContains(t, got, "everything") // "%" matches everything
	assert.NotContains(t, got, "service")    // _regex: dialect and anchoring differ per backend
	assert.NotContains(t, got, "other")      // _eq_f compares two FIELDS
	assert.NotContains(t, got, "never")      // negated pattern
}

// eqVals builds the equality-shaped values the pre-pattern tests were written against.
func eqVals(values ...string) []whereFieldValue {
	out := make([]whereFieldValue, len(values))
	for i, v := range values {
		out[i] = whereFieldValue{Raw: v}
	}
	return out
}

func TestValidateReferencedLabelValues(t *testing.T) {
	ctx := mockRequestContext()
	logReq := FetchLogRequest{AccountId: "acct", StartTime: 1000, EndTime: 2000}

	t.Run("wrong value returns closest-match error", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{
			"namespace": {"prod", "staging", "dev"},
		}}
		err := validateReferencedLabelValues(ctx, src, logReq, map[string][]whereFieldValue{"namespace": eqVals("prodd")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "prodd")
		assert.Contains(t, err.Error(), "namespace")
		assert.Contains(t, err.Error(), "prod") // closest suggestion
	})

	t.Run("present value passes", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{"namespace": {"prod", "dev"}}}
		assert.NoError(t, validateReferencedLabelValues(ctx, src, logReq, map[string][]whereFieldValue{"namespace": eqVals("prod")}))
	})

	t.Run("no close match gives action-agnostic guidance", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{"namespace": {"prod", "dev"}}}
		err := validateReferencedLabelValues(ctx, src, logReq, map[string][]whereFieldValue{"namespace": eqVals("zzzzzz")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "zzzzzz")
		assert.Contains(t, err.Error(), "verify the value")
		assert.NotContains(t, err.Error(), "logs_list_label_values") // don't name a tool the caller may lack
	})

	t.Run("line-content field is never value-checked", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{"message": {"whatever"}}}
		assert.NoError(t, validateReferencedLabelValues(ctx, src, logReq, map[string][]whereFieldValue{"message": eqVals("boom")}))
	})

	t.Run("fails open on empty value set", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{}}
		assert.NoError(t, validateReferencedLabelValues(ctx, src, logReq, map[string][]whereFieldValue{"namespace": eqVals("prodd")}))
	})

	t.Run("fails open when value lookup errors", func(t *testing.T) {
		src := &fakeLabelSource{valuesErr: errors.New("relay down")}
		assert.NoError(t, validateReferencedLabelValues(ctx, src, logReq, map[string][]whereFieldValue{"namespace": eqVals("prodd")}))
	})

	t.Run("no referenced values is a no-op", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{"namespace": {"prod"}}}
		assert.NoError(t, validateReferencedLabelValues(ctx, src, logReq, map[string][]whereFieldValue{}))
	})

	t.Run("widens the lookup window by 7 real days, not 7 seconds worth of milliseconds", func(t *testing.T) {
		// StartTime/EndTime are millisecond epoch. A 5-minute request window is narrower
		// than the 7-day lookback, so the widened StartTime sent to the source must be a
		// full 7 days (in ms) before EndTime — not ~10 minutes, which is what
		// (endTime - valueValidationLookback) works out to if the seconds constant is
		// subtracted from a millisecond timestamp without scaling.
		const endTime = 1_000_000_000_000
		const fiveMinutesMs = 5 * 60 * 1000
		narrowReq := FetchLogRequest{AccountId: "acct", StartTime: endTime - fiveMinutesMs, EndTime: endTime}
		src := &fakeLabelSource{labelValues: map[string][]string{"namespace": {"prod"}}}

		assert.NoError(t, validateReferencedLabelValues(ctx, src, narrowReq, map[string][]whereFieldValue{"namespace": eqVals("prod")}))

		wantStart := int64(endTime - valueValidationLookback*1000)
		assert.Equal(t, wantStart, src.lastValuesReq.StartTime)
	})
}

// The value check is the one that catches a valid field filtered to a value the backend
// has never seen — the real-world "kubernetes.labels.app = app-dev turns 224,979 hits into
// 0" case. It only works if the index reaches the source and a truncated value page is not
// mistaken for the complete value universe.
func TestValidateReferencedLabelValues_IndexAndTruncation(t *testing.T) {
	ctx := mockRequestContext()

	t.Run("the log query's index reaches the value lookup", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{"namespace": {"prod"}}}
		req := FetchLogRequest{
			AccountId: "acct", StartTime: 1000, EndTime: 2000,
			Request: map[string]any{"index": "logs-*", "query_type": "dsl"},
		}
		require.NoError(t, validateReferencedLabelValues(ctx, src, req, map[string][]whereFieldValue{"namespace": eqVals("prod")}))
		assert.Equal(t, map[string]any{"index": "logs-*"}, src.lastValuesReq.Request,
			"the index must travel; other provider-specific keys must not")
	})

	// Loki reads Request["query"] and Signoz reads filterAttributeKeyDataType/searchText in
	// QueryLabelValues, so a non-ES caller must keep receiving a nil Request exactly as before.
	t.Run("no index yields a nil request for non-ES providers", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{"namespace": {"prod"}}}
		req := FetchLogRequest{AccountId: "acct", StartTime: 1000, EndTime: 2000}
		require.NoError(t, validateReferencedLabelValues(ctx, src, req, map[string][]whereFieldValue{"namespace": eqVals("prod")}))
		assert.Nil(t, src.lastValuesReq.Request)
	})

	// A provider that truncates returns exactly its page size, and the value the caller asked
	// about may be one of the ones that did not fit. The page alone can never settle that —
	// only the backend can.
	t.Run("a truncated page never settles it on its own", func(t *testing.T) {
		values := make([]string, maxLabelValuesToScan)
		for i := range values {
			values[i] = "ns-" + strconv.Itoa(i)
		}
		req := FetchLogRequest{AccountId: "acct", StartTime: 1000, EndTime: 2000}
		refs := map[string][]whereFieldValue{"namespace": eqVals("a-real-value-past-the-page")}

		t.Run("value the backend holds is not blamed", func(t *testing.T) {
			src := &fakeLabelSource{
				labelValues: map[string][]string{"namespace": values},
				probeLogs:   []OutputLog{{Message: "a line from that namespace"}},
			}
			assert.NoError(t, validateReferencedLabelValues(ctx, src, req, refs),
				"a truncated page must not be treated as the complete value universe")
		})

		t.Run("value the backend lacks is diagnosed, without suggestions", func(t *testing.T) {
			src := &fakeLabelSource{labelValues: map[string][]string{"namespace": values}}
			err := validateReferencedLabelValues(ctx, src, req, refs)
			require.Error(t, err, "a >1000-value label used to give the agent no hint at all")
			assert.Contains(t, err.Error(), "verify the value")
			assert.NotContains(t, err.Error(), "closest valid value",
				"the nearest real value may be one of the ones that did not fit on the page")
		})

		t.Run("value present on the truncated page costs no probe", func(t *testing.T) {
			src := &fakeLabelSource{labelValues: map[string][]string{"namespace": values}}
			assert.NoError(t, validateReferencedLabelValues(ctx, src, req,
				map[string][]whereFieldValue{"namespace": eqVals("ns-7")}))
			assert.Zero(t, src.probeCalled)
		})
	})

	t.Run("still diagnoses below the cap", func(t *testing.T) {
		values := make([]string, maxLabelValuesToScan-1)
		for i := range values {
			values[i] = "ns-" + strconv.Itoa(i)
		}
		src := &fakeLabelSource{labelValues: map[string][]string{"namespace": values}}
		req := FetchLogRequest{AccountId: "acct", StartTime: 1000, EndTime: 2000}
		assert.Error(t, validateReferencedLabelValues(ctx, src, req,
			map[string][]whereFieldValue{"namespace": eqVals("definitely-not-present")}))
	})
}

func TestResolveESLabelValuesIndex(t *testing.T) {
	tests := []struct {
		name         string
		requestIndex string
		defaultIndex string
		want         string
	}{
		{"request index wins", "logs-req-*", "logs-default-*", "logs-req-*"},
		{"falls back to the account default", "", "logs-default-*", "logs-default-*"},
		{"nothing configured stays empty so the caller errors", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolveESLabelValuesIndex(tt.requestIndex, tt.defaultIndex))
		})
	}
}

// _body is llm-server's canonical body token, not a field any backend can aggregate.
// It must be skipped by the value check exactly as the literal body names are.
func TestValidateReferencedLabelValues_CanonicalBodyIsSkipped(t *testing.T) {
	ctx := mockRequestContext()
	src := &fakeLabelSource{labelValues: map[string][]string{"_body": {"whatever"}}}
	req := FetchLogRequest{AccountId: "acct", StartTime: 1000, EndTime: 2000}

	assert.NoError(t, validateReferencedLabelValues(ctx, src, req, map[string][]whereFieldValue{"_body": eqVals("anything")}))
	assert.NotEqual(t, "_body", src.lastValuesReq.LabelName, "_body must never reach QueryLabelValues")
}

// Agent-generated queries use _ilike, not _eq, so before these patterns were collected
// the value diagnosis never ran on real traffic. The danger in collecting them is the
// opposite failure: `%auth%` is not an exact value, so exact-matching a trimmed core
// would accuse a filter that is in fact correct.
func TestValidateReferencedLabelValues_Patterns(t *testing.T) {
	ctx := mockRequestContext()
	req := FetchLogRequest{AccountId: "acct", StartTime: 1000, EndTime: 2000}
	src := func(values ...string) *fakeLabelSource {
		return &fakeLabelSource{labelValues: map[string][]string{"app": values}}
	}
	pattern := func(raw string, fold bool, segs ...string) map[string][]whereFieldValue {
		return map[string][]whereFieldValue{"app": {{Raw: raw, Segments: segs, Fold: fold}}}
	}

	t.Run("a pattern no known value can satisfy is diagnosed", func(t *testing.T) {
		err := validateReferencedLabelValues(ctx, src("prod", "dev"), req, pattern("%auth%", true, "auth"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "%auth%", "the error must name the pattern the caller wrote")
	})

	t.Run("a pattern that a known value contains passes", func(t *testing.T) {
		// The trap: trimming to "pro" and exact-matching would wrongly accuse this.
		assert.NoError(t, validateReferencedLabelValues(ctx, src("prod"), req, pattern("%pro%", true, "pro")))
	})

	t.Run("a prefix pattern passes against a value that merely contains it", func(t *testing.T) {
		// Signoz/Dynatrace/Jaeger/GCP degrade LIKE to a substring op, so this really
		// does match there. Anchoring would make it a false positive.
		assert.NoError(t, validateReferencedLabelValues(ctx, src("my-api-server-1"), req,
			pattern("api-server%", false, "api-server")))
	})

	t.Run("case-insensitive patterns fold", func(t *testing.T) {
		assert.NoError(t, validateReferencedLabelValues(ctx, src("prod"), req, pattern("%PROD%", true, "PROD")))
	})

	t.Run("case-sensitive patterns do not fold", func(t *testing.T) {
		assert.Error(t, validateReferencedLabelValues(ctx, src("prod"), req, pattern("%PROD%", false, "PROD")))
	})

	t.Run("every segment must be present", func(t *testing.T) {
		assert.NoError(t, validateReferencedLabelValues(ctx, src("a-xx-b"), req, pattern("%a%b%", false, "a", "b")))
		assert.Error(t, validateReferencedLabelValues(ctx, src("a-xx-c"), req, pattern("%a%b%", false, "a", "b")))
	})

	t.Run("a truncated value set is settled by the backend for patterns too", func(t *testing.T) {
		values := make([]string, maxLabelValuesToScan)
		for i := range values {
			values[i] = "app-" + strconv.Itoa(i)
		}
		pat := pattern("%definitely-absent%", true, "definitely-absent")

		confirmed := src(values...)
		confirmed.probeLogs = []OutputLog{{Message: "definitely-absent-svc started"}}
		assert.NoError(t, validateReferencedLabelValues(ctx, confirmed, req, pat))

		err := validateReferencedLabelValues(ctx, src(values...), req, pat)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "widen or remove this filter")
		assert.NotContains(t, err.Error(), "closest valid value")
	})
}

// ---------------------------------------------------------------------------
// Traces — value validation. Mirrors the log cases above; the trace path differs in
// that it only runs against a source declaring complete value enumeration.
// ---------------------------------------------------------------------------

// Real millisecond epochs (a one-hour window on 2023-11-26), not small mock numbers: the
// validator widens StartTime back by 7 days, and a toy EndTime puts that widened start before
// the epoch — a window no real request produces and one the clamp below would mask.
func traceValuesReq() TracesV3Request {
	return TracesV3Request{AccountId: "acct", ProviderType: "otel_clickhouse", StartTime: 1700996400000, EndTime: 1701000000000}
}

func traceRefValues(field string, values ...whereFieldValue) map[string][]whereFieldValue {
	return map[string][]whereFieldValue{field: values}
}

func TestValidateReferencedTraceLabelValues(t *testing.T) {
	ctx := mockRequestContext()

	t.Run("wrong value is diagnosed with the closest match", func(t *testing.T) {
		src := &completeFakeTraceSource{fakeTraceSource{
			values: map[string][]string{"workload_name": {"services-server", "llm-server"}},
		}}
		err := validateReferencedTraceLabelValues(ctx, src, traceValuesReq(),
			traceRefValues("workload_name", whereFieldValue{Raw: "services-serve"}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no traces matched")
		assert.Contains(t, err.Error(), "services-serve")
		assert.Contains(t, err.Error(), "services-server")
	})

	t.Run("value that exists passes", func(t *testing.T) {
		src := &completeFakeTraceSource{fakeTraceSource{
			values: map[string][]string{"workload_name": {"services-server"}},
		}}
		assert.NoError(t, validateReferencedTraceLabelValues(ctx, src, traceValuesReq(),
			traceRefValues("workload_name", whereFieldValue{Raw: "services-server"})))
	})

	t.Run("no close match gives action-agnostic guidance", func(t *testing.T) {
		src := &completeFakeTraceSource{fakeTraceSource{
			values: map[string][]string{"workload_name": {"services-server"}},
		}}
		err := validateReferencedTraceLabelValues(ctx, src, traceValuesReq(),
			traceRefValues("workload_name", whereFieldValue{Raw: "zzzzzzzz"}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "trace provider")
		assert.Contains(t, err.Error(), "verify the value")
	})

	// The guard that keeps a sampling backend from calling a real value wrong.
	t.Run("source that does not declare complete enumeration is skipped", func(t *testing.T) {
		src := &fakeTraceSource{values: map[string][]string{"workload_name": {"services-server"}}}
		assert.NoError(t, validateReferencedTraceLabelValues(ctx, src, traceValuesReq(),
			traceRefValues("workload_name", whereFieldValue{Raw: "definitely-not-there"})))
	})

	t.Run("widens the lookup window by 7 real days", func(t *testing.T) {
		src := &completeFakeTraceSource{fakeTraceSource{
			values: map[string][]string{"workload_name": {"services-server"}},
		}}
		req := traceValuesReq()
		require.NoError(t, validateReferencedTraceLabelValues(ctx, src, req,
			traceRefValues("workload_name", whereFieldValue{Raw: "services-server"})))
		assert.Equal(t, req.EndTime-valueValidationLookback*1000, src.lastValuesReq.StartTime)
		assert.Equal(t, req.EndTime, src.lastValuesReq.EndTime)
	})

	t.Run("floors the widened window at the epoch", func(t *testing.T) {
		// An EndTime inside the first 7 days of 1970 would widen to a NEGATIVE start.
		// ClickHouse's DateTime is unsigned, so that wraps into the far future and returns
		// no values — the validator would fail open and lose the diagnosis.
		src := &completeFakeTraceSource{fakeTraceSource{
			values: map[string][]string{"workload_name": {"services-server"}},
		}}
		req := traceValuesReq()
		req.StartTime, req.EndTime = 1000, 5_000_000
		require.NoError(t, validateReferencedTraceLabelValues(ctx, src, req,
			traceRefValues("workload_name", whereFieldValue{Raw: "services-server"})))
		assert.Zero(t, src.lastValuesReq.StartTime, "widened start must never precede the epoch")
		assert.Equal(t, req.EndTime, src.lastValuesReq.EndTime)
	})

	t.Run("fails open on lookup error", func(t *testing.T) {
		src := &completeFakeTraceSource{fakeTraceSource{valuesErr: errors.New("clickhouse down")}}
		assert.NoError(t, validateReferencedTraceLabelValues(ctx, src, traceValuesReq(),
			traceRefValues("workload_name", whereFieldValue{Raw: "anything"})))
	})

	t.Run("fails open on empty value set", func(t *testing.T) {
		src := &completeFakeTraceSource{fakeTraceSource{values: map[string][]string{"workload_name": {}}}}
		assert.NoError(t, validateReferencedTraceLabelValues(ctx, src, traceValuesReq(),
			traceRefValues("workload_name", whereFieldValue{Raw: "anything"})))
	})

	t.Run("fails open with no referenced values", func(t *testing.T) {
		src := &completeFakeTraceSource{}
		assert.NoError(t, validateReferencedTraceLabelValues(ctx, src, traceValuesReq(), nil))
	})

	// A provider that truncates its own page returns exactly its page size; treating that as the
	// complete universe would report a REAL value as unknown.
	t.Run("fails open at exactly maxLabelValuesToScan, diagnoses at cap-1", func(t *testing.T) {
		full := make([]string, maxLabelValuesToScan)
		for i := range full {
			full[i] = fmt.Sprintf("workload-%d", i)
		}
		truncated := &completeFakeTraceSource{fakeTraceSource{values: map[string][]string{"workload_name": full}}}
		assert.NoError(t, validateReferencedTraceLabelValues(ctx, truncated, traceValuesReq(),
			traceRefValues("workload_name", whereFieldValue{Raw: "not-in-the-page"})))

		under := &completeFakeTraceSource{fakeTraceSource{values: map[string][]string{"workload_name": full[:maxLabelValuesToScan-1]}}}
		assert.Error(t, validateReferencedTraceLabelValues(ctx, under, traceValuesReq(),
			traceRefValues("workload_name", whereFieldValue{Raw: "not-in-the-page"})))
	})

	t.Run("unsatisfiable ilike pattern is diagnosed and a matching one passes", func(t *testing.T) {
		src := &completeFakeTraceSource{fakeTraceSource{
			values: map[string][]string{"endpoint": {"/rpc/query", "/rpc/logs"}},
		}}
		err := validateReferencedTraceLabelValues(ctx, src, traceValuesReq(),
			traceRefValues("endpoint", whereFieldValue{Raw: "%checkout%", Segments: []string{"checkout"}, Fold: true}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pattern")

		assert.NoError(t, validateReferencedTraceLabelValues(ctx, src, traceValuesReq(),
			traceRefValues("endpoint", whereFieldValue{Raw: "%RPC%", Segments: []string{"RPC"}, Fold: true})))
	})
}

// Every log provider's value listing is a page (labelValuesPageSize) and, for Splunk,
// Dynatrace, SolarWinds and Loggly, a sample of recent log lines — so a value's absence from
// the listing is not evidence the backend lacks it. Before this, the SQL providers paged at
// 100 and Dynatrace at 50 while the truncation guard sat at 1000, so a short page never
// tripped it and a real value was reported to the agent as a typo. These tests pin the
// confirmation step that makes the verdict independent of the page size.
func TestValidateReferencedLabelValues_ConfirmsBeforeBlaming(t *testing.T) {
	ctx := mockRequestContext()
	req := FetchLogRequest{AccountId: "acct", StartTime: 1000, EndTime: 2000}
	missing := map[string][]whereFieldValue{"namespace": eqVals("payments")}

	t.Run("a value the backend holds is never blamed, however short the page", func(t *testing.T) {
		// A 3-value page missing "payments" is exactly the shape a 50- or 100-value provider
		// returns for a busy label.
		src := &fakeLabelSource{
			labelValues: map[string][]string{"namespace": {"prod", "staging", "dev"}},
			probeLogs:   []OutputLog{{Message: "a log line from the payments namespace"}},
		}
		assert.NoError(t, validateReferencedLabelValues(ctx, src, req, missing),
			"the probe found logs, so the page was simply truncated")
		assert.Equal(t, 1, src.probeCalled, "the probe runs only for the value the page could not confirm")
	})

	t.Run("a value the backend really lacks is still diagnosed", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{"namespace": {"prod", "payment"}}}
		err := validateReferencedLabelValues(ctx, src, req, missing)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "payments")
		assert.Contains(t, err.Error(), "payment", "a complete page is still good enough to suggest from")
	})

	t.Run("an unrunnable probe fails open", func(t *testing.T) {
		src := &fakeLabelSource{
			labelValues: map[string][]string{"namespace": {"prod"}},
			probeErr:    errors.New("backend down"),
		}
		assert.NoError(t, validateReferencedLabelValues(ctx, src, req, missing),
			"a value we could not confirm must never be blamed")
	})

	t.Run("a confirmed value costs nothing extra", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{"namespace": {"prod"}}}
		require.NoError(t, validateReferencedLabelValues(ctx, src, req,
			map[string][]whereFieldValue{"namespace": eqVals("prod")}))
		assert.Zero(t, src.probeCalled, "a value present in the page needs no confirmation query")
	})

	t.Run("the probe carries one filter, one row and the widened window", func(t *testing.T) {
		const endTime = 1_000_000_000_000
		narrow := FetchLogRequest{
			AccountId: "acct", LogProvider: "pinot", LogProviderSource: "user",
			StartTime: endTime - 5*60*1000, EndTime: endTime,
			Request: map[string]any{"index": "logs-*", "query_type": "dsl"},
		}
		src := &fakeLabelSource{labelValues: map[string][]string{"namespace": {"prod"}}}
		require.Error(t, validateReferencedLabelValues(ctx, src, narrow, missing))

		require.Len(t, src.probeReqs, 1)
		probe := src.probeReqs[0]
		assert.Equal(t, 1, probe.Limit, "one row answers 'does anything match'")
		assert.Equal(t, int64(endTime-valueValidationLookback*1000), probe.StartTime,
			"the probe must see the same widened window the value listing did")
		assert.Equal(t, map[string]any{"index": "logs-*"}, probe.Request,
			"the index travels; other provider-specific keys must not")
		assert.Equal(t, query.BinaryWhereClause{"namespace": {query.Eq: "payments"}}, probe.QueryRequest.Where.Binary,
			"only the filter under suspicion — anything else could be the real reason for the empty result")
		assert.Empty(t, probe.QueryRequest.Where.And)
		assert.Empty(t, probe.QueryRequest.Where.Or)
		assert.Nil(t, probe.QueryRequest.Where.Not)
	})

	t.Run("a pattern is probed as a pattern, not as a literal", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{"app": {"prod"}}}
		require.Error(t, validateReferencedLabelValues(ctx, src, req,
			map[string][]whereFieldValue{"app": {{Raw: "%auth%", Segments: []string{"auth"}, Fold: true}}}))

		require.Len(t, src.probeReqs, 1)
		assert.Equal(t, query.BinaryWhereClause{"app": {query.ILike: "%auth%"}},
			src.probeReqs[0].QueryRequest.Where.Binary,
			"probing `%auth%` with _eq would look up the literal string and always come back empty")
	})

	t.Run("a pattern the backend can satisfy is never blamed", func(t *testing.T) {
		src := &fakeLabelSource{
			labelValues: map[string][]string{"app": {"prod", "dev"}},
			probeLogs:   []OutputLog{{Message: "auth-service started"}},
		}
		assert.NoError(t, validateReferencedLabelValues(ctx, src, req,
			map[string][]whereFieldValue{"app": {{Raw: "%auth%", Segments: []string{"auth"}, Fold: true}}}))
	})
}

// The page size is the one number the truncation guard is built on, so it must stay a single
// constant: the log providers drifting to 100 (Dynatrace 50) while the guard stayed at 1000 is
// precisely why a truncated page was read as a complete value set.
func TestLabelValuesPageSizeIsShared(t *testing.T) {
	assert.Equal(t, labelValuesPageSize, maxLabelValuesToScan,
		"the scan guard must equal the page every log provider asks for")
	assert.Equal(t, labelValuesPageSize, esLabelValuesTermsSize,
		"Elasticsearch's terms size must not drift from the shared page size")
}

// An ILIKE with no wildcards is case-insensitive EQUALITY, and its Fold flag has to survive
// collection or the probe asks a case-sensitive question about a case-insensitive filter —
// blaming `_ilike "payments"` against a backend that stores "Payments".
func TestValidateReferencedLabelValues_AnchoredILikeKeepsItsFold(t *testing.T) {
	ctx := mockRequestContext()
	req := FetchLogRequest{AccountId: "acct", StartTime: 1000, EndTime: 2000}

	t.Run("collection keeps Fold on an anchored ILIKE", func(t *testing.T) {
		out := map[string][]whereFieldValue{}
		collectWhereFieldValues(query.QueryWhereClause{
			Binary: query.BinaryWhereClause{"namespace": {query.ILike: "payments"}},
		}, out)
		require.Len(t, out["namespace"], 1)
		assert.Empty(t, out["namespace"][0].Segments, "no wildcards means the exact path applies")
		assert.True(t, out["namespace"][0].Fold, "but it is still case-insensitive")
	})

	t.Run("the probe asks case-insensitively", func(t *testing.T) {
		src := &fakeLabelSource{
			labelValues: map[string][]string{"namespace": {"Payments", "prod"}},
			probeLogs:   []OutputLog{{Message: "a line from Payments"}},
		}
		refs := map[string][]whereFieldValue{}
		collectWhereFieldValues(query.QueryWhereClause{
			Binary: query.BinaryWhereClause{"namespace": {query.ILike: "payments"}},
		}, refs)

		assert.NoError(t, validateReferencedLabelValues(ctx, src, req, refs),
			"the value differs only in case, so the filter is correct and must not be blamed")
		require.Len(t, src.probeReqs, 1)
		assert.Equal(t, query.BinaryWhereClause{"namespace": {query.ILike: "payments"}},
			src.probeReqs[0].QueryRequest.Where.Binary)
	})
}
