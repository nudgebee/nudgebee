package api

import (
	"os"
	"testing"

	"nudgebee/llm/events"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEventAnalysisDigestLive runs the real generator against a real database and
// a real LLM. Gated on DIGEST_LIVE_TEST because it costs money and mutates
// event_analysis_digest.
//
//	DIGEST_LIVE_TEST=1 go test -v -run TestEventAnalysisDigestLive ./api/
//
// Set DIGEST_LIVE_ACCOUNT to pin one account; otherwise the first pending slot
// the gap scan returns is generated.
func TestEventAnalysisDigestLive(t *testing.T) {
	if os.Getenv("DIGEST_LIVE_TEST") == "" {
		t.Skip("set DIGEST_LIVE_TEST=1 to run against the live DB + LLM")
	}

	scanCtx, cancel := newDigestContext("")
	defer cancel()

	periods, err := events.FindPendingDigestPeriods(scanCtx)
	require.NoError(t, err, "gap scan should succeed")
	require.NotEmpty(t, periods, "expected at least one pending (account, week) slot")

	target := periods[0]
	if want := os.Getenv("DIGEST_LIVE_ACCOUNT"); want != "" {
		found := false
		for _, p := range periods {
			if p.CloudAccountID == want {
				target, found = p, true
				break
			}
		}
		require.True(t, found, "DIGEST_LIVE_ACCOUNT %s has no pending period", want)
	}
	t.Logf("generating digest for account=%s week=%s", target.CloudAccountID, target.PeriodStart.Format("2006-01-02"))

	ctx, cancelGen := newDigestContext(target.TenantID)
	defer cancelGen()

	// Exercise the read path on its own first so a query bug is distinguishable
	// from an LLM failure in the output.
	metrics, err := events.GetDigestMetrics(ctx, target)
	require.NoError(t, err, "metrics query")
	t.Logf("metrics: analyses=%d failed=%d classes=%d events=%d new=%d recurring=%d services=%d p1=%d%% noise=%d%%",
		metrics.Analyses, metrics.Failed, metrics.FailureClasses, metrics.EventsAnalysed,
		metrics.NewIncidents, metrics.Recurring, metrics.Services, metrics.P1Pct, metrics.NoisePct)

	classes, err := events.GetDigestClasses(ctx, target, digestMaxClasses)
	require.NoError(t, err, "classes query")
	for _, c := range classes {
		t.Logf("  class=%-50s events=%3d new=%3d worst=%5d owner=%q", c.AggregationKey, c.Events, c.NewIncidents, c.WorstRecurrence, c.Owner)
	}

	require.NoError(t, generateDigestForPeriod(ctx, target, events.DigestSourceScheduled), "generation should succeed")

	stored, err := events.GetDigest(ctx, target.CloudAccountID, target.PeriodStart)
	require.NoError(t, err, "digest should be readable back")
	assert.Equal(t, events.DigestStatusGenerated, stored.Status, "expected a fully generated digest; error=%s", stored.ErrorMessage)
	assert.NotEmpty(t, stored.Summary, "briefing should not be empty")

	t.Logf("\n===== BRIEFING =====\n%s\n====================", stored.Summary)
}

// TestEventAnalysisDigestLiveAll exercises the exact function the cron calls,
// filling every pending (account, week) slot. This is the multi-week path — it
// proves the per-account failure isolation and the gap scan draining to empty,
// which the single-period test cannot.
//
//	DIGEST_LIVE_TEST=1 go test -v -run TestEventAnalysisDigestLiveAll -timeout 30m ./api/
func TestEventAnalysisDigestLiveAll(t *testing.T) {
	if os.Getenv("DIGEST_LIVE_TEST") == "" {
		t.Skip("set DIGEST_LIVE_TEST=1 to run against the live DB + LLM")
	}

	scanCtx, cancel := newDigestContext("")
	defer cancel()

	before, err := events.FindPendingDigestPeriods(scanCtx)
	require.NoError(t, err)
	t.Logf("pending before: %d", len(before))

	require.NoError(t, generateMissingDigests(), "cron entrypoint should not error")

	after, err := events.FindPendingDigestPeriods(scanCtx)
	require.NoError(t, err)
	t.Logf("pending after: %d", len(after))

	// The scan treats failed rows as pending, so a drained queue also means no
	// slot ended in a failed state.
	assert.Empty(t, after, "every pending slot should be generated or partial")
}
