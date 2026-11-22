package workflow

import (
	"context"
	"strings"
	"sync"
	"testing"

	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	commonpb "go.temporal.io/api/common/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
)

func TestCollectFailedStepsPicksFirstInDefinitionOrder(t *testing.T) {
	def := []model.Task{{ID: "b_step"}, {ID: "group", Tasks: []model.Task{{ID: "a_step"}}}, {ID: "ok"}}
	results := map[string]map[string]any{
		"a_step": {"status": model.TaskStatusFailed, "error": "second"},
		"b_step": {"status": model.TaskStatusFailed, "error": "first"},
		"ok":     {"status": string(model.TaskStatusCompleted)},
	}

	got := collectFailedSteps(def, results)

	assert.Equal(t, model.FailedSteps{FailedStepCount: 2, FirstFailedStep: "b_step", FirstFailedStepError: "first"}, got)
	assert.Equal(t, model.FailedSteps{}, collectFailedSteps(def, map[string]map[string]any{"ok": results["ok"]}))
}

func TestStepErrorMessageStripsTemporalWrappers(t *testing.T) {
	wrapped := "activity error (type: core.print, scheduledEventID: 0, startedEventID: 0, identity: ): User not found (type: SlackError, retryable: false)"
	assert.Equal(t, "User not found", stepErrorMessage(wrapped))
	assert.Equal(t, "plain error", stepErrorMessage("plain error"))
}

func TestCollectFailedStepsTruncatesError(t *testing.T) {
	long := strings.Repeat("x", maxFailedStepErrorRunes+50)
	got := collectFailedSteps(nil, map[string]map[string]any{"s": {"status": model.TaskStatusFailed, "error": long}})
	assert.Equal(t, maxFailedStepErrorRunes+1, len([]rune(got.FirstFailedStepError)))
}

func TestFailedStepsStatusMessageRoundTrip(t *testing.T) {
	msg := failedStepsStatusMessage(model.FailedSteps{FailedStepCount: 1, FirstFailedStep: "send-slack-dm", FirstFailedStepError: "User not found"})
	assert.Equal(t, "1 step failed; first: send-slack-dm: User not found", msg)
	assert.Equal(t, 1, failedStepCountFromStatusMessage(model.WorkflowExecutionStatusCompleted, &msg))

	many := failedStepsStatusMessage(model.FailedSteps{FailedStepCount: 3, FirstFailedStep: "x"})
	assert.Equal(t, 3, failedStepCountFromStatusMessage(model.WorkflowExecutionStatusCompleted, &many))

	// Failed runs carry their own error text; only a completed run is marked.
	assert.Equal(t, 0, failedStepCountFromStatusMessage(model.WorkflowExecutionStatusFailed, &msg))
	other := "Execution canceled"
	assert.Equal(t, 0, failedStepCountFromStatusMessage(model.WorkflowExecutionStatusCompleted, &other))
	assert.Equal(t, 0, failedStepCountFromStatusMessage(model.WorkflowExecutionStatusCompleted, nil))
	assert.Equal(t, "", failedStepsStatusMessage(model.FailedSteps{}))
}

func failedStepsMemoPB(t *testing.T, dc converter.DataConverter, memo map[string]any) *commonpb.Memo {
	t.Helper()
	fields := map[string]*commonpb.Payload{}
	for k, v := range memo {
		p, err := dc.ToPayload(v)
		if err != nil {
			t.Fatalf("encode memo %s: %v", k, err)
		}
		fields[k] = p
	}
	return &commonpb.Memo{Fields: fields}
}

// The dashboard rows read the mark straight off the visibility record's Memo.
func TestExecutionSummaryCarriesFailedSteps(t *testing.T) {
	dc := converter.GetDefaultDataConverter()
	s := &Service{dataConverter: dc}
	want := model.FailedSteps{FailedStepCount: 1, FirstFailedStep: "send-slack-dm", FirstFailedStepError: "User not found"}

	summary := s.executionSummaryFromInfo(&workflowpb.WorkflowExecutionInfo{Memo: failedStepsMemoPB(t, dc, failedStepsMemo(want))})
	assert.Equal(t, want, summary.FailedSteps)

	clean := s.executionSummaryFromInfo(&workflowpb.WorkflowExecutionInfo{Memo: failedStepsMemoPB(t, dc, map[string]any{model.MemoWorkflowVersionNumber: int64(2)})})
	assert.Equal(t, model.FailedSteps{}, clean.FailedSteps)
}

type statusWriteWithMessage struct {
	WorkflowID string
	Status     model.WorkflowExecutionStatus
	Message    string
}

// registerFailedStepActivities makes core.print fail (non-retryable) when its
// message is "fail", echoing the secret so the test can check scrubbing.
func (s *ExecutorTestSuite) registerFailedStepActivities(writes *[]statusWriteWithMessage, memos *[]map[string]any, mu *sync.Mutex) {
	s.env.RegisterActivityWithOptions(func(ctx context.Context, params map[string]any) (any, error) {
		msg, _ := params["message"].(string)
		if msg == "fail" {
			return nil, temporal.NewNonRetryableApplicationError("User not found (token s3cr3t-value)", "SlackError", nil)
		}
		return map[string]string{"data": msg}, nil
	}, activity.RegisterOptions{Name: "core.print"})
	s.env.RegisterActivityWithOptions(func(ctx context.Context, wf *model.Workflow, status model.WorkflowExecutionStatus, message string) error {
		mu.Lock()
		defer mu.Unlock()
		*writes = append(*writes, statusWriteWithMessage{WorkflowID: wf.ID, Status: status, Message: message})
		return nil
	}, activity.RegisterOptions{Name: "Internal_UpdateLastExecutionStatusActivity"})
	s.env.RegisterActivityWithOptions(func(ctx context.Context, workflowID string) (map[string]any, error) {
		return map[string]any{}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowStateActivity"})
	s.env.RegisterActivityWithOptions(func(ctx context.Context, tenantID, accountID string, restrictToAccount bool) (FetchConfigsResponse, error) {
		return FetchConfigsResponse{Secrets: map[string]any{"slack_token": "s3cr3t-value"}}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowConfigsActivity"})
	s.env.RegisterActivityWithOptions(func(ctx context.Context, typeRefID string, failed bool) error {
		return nil
	}, activity.RegisterOptions{Name: "Internal_UpdateEventResolutionStatusActivity"})
	s.env.RegisterActivityWithOptions(func(ctx context.Context, workflowID string, stateUpdates map[string]model.StateUpdateDTO, executionID, taskID string) error {
		return nil
	}, activity.RegisterOptions{Name: "UpdateWorkflowStateActivity"})
	s.env.OnUpsertMemo(mock.Anything).Run(func(args mock.Arguments) {
		mu.Lock()
		defer mu.Unlock()
		*memos = append(*memos, args.Get(0).(map[string]any))
	}).Return(nil).Maybe()
}

func continueOnFailureWorkflow(id string, failing bool) *model.Workflow {
	msg := "hello"
	if failing {
		msg = "fail"
	}
	return &model.Workflow{
		ID:        id,
		TenantID:  "test-tenant",
		AccountID: "test-account",
		Name:      "notify",
		Definition: model.WorkflowDefinition{
			Tasks: []model.Task{{
				ID:            "send-slack-dm",
				Type:          "core.print",
				Params:        map[string]any{"message": msg},
				FailurePolicy: &model.FailurePolicy{Action: "continue"},
			}},
		},
	}
}

// #40461: a run whose only step failed under failure_policy "continue" stays
// COMPLETED but records the failed step on its Memo and status message.
func (s *ExecutorTestSuite) TestContinueOnFailureStepMarksCompletedRun() {
	var mu sync.Mutex
	var writes []statusWriteWithMessage
	var memos []map[string]any
	s.registerFailedStepActivities(&writes, &memos, &mu)
	executor := s.newStatusExecutor()

	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.ExecuteWorkflow(executor.ExecuteWorkflowInternal, continueOnFailureWorkflow("wf-uuid", true), map[string]any{})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	mu.Lock()
	defer mu.Unlock()
	s.Require().Len(memos, 1, "the failed step is recorded on the run's Memo")
	s.Equal(1, memos[0][model.MemoFailedStepCount])
	s.Equal("send-slack-dm", memos[0][model.MemoFirstFailedStep])
	errText, _ := memos[0][model.MemoFirstFailedStepError].(string)
	s.Equal("User not found (token ***)", errText)
	s.NotContains(errText, "s3cr3t-value", "step error is secret-scrubbed before it is recorded")

	s.Require().NotEmpty(writes)
	last := writes[len(writes)-1]
	s.Equal(model.WorkflowExecutionStatusCompleted, last.Status)
	s.Equal("1 step failed; first: send-slack-dm: User not found (token ***)", last.Message)
	s.NotContains(last.Message, "s3cr3t-value")
}

func (s *ExecutorTestSuite) TestCleanRunRecordsNoFailedSteps() {
	var mu sync.Mutex
	var writes []statusWriteWithMessage
	var memos []map[string]any
	s.registerFailedStepActivities(&writes, &memos, &mu)
	executor := s.newStatusExecutor()

	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.ExecuteWorkflow(executor.ExecuteWorkflowInternal, continueOnFailureWorkflow("wf-uuid", false), map[string]any{})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	mu.Lock()
	defer mu.Unlock()
	s.Empty(memos)
	s.Require().NotEmpty(writes)
	s.Equal(statusWriteWithMessage{WorkflowID: "wf-uuid", Status: model.WorkflowExecutionStatusCompleted}, writes[len(writes)-1])
}

// A called run gets the same mark on the callee's row.
func (s *ExecutorTestSuite) TestCalledRunWithFailedStepMarksCalleeRow() {
	var mu sync.Mutex
	var writes []statusWriteWithMessage
	var memos []map[string]any
	s.registerFailedStepActivities(&writes, &memos, &mu)
	executor := s.newStatusExecutor()

	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.RegisterWorkflow(callerWorkflow)
	s.env.ExecuteWorkflow(callerWorkflow, continueOnFailureWorkflow("inline-call_child-1234", true), "callee-uuid")

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	mu.Lock()
	defer mu.Unlock()
	s.Require().NotEmpty(writes)
	last := writes[len(writes)-1]
	s.Equal("callee-uuid", last.WorkflowID)
	s.Equal(model.WorkflowExecutionStatusCompleted, last.Status)
	s.True(strings.HasPrefix(last.Message, "1 step failed; first: send-slack-dm"), last.Message)
}
