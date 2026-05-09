package optimizer

import (
	"context"
	"nudgebee/runbook/internal/model"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockGenerator is a mock TaskGenerator
type MockGenerator struct {
	mock.Mock
}

func (m *MockGenerator) GenerateTasks(ctx context.Context, ao model.AutoOptimize, recommendations []model.RecommendationWithResource) ([]model.AutoOptimizeTask, error) {
	args := m.Called(ctx, ao, recommendations)
	return args.Get(0).([]model.AutoOptimizeTask), args.Error(1)
}

func TestGenerateTasks_NamespacePriority(t *testing.T) {
	mockRepo := new(MockOptimizerRepository)
	mockGen := new(MockGenerator)

	// Create factory and register mock generator
	factory := NewExecutorFactory()
	factory.Register("vertical_rightsize", mockGen)

	svc := &optimizerService{
		dao:     mockRepo,
		factory: factory,
	}

	ctx := context.Background()
	aoID := uuid.New()
	accountID := uuid.New()
	tenantID := uuid.New()

	ao := &model.AutoOptimize{
		ID:        aoID,
		AccountID: accountID,
		TenantID:  tenantID,
		Category:  "vertical_rightsize",
		Status:    model.AutoOptimizeStatusActive,
		ResourceFilters: []model.AutoOptimizeResourceFilter{
			{Namespace: stringPtr("default")}, // Namespace-level rule
		},
	}

	// Mock GetAutoOptimize
	mockRepo.On("GetAutoOptimize", ctx, aoID).Return(ao, nil)

	// Mock Pre-flight checks (Agent)
	mockRepo.On("GetAgent", ctx, accountID).Return(&model.Agent{Status: "Connected"}, nil)

	// Mock SaveAutoOptimize (Execution Status update)
	mockRepo.On("SaveAutoOptimize", ctx, mock.Anything).Return(nil)

	// Mock GetFullRecommendations
	mockRepo.On("GetFullRecommendationsForOptimizerCategory", ctx, accountID, "vertical_rightsize").Return([]model.RecommendationWithResource{}, nil)

	// Mock Generator response
	tasks := []model.AutoOptimizeTask{
		{
			ID: uuid.New(),
			ResourceFilter: model.AutoOptimizeResourceFilter{
				Namespace: stringPtr("default"),
				Type:      stringPtr("Deployment"),
				Name:      stringPtr("specific-app"),
			},
			Status: string(model.AutopilotTaskStatusScheduled),
		},
		{
			ID: uuid.New(),
			ResourceFilter: model.AutoOptimizeResourceFilter{
				Namespace: stringPtr("default"),
				Type:      stringPtr("Deployment"),
				Name:      stringPtr("other-app"),
			},
			Status: string(model.AutopilotTaskStatusScheduled),
		},
	}
	mockGen.On("GenerateTasks", ctx, mock.Anything, mock.Anything).Return(tasks, nil)

	// Mock Workload Priority Check
	// The repo should return a specific filter for 'specific-app' in 'default'
	specificFilter := model.AutoOptimizeResourceFilter{
		Namespace: stringPtr("default"),
		Type:      stringPtr("Deployment"),
		Name:      stringPtr("specific-app"),
	}
	// Note: We need to match the signature added to Repo
	mockRepo.On("GetWorkloadFiltersForNamespace", ctx, accountID, tenantID, "default", "vertical_rightsize").Return([]model.AutoOptimizeResourceFilter{specificFilter}, nil)

	// Mock SaveAutoOptimizeTasks
	// We verify that the tasks passed to save have correct status
	mockRepo.On("SaveAutoOptimizeTasks", ctx, mock.MatchedBy(func(savedTasks []model.AutoOptimizeTask) bool {
		if len(savedTasks) != 2 {
			return false
		}
		// find specific-app task
		for _, t := range savedTasks {
			switch *t.ResourceFilter.Name {
			case "specific-app":
				if t.Status != string(model.AutopilotTaskStatusSkipped) {
					return false
				}
				if t.Reason == nil || *t.Reason != "Will be handled by workload level auto optimize" {
					return false
				}
			case "other-app":
				if t.Status != string(model.AutopilotTaskStatusScheduled) {
					return false
				}
			}
		}
		return true
	})).Return(nil)

	// Run
	_, err := svc.GenerateTasks(ctx, aoID)
	assert.NoError(t, err)
}
