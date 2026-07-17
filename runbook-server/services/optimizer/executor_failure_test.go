package optimizer

import (
	"context"
	"nudgebee/runbook/internal/model"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// When a scheduled run fails before producing any tasks, GenerateTasks must
// leave a visible Failed task row and reset the config out of InProgress,
// instead of silently erroring (the audit gap in #33483).
func TestGenerateTasks_RecordsRunFailureAndResetsState(t *testing.T) {
	mockRepo := new(MockOptimizerRepository)

	svc := &optimizerService{
		dao:     mockRepo,
		factory: NewExecutorFactory(),
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
	}

	mockRepo.On("GetAutoOptimize", mock.Anything, aoID).Return(ao, nil)
	mockRepo.On("GetAgent", mock.Anything, accountID).Return(&model.Agent{Status: "Connected"}, nil)
	mockRepo.On("SaveAutoOptimize", mock.Anything, mock.Anything).Return(nil)

	// Generation fails before any task is produced.
	mockRepo.On("GetFullRecommendationsForOptimizerCategory", mock.Anything, accountID, "vertical_rightsize").
		Return([]model.RecommendationWithResource{}, assert.AnError)

	// Exactly one terminal failure record is persisted, scoped to the config.
	mockRepo.On("SaveAutoOptimizeTasks", mock.Anything, mock.MatchedBy(func(tasks []model.AutoOptimizeTask) bool {
		if len(tasks) != 1 {
			return false
		}
		task := tasks[0]
		return task.Status == string(model.AutopilotTaskStatusFailed) &&
			task.AutoPilotID == aoID &&
			task.AccountID == accountID &&
			task.TenantID == tenantID &&
			task.RecommendationID == nil &&
			task.Reason != nil
	})).Return(nil)

	tasks, err := svc.GenerateTasks(ctx, aoID)

	// The run completes cleanly (failure captured as data, not a hard error),
	// so the Temporal activity won't retry and duplicate the record.
	assert.NoError(t, err)
	assert.Empty(t, tasks)
	mockRepo.AssertExpectations(t)

	// Execution status was reset to Idle, not left stuck InProgress.
	mockRepo.AssertCalled(t, "SaveAutoOptimize", mock.Anything, mock.MatchedBy(func(saved model.AutoOptimize) bool {
		return saved.ExecutionStatus == string(model.AutopilotExecutionStatusIdle)
	}))
}

// A disconnected agent is environmental, not a run failure: the run must skip
// quietly (no tasks, no failure record, no error) so the activity completes
// instead of erroring and retrying (mirrors #33393).
func TestGenerateTasks_NotConnectedAgentSkipsQuietly(t *testing.T) {
	mockRepo := new(MockOptimizerRepository)

	svc := &optimizerService{
		dao:     mockRepo,
		factory: NewExecutorFactory(),
	}

	ctx := context.Background()
	aoID := uuid.New()
	accountID := uuid.New()

	ao := &model.AutoOptimize{
		ID:        aoID,
		AccountID: accountID,
		TenantID:  uuid.New(),
		Category:  "vertical_rightsize",
		Status:    model.AutoOptimizeStatusActive,
	}

	mockRepo.On("GetAutoOptimize", mock.Anything, aoID).Return(ao, nil)
	mockRepo.On("GetAgent", mock.Anything, accountID).Return(&model.Agent{Status: "NotConnected"}, nil)

	tasks, err := svc.GenerateTasks(ctx, aoID)

	assert.NoError(t, err)
	assert.Empty(t, tasks)
	// Never reached InProgress / recommendation fetch / task persistence.
	mockRepo.AssertNotCalled(t, "SaveAutoOptimize", mock.Anything, mock.Anything)
	mockRepo.AssertNotCalled(t, "GetFullRecommendationsForOptimizerCategory", mock.Anything, mock.Anything, mock.Anything)
	mockRepo.AssertNotCalled(t, "SaveAutoOptimizeTasks", mock.Anything, mock.Anything)
}
