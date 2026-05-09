package optimizer

import (
	"context"
	"nudgebee/runbook/internal/model"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestCheckRestrictions(t *testing.T) {
	mockRepo := new(MockOptimizerRepository)
	svc := &optimizerService{dao: mockRepo}

	ctx := context.Background()
	accountID := uuid.New()
	tenantID := uuid.New()
	resourceFilters := []model.AutoOptimizeResourceFilter{{Namespace: stringPtr("default")}}

	// Setup conflicting IDs
	conflictingID := uuid.New()
	nonConflictingID := uuid.New()

	// Scenario 1: Vertical vs Continuous (Should Fail)
	t.Run("Vertical blocks Continuous", func(t *testing.T) {
		mockRepo.On("GetAutoOptimizeIdsByFilter", ctx, accountID, tenantID, mock.Anything, resourceFilters).Return([]uuid.UUID{conflictingID}, nil).Once()

		existingAO := &model.AutoOptimize{
			ID:       conflictingID,
			Category: "continuous_rightsize",
			Status:   model.AutoOptimizeStatusActive,
			Name:     stringPtr("Continuous Opt"),
		}
		mockRepo.On("GetAutoOptimize", ctx, conflictingID).Return(existingAO, nil).Once()

		err := svc.checkRestrictions(ctx, accountID, tenantID, "vertical_rightsize", resourceFilters, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "restriction failed")
	})

	// Scenario 2: Continuous vs Vertical (Should Fail)
	t.Run("Continuous blocks Vertical", func(t *testing.T) {
		mockRepo.On("GetAutoOptimizeIdsByFilter", ctx, accountID, tenantID, mock.Anything, resourceFilters).Return([]uuid.UUID{conflictingID}, nil).Once()

		existingAO := &model.AutoOptimize{
			ID:       conflictingID,
			Category: "vertical_rightsize",
			Status:   model.AutoOptimizeStatusActive,
			Name:     stringPtr("Vertical Opt"),
		}
		mockRepo.On("GetAutoOptimize", ctx, conflictingID).Return(existingAO, nil).Once()

		err := svc.checkRestrictions(ctx, accountID, tenantID, "continuous_rightsize", resourceFilters, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "restriction failed")
	})

	// Scenario 3: Vertical vs Horizontal (Should Pass)
	t.Run("Vertical allows Horizontal", func(t *testing.T) {
		mockRepo.On("GetAutoOptimizeIdsByFilter", ctx, accountID, tenantID, mock.Anything, resourceFilters).Return([]uuid.UUID{nonConflictingID}, nil).Once()

		existingAO := &model.AutoOptimize{
			ID:       nonConflictingID,
			Category: "horizontal_rightsize",
			Status:   model.AutoOptimizeStatusActive,
			Name:     stringPtr("Horizontal Opt"),
		}
		mockRepo.On("GetAutoOptimize", ctx, nonConflictingID).Return(existingAO, nil).Once()

		err := svc.checkRestrictions(ctx, accountID, tenantID, "vertical_rightsize", resourceFilters, nil)
		assert.NoError(t, err)
	})

	// Scenario 4: Same Category (Should Fail)
	t.Run("Same Category blocks itself", func(t *testing.T) {
		mockRepo.On("GetAutoOptimizeIdsByFilter", ctx, accountID, tenantID, mock.Anything, resourceFilters).Return([]uuid.UUID{conflictingID}, nil).Once()

		existingAO := &model.AutoOptimize{
			ID:       conflictingID,
			Category: "vertical_rightsize",
			Status:   model.AutoOptimizeStatusActive,
			Name:     stringPtr("Existing Vertical"),
		}
		mockRepo.On("GetAutoOptimize", ctx, conflictingID).Return(existingAO, nil).Once()

		err := svc.checkRestrictions(ctx, accountID, tenantID, "vertical_rightsize", resourceFilters, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "restriction failed")
	})
}

func stringPtr(s string) *string {
	return &s
}
