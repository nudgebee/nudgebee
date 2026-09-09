package observability

import (
	"errors"
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

	// A provider that truncates returns exactly its page size. Treating that as the whole
	// value set would report a REAL value as unknown.
	t.Run("fails open on a value set truncated at the cap", func(t *testing.T) {
		values := make([]string, maxLabelValuesToScan)
		for i := range values {
			values[i] = "ns-" + strconv.Itoa(i)
		}
		src := &fakeLabelSource{labelValues: map[string][]string{"namespace": values}}
		req := FetchLogRequest{AccountId: "acct", StartTime: 1000, EndTime: 2000}
		assert.NoError(t, validateReferencedLabelValues(ctx, src, req,
			map[string][]whereFieldValue{"namespace": eqVals("a-real-value-past-the-page")}),
			"a truncated page must not be treated as the complete value universe")
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

	t.Run("a truncated value set fails open for patterns too", func(t *testing.T) {
		values := make([]string, maxLabelValuesToScan)
		for i := range values {
			values[i] = "app-" + strconv.Itoa(i)
		}
		assert.NoError(t, validateReferencedLabelValues(ctx, src(values...), req,
			pattern("%definitely-absent%", true, "definitely-absent")))
	})
}
