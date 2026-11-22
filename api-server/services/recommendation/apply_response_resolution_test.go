package recommendation

import (
	"testing"

	"nudgebee/services/account/adapter"
	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
)

func TestWithSettledStatus(t *testing.T) {
	created := models.RecommendationResolution{Id: "r1", Status: models.RecommendationResolutionStatusInProgress}

	failed := withSettledStatus(created, adapter.RecommendationResolutionStatusFailed)
	assert.Equal(t, models.RecommendationResolutionStatusFailed, failed.Status)
	assert.Equal(t, "r1", failed.Id)

	success := withSettledStatus(created, adapter.RecommendationResolutionStatusSuccess)
	assert.Equal(t, models.RecommendationResolutionStatusSuccess, success.Status)

	assert.Equal(t, models.RecommendationResolutionStatusInProgress, created.Status, "the created resolution is copied, not mutated")
}
