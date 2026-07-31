package workflow

import (
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	commonapi "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	historyapi "go.temporal.io/api/history/v1"
	workflowapi "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
)

// switchWorkflowDefinition mirrors the shape the canvas produces for a Switch:
// the branch tasks carry no depends_on, they are reachable only through
// `cases[].next`.
func switchWorkflowDefinition() model.WorkflowDefinition {
	return model.WorkflowDefinition{
		Tasks: []model.Task{
			{
				ID:   "dispatch",
				Type: "core.switch",
				Params: map[string]any{
					"expression": "{{ Tasks.catalogue.output.platform }}",
					"cases": []any{
						map[string]any{"value": "kubernetes", "next": "call_k8s"},
						map[string]any{"value": "linux", "next": "call_linux"},
						map[string]any{"value": "aws", "next": "call_aws"},
					},
				},
			},
			{ID: "call_k8s", Type: "core.call-workflow", Params: map[string]any{"workflow_name": "demo-restart-k8s"}},
			{ID: "call_linux", Type: "core.call-workflow", Params: map[string]any{"workflow_name": "demo-restart-linux"}},
			{ID: "call_aws", Type: "core.call-workflow", Params: map[string]any{"workflow_name": "demo-restart-aws"}},
		},
	}
}

func payloadOf(t *testing.T, dc converter.DataConverter, v any) *commonapi.Payloads {
	t.Helper()
	p, err := dc.ToPayloads(v)
	assert.NoError(t, err)
	return p
}

// switchHistoryEvents replays what the executor actually writes for a switch
// that routed to `call_k8s`: the switch resolves as an activity under its own
// id, then the selected branch runs as a child workflow under the hydrated id
// "dispatch-call_k8s".
func switchHistoryEvents(t *testing.T, dc converter.DataConverter, branchStatus enums.EventType) []*historyapi.HistoryEvent {
	t.Helper()
	events := []*historyapi.HistoryEvent{
		{
			EventId:   5,
			EventType: enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
			Attributes: &historyapi.HistoryEvent_ActivityTaskScheduledEventAttributes{
				ActivityTaskScheduledEventAttributes: &historyapi.ActivityTaskScheduledEventAttributes{
					ActivityId:   "dispatch",
					ActivityType: &commonapi.ActivityType{Name: "core.switch"},
				},
			},
		},
		{
			EventId:   6,
			EventType: enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
			Attributes: &historyapi.HistoryEvent_ActivityTaskCompletedEventAttributes{
				ActivityTaskCompletedEventAttributes: &historyapi.ActivityTaskCompletedEventAttributes{
					ScheduledEventId: 5,
					Result: payloadOf(t, dc, map[string]any{
						"selected_case": "kubernetes",
						"routed_to":     []any{"call_k8s"},
					}),
				},
			},
		},
		{
			EventId:   7,
			EventType: enums.EVENT_TYPE_START_CHILD_WORKFLOW_EXECUTION_INITIATED,
			Attributes: &historyapi.HistoryEvent_StartChildWorkflowExecutionInitiatedEventAttributes{
				StartChildWorkflowExecutionInitiatedEventAttributes: &historyapi.StartChildWorkflowExecutionInitiatedEventAttributes{
					WorkflowId: "parent-wf-dispatch-call_k8s-1234",
					Memo: &commonapi.Memo{Fields: map[string]*commonapi.Payload{
						"parent_task_id": payloadOf(t, dc, "dispatch-call_k8s").GetPayloads()[0],
					}},
				},
			},
		},
	}

	switch branchStatus {
	case enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_FAILED:
		events = append(events, &historyapi.HistoryEvent{
			EventId:   8,
			EventType: enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_FAILED,
			Attributes: &historyapi.HistoryEvent_ChildWorkflowExecutionFailedEventAttributes{
				ChildWorkflowExecutionFailedEventAttributes: &historyapi.ChildWorkflowExecutionFailedEventAttributes{
					WorkflowExecution: &commonapi.WorkflowExecution{WorkflowId: "parent-wf-dispatch-call_k8s-1234", RunId: "child-run"},
				},
			},
		})
	case enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED:
		events = append(events, &historyapi.HistoryEvent{
			EventId:   8,
			EventType: enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED,
			Attributes: &historyapi.HistoryEvent_ChildWorkflowExecutionCompletedEventAttributes{
				ChildWorkflowExecutionCompletedEventAttributes: &historyapi.ChildWorkflowExecutionCompletedEventAttributes{
					WorkflowExecution: &commonapi.WorkflowExecution{WorkflowId: "parent-wf-dispatch-call_k8s-1234", RunId: "child-run"},
					Result:            payloadOf(t, dc, map[string]any{"restarted": true}),
				},
			},
		})
	}

	return events
}

// sliceHistoryIterator replays a fixed event list. MockHistoryIterator can't be
// used here: its HasNext() reads a static bool, so it cannot terminate.
type sliceHistoryIterator struct {
	events []*historyapi.HistoryEvent
	idx    int
}

func (it *sliceHistoryIterator) HasNext() bool { return it.idx < len(it.events) }

func (it *sliceHistoryIterator) Next() (*historyapi.HistoryEvent, error) {
	e := it.events[it.idx]
	it.idx++
	return e, nil
}

func newHistoryIterator(events []*historyapi.HistoryEvent) *sliceHistoryIterator {
	return &sliceHistoryIterator{events: events}
}

func tasksByID(tasks []model.TaskExecutionDetails) map[string]model.TaskExecutionDetails {
	byID := make(map[string]model.TaskExecutionDetails, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	return byID
}

// A step selected by a Switch runs under the hydrated id "{switch}-{branch}".
// It must be reported under the id of the node the user drew, otherwise the run
// view leaves that node grey even though it executed (#35389).
func TestProcessWorkflowHistorySurfacesSwitchBranchStatus(t *testing.T) {
	dc := converter.GetDefaultDataConverter()
	mockTemporalClient := new(MockTemporalClient)
	service := &Service{temporalClient: mockTemporalClient, dataConverter: dc}
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	// Child drill-down: memo carries the hydrated parent task id; the nested task
	// list resolves through ListWorkflow, which returns nothing here.
	mockTemporalClient.On("DescribeWorkflowExecution", mock.Anything, "parent-wf-dispatch-call_k8s-1234", "child-run").Return(
		&workflowservice.DescribeWorkflowExecutionResponse{
			WorkflowExecutionInfo: &workflowapi.WorkflowExecutionInfo{
				Memo: &commonapi.Memo{Fields: map[string]*commonapi.Payload{
					"parent_task_id":      payloadOf(t, dc, "dispatch-call_k8s").GetPayloads()[0],
					"child_definition_id": payloadOf(t, dc, "inline-call_k8s-1234").GetPayloads()[0],
				}},
			},
		}, nil)
	mockTemporalClient.On("ListWorkflow", mock.Anything, mock.Anything).Return(
		&workflowservice.ListWorkflowExecutionsResponse{}, nil)

	events := switchHistoryEvents(t, dc, enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED)
	details, err := service.processWorkflowHistory(sc, "test-account", newHistoryIterator(events), switchWorkflowDefinition())
	assert.NoError(t, err)

	byID := tasksByID(details.Tasks)

	branch, ok := byID["call_k8s"]
	assert.True(t, ok, "selected branch must be reported under its own step id, not %q", "dispatch-call_k8s")
	assert.Equal(t, model.TaskStatusCompleted, branch.Status)
	assert.Equal(t, "core.call-workflow", branch.Type, "branch type comes from the definition node, not the 'uses' placeholder")

	_, renamedLeaked := byID["dispatch-call_k8s"]
	assert.False(t, renamedLeaked, "the hydrated runtime id must not reach the API")

	dispatch, ok := byID["dispatch"]
	assert.True(t, ok, "the switch itself keeps its own row")
	assert.Equal(t, model.TaskStatusCompleted, dispatch.Status)

	for _, unselected := range []string{"call_linux", "call_aws"} {
		task, ok := byID[unselected]
		assert.True(t, ok, "unselected branch %q must be reported", unselected)
		assert.Equal(t, model.TaskStatusSkipped, task.Status, "unselected branch %q must be distinguishable from a step that ran without status", unselected)
	}
}

// A branch whose child workflow failed used to stay SCHEDULED forever: nothing
// consumed CHILD_WORKFLOW_EXECUTION_FAILED.
func TestProcessWorkflowHistoryReportsFailedSwitchBranch(t *testing.T) {
	dc := converter.GetDefaultDataConverter()
	mockTemporalClient := new(MockTemporalClient)
	service := &Service{temporalClient: mockTemporalClient, dataConverter: dc}
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	mockTemporalClient.On("DescribeWorkflowExecution", mock.Anything, "parent-wf-dispatch-call_k8s-1234", "child-run").Return(
		&workflowservice.DescribeWorkflowExecutionResponse{
			WorkflowExecutionInfo: &workflowapi.WorkflowExecutionInfo{
				Memo: &commonapi.Memo{Fields: map[string]*commonapi.Payload{
					"parent_task_id": payloadOf(t, dc, "dispatch-call_k8s").GetPayloads()[0],
				}},
			},
		}, nil)

	events := switchHistoryEvents(t, dc, enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_FAILED)
	details, err := service.processWorkflowHistory(sc, "test-account", newHistoryIterator(events), switchWorkflowDefinition())
	assert.NoError(t, err)

	byID := tasksByID(details.Tasks)
	branch, ok := byID["call_k8s"]
	assert.True(t, ok)
	assert.Equal(t, model.TaskStatusFailed, branch.Status)
}

// Only "{switch}-{branch}" pairs the definition declares are rewritten — a task
// whose own id contains a dash must be left alone.
func TestProcessWorkflowHistoryKeepsDashedTaskIDs(t *testing.T) {
	dc := converter.GetDefaultDataConverter()
	mockTemporalClient := new(MockTemporalClient)
	service := &Service{temporalClient: mockTemporalClient, dataConverter: dc}
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	def := switchWorkflowDefinition()
	def.Tasks = append(def.Tasks, model.Task{ID: "dispatch-report", Type: "scripting.run_script"})

	events := []*historyapi.HistoryEvent{
		{
			EventId:   5,
			EventType: enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
			Attributes: &historyapi.HistoryEvent_ActivityTaskScheduledEventAttributes{
				ActivityTaskScheduledEventAttributes: &historyapi.ActivityTaskScheduledEventAttributes{
					ActivityId:   "dispatch-report",
					ActivityType: &commonapi.ActivityType{Name: "scripting.run_script"},
				},
			},
		},
	}

	details, err := service.processWorkflowHistory(sc, "test-account", newHistoryIterator(events), def)
	assert.NoError(t, err)

	byID := tasksByID(details.Tasks)
	_, ok := byID["dispatch-report"]
	assert.True(t, ok, "a defined task id containing a dash must not be split into a switch branch")
	assert.NotContains(t, byID, "report")
}
