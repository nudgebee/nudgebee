package optimizer

import (
	"context"
	"encoding/json"
	"nudgebee/runbook/internal/model"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestVerticalRightsizeGenerator_Debug(t *testing.T) {
	generator := &VerticalRightsizeGenerator{}
	ctx := context.Background()

	// JSON payload from SQL query
	jsonStr := `{"services-server":[{"add_info":{"cpu_percentile_92":0.03628734666666665,"cpu_percentile_95":0.0440908419444444,"cpu_percentile_97":0.057155021333333306,"cpu_percentile_99":0.07933648033333322},"allocated":{"limit":0.25,"request":0.1},"description":"...","info":null,"metric":{},"priority":{"limit":3,"request":1},"recommended":{"limit":null,"request":0.08},"resource":"cpu","strategy":{"name":"nudgebee","settings":{}}},{"add_info":{},"allocated":{"limit":1073741824,"request":524288000},"description":"...","info":null,"metric":{},"priority":{"limit":3,"request":3},"recommended":{"limit":800063488,"request":800063488},"resource":"memory","strategy":{"name":"nudgebee","settings":{}}}]}`

	var recMap map[string]any
	err := json.Unmarshal([]byte(jsonStr), &recMap)
	assert.NoError(t, err)

	ao := model.AutoOptimize{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Category:  "vertical_rightsize",
		Rule: map[string]any{
			"scale_up": true,
			"cpu":      true,
			"memory":   true,
		},
		Status: model.AutoOptimizeStatusActive,
		ResourceFilters: []model.AutoOptimizeResourceFilter{
			{
				Name:      stringPtr("services-server"),
				Namespace: stringPtr("nudgebee"),
				Type:      stringPtr("Deployment"),
			},
		},
	}

	recs := []model.RecommendationWithResource{{
		Recommendation: model.Recommendation{
			ID:             uuid.New(),
			Recommendation: recMap,
		},
		ResourceIdentifier: "nudgebee/Deployment/services-server",
	}}

	tasks, err := generator.GenerateTasks(ctx, ao, recs)
	assert.NoError(t, err)

	// Print why it failed if it failed
	if len(tasks) == 0 {
		t.Logf("Rec map: %+v", recMap)
		for k, v := range recMap {
			t.Logf("Key: %s, Type: %T", k, v)
			if arr, ok := v.([]any); ok {
				t.Logf("  Is []any, len: %d", len(arr))
			} else {
				t.Logf("  Not []any")
			}
		}
	}

	assert.Len(t, tasks, 1)
	if len(tasks) > 0 {
		assert.Equal(t, "Vertical Rightsize Deployment nudgebee/services-server", tasks[0].Name)
	}
}
