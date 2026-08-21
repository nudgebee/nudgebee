package optimizer

import (
	"context"
	"strings"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/workflow"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Regression tests for #34943 — a run that goes ahead against a recommendation
// whose pull request is still open gets handed back the resolution it already
// recorded, aborts on a duplicate key before writing any status, and leaves the
// task in Scheduled. A task stuck in Scheduled reads as "waiting its turn", so
// it held that workload back from every later run, permanently.

// TestScheduledTaskStaleAfterExceedsWorkflowTimeout pins the relationship the
// staleness bound depends on. Tasks older than ScheduledTaskStaleAfter stop
// blocking their recommendation; if that window ever dropped to or below the
// workflow's execution timeout, a slow-but-live run could be double-scheduled
// and open a second pull request — the regression #33523 fixed.
func TestScheduledTaskStaleAfterExceedsWorkflowTimeout(t *testing.T) {
	assert.Greater(t, model.ScheduledTaskStaleAfter, workflow.OptimizerWorkflowExecutionTimeout,
		"ScheduledTaskStaleAfter must stay above the optimizer workflow execution timeout, "+
			"otherwise a task belonging to a still-running workflow can be treated as stale "+
			"and its recommendation scheduled a second time")
}

// TestGenerateTasks_SkipsRecommendationWithOpenPR is the first line of defence:
// when a recommendation still has an open pull request, the run must mark the
// task Skipped rather than execute it. Executing is what walked into the
// duplicate-resolution abort.
func TestGenerateTasks_SkipsRecommendationWithOpenPR(t *testing.T) {
	mockRepo := new(MockOptimizerRepository)
	mockGen := new(MockGenerator)

	factory := NewExecutorFactory()
	factory.Register("vertical_rightsize", mockGen)

	svc := &optimizerService{dao: mockRepo, factory: factory}

	ctx := context.Background()
	aoID, accountID, tenantID := uuid.New(), uuid.New(), uuid.New()
	openRecID, freeRecID := uuid.New(), uuid.New()

	ao := &model.AutoOptimize{
		ID:        aoID,
		AccountID: accountID,
		TenantID:  tenantID,
		Category:  "vertical_rightsize",
		Status:    model.AutoOptimizeStatusActive,
	}

	mockRepo.On("GetAutoOptimize", ctx, aoID).Return(ao, nil)
	mockRepo.On("GetAgent", ctx, accountID).Return(&model.Agent{Status: "Connected"}, nil)
	mockRepo.On("SaveAutoOptimize", ctx, mock.Anything).Return(nil)
	mockRepo.On("GetFullRecommendationsForOptimizerCategory", ctx, accountID, "vertical_rightsize").
		Return([]model.RecommendationWithResource{}, nil)

	tasks := []model.AutoOptimizeTask{
		{ID: uuid.New(), RecommendationID: &openRecID, Name: "Vertical Rightsize Deployment ns/with-open-pr", Status: string(model.AutopilotTaskStatusScheduled)},
		{ID: uuid.New(), RecommendationID: &freeRecID, Name: "Vertical Rightsize Deployment ns/no-open-pr", Status: string(model.AutopilotTaskStatusScheduled)},
	}
	mockGen.On("GenerateTasks", ctx, mock.Anything, mock.Anything).Return(tasks, nil)

	mockRepo.On("GetActiveTasksForRecommendations", ctx, mock.Anything).
		Return(map[uuid.UUID]model.AutoOptimizeTask{}, nil)

	// Only the first recommendation has a resolution the api-server's open-PR
	// guard would still consider open.
	prURL := "https://github.com/acme/infra/pull/860"
	mockRepo.On("GetActiveResolutionsForRecommendations", ctx, mock.Anything).
		Return(map[uuid.UUID][]model.RecommendationResolution{
			openRecID: {{
				ID:               uuid.New(),
				RecommendationID: openRecID,
				Type:             string(model.RecommendationResolutionTypePullRequest),
				Status:           string(model.RecommendationResolutionStatusFailed),
				TypeReferenceID:  prURL,
			}},
		}, nil)

	var saved []model.AutoOptimizeTask
	mockRepo.On("SaveAutoOptimizeTasks", ctx, mock.MatchedBy(func(ts []model.AutoOptimizeTask) bool {
		saved = ts
		return true
	})).Return(nil)

	_, err := svc.GenerateTasks(ctx, aoID)
	assert.NoError(t, err)

	byRec := map[uuid.UUID]model.AutoOptimizeTask{}
	for _, task := range saved {
		if task.RecommendationID != nil {
			byRec[*task.RecommendationID] = task
		}
	}

	openTask, ok := byRec[openRecID]
	assert.True(t, ok, "expected a task for the recommendation with an open PR")
	assert.Equal(t, string(model.AutopilotTaskStatusSkipped), openTask.Status,
		"a recommendation whose PR is still open must be skipped, not executed")
	if assert.NotNil(t, openTask.Reason) {
		assert.True(t, strings.Contains(*openTask.Reason, prURL),
			"the skip reason should name the open PR, got %q", *openTask.Reason)
	}

	freeTask, ok := byRec[freeRecID]
	assert.True(t, ok, "expected a task for the unblocked recommendation")
	assert.Equal(t, string(model.AutopilotTaskStatusScheduled), freeTask.Status,
		"a recommendation with no open PR must still be scheduled")
}
