package recommendation

import (
	"testing"
	"time"

	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Guardrails around rewriting an already-open pull request (#34959). The
// materiality threshold (the rule's change_pct, typically 10%) overlaps the
// rule's own buffer (10-15%), so without a cooldown a workload sitting near the
// line would rewrite its pull request on every hourly run.

func resolutionAt(count int, lastRefresh *time.Time) *models.RecommendationResolution {
	return &models.RecommendationResolution{
		Id:                 "res-1",
		ResolverType:       models.RecommendationResolutionResolverTypeAutoOptimize,
		ValueRefreshCount:  count,
		LastValueRefreshAt: lastRefresh,
	}
}

func TestValueRefreshBlocked_AllowsAFirstRefresh(t *testing.T) {
	assert.Empty(t, valueRefreshBlocked(resolutionAt(0, nil)),
		"a pull request that has never been refreshed must be eligible")
}

func TestValueRefreshBlocked_HoldsWithinCooldown(t *testing.T) {
	recent := time.Now().Add(-1 * time.Hour)
	blocked := valueRefreshBlocked(resolutionAt(1, &recent))

	require.NotEmpty(t, blocked, "a refresh an hour ago is well inside the cooldown")
	assert.Contains(t, blocked, "less than")
}

func TestValueRefreshBlocked_AllowsOnceCooldownHasPassed(t *testing.T) {
	old := time.Now().Add(-valueRefreshCooldown - time.Minute)
	assert.Empty(t, valueRefreshBlocked(resolutionAt(1, &old)))
}

func TestValueRefreshBlocked_StopsAtTheCap(t *testing.T) {
	old := time.Now().Add(-30 * 24 * time.Hour)
	blocked := valueRefreshBlocked(resolutionAt(valueRefreshCap, &old))

	require.NotEmpty(t, blocked, "the cap must hold even long after the cooldown expired")
	assert.Contains(t, blocked, "leaving it for review")
}

// A pull request a person raised by hand is never rewritten, regardless of drift.
func TestMaybeRefreshOpenPR_LeavesForeignPullRequestsAlone(t *testing.T) {
	res := resolutionAt(0, nil)
	res.ResolverType = models.RecommendationResolutionResolverTypeUser

	decision := maybeRefreshOpenPR(nil, res,
		map[string]any{"app": map[string]any{"cpu": map[string]any{"request": "900m"}}},
		map[string]float64{"cpu": 10})

	assert.False(t, decision.Refreshed)
	assert.Contains(t, decision.Reason, "not raised by an auto optimize")
}

// A caller that did not opt in — everything except a scheduled auto optimize —
// must never trigger a rewrite, and must not even reach the comparison.
func TestMaybeRefreshOpenPR_RequiresOptIn(t *testing.T) {
	decision := maybeRefreshOpenPR(nil, resolutionAt(0, nil),
		map[string]any{"app": map[string]any{"cpu": map[string]any{"request": "900m"}}},
		nil)

	assert.False(t, decision.Refreshed)
	assert.Contains(t, decision.Reason, "did not opt in")
}

// Without recorded values there is nothing to compare against, so the safe
// outcome is to leave the pull request untouched rather than rewrite it blindly.
func TestMaybeRefreshOpenPR_NeedsRecordedValuesToCompare(t *testing.T) {
	res := resolutionAt(0, nil)
	res.Data = models.NewJsonObject(map[string]any{"pr_url": "https://example.com/pull/1"})

	decision := maybeRefreshOpenPR(nil, res,
		map[string]any{"app": map[string]any{"cpu": map[string]any{"request": "900m"}}},
		map[string]float64{"cpu": 10})

	assert.False(t, decision.Refreshed)
	assert.Contains(t, decision.Reason, "no recorded values")
}

// Movement inside the threshold leaves the pull request alone — the common case,
// and the one that keeps this from becoming churn.
func TestMaybeRefreshOpenPR_LeavesPullRequestAloneWithinThreshold(t *testing.T) {
	res := resolutionAt(0, nil)
	res.Data = models.NewJsonObject(map[string]any{
		"pr_url": "https://example.com/pull/1",
		"data": map[string]any{
			"k8s-collector": map[string]any{"memory": map[string]any{"request": "223357747"}},
		},
	})

	decision := maybeRefreshOpenPR(nil, res,
		map[string]any{
			"k8s-collector": map[string]any{"memory": map[string]any{"request": "224395264"}},
		},
		map[string]float64{"cpu": 10, "memory": 10})

	assert.False(t, decision.Refreshed)
	assert.Contains(t, decision.Reason, "within the change threshold")
}
