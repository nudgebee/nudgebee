package k8s

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRightsizeRules_ApplyCPURules(t *testing.T) {
	rules := &RightsizeRules{
		CPU: map[string]any{
			"algo":       "P99",
			"buffer_pct": 10.0,
			"trigger": map[string]any{
				"change_pct": 5.0,
			},
			"max_cpu": "1",
		},
	}

	containerName := "nginx"
	recommendation := map[string]any{
		"add_info": map[string]any{
			"cpu_percentile_99": "500m",
		},
	}
	allocated := map[string]any{
		"request": "400m",
		"limit":   "800m",
	}

	// 500m + 10% buffer = 550m
	// Change from 400m to 550m is 37.5%, which is > 5%
	req, lim, errs := rules.ApplyCPURules(containerName, recommendation, allocated)

	assert.Len(t, errs, 0)
	assert.NotNil(t, req)
	assert.Equal(t, "550m", *req)
	assert.Nil(t, lim) // Algo P99 sets limit to nil in Python logic
}

func TestRightsizeRules_CPUMinChange(t *testing.T) {
	rules := &RightsizeRules{
		CPU: map[string]any{
			"trigger": map[string]any{
				"change_pct": 10.0,
			},
		},
	}

	recommendation := map[string]any{
		"recommended": map[string]any{
			"request": "105m",
		},
	}
	allocated := map[string]any{
		"request": "100m",
	}

	// Change is 5%, threshold is 10% -> Should skip
	req, _, errs := rules.ApplyCPURules("nginx", recommendation, allocated)

	assert.Len(t, errs, 1)
	assert.Contains(t, errs[0], "CPU change skipped")
	assert.Nil(t, req)
}

func TestRightsizeRules_CPUMaxThreshold(t *testing.T) {
	rules := &RightsizeRules{
		CPU: map[string]any{
			"max_cpu": "200m",
		},
	}

	recommendation := map[string]any{
		"recommended": map[string]any{
			"request": "500m",
		},
	}
	allocated := map[string]any{
		"request": "100m",
	}

	req, _, errs := rules.ApplyCPURules("nginx", recommendation, allocated)

	assert.Len(t, errs, 1)
	assert.Contains(t, errs[0], "exceeds maximum threshold")
	assert.Nil(t, req) // Python logic caps it by setting to None (nil) in this specific check?
	// Actually Python code does:
	// if request and rule["max_cpu"] and request > rule["max_cpu"]:
	//    error_msg = ...
	//    request = None
}

func TestRightsizeRules_ApplyMemoryRules(t *testing.T) {
	rules := &RightsizeRules{
		Memory: map[string]any{
			"buffer_pct": 20.0,
			"trigger": map[string]any{
				"change_pct": 5.0,
			},
		},
	}

	recommendation := map[string]any{
		"recommended": map[string]any{
			"request": "100Mi",
			"limit":   "200Mi",
		},
		"add_info": map[string]any{
			"actual_recommended_request": "100Mi",
			"actual_recommended_limit":   "200Mi",
		},
	}
	allocated := map[string]any{
		"request": "50Mi",
		"limit":   "100Mi",
	}

	// 100Mi + 20% = 120Mi
	// 200Mi + 20% = 240Mi
	req, lim, errs := rules.ApplyMemoryRules("nginx", recommendation, allocated)

	assert.Len(t, errs, 0)
	assert.NotNil(t, req)
	assert.Equal(t, "120Mi", *req)
	assert.NotNil(t, lim)
	assert.Equal(t, "240Mi", *lim)
}

func TestRightsizeRules_ManualScale(t *testing.T) {
	rules := &RightsizeRules{
		CPU: map[string]any{
			"buffer_pct": 10.0,
			"trigger": map[string]any{
				"change_pct": 5.0,
			},
		},
	}

	allocated := map[string]any{
		"request": "100m",
	}

	// No recommendation provided.
	// Base should be 100m. 100m + 10% = 110m.
	req, _, errs := rules.ApplyCPURules("nginx", nil, allocated)

	assert.Len(t, errs, 0)
	assert.NotNil(t, req)
	assert.Equal(t, "110m", *req)
}

func TestRightsizeRules_RemoveLimit(t *testing.T) {
	rules := &RightsizeRules{
		CPU: map[string]any{
			"remove_limit": true,
		},
	}

	allocated := map[string]any{
		"limit": "1",
	}

	_, lim, _ := rules.ApplyCPURules("nginx", nil, allocated)
	assert.Nil(t, lim) // remove_limit should result in nil (which caller treats as removal)
}

func TestRightsizeRules_Constraints(t *testing.T) {
	rules := &RightsizeRules{
		CPU: map[string]any{
			"min_cpu": "500m",
			"max_cpu": "1",
		},
	}

	t.Run("Below Min", func(t *testing.T) {
		allocated := map[string]any{"request": "100m"}
		req, _, errs := rules.ApplyCPURules("nginx", nil, allocated)
		assert.Len(t, errs, 1)
		assert.Contains(t, errs[0], "below minimum threshold")
		assert.NotNil(t, req)
		assert.Equal(t, "500m", *req)
	})

	t.Run("Above Max", func(t *testing.T) {
		allocated := map[string]any{"request": "2"}
		req, _, errs := rules.ApplyCPURules("nginx", nil, allocated)
		assert.Len(t, errs, 1)
		assert.Contains(t, errs[0], "exceeds maximum threshold")
		assert.Nil(t, req)
	})
}
