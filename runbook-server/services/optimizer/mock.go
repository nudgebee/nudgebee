package optimizer

import (
	"context"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

// MockOptimizerService is a mock implementation of the Service interface.
type MockOptimizerService struct {
	mock.Mock
}

func (m *MockOptimizerService) GetRecommendations(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, ruleName *string, status []string, categories []string) (map[string][]string, error) {
	args := m.Called(ctx, sc, accountID, ruleName, status, categories)
	return args.Get(0).(map[string][]string), args.Error(1)
}

func (m *MockOptimizerService) GetWorkload(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, status *string, resourceFilters []model.AutoOptimizeResourceFilter) ([]string, error) {
	args := m.Called(ctx, sc, accountID, status, resourceFilters)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockOptimizerService) SkipExecution(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, autoOptimizeID uuid.UUID, byMinutes int) (string, error) {
	args := m.Called(ctx, sc, accountID, autoOptimizeID, byMinutes)
	return args.String(0), args.Error(1)
}

func (m *MockOptimizerService) UpdateAutoOptimize(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, req model.AutoOptimizeRequestModel) (*string, error) {
	args := m.Called(ctx, sc, accountID, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*string), args.Error(1)
}

func (m *MockOptimizerService) CreateAutoOptimize(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, req model.AutoOptimizeRequestModel) (*string, error) {
	args := m.Called(ctx, sc, accountID, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*string), args.Error(1)
}

func (m *MockOptimizerService) ChangeStatus(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, autoOptimizeID uuid.UUID, status model.AutoOptimizeStatus) error {
	args := m.Called(ctx, sc, accountID, autoOptimizeID, status)
	return args.Error(0)
}

func (m *MockOptimizerService) GetActiveAutoOptimizes(ctx context.Context) ([]model.AutoOptimize, error) {
	args := m.Called(ctx)
	return args.Get(0).([]model.AutoOptimize), args.Error(1)
}

func (m *MockOptimizerService) ExecuteAutoOptimize(ctx context.Context, autoOptimizeID uuid.UUID) error {
	args := m.Called(ctx, autoOptimizeID)
	return args.Error(0)
}

func (m *MockOptimizerService) GenerateTasks(ctx context.Context, autoOptimizeID uuid.UUID) ([]model.AutoOptimizeTask, error) {
	args := m.Called(ctx, autoOptimizeID)
	return args.Get(0).([]model.AutoOptimizeTask), args.Error(1)
}

func (m *MockOptimizerService) CompleteAutoOptimize(ctx context.Context, autoOptimizeID uuid.UUID) error {
	args := m.Called(ctx, autoOptimizeID)
	return args.Error(0)
}

func (m *MockOptimizerService) SyncSchedules(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}
