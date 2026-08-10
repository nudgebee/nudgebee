package api

import (
	"os"
	"testing"
	"time"

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
// Set DIGEST_LIVE_TENANT to pin one tenant; otherwise the first pending slot
// the gap scan returns is generated.
func TestEventAnalysisDigestLive(t *testing.T) {
	if os.Getenv("DIGEST_LIVE_TEST") == "" {
		t.Skip("set DIGEST_LIVE_TEST=1 to run against the live DB + LLM")
	}

	scanCtx, cancel := newDigestContext("")
	defer cancel()

	periods, err := events.FindPendingDigestPeriods(scanCtx)
	require.NoError(t, err, "gap scan should succeed")
	require.NotEmpty(t, periods, "expected at least one pending (tenant, week) slot")

	target := periods[0]
	if want := os.Getenv("DIGEST_LIVE_TENANT"); want != "" {
		found := false
		for _, p := range periods {
			if p.TenantID == want {
				target, found = p, true
				break
			}
		}
		require.True(t, found, "DIGEST_LIVE_TENANT %s has no pending period", want)
	}
	t.Logf("generating digest for tenant=%s week=%s", target.TenantID, target.PeriodStart.Format("2006-01-02"))

	ctx, cancelGen := newDigestContext(target.TenantID)
	defer cancelGen()

	// Exercise the read path on its own first so a query bug is distinguishable
	// from an LLM failure in the output.
	metrics, err := events.GetDigestMetrics(ctx, target)
	require.NoError(t, err, "metrics query")
	t.Logf("pipeline: analysed=%d complete=%d (%d%%) failed=%d",
		metrics.EventsAnalysed, metrics.EventsComplete, metrics.CompletionPct, metrics.FailedEvents)
	t.Logf("signal:   real=%d classes=%d services=%d new=%d recurring=%d (%d%%) p1=%d%%",
		metrics.RealEvents, metrics.FailureClasses, metrics.Services,
		metrics.NewIncidents, metrics.Recurrences, metrics.RecurrencePct, metrics.P1Pct)
	t.Logf("noise:    %d%% (%d synthetic, %d suppressed)",
		metrics.NoisePct, metrics.SyntheticEvents, metrics.SuppressedEvents)
	for _, n := range metrics.NoiseClasses {
		t.Logf("  excluded=%-32s [%-12s] events=%3d (%2d%%) %s", n.AggregationKey, n.AccountName, n.Events, n.Pct, n.Reason)
	}

	classes, err := events.GetDigestClasses(ctx, target)
	require.NoError(t, err, "classes query")
	for _, c := range classes {
		t.Logf("  [%-12s] class=%-40s events=%3d new=%3d recur=%3d worst=%4d owner=%q",
			c.AccountName, c.AggregationKey, c.Events, c.NewIncidents, c.Recurrences, c.WorstRecurrence, c.Owner)
	}

	prior, err := events.GetPriorClasses(ctx, target, events.DigestLookbackWeeks)
	require.NoError(t, err, "prior classes query")
	t.Logf("carry-over candidates from earlier weeks: %d", len(prior))
	for _, pc := range prior {
		t.Logf("  prior=%-40s [%-12s] weeks=%d events=%d", pc.AggregationKey, pc.AccountName, pc.Weeks, pc.Events)
	}

	require.NoError(t, generateDigestForPeriod(ctx, target, events.DigestSourceScheduled), "generation should succeed")

	stored, err := events.GetDigest(ctx, target.TenantID, target.PeriodStart)
	require.NoError(t, err, "digest should be readable back")
	assert.Equal(t, events.DigestStatusGenerated, stored.Status, "expected a fully generated digest; error=%s", stored.ErrorMessage)
	assert.NotEmpty(t, stored.Summary, "briefing should not be empty")

	t.Logf("\n===== BRIEFING =====\n%s\n====================", stored.Summary)
}

// TestEventAnalysisDigestLivePeriod regenerates one explicit (tenant, week),
// bypassing the gap scan. The scan only offers completed weeks that still need a
// scheduled run, so it cannot reach the current week or re-run a week that is
// already generated — which is exactly what verifying a counter change needs.
//
//	DIGEST_LIVE_TEST=1 DIGEST_LIVE_TENANT=<uuid> DIGEST_LIVE_WEEK=2026-08-03 \
//	  go test -v -run TestEventAnalysisDigestLivePeriod -timeout 20m ./api/
func TestEventAnalysisDigestLivePeriod(t *testing.T) {
	if os.Getenv("DIGEST_LIVE_TEST") == "" {
		t.Skip("set DIGEST_LIVE_TEST=1 to run against the live DB + LLM")
	}
	tenant := os.Getenv("DIGEST_LIVE_TENANT")
	week := os.Getenv("DIGEST_LIVE_WEEK")
	if tenant == "" || week == "" {
		t.Skip("set DIGEST_LIVE_TENANT and DIGEST_LIVE_WEEK (YYYY-MM-DD, a Monday)")
	}

	start, err := time.Parse("2006-01-02", week)
	require.NoError(t, err, "DIGEST_LIVE_WEEK must be YYYY-MM-DD")
	require.Equal(t, time.Monday, start.Weekday(), "a digest week starts on Monday")

	target := events.DigestPeriod{
		TenantID:    tenant,
		PeriodStart: start,
		PeriodEnd:   start.AddDate(0, 0, 7),
	}

	ctx, cancel := newDigestContext(tenant)
	defer cancel()

	metrics, err := events.GetDigestMetrics(ctx, target)
	require.NoError(t, err, "metrics query")
	t.Logf("pipeline: analysed=%d complete=%d (%d%%) failed=%d",
		metrics.EventsAnalysed, metrics.EventsComplete, metrics.CompletionPct, metrics.FailedEvents)
	t.Logf("signal:   real=%d classes=%d new=%d recurring=%d (%d%%) p1=%d%%",
		metrics.RealEvents, metrics.FailureClasses, metrics.NewIncidents,
		metrics.Recurrences, metrics.RecurrencePct, metrics.P1Pct)
	t.Logf("noise:    %d%% (%d synthetic, %d suppressed)",
		metrics.NoisePct, metrics.SyntheticEvents, metrics.SuppressedEvents)
	for _, n := range metrics.NoiseClasses {
		t.Logf("  excluded=%-32s [%-12s] events=%3d (%2d%%) %s", n.AggregationKey, n.AccountName, n.Events, n.Pct, n.Reason)
	}

	require.NoError(t, generateDigestForPeriod(ctx, target, events.DigestSourceOnDemand))

	stored, err := events.GetDigest(ctx, tenant, start)
	require.NoError(t, err, "digest should be readable back")
	assert.Equal(t, events.DigestStatusGenerated, stored.Status, "error=%s", stored.ErrorMessage)
	assert.NotEmpty(t, stored.Summary, "briefing should not be empty")

	t.Logf("\n===== BRIEFING =====\n%s\n====================", stored.Summary)
}

// TestEventAnalysisDigestLiveAll exercises the exact function the cron calls,
// filling every pending (tenant, week) slot. This is the multi-week path — it
// proves the gap scan draining to empty, which the single-period test cannot.
//
//	DIGEST_LIVE_TEST=1 go test -v -run TestEventAnalysisDigestLiveAll -timeout 30m ./api/
func TestEventAnalysisDigestLiveAll(t *testing.T) {
	if os.Getenv("DIGEST_LIVE_TEST") == "" {
		t.Skip("set DIGEST_LIVE_TEST=1 to run against the live DB + LLM")
	}

	beforeCtx, cancelBefore := newDigestContext("")
	defer cancelBefore()
	before, err := events.FindPendingDigestPeriods(beforeCtx)
	require.NoError(t, err)
	t.Logf("pending before: %d", len(before))

	require.NoError(t, generateMissingDigests(), "cron entrypoint should not error")

	// A fresh context for the second scan. Filling every pending slot can outrun
	// digestGenerationTimeout — a full run took 635s against a 600s budget — so
	// reusing the first scan's context fails on a deadline the run itself never
	// hit. generateMissingDigests is unaffected: it gives each period its own
	// context and only uses the scan context's logger afterwards.
	afterCtx, cancelAfter := newDigestContext("")
	defer cancelAfter()
	after, err := events.FindPendingDigestPeriods(afterCtx)
	require.NoError(t, err)
	t.Logf("pending after: %d", len(after))

	// The scan treats failed rows as pending, so a drained queue also means no
	// slot ended in a failed state.
	assert.Empty(t, after, "every pending slot should be generated or partial")
}
