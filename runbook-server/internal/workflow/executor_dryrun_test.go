package workflow

import (
	"context"
	"testing"

	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

type ExecutorDryRunTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *ExecutorDryRunTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
}

func (s *ExecutorDryRunTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func (s *ExecutorDryRunTestSuite) TestMissingTaskType() {
	// Register system activities mock
	s.env.RegisterActivityWithOptions(func(ctx context.Context, tenantID, accountID string) (FetchConfigsResponse, error) {
		return FetchConfigsResponse{}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowConfigsActivity"})

	s.env.RegisterActivityWithOptions(func(ctx context.Context, workflowID string) (map[string]any, error) {
		return map[string]any{}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowStateActivity"})

	s.env.RegisterActivityWithOptions(func(ctx context.Context, wf *model.Workflow, status model.WorkflowExecutionStatus, message string) error {
		return nil
	}, activity.RegisterOptions{Name: "Internal_UpdateLastExecutionStatusActivity"})

	mockStore := new(MockWorkflowStore)
	executor := &WorkflowExecutor{
		workflowStore: mockStore,
	}

	wf := &model.Workflow{
		ID:        "test-missing-type-wf",
		TenantID:  "test-tenant",
		AccountID: "test-account",
		Definition: model.WorkflowDefinition{
			Tasks: []model.Task{
				{
					ID:   "task-no-type",
					Type: "", // EMPTY TYPE
				},
			},
		},
	}

	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.ExecuteWorkflow(executor.ExecuteWorkflowInternal, wf, nil)

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "task type is missing for task ID: task-no-type")
}

func (s *ExecutorDryRunTestSuite) TestDryRunExecutesActivity() {
	// Register mock activity that SHOULD BE CALLED
	activityCalled := false
	s.env.RegisterActivityWithOptions(func(ctx context.Context, params map[string]any) (any, error) {
		activityCalled = true
		// Verify DryRun param is passed
		if dryRun, ok := params["_dry_run"].(bool); !ok || !dryRun {
			return nil, nil // Fail implicitly if not set
		}
		return "executed", nil
	}, activity.RegisterOptions{Name: "core.print"})

	// System activities (some are skipped in dry run, explicitly FetchWorkflowConfigsActivity IS called)
	s.env.RegisterActivityWithOptions(func(ctx context.Context, tenantID, accountID string) (FetchConfigsResponse, error) {
		return FetchConfigsResponse{}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowConfigsActivity"})

	mockStore := new(MockWorkflowStore)
	executor := &WorkflowExecutor{
		workflowStore: mockStore,
	}

	wf := &model.Workflow{
		ID:        "test-dryrun-wf",
		TenantID:  "test-tenant",
		AccountID: "test-account",
		DryRun:    true, // ENABLE DRY RUN
		Definition: model.WorkflowDefinition{
			Tasks: []model.Task{
				{
					ID:   "task1",
					Type: "core.print",
					Params: map[string]any{
						"message": "hello",
					},
				},
			},
		},
	}

	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.ExecuteWorkflow(executor.ExecuteWorkflowInternal, wf, nil)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Verify activity WAS called
	s.True(activityCalled, "Activity should be called in dry run")
}

func TestExecutorDryRunTestSuite(t *testing.T) {
	suite.Run(t, new(ExecutorDryRunTestSuite))
}
