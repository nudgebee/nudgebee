package workflow

import (
	"errors"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

func scheduleWorkflow(cron, target string) model.WorkflowDefinition {
	return model.WorkflowDefinition{
		Triggers: []model.Trigger{{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": cron, "target": target}}},
		Tasks:    []model.Task{{ID: "t1", Type: "data.transform", Params: map[string]any{"value": "{{ inputs.target }}"}}},
	}
}

func TestSchedulesAreRegisteredFromTheLiveVersionNotTheDraft(t *testing.T) {
	// A draft save re-registers triggers. The schedule runs as the owner, so
	// its cron and inputs must come from the live version: an editor must not
	// be able to change what runs with the owner's rights without making a
	// version live (#41035 review).
	service, mockStore, mockTemporal := newRollbackTestService(t)
	mockStore.runAs = map[string]string{"wf-1": "owner"}
	mockStore.On("GetLiveWorkflowVersion", mock.Anything, "wf-1").
		Return(&model.WorkflowVersion{ID: "v1", VersionNumber: 1, IsLive: true, Definition: scheduleWorkflow("0 * * * *", "live-account")}, nil)
	mockTemporal.On("Describe", mock.Anything).Return(nil, serviceerror.NewNotFound("schedule not found"))

	var created client.ScheduleOptions
	mockTemporal.On("Create", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { created = args.Get(1).(client.ScheduleOptions) }).
		Return(nil, errors.New("stop after capture"))

	draft := &model.Workflow{ID: "wf-1", Status: model.WorkflowStatusActive, Definition: scheduleWorkflow("*/1 * * * *", "editor-chosen-account")}
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "editor", []string{"test-account"})
	_, _, err := service.handleWorkflowTrigger(sc, "wf-1", "test-tenant", "test-account", draft)
	require.ErrorContains(t, err, "stop after capture")

	assert.Equal(t, []string{"0 * * * *"}, created.Spec.CronExpressions, "cron comes from the live version")
	action := created.Action.(*client.ScheduleWorkflowAction)
	assert.Equal(t, map[string]any{"target": "live-account"}, action.Args[1], "inputs come from the live version")
	assert.Equal(t, "owner", action.Args[0].(*model.Workflow).PrincipalUserID, "the schedule acts as the owner, not the editor")
}
