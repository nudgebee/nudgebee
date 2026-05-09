package optimizer

import (
	"context"
	"fmt"
	"nudgebee/runbook/internal/model"
	"time"

	"github.com/google/uuid"
)

// TaskGenerator defines the interface for strategies that convert recommendations into executable tasks.
type TaskGenerator interface {
	// GenerateTasks takes the AutoOptimize config and a list of recommendations,
	// and returns a list of AutoOptimizeTask models ready to be saved.
	GenerateTasks(ctx context.Context, ao model.AutoOptimize, recommendations []model.RecommendationWithResource) ([]model.AutoOptimizeTask, error)
}

// ExecutorFactory handles the creation of TaskGenerators based on category.
type ExecutorFactory struct {
	generators map[string]TaskGenerator
}

func NewExecutorFactory() *ExecutorFactory {
	return &ExecutorFactory{
		generators: make(map[string]TaskGenerator),
	}
}

func (f *ExecutorFactory) Register(category string, generator TaskGenerator) {
	f.generators[category] = generator
}

func (f *ExecutorFactory) GetGenerator(category string) (TaskGenerator, error) {
	gen, ok := f.generators[category]
	if !ok {
		return nil, fmt.Errorf("no task generator found for category: %s", category)
	}
	return gen, nil
}

// BaseTaskGenerator provides common utility methods for generators (optional helper)
type BaseTaskGenerator struct{}

func (b *BaseTaskGenerator) CreateBaseTask(ao model.AutoOptimize, rec model.Recommendation) model.AutoOptimizeTask {
	// Helper to populate common fields like TenantID, AccountID, etc.
	now := time.Now().UTC()
	return model.AutoOptimizeTask{
		ID:               uuid.New(),
		AutoPilotID:      ao.ID,
		TenantID:         ao.TenantID,
		AccountID:        ao.AccountID,
		RecommendationID: &rec.ID,
		Status:           string(model.AutopilotTaskStatusScheduled),
		CreatedAt:        now,
		UpdatedAt:        now,
		ResourceFilter:   model.AutoOptimizeResourceFilter{}, // Needs to be populated from rec/resource
		Meta:             make(map[string]interface{}),
		Attributes:       model.AutoOptimizeTaskAttributes{},
	}
}
