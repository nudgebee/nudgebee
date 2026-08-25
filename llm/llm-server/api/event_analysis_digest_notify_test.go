package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The three findings that ride in the message are the three a reader should
// open first, which is not the same as the three loudest. A P1 seen once
// outranks a P3 seen two hundred times.
func TestRankFindingsForNotificationPrefersPriorityOverVolume(t *testing.T) {
	ranked := rankFindingsForNotification([]classFinding{
		{AggregationKey: "noisy-info", Priority: "P3", Events: 200},
		{AggregationKey: "one-off-p1", Priority: "P1", Events: 1},
		{AggregationKey: "medium", Priority: "P2", Events: 50},
	})

	require.Len(t, ranked, 3)
	assert.Equal(t, "one-off-p1", ranked[0].AggregationKey)
	assert.Equal(t, "medium", ranked[1].AggregationKey)
	assert.Equal(t, "noisy-info", ranked[2].AggregationKey)
}

// Within one priority, the class nobody has fixed for weeks leads. That is the
// whole point of a weekly review — a fourth-week recurrence is a process
// failure, a first-week spike is just this week.
func TestRankFindingsForNotificationPrefersCarriedOver(t *testing.T) {
	ranked := rankFindingsForNotification([]classFinding{
		{AggregationKey: "new-loud", Priority: "P1", Events: 100, CarriedOverWeeks: 0},
		{AggregationKey: "old-quiet", Priority: "P1", Events: 4, CarriedOverWeeks: 4},
	})

	assert.Equal(t, "old-quiet", ranked[0].AggregationKey)
}

// HIGH and CRITICAL come from the event pipeline, P1 from the digest prompt.
// Both name the same tier, so they must rank together — treating "HIGH" as
// unknown would bury real incidents below P2s.
func TestRankFindingsForNotificationPriorityAliases(t *testing.T) {
	for _, p := range []string{"P1", "HIGH", "high", "Critical"} {
		ranked := rankFindingsForNotification([]classFinding{
			{AggregationKey: "p2", Priority: "P2", Events: 900},
			{AggregationKey: "top", Priority: p, Events: 1},
		})
		assert.Equal(t, "top", ranked[0].AggregationKey, "priority %q should rank as top tier", p)
	}
}

// Ranking must not reorder the caller's slice: generateDigestForPeriod stores
// class_summaries in its own order, and the notification is a read of that row.
func TestRankFindingsForNotificationDoesNotMutateInput(t *testing.T) {
	findings := []classFinding{
		{AggregationKey: "first", Priority: "P3"},
		{AggregationKey: "second", Priority: "P1"},
	}
	_ = rankFindingsForNotification(findings)

	assert.Equal(t, "first", findings[0].AggregationKey, "input slice reordered")
}

// A digest row whose class_summaries is SQL NULL is an empty week, not a
// corrupt row — the caller skips it rather than logging an error and marking it
// undeliverable.
func TestParseClassFindingsEmptyIsNotAnError(t *testing.T) {
	for _, raw := range []string{"", "null", "[]"} {
		findings, err := parseClassFindings(json.RawMessage(raw))
		assert.NoError(t, err, "raw %q", raw)
		assert.Empty(t, findings, "raw %q", raw)
	}
}

func TestParseClassFindingsRejectsGarbage(t *testing.T) {
	_, err := parseClassFindings(json.RawMessage(`{"not":"an array"}`))
	assert.Error(t, err)
}

// A base URL with a trailing slash would otherwise yield "//home?bcortex=digests",
// which some clients resolve as a protocol-relative host and send the reader to
// a different site entirely.
func TestDigestDeepLinkJoinsCleanly(t *testing.T) {
	want := "https://app.example.com/home?bcortex=digests"
	assert.Equal(t, want, digestDeepLink("https://app.example.com"))
	assert.Equal(t, want, digestDeepLink("https://app.example.com/"))
	assert.Equal(t, want, digestDeepLink("https://app.example.com///"))

	// An unconfigured base URL must yield nothing rather than a relative path:
	// the renderer treats a non-empty digest_url as authoritative, so "/home?..."
	// would suppress its own fallback and post a link no chat client can follow.
	assert.Empty(t, digestDeepLink(""))
	assert.Empty(t, digestDeepLink("   "))
	assert.Empty(t, digestDeepLink("/"))
}

// Account attribution is what makes a tenant-wide review actionable, and the
// same aggregation key in two accounts is two unrelated incidents. The count
// must not collapse them.
func TestCountAccounts(t *testing.T) {
	findings := []classFinding{
		{AccountID: "acct-a", AggregationKey: "HighErrorCriticalLogs"},
		{AccountID: "acct-b", AggregationKey: "HighErrorCriticalLogs"},
		{AccountID: "acct-a", AggregationKey: "KubePodCrashLooping"},
		{AccountID: "", AggregationKey: "unattributed"},
	}

	assert.Equal(t, 2, countAccounts(findings), "two distinct accounts, blank not counted")
}
