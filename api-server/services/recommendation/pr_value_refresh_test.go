package recommendation

import (
	"testing"
	"time"

	"nudgebee/services/account/adapter"
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

// A rule can reject one dimension while another passes, and the run's payload
// then carries that dimension empty. Sent to the agent like that, the empty cpu
// read as "remove it": a refresh for a memory limit also put the base branch's
// cpu back. What the pull request already proposes has to travel with the update.
func TestMergeRefreshValues_KeepsWhatTheRunLeftOut(t *testing.T) {
	recorded := map[string]any{
		"app": map[string]any{
			"cpu":    map[string]any{"request": "320m"},
			"memory": map[string]any{"request": "339332505", "limit": "357Mi"},
		},
	}
	run := map[string]any{
		"app": map[string]any{
			"cpu":    map[string]any{},
			"memory": map[string]any{"request": "339332505", "limit": "350Mi"},
		},
	}

	merged := mergeRefreshValues(recorded, run)

	app := merged["app"].(map[string]any)
	assert.Equal(t, map[string]any{"request": "320m"}, app["cpu"],
		"the pull request's cpu must be carried over, not sent empty")
	assert.Equal(t, map[string]any{"request": "339332505", "limit": "350Mi"}, app["memory"],
		"a value the run produced wins over the recorded one")

	assert.Empty(t, run["app"].(map[string]any)["cpu"], "the run's payload must not be modified")

	prompt := buildValueRefreshPrompt("https://example.com/pull/1",
		adapter.FormatRequestedValuesForPrompt(merged), nil)
	assert.Contains(t, prompt, `"cpu":{"request":"320m"}`)
	assert.NotContains(t, prompt, `"cpu":{}`)
}

// The carried-over values are what the refresh is dispatched with — the same map
// is put in the agent's instruction and recorded once the update lands.
func TestMaybeRefreshOpenPR_DispatchesWithWhatTheRunLeftOut(t *testing.T) {
	var dispatched map[string]any
	var dispatchedDrifts []valueDrift
	original := dispatchRefresh
	dispatchRefresh = func(_ adapter.AccountAdapterContext, _ *models.RecommendationResolution, values map[string]any, drifts []valueDrift) {
		dispatched, dispatchedDrifts = values, drifts
	}
	t.Cleanup(func() { dispatchRefresh = original })

	res := resolutionAt(0, nil)
	res.Data = models.NewJsonObject(map[string]any{
		"pr_url": "https://example.com/pull/1",
		"data": map[string]any{
			"app": map[string]any{
				"cpu":    map[string]any{"request": "320m"},
				"memory": map[string]any{"request": "339332505", "limit": "400Mi"},
			},
		},
	})

	decision := maybeRefreshOpenPR(stubAdapterContext{}, res,
		map[string]any{
			"app": map[string]any{
				"cpu":    map[string]any{},
				"memory": map[string]any{"request": "339332505", "limit": "350Mi"},
			},
		},
		map[string]float64{"cpu": 10, "memory": 10})

	require.True(t, decision.Refreshed)
	require.Len(t, dispatchedDrifts, 1, "only the memory limit moved")
	require.NotNil(t, dispatched)
	app := dispatched["app"].(map[string]any)
	assert.Equal(t, map[string]any{"request": "320m"}, app["cpu"],
		"the refresh must not be dispatched with an empty cpu")
	assert.Equal(t, map[string]any{"request": "339332505", "limit": "350Mi"}, app["memory"])
}

// The same holds one level up and one level down: a container the run produced
// nothing for, and a single field (a limit) missing next to one that is present.
func TestMergeRefreshValues_KeepsContainersAndFieldsTheRunLeftOut(t *testing.T) {
	recorded := map[string]any{
		"app":     map[string]any{"memory": map[string]any{"request": "256Mi", "limit": "512Mi"}},
		"sidecar": map[string]any{"cpu": map[string]any{"request": "50m"}},
		"init":    map[string]any{"cpu": map[string]any{"request": "10m"}},
	}
	run := map[string]any{
		"app":  map[string]any{"memory": map[string]any{"request": "300Mi", "limit": nil}},
		"init": nil,
	}

	merged := mergeRefreshValues(recorded, run)

	assert.Equal(t, map[string]any{"request": "300Mi", "limit": "512Mi"},
		merged["app"].(map[string]any)["memory"])
	assert.Equal(t, map[string]any{"cpu": map[string]any{"request": "50m"}}, merged["sidecar"])
	assert.Equal(t, map[string]any{"cpu": map[string]any{"request": "10m"}}, merged["init"],
		"a container sent as null is one the run produced nothing for")
}

// A pull request that never set a dimension has nothing to carry over for it. The
// dimension is then left out: an empty one is what the agent read as "remove".
func TestMergeRefreshValues_NeverSendsAnEmptyDimension(t *testing.T) {
	recorded := map[string]any{
		"app": map[string]any{"cpu": map[string]any{}, "memory": map[string]any{"request": "256Mi"}},
	}
	run := map[string]any{
		"app":     map[string]any{"cpu": map[string]any{}, "memory": map[string]any{"request": "300Mi"}},
		"sidecar": map[string]any{"cpu": map[string]any{"limit": nil}, "memory": map[string]any{"request": "64Mi"}},
	}

	merged := mergeRefreshValues(recorded, run)

	assert.Equal(t, map[string]any{"memory": map[string]any{"request": "300Mi"}}, merged["app"])
	assert.Equal(t, map[string]any{"memory": map[string]any{"request": "64Mi"}}, merged["sidecar"])

	prompt := buildValueRefreshPrompt("https://example.com/pull/1",
		adapter.FormatRequestedValuesForPrompt(merged), nil)
	assert.NotContains(t, prompt, "{}")
	assert.NotContains(t, prompt, "null")
}

// The payload also carries keys that are not containers (rich_description), and a
// recorded value that was never a readable quantity is nothing to carry over.
func TestMergeRefreshValues_LeavesEverythingElseAsTheRunSentIt(t *testing.T) {
	recorded := map[string]any{
		"app":              map[string]any{"cpu": map[string]any{"request": "not-a-quantity"}},
		"rich_description": map[string]any{"cpu": map[string]any{"request": "1"}},
	}
	run := map[string]any{
		"app":              map[string]any{"memory": map[string]any{"request": "256Mi"}},
		"rich_description": "## Vertical Rightsizing Recommendation",
	}

	merged := mergeRefreshValues(recorded, run)

	assert.Equal(t, map[string]any{"memory": map[string]any{"request": "256Mi"}}, merged["app"])
	assert.Equal(t, "## Vertical Rightsizing Recommendation", merged["rich_description"])
}

// "Within the threshold" only speaks for what was compared. A value the pull
// request sets that the run produced nothing for is named, so a pull request that
// is stale on it does not read as checked.
func TestMaybeRefreshOpenPR_SaysWhatWasNotCompared(t *testing.T) {
	res := resolutionAt(0, nil)
	res.Data = models.NewJsonObject(map[string]any{
		"pr_url": "https://example.com/pull/1",
		"data": map[string]any{
			"app": map[string]any{
				"cpu":    map[string]any{"request": "320m"},
				"memory": map[string]any{"request": "339332505"},
			},
		},
	})

	decision := maybeRefreshOpenPR(nil, res,
		map[string]any{
			"app": map[string]any{
				"cpu":    map[string]any{},
				"memory": map[string]any{"request": "339332505"},
			},
		},
		map[string]float64{"cpu": 10, "memory": 10})

	assert.False(t, decision.Refreshed)
	assert.Contains(t, decision.Reason, "within the change threshold")
	assert.Contains(t, decision.Reason, "app cpu request")
	assert.NotContains(t, decision.Reason, "memory", "a compared value is not listed")
}
