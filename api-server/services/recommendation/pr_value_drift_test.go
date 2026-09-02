package recommendation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures below are the real values from the two open pull requests on dev
// that motivated #34959, so the thresholds are exercised against the case they
// were designed for rather than invented numbers.
//
// Both thresholds are 10, the trigger percentage the auto optimize rule uses.
var ruleThresholds = map[string]float64{"cpu": 10, "memory": 10}

func values(payload map[string]any) map[string]any { return payload }

// notifications: PR #802 has been open since 9 July proposing cpu 33m; the
// recommendation now wants 80m. Merging the stale pull request would give the
// workload roughly 2.4x less cpu than it needs — this must be caught.
func TestDetectValueDrift_CatchesMateriallyStaleCPU(t *testing.T) {
	open := values(map[string]any{
		"notifications": map[string]any{
			"cpu":    map[string]any{"request": "33m"},
			"memory": map[string]any{"request": "307832Ki", "limit": "307832Ki"},
		},
	})
	current := values(map[string]any{
		"notifications": map[string]any{
			"cpu":    map[string]any{"request": "80m"},
			"memory": map[string]any{"request": "338690048", "limit": "338690048"},
		},
	})

	drifts := detectValueDrift(open, current, ruleThresholds)
	require.NotEmpty(t, drifts, "a 33m → 80m cpu move must count as drifted")

	described := describeDrifts(drifts)
	assert.Contains(t, described, "cpu request: 33m → 80m")
	assert.Contains(t, described, "+142%")
}

// k8s-collector: PR #860's memory differs from the current recommendation by
// 0.5%. Rewriting a pull request over that would be churn.
func TestDetectValueDrift_IgnoresTrivialMovement(t *testing.T) {
	open := values(map[string]any{
		"k8s-collector": map[string]any{
			"memory": map[string]any{"request": "223357747", "limit": "223357747"},
		},
	})
	current := values(map[string]any{
		"k8s-collector": map[string]any{
			"memory": map[string]any{"request": "224395264", "limit": "224395264"},
		},
	})

	assert.Empty(t, detectValueDrift(open, current, ruleThresholds),
		"0.5%% movement is well under the 10%% threshold and must be left alone")
}

// The same pull request also has no cpu at all, while the recommendation now
// wants 188m. A pull request that is missing a dimension entirely is materially
// incomplete, not merely slightly stale.
func TestDetectValueDrift_TreatsAnAddedDimensionAsDrift(t *testing.T) {
	open := values(map[string]any{
		"k8s-collector": map[string]any{
			"cpu":    map[string]any{},
			"memory": map[string]any{"request": "223357747"},
		},
	})
	current := values(map[string]any{
		"k8s-collector": map[string]any{
			"cpu":    map[string]any{"request": "188m"},
			"memory": map[string]any{"request": "223357747"},
		},
	})

	drifts := detectValueDrift(open, current, ruleThresholds)
	require.Len(t, drifts, 1)
	assert.Contains(t, drifts[0].String(), "now set to 188m")
}

// Units differ freely between runs — the same figure is written as bytes, Ki and
// Mi depending on which code path produced it. Comparing text would report drift
// where there is none.
func TestDetectValueDrift_ComparesAcrossUnitFormats(t *testing.T) {
	open := values(map[string]any{
		"app": map[string]any{"memory": map[string]any{"request": "256Mi"}},
	})
	current := values(map[string]any{
		"app": map[string]any{"memory": map[string]any{"request": "262144Ki"}}, // identical
	})

	assert.Empty(t, detectValueDrift(open, current, ruleThresholds),
		"256Mi and 262144Ki are the same quantity and must not read as drift")
}

// Thousands separators appear in some stored payloads ("4,000Mi"). A formatting
// choice must not be mistaken for a value change.
func TestDetectValueDrift_ToleratesThousandsSeparators(t *testing.T) {
	open := values(map[string]any{
		"ml": map[string]any{"memory": map[string]any{"request": "4,000Mi"}},
	})
	current := values(map[string]any{
		"ml": map[string]any{"memory": map[string]any{"request": "4000Mi"}},
	})

	assert.Empty(t, detectValueDrift(open, current, ruleThresholds))
}

// A field the pull request sets but the recommendation has since dropped is
// deliberately not drift: removing a limit is a different, riskier change than
// keeping numbers current.
func TestDetectValueDrift_IgnoresFieldsTheRecommendationDropped(t *testing.T) {
	open := values(map[string]any{
		"app": map[string]any{"memory": map[string]any{"request": "256Mi", "limit": "512Mi"}},
	})
	current := values(map[string]any{
		"app": map[string]any{"memory": map[string]any{"request": "256Mi"}},
	})

	assert.Empty(t, detectValueDrift(open, current, ruleThresholds))
}

// Each dimension is judged by its own threshold; collapsing them to one number
// would silently apply the stricter of the two to both.
func TestDetectValueDrift_AppliesPerDimensionThresholds(t *testing.T) {
	open := values(map[string]any{
		"app": map[string]any{
			"cpu":    map[string]any{"request": "100m"},
			"memory": map[string]any{"request": "100Mi"},
		},
	})
	current := values(map[string]any{
		"app": map[string]any{
			"cpu":    map[string]any{"request": "115m"},  // +15%
			"memory": map[string]any{"request": "115Mi"}, // +15%
		},
	})

	drifts := detectValueDrift(open, current, map[string]float64{"cpu": 10, "memory": 50})
	require.Len(t, drifts, 1, "only cpu crosses its own threshold")
	assert.Equal(t, "cpu", drifts[0].Dimension)
}

// No thresholds means the caller did not opt in — every non-auto-optimize caller.
func TestDetectValueDrift_WithoutThresholdsNeverDrifts(t *testing.T) {
	open := values(map[string]any{"app": map[string]any{"cpu": map[string]any{"request": "10m"}}})
	current := values(map[string]any{"app": map[string]any{"cpu": map[string]any{"request": "900m"}}})

	assert.Empty(t, detectValueDrift(open, current, nil),
		"a caller that did not opt in must never have its pull request rewritten")
}

// An unparseable or absent old value must not be treated as a silent zero, which
// would make every comparison look like infinite drift.
func TestDetectValueDrift_SkipsUnparseableValues(t *testing.T) {
	open := values(map[string]any{
		"app": map[string]any{"memory": map[string]any{"request": "not-a-quantity"}},
	})
	current := values(map[string]any{
		"app": map[string]any{"memory": map[string]any{"request": "256Mi"}},
	})

	drifts := detectValueDrift(open, current, ruleThresholds)
	require.Len(t, drifts, 1)
	assert.Empty(t, drifts[0].Old, "an unreadable old value is reported as newly set, not as a percentage")
}
