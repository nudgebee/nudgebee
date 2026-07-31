package k8s

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The thresholds carried to the api-server decide whether an already-open pull
// request is stale enough to rewrite (#34959). They come from the auto optimize
// rule itself, so there is one number a user reasons about rather than a second
// hidden one.

func TestRuleChangeThresholds_ReadsEachDimensionSeparately(t *testing.T) {
	// The rule reaches the task as its meta, shaped as the auto optimize stores it.
	params := map[string]any{
		"kind": "Deployment",
		"name": "notifications",
		"cpu": map[string]any{
			"algo":       "P99",
			"buffer_pct": float64(10),
			"trigger":    map[string]any{"change_pct": float64(10), "max_change_pct": float64(100)},
		},
		"memory": map[string]any{
			"algo":       "max",
			"buffer_pct": float64(15),
			"trigger":    map[string]any{"change_pct": float64(25), "max_change_pct": float64(100)},
		},
	}

	assert.Equal(t, map[string]float64{"cpu": 10, "memory": 25}, ruleChangeThresholds(params),
		"each dimension keeps its own threshold; collapsing them would apply one to the other")
}

func TestRuleChangeThresholds_AcceptsIntegerThresholds(t *testing.T) {
	// Unmarshalled rules give float64, but a rule built in Go gives int.
	params := map[string]any{
		"cpu": map[string]any{"trigger": map[string]any{"change_pct": 10}},
	}

	assert.Equal(t, map[string]float64{"cpu": 10}, ruleChangeThresholds(params))
}

func TestRuleChangeThresholds_AbsentOrMalformedRuleNeverRefreshes(t *testing.T) {
	cases := map[string]map[string]any{
		"no rule at all":            {},
		"dimension is not a map":    {"cpu": "P99"},
		"no trigger block":          {"cpu": map[string]any{"algo": "P99"}},
		"trigger has no change_pct": {"cpu": map[string]any{"trigger": map[string]any{"max_change_pct": float64(100)}}},
		"threshold is zero":         {"cpu": map[string]any{"trigger": map[string]any{"change_pct": float64(0)}}},
		"threshold is text":         {"cpu": map[string]any{"trigger": map[string]any{"change_pct": "10"}}},
	}

	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Nil(t, ruleChangeThresholds(params),
				"an unreadable rule must leave the open pull request alone rather than rewrite it on a guessed threshold")
		})
	}
}
