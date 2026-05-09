package optimizer

import (
	"context"
	"nudgebee/runbook/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

type MockOptimizerRepository struct {
	mock.Mock
}

func (m *MockOptimizerRepository) GetAutoOptimize(ctx context.Context, id uuid.UUID) (*model.AutoOptimize, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.AutoOptimize), args.Error(1)
}

func (m *MockOptimizerRepository) SaveAutoOptimize(ctx context.Context, ao model.AutoOptimize) error {
	args := m.Called(ctx, ao)
	return args.Error(0)
}

func (m *MockOptimizerRepository) GetActiveAutoOptimizes(ctx context.Context) ([]model.AutoOptimize, error) {
	args := m.Called(ctx)
	return args.Get(0).([]model.AutoOptimize), args.Error(1)
}

func (m *MockOptimizerRepository) GetAutoOptimizeIdsByFilter(ctx context.Context, accountID, tenantID uuid.UUID, status *string, resourceFilters []model.AutoOptimizeResourceFilter) ([]uuid.UUID, error) {
	args := m.Called(ctx, accountID, tenantID, status, resourceFilters)
	return args.Get(0).([]uuid.UUID), args.Error(1)
}

func (m *MockOptimizerRepository) GetResourceFilters(ctx context.Context, accountID, tenantID uuid.UUID, categories []string) ([]string, []string, error) {
	args := m.Called(ctx, accountID, tenantID, categories)
	return args.Get(0).([]string), args.Get(1).([]string), args.Error(2)
}

func (m *MockOptimizerRepository) GetRecommendations(ctx context.Context, accountID, tenantID uuid.UUID, ruleName *string, status []string, inFilter, likeFilter []string) ([]uuid.UUID, error) {
	args := m.Called(ctx, accountID, tenantID, ruleName, status, inFilter, likeFilter)
	return args.Get(0).([]uuid.UUID), args.Error(1)
}

func (m *MockOptimizerRepository) GetFullRecommendationsForOptimizerCategory(ctx context.Context, accountID uuid.UUID, category string) ([]model.RecommendationWithResource, error) {
	args := m.Called(ctx, accountID, category)
	return args.Get(0).([]model.RecommendationWithResource), args.Error(1)
}

func (m *MockOptimizerRepository) SaveAutoOptimizeTasks(ctx context.Context, tasks []model.AutoOptimizeTask) error {
	args := m.Called(ctx, tasks)
	return args.Error(0)
}

func (m *MockOptimizerRepository) DeleteResourceFilters(ctx context.Context, autoOptimizeID uuid.UUID) error {
	args := m.Called(ctx, autoOptimizeID)
	return args.Error(0)
}

func (m *MockOptimizerRepository) SaveResourceFilters(ctx context.Context, filters []model.AutoOptimizeResourceMap) error {
	args := m.Called(ctx, filters)
	return args.Error(0)
}

func (m *MockOptimizerRepository) GetFiltersForAutoOptimize(ctx context.Context, autoOptimizeID uuid.UUID) ([]model.AutoOptimizeResourceFilter, error) {
	args := m.Called(ctx, autoOptimizeID)
	return args.Get(0).([]model.AutoOptimizeResourceFilter), args.Error(1)
}

func (m *MockOptimizerRepository) DeleteScheduledTasks(ctx context.Context, autoOptimizeID uuid.UUID) error {
	args := m.Called(ctx, autoOptimizeID)
	return args.Error(0)
}

func (m *MockOptimizerRepository) GetAgent(ctx context.Context, accountID uuid.UUID) (*model.Agent, error) {
	args := m.Called(ctx, accountID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.Agent), args.Error(1)
}

func (m *MockOptimizerRepository) GetWorkloadFiltersForNamespace(ctx context.Context, accountID, tenantID uuid.UUID, namespace string, category string) ([]model.AutoOptimizeResourceFilter, error) {
	args := m.Called(ctx, accountID, tenantID, namespace, category)
	return args.Get(0).([]model.AutoOptimizeResourceFilter), args.Error(1)
}

func (m *MockOptimizerRepository) UpdateRecommendationStatus(ctx context.Context, id uuid.UUID, status string) error {
	args := m.Called(ctx, id, status)
	return args.Error(0)
}

func (m *MockOptimizerRepository) GetActiveTasksForRecommendations(ctx context.Context, recommendationIDs []uuid.UUID) (map[uuid.UUID]model.AutoOptimizeTask, error) {
	args := m.Called(ctx, recommendationIDs)
	return args.Get(0).(map[uuid.UUID]model.AutoOptimizeTask), args.Error(1)
}

func (m *MockOptimizerRepository) GetActiveResolutionsForRecommendations(ctx context.Context, recommendationIDs []uuid.UUID) (map[uuid.UUID][]model.RecommendationResolution, error) {
	args := m.Called(ctx, recommendationIDs)
	return args.Get(0).(map[uuid.UUID][]model.RecommendationResolution), args.Error(1)
}
