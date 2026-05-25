package optimizer

import (
	"context"
	"nudgebee/runbook/internal/model"

	"github.com/google/uuid"
)

// OptimizerRepository defines the data access interface for the optimizer service.
type OptimizerRepository interface {
	GetAutoOptimize(ctx context.Context, id uuid.UUID) (*model.AutoOptimize, error)
	SaveAutoOptimize(ctx context.Context, ao model.AutoOptimize) error
	GetActiveAutoOptimizes(ctx context.Context) ([]model.AutoOptimize, error)
	GetAutoOptimizeIdsByFilter(ctx context.Context, accountID, tenantID uuid.UUID, status *string, resourceFilters []model.AutoOptimizeResourceFilter) ([]uuid.UUID, error)
	GetResourceFilters(ctx context.Context, accountID, tenantID uuid.UUID, categories []string) ([]string, []string, error)
	GetRecommendations(ctx context.Context, accountID, tenantID uuid.UUID, ruleName *string, status []string, inFilter, likeFilter []string) ([]uuid.UUID, error)
	GetFullRecommendationsForOptimizerCategory(ctx context.Context, accountID uuid.UUID, category string) ([]model.RecommendationWithResource, error)
	SaveAutoOptimizeTasks(ctx context.Context, tasks []model.AutoOptimizeTask) error
	DeleteResourceFilters(ctx context.Context, autoOptimizeID uuid.UUID) error
	SaveResourceFilters(ctx context.Context, filters []model.AutoOptimizeResourceMap) error
	GetFiltersForAutoOptimize(ctx context.Context, autoOptimizeID uuid.UUID) ([]model.AutoOptimizeResourceFilter, error)
	DeleteScheduledTasks(ctx context.Context, autoOptimizeID uuid.UUID) error
	GetAgent(ctx context.Context, accountID uuid.UUID) (*model.Agent, error)
	GetWorkloadFiltersForNamespace(ctx context.Context, accountID, tenantID uuid.UUID, namespace string, category string) ([]model.AutoOptimizeResourceFilter, error)
	UpdateRecommendationStatus(ctx context.Context, id uuid.UUID, status string) error
	GetActiveTasksForRecommendations(ctx context.Context, recommendationIDs []uuid.UUID) (map[uuid.UUID]model.AutoOptimizeTask, error)
	GetActiveResolutionsForRecommendations(ctx context.Context, recommendationIDs []uuid.UUID) (map[uuid.UUID][]model.RecommendationResolution, error)
}
