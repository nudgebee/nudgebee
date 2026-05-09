package k8s

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApplyCPURules_UserScenario_NBALGO_Downscale(t *testing.T) {
	// Scenario from user:
	// Allocated: Request=0.5 (500m), Limit=0.8 (800m)
	// Recommended: Request=0.086 (86m), Limit=null
	// Config: ChangePct=10, MaxChangePct=100
	// Expected: Accepted (Change is ~82.8%)

	rules := &RightsizeRules{
		CPU: map[string]any{
			"algo":       "NBALGO",
			"buffer_pct": 0.0,
			"trigger": map[string]any{
				"change_pct":     10.0,
				"max_change_pct": 100.0,
			},
		},
		Memory: map[string]any{},
	}

	recommendation := map[string]any{
		"resource": "cpu",
		"recommended": map[string]any{
			"request": 0.086,
			"limit":   nil,
		},
		"add_info": map[string]any{
			"cpu_percentile_99": 0.086,
		},
	}

	allocated := map[string]any{
		"request": "500m",
		"limit":   "800m",
	}

	newReq, newLim, errors := rules.ApplyCPURules("test-container", recommendation, allocated)

	assert.Empty(t, errors, "Should have no errors")
	assert.NotNil(t, newReq, "New request should be generated")
	assert.Equal(t, "86m", *newReq, "Request should be 86m")
	assert.Nil(t, newLim, "Limit should be nil (removed)")
}

func TestApplyCPURules_Downscale_ExceedsMaxChange(t *testing.T) {
	// Scenario: Massive downscale that should be rejected
	// Allocated: 1.0 (1000m)
	// Recommended: 0.1 (100m)
	// Change: 900m / 1000m = 90%
	// Max Allowed: 50%

	rules := &RightsizeRules{
		CPU: map[string]any{
			"trigger": map[string]any{
				"change_pct":     10.0,
				"max_change_pct": 50.0,
			},
		},
	}

	recommendation := map[string]any{
		"recommended": map[string]any{
			"request": 0.1,
			"limit":   nil,
		},
	}

	allocated := map[string]any{
		"request": "1000m",
		"limit":   "1000m",
	}

	newReq, newLim, errors := rules.ApplyCPURules("test-container", recommendation, allocated)

	assert.Nil(t, newReq, "Request change should be rejected")
	assert.Nil(t, newLim, "Limit change should be rejected")
	assert.NotEmpty(t, errors, "Should have error about max change")
	assert.Contains(t, errors[0], "CPU change rejected", "Error should mention rejection")
}

func TestApplyCPURules_Direction_Down_Blocks_Upscale(t *testing.T) {
	// Scenario: Direction is DOWN, but recommendation is UP
	// Allocated: 0.5
	// Recommended: 0.8

	rules := &RightsizeRules{
		Direction: "down",
		CPU: map[string]any{
			"trigger": map[string]any{"change_pct": 10.0},
		},
	}

	recommendation := map[string]any{
		"recommended": map[string]any{
			"request": 0.8,
			"limit":   nil,
		},
	}

	allocated := map[string]any{
		"request": "500m",
		"limit":   "500m",
	}

	newReq, newLim, errors := rules.ApplyCPURules("test-container", recommendation, allocated)

	assert.Nil(t, newReq)
	assert.Nil(t, newLim)
	assert.NotEmpty(t, errors)
	assert.Contains(t, errors[0], "Change does not match direction 'down'")
}

func TestApplyCPURules_Legacy_Percentage_Bug_Reproduction(t *testing.T) {
	// This test proves the fix works.
	// If we used the OLD formula (change / new), this would be (0.5 - 0.086) / 0.086 = 481% change
	// With the NEW formula (change / old), this is (0.5 - 0.086) / 0.5 = 82.8% change
	// Max allowed is 100%.
	// Old formula -> Reject (481 > 100)
	// New formula -> Accept (82.8 < 100)

	rules := &RightsizeRules{
		CPU: map[string]any{
			"trigger": map[string]any{
				"change_pct":     10.0,
				"max_change_pct": 100.0,
			},
		},
	}

	recommendation := map[string]any{
		"recommended": map[string]any{
			"request": 0.086,
		},
	}

	allocated := map[string]any{
		"request": "0.5",
	}

	newReq, _, errors := rules.ApplyCPURules("test-container", recommendation, allocated)

	assert.Empty(t, errors)
	assert.NotNil(t, newReq)
}

// Helper to test marshaling input
func TestJSONInputParsing(t *testing.T) {
	inputJSON := `
	{
		"cpu": {
			"algo": "NBALGO", 
			"buffer_pct": 0, 
			"trigger": {"change_pct": 10, "max_change_pct": 100}
		},
		"recommendation": {
			"notifications": [
				{
					"recommended": {"limit": null, "request": 0.086},
					"allocated": {"limit": 0.8, "request": 0.5},
					"resource": "cpu"
				}
			]
		}
	}`

	var params map[string]any
	err := json.Unmarshal([]byte(inputJSON), &params)
	assert.NoError(t, err)

	// Extract recommendation blob like the Task does
	recBlob := params["recommendation"].(map[string]any)

	// Mock extraction of cpuRec
	var cpuRec map[string]any
	if list, ok := recBlob["notifications"].([]any); ok {
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				if m["resource"] == "cpu" {
					cpuRec = m
				}
			}
		}
	}

	assert.NotNil(t, cpuRec)

	rules := &RightsizeRules{
		CPU: params["cpu"].(map[string]any),
	}

	allocated := cpuRec["allocated"].(map[string]any)

	newReq, _, errors := rules.ApplyCPURules("notifications", cpuRec, allocated)
	assert.Empty(t, errors)
	assert.Equal(t, "86m", *newReq)
}

func TestApplyCPURules_StandardAlgo_P99(t *testing.T) {
	// Scenario: Algo is P99. Should pick cpu_percentile_99 from add_info.
	// AddInfo has P99 = 0.2 (200m). Recommended has 0.1 (100m).
	// Should pick 0.2.

	rules := &RightsizeRules{
		CPU: map[string]any{
			"algo":       "P99",
			"buffer_pct": 0.0,
			"trigger": map[string]any{
				"change_pct": 1.0,
			},
		},
	}

	recommendation := map[string]any{
		"recommended": map[string]any{
			"request": 0.1,
		},
		"add_info": map[string]any{
			"cpu_percentile_99": 0.2, // This should be chosen
			"cpu_percentile_95": 0.15,
		},
	}

	allocated := map[string]any{
		"request": "0.15", // 150m
	}

	newReq, _, errors := rules.ApplyCPURules("test-container", recommendation, allocated)

	assert.Empty(t, errors)
	assert.NotNil(t, newReq)
	assert.Equal(t, "200m", *newReq, "Should have picked P99 value (0.2)")
}

func TestApplyCPURules_StandardAlgo_P95(t *testing.T) {
	// Scenario: Algo is P95. Should pick cpu_percentile_95 from add_info.

	rules := &RightsizeRules{
		CPU: map[string]any{
			"algo":       "P95",
			"buffer_pct": 0.0,
			"trigger": map[string]any{
				"change_pct": 1.0,
			},
		},
	}

	recommendation := map[string]any{
		"recommended": map[string]any{
			"request": 0.1,
		},
		"add_info": map[string]any{
			"cpu_percentile_99": 0.2,
			"cpu_percentile_95": 0.15, // This should be chosen
		},
	}

	allocated := map[string]any{
		"request": "0.1",
	}

	newReq, _, errors := rules.ApplyCPURules("test-container", recommendation, allocated)

	assert.Empty(t, errors)
	assert.NotNil(t, newReq)
	assert.Equal(t, "150m", *newReq, "Should have picked P95 value (0.15)")
}

func TestApplyCPURules_NoRecommendation_ManualScale(t *testing.T) {
	// Scenario: No recommendation provided. User wants to scale manually by 10%.
	// Allocated: 0.5 (500m)
	// Config: ChangePct=10, Direction=Down (implied by negative or context, but here we test buffer logic)
	// Actually, ApplyCPURules doesn't take direction directly for calculation, it takes rules.
	// If algo is missing/unknown AND recommendation is missing, it falls back to allocated.
	// Then buffer is applied.

	rules := &RightsizeRules{
		CPU: map[string]any{
			"buffer_pct": 10.0, // Scale up by 10%
		},
	}

	recommendation := map[string]any{
		// Empty recommendation
	}

	allocated := map[string]any{
		"request": "0.5",
		"limit":   "1.0",
	}

	newReq, newLim, errors := rules.ApplyCPURules("test-container", recommendation, allocated)

	assert.Empty(t, errors)
	assert.NotNil(t, newReq)
	assert.NotNil(t, newLim)

	// 0.5 * 1.1 = 0.55 = 550m
	assert.Equal(t, "550m", *newReq)
	// 1.0 * 1.1 = 1.1 = 1100m (1.1)
	assert.Equal(t, "1100m", *newLim) // 1.1 might be string formatted as "1100m" or "1.1" depending on implementation
}
