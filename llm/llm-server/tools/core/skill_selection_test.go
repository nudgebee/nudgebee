package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSelectRelevantSkills_EmptyInputs(t *testing.T) {
	assert.Nil(t, SelectRelevantSkills("anything", nil, 3))
	assert.Nil(t, SelectRelevantSkills("anything", []SkillCandidate{}, 3))
}

func TestSelectRelevantSkills_NonPositiveTopKReturnsAll(t *testing.T) {
	cands := []SkillCandidate{
		{ID: "a", Name: "alpha", Description: "first"},
		{ID: "b", Name: "beta", Description: "second"},
	}
	got := SelectRelevantSkills("alpha", cands, 0)
	assert.ElementsMatch(t, []string{"a", "b"}, got, "topK<=0 must return every input id")

	got = SelectRelevantSkills("alpha", cands, -5)
	assert.ElementsMatch(t, []string{"a", "b"}, got, "negative topK must return every input id")
}

func TestSelectRelevantSkills_FewerCandidatesThanTopK(t *testing.T) {
	cands := []SkillCandidate{
		{ID: "a", Name: "alpha", Description: "first"},
		{ID: "b", Name: "beta", Description: "second"},
	}
	got := SelectRelevantSkills("nothing matches here", cands, 5)
	assert.ElementsMatch(t, []string{"a", "b"}, got,
		"when len(candidates) <= topK we have nothing to gain from filtering")
}

func TestSelectRelevantSkills_EmptyQueryReturnsAll(t *testing.T) {
	cands := []SkillCandidate{
		{ID: "a", Name: "alpha", Description: "first"},
		{ID: "b", Name: "beta", Description: "second"},
		{ID: "c", Name: "gamma", Description: "third"},
		{ID: "d", Name: "delta", Description: "fourth"},
	}
	got := SelectRelevantSkills("", cands, 2)
	assert.ElementsMatch(t, []string{"a", "b", "c", "d"}, got,
		"empty query gives no signal — degrade to show-all rather than picking arbitrarily")
}

func TestSelectRelevantSkills_PicksByQueryOverlap(t *testing.T) {
	cands := []SkillCandidate{
		{ID: "kafka", Name: "Kafka troubleshooting", Description: "consumer lag offsets partitions"},
		{ID: "redis", Name: "Redis cluster ops", Description: "memory eviction policies"},
		{ID: "postgres", Name: "Postgres slow queries", Description: "explain analyze indexes vacuum"},
		{ID: "k8s", Name: "Kubernetes basics", Description: "pods deployments services"},
	}
	got := SelectRelevantSkills("how do I debug postgres slow query plans", cands, 2)
	assert.Contains(t, got, "postgres", "the postgres skill must be picked for a postgres question")
	assert.NotContains(t, got, "redis", "redis is unrelated to the query")
	assert.LessOrEqual(t, len(got), 2)
}

func TestSelectRelevantSkills_DropsZeroOverlap(t *testing.T) {
	cands := []SkillCandidate{
		{ID: "kafka", Name: "Kafka troubleshooting", Description: "consumer lag"},
		{ID: "redis", Name: "Redis cluster ops", Description: "memory eviction"},
		{ID: "postgres", Name: "Postgres slow queries", Description: "explain analyze"},
		{ID: "k8s", Name: "Kubernetes pods", Description: "deployments services"},
	}
	// topK must be < len(candidates) for the filtering path to run at all —
	// otherwise we return every input id unchanged (no useful filtering possible).
	got := SelectRelevantSkills("postgres replication lag", cands, 3)
	// "postgres" matches, "kafka" matches via "lag", "redis"/"k8s" do not.
	assert.Contains(t, got, "postgres")
	assert.Contains(t, got, "kafka")
	assert.NotContains(t, got, "redis")
	assert.NotContains(t, got, "k8s")
}

func TestSelectRelevantSkills_HonoursTopK(t *testing.T) {
	cands := []SkillCandidate{
		{ID: "a", Name: "log analysis", Description: "errors stack traces panics"},
		{ID: "b", Name: "logs ingestion", Description: "errors throughput pipeline"},
		{ID: "c", Name: "log retention", Description: "errors archive cold storage"},
		{ID: "d", Name: "alerting", Description: "errors notification routes"},
	}
	got := SelectRelevantSkills("errors", cands, 2)
	assert.Equal(t, 2, len(got), "topK must cap the result size when more docs match than K")
}

func TestSelectRelevantSkills_StopWordsIgnored(t *testing.T) {
	cands := []SkillCandidate{
		{ID: "match", Name: "Loki query language", Description: "labels filters streams"},
		{ID: "noise", Name: "Generic guide", Description: "the and is a are with for"},
	}
	// Without stopword filtering "the and is a are with for" would dominate the
	// query because every term matches doc "noise". With filtering applied the
	// only meaningful query token is "loki" → only "match" should win.
	got := SelectRelevantSkills("how do I write a loki query", cands, 1)
	assert.Equal(t, []string{"match"}, got)
}

// TestSelectRelevantSkills_DuplicateQueryTermsDoNotInflateDf guards against a
// regression caught in PR review: if queryTokens contains the same term twice
// (e.g. the user typed "error error"), the document-frequency tally for that
// term must still reflect the true number of docs containing it. Counting df
// per query-token occurrence inflates df and deflates IDF, which silently
// underweights the repeated term in BM25 scoring — the opposite of what a user
// who typed it twice almost certainly intended.
//
// The scenario below is deliberately constructed so that under the buggy df
// counting the "error" term would get almost no weight (df == n == full corpus)
// and a doc that only matches on a rarer single-occurrence term would win.
// With the correct counting the doc that matches "error" *and* the rarer term
// must win.
func TestSelectRelevantSkills_DuplicateQueryTermsDoNotInflateDf(t *testing.T) {
	cands := []SkillCandidate{
		{ID: "winner", Name: "error recovery", Description: "panic crash restart"},
		{ID: "noisy1", Name: "throughput metrics", Description: "error rate"},
		{ID: "noisy2", Name: "error budget", Description: "slo compliance"},
		{ID: "noisy3", Name: "error logging", Description: "structured json"},
	}
	// Repeat "error" intentionally — user emphasis.
	got := SelectRelevantSkills("error error panic crash", cands, 1)
	assert.Equal(t, []string{"winner"}, got,
		"duplicate query terms must not skew df; the doc matching the rare terms still wins")
}

// TestSelectRelevantSkills_ZeroOverlapReturnsEmptySlice pins the nil-vs-empty
// distinction called out in PR review. When selection runs against a non-empty
// candidate set and every doc scores zero, the return must be an empty slice
// (len 0, non-nil) — NOT nil — so the caller can distinguish "selection was
// disabled" from "selection ran and chose nothing" and propagate the correct
// downstream filter semantics (drop all inherited skills, keep own-name only).
func TestSelectRelevantSkills_ZeroOverlapReturnsEmptySlice(t *testing.T) {
	cands := []SkillCandidate{
		{ID: "a", Name: "kafka", Description: "consumer lag"},
		{ID: "b", Name: "redis", Description: "eviction"},
		{ID: "c", Name: "postgres", Description: "vacuum"},
	}
	// Query that shares no token with any candidate.
	got := SelectRelevantSkills("tensorflow gradient descent", cands, 2)
	assert.NotNil(t, got, "zero-overlap selection must return empty slice, not nil")
	assert.Equal(t, 0, len(got))
}

func TestSelectRelevantSkills_AllDocsEmptyAfterTokenization(t *testing.T) {
	cands := []SkillCandidate{
		{ID: "a", Name: "!!!", Description: "..."},
		{ID: "b", Name: "???", Description: "---"},
		{ID: "c", Name: "###", Description: "***"},
	}
	got := SelectRelevantSkills("anything goes", cands, 2)
	// Defensive fallback path: docs tokenize to nothing, so we cannot score —
	// preserve input order and trim to topK rather than panicking on /0.
	assert.Equal(t, []string{"a", "b"}, got)
}

func TestTokenizeForSkillSelection(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{}},             // single-char dropped — pre-allocated empty slice
		{"the and or is", []string{}}, // pure stopwords — pre-allocated empty slice
		{"How to scale a Deployment", []string{"how", "scale", "deployment"}},    // mixed case + stopword
		{"k8s/cluster.yaml", []string{"k8s", "cluster", "yaml"}},                 // punctuation splits
		{"Postgres-slow-query", []string{"postgres", "slow", "query"}},           // hyphen splits
		{"Memory  utilization 95%", []string{"memory", "utilization", "95"}},     // digits kept, percent dropped
		{"NULL pointer dereference", []string{"null", "pointer", "dereference"}}, // all kept
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, TokenizeForSkillSelection(tc.in))
		})
	}
}
