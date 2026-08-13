package observability

import (
	"errors"
	"nudgebee/services/query"
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
			// Non-equality operators carry patterns, not discrete values → ignored.
			{Binary: query.BinaryWhereClause{"content": {query.Contains: "error"}}},
			{Binary: query.BinaryWhereClause{"service": {query.Regex: "api.*"}}},
		},
	}
	got := map[string][]string{}
	collectWhereFieldValues(where, got)

	assert.Equal(t, []string{"prod"}, got["namespace"])
	assert.ElementsMatch(t, []string{"pod-a", "pod-b"}, got["pod"])
	assert.NotContains(t, got, "container") // _neq is not an equality value
	assert.NotContains(t, got, "content")   // _contains ignored
	assert.NotContains(t, got, "service")   // _regex ignored
}

func TestValidateReferencedLabelValues(t *testing.T) {
	ctx := mockRequestContext()
	logReq := FetchLogRequest{AccountId: "acct", StartTime: 1000, EndTime: 2000}

	t.Run("wrong value returns closest-match error", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{
			"namespace": {"prod", "staging", "dev"},
		}}
		err := validateReferencedLabelValues(ctx, src, logReq, map[string][]string{"namespace": {"prodd"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "prodd")
		assert.Contains(t, err.Error(), "namespace")
		assert.Contains(t, err.Error(), "prod") // closest suggestion
	})

	t.Run("present value passes", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{"namespace": {"prod", "dev"}}}
		assert.NoError(t, validateReferencedLabelValues(ctx, src, logReq, map[string][]string{"namespace": {"prod"}}))
	})

	t.Run("no close match gives action-agnostic guidance", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{"namespace": {"prod", "dev"}}}
		err := validateReferencedLabelValues(ctx, src, logReq, map[string][]string{"namespace": {"zzzzzz"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "zzzzzz")
		assert.Contains(t, err.Error(), "verify the value")
		assert.NotContains(t, err.Error(), "logs_list_label_values") // don't name a tool the caller may lack
	})

	t.Run("line-content field is never value-checked", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{"message": {"whatever"}}}
		assert.NoError(t, validateReferencedLabelValues(ctx, src, logReq, map[string][]string{"message": {"boom"}}))
	})

	t.Run("fails open on empty value set", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{}}
		assert.NoError(t, validateReferencedLabelValues(ctx, src, logReq, map[string][]string{"namespace": {"prodd"}}))
	})

	t.Run("fails open when value lookup errors", func(t *testing.T) {
		src := &fakeLabelSource{valuesErr: errors.New("relay down")}
		assert.NoError(t, validateReferencedLabelValues(ctx, src, logReq, map[string][]string{"namespace": {"prodd"}}))
	})

	t.Run("no referenced values is a no-op", func(t *testing.T) {
		src := &fakeLabelSource{labelValues: map[string][]string{"namespace": {"prod"}}}
		assert.NoError(t, validateReferencedLabelValues(ctx, src, logReq, map[string][]string{}))
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

		assert.NoError(t, validateReferencedLabelValues(ctx, src, narrowReq, map[string][]string{"namespace": {"prod"}}))

		wantStart := int64(endTime - valueValidationLookback*1000)
		assert.Equal(t, wantStart, src.lastValuesReq.StartTime)
	})
}
