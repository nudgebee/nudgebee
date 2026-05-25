package optimizer

import (
	"context"
	"nudgebee/runbook/internal/model"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestHorizontalRightsizeGenerator_MLParsing(t *testing.T) {
	generator := &HorizontalRightsizeGenerator{}
	ctx := context.Background()

	ao := model.AutoOptimize{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Category:  "horizontal_rightsize",
		Rule:      map[string]interface{}{},
	}

	// 1. Static Parsing (already works, but verify)
	t.Run("Static recommended_replicas", func(t *testing.T) {
		recs := []model.RecommendationWithResource{{
			Recommendation: model.Recommendation{
				Recommendation: map[string]any{"recommended_replicas": 5.0},
			},
			ResourceIdentifier: "ns/Deployment/app",
		}}
		tasks, _ := generator.GenerateTasks(ctx, ao, recs)
		assert.Len(t, tasks, 1)
		assert.Equal(t, 5.0, tasks[0].Meta["change_to"])
	})

	// 2. ML-Based Parsing
	t.Run("ML-Based time-series", func(t *testing.T) {
		// Target time: next hour
		nextHour := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
		timeKey := nextHour.Format("2006-01-02T15:04:05")

		recData := map[string]any{
			"recommendation": map[string]any{
				"recommended": map[string]any{
					timeKey: 10.0,
				},
			},
		}

		recs := []model.RecommendationWithResource{{
			Recommendation: model.Recommendation{
				Recommendation: recData,
			},
			ResourceIdentifier: "ns/Deployment/app",
		}}

		tasks, err := generator.GenerateTasks(ctx, ao, recs)
		assert.NoError(t, err)
		assert.Len(t, tasks, 1)
		assert.Equal(t, 10.0, tasks[0].Meta["change_to"])
	})
}
