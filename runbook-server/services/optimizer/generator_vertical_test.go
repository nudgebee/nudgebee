package optimizer

import (
	"context"
	"nudgebee/runbook/internal/model"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestVerticalRightsizeGenerator_GenerateTasks(t *testing.T) {
	generator := &VerticalRightsizeGenerator{}
	ctx := context.Background()

	accountID := uuid.New()
	ao := model.AutoOptimize{
		ID:        uuid.New(),
		AccountID: accountID,
		TenantID:  uuid.New(),
		Category:  "vertical_rightsize",
		Rule: map[string]interface{}{
			"scale_up": true,
			"cpu": map[string]interface{}{
				"algo": "P99",
			},
		},
	}

	recData := map[string]interface{}{
		"nginx": []interface{}{
			map[string]interface{}{
				"resource": "cpu",
				"recommended": map[string]interface{}{
					"request": "100m",
				},
				"add_info": map[string]interface{}{
					"cpu_percentile_99": "120m",
				},
			},
		},
	}

	recommendations := []model.RecommendationWithResource{
		{
			Recommendation: model.Recommendation{
				ID:             uuid.New(),
				Recommendation: recData,
			},
			ResourceIdentifier: "default/Deployment/nginx",
		},
	}

	tasks, err := generator.GenerateTasks(ctx, ao, recommendations)
	assert.NoError(t, err)
	assert.Len(t, tasks, 1)

	task := tasks[0]
	assert.Contains(t, task.Meta, "recommendation")
	assert.Equal(t, recData, task.Meta["recommendation"])
	assert.Contains(t, task.Meta, "ticket_config")
}
