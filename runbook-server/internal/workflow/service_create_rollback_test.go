package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks"
	"nudgebee/runbook/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

// CreateWorkflow commits the workflow row and its v1 version, then registers
// triggers. The second stage is not covered by the first stage's transaction, so
// a failure there used to return an error while leaving the row behind: the user
// was told the automation was not saved, saw it in their list anyway, and the
// retry was refused by the duplicate-name guard (#35385).
//
// The two cases below are the whole contract. Rollback removes the row only when
// the external teardown came back clean; when it did not, the row is kept on
// purpose, because a surviving Temporal schedule with no row is invisible and
// keeps firing, which is worse than the orphan it replaces.
func newRollbackTestService(t *testing.T) (*Service, *MockWorkflowStore, *MockTemporalClient) {
	t.Helper()

	mockTemporalClient := &MockTemporalClient{}
	mockStore := new(MockWorkflowStore)
	mockTaskRegistry := tasks.NewInitializedTaskRegistry()
	mockDataConverter := converter.GetDefaultDataConverter()
	mockConfigService := new(MockConfigService)
	workflowExecutor := &WorkflowExecutor{
		temporalClient: mockTemporalClient,
		workflowStore:  mockStore,
		dataConverter:  mockDataConverter,
	}
	service := NewService(mockTemporalClient, mockStore, mockDataConverter, mockTaskRegistry, workflowExecutor, mockConfigService)

	return service, mockStore, mockTemporalClient
}

func scheduledWorkflowFixture() model.Workflow {
	return model.Workflow{
		Name: "rollback-probe",
		Definition: model.WorkflowDefinition{
			Triggers: []model.Trigger{
				{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 * * * *"}},
			},
			Tasks: []model.Task{
				{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo 'hello'"}},
			},
		},
	}
}

// expectCreateThenFailingScheduleRegistration wires a create that commits and a
// schedule registration that then fails — the exact shape of a Temporal outage
// arriving between the two stages.
func expectCreateThenFailingScheduleRegistration(mockStore *MockWorkflowStore, mockTemporal *MockTemporalClient, name string) {
	mockStore.On("FindByName", mock.Anything, "test-tenant", "test-account", name).Return(nil, sql.ErrNoRows)
	mockStore.On("CreateWorkflowWithInitialVersion", mock.Anything, "test-tenant", "test-account", mock.Anything).
		Return("stored-id", &model.WorkflowVersion{ID: "v1", VersionNumber: 1, Source: model.WorkflowVersionSourceCreate, IsLive: true}, nil)
	mockStore.On("GetLiveWorkflowVersion", mock.Anything, mock.Anything).
		Return(&model.WorkflowVersion{ID: "v1", VersionNumber: 1, IsLive: true}, nil)

	// No pre-existing schedule, and creating the new one fails.
	mockTemporal.On("Describe", mock.Anything).Return(nil, serviceerror.NewNotFound("schedule not found"))
	mockTemporal.On("Create", mock.Anything, mock.Anything).Return(nil, errors.New("temporal unavailable"))
}

func TestCreateWorkflow_RollsBackWhenTriggerRegistrationFails(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	t.Run("teardown clean: the row is removed so the retry is not blocked", func(t *testing.T) {
		service, mockStore, mockTemporal := newRollbackTestService(t)
		wf := scheduledWorkflowFixture()
		expectCreateThenFailingScheduleRegistration(mockStore, mockTemporal, wf.Name)

		// Schedules enumerate cleanly and there are none left to remove.
		mockTemporal.On("List", mock.Anything, mock.Anything).
			Return(&MockScheduleListIterator{Schedules: []*client.ScheduleListEntry{}}, nil)
		mockStore.On("Delete", mock.Anything, "test-tenant", "test-account", "stored-id").Return(nil)

		id, token, err := service.CreateWorkflow(sc, "test-account", wf)

		assert.Error(t, err, "the caller must still see the registration failure")
		assert.Contains(t, err.Error(), "temporal unavailable", "the rollback must not mask the original error")
		assert.Empty(t, id, "a rolled-back create owns no workflow id")
		assert.Empty(t, token)
		mockStore.AssertCalled(t, "Delete", mock.Anything, "test-tenant", "test-account", "stored-id")
	})

	t.Run("teardown incomplete: the row is kept rather than orphaning a schedule", func(t *testing.T) {
		service, mockStore, mockTemporal := newRollbackTestService(t)
		wf := scheduledWorkflowFixture()
		expectCreateThenFailingScheduleRegistration(mockStore, mockTemporal, wf.Name)

		// Schedules cannot be enumerated, so the teardown cannot prove the workflow
		// owns none. Deleting the row here could strand a schedule that still fires
		// with nothing to fire against.
		mockTemporal.On("List", mock.Anything, mock.Anything).Return(nil, errors.New("temporal unavailable"))

		id, token, err := service.CreateWorkflow(sc, "test-account", wf)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "temporal unavailable")
		assert.Equal(t, "stored-id", id, "the retained row's id is reported, not an empty one")
		assert.Empty(t, token)
		mockStore.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})
}

// One way trigger registration fails is the request context timing out or being
// cancelled. The rollback then runs on that same context, so unless it detaches
// first, every call it makes fails instantly and the row it was meant to remove
// survives — the rollback would be useless in one of the cases it exists for.
func TestCreateWorkflow_RollsBackOnACancelledRequestContext(t *testing.T) {
	service, mockStore, mockTemporal := newRollbackTestService(t)
	wf := scheduledWorkflowFixture()
	wf.Name = "rollback-cancelled-probe"
	expectCreateThenFailingScheduleRegistration(mockStore, mockTemporal, wf.Name)
	mockTemporal.On("List", mock.Anything, mock.Anything).
		Return(&MockScheduleListIterator{Schedules: []*client.ScheduleListEntry{}}, nil)

	// Behave like a real driver: refuse the delete if the context handed to it is
	// already done. Without the detach the rollback passes exactly such a context.
	mockStore.On("Delete", mock.Anything, "test-tenant", "test-account", "stored-id").
		Return(nil).
		Run(func(args mock.Arguments) {
			if c, ok := args.Get(0).(context.Context); ok && c.Err() != nil {
				t.Errorf("store.Delete received an already-cancelled context (%v); the rollback did not detach", c.Err())
			}
		})

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"}).
		WithContext(cancelledCtx)

	_, _, err := service.CreateWorkflow(sc, "test-account", wf)

	assert.ErrorContains(t, err, "temporal unavailable")
	mockStore.AssertCalled(t, "Delete", mock.Anything, "test-tenant", "test-account", "stored-id")
}

// A user-managed webhook integration is shared: the workflow binds to it rather
// than owning it (normalizeWebhookTriggers keeps the name unprefixed). Rolling
// back a create must not delete it out from under the user's other automations —
// only auto-managed "wf-<id>-" shadow integrations belong to the workflow.
func TestCreateWorkflow_RollbackLeavesUserManagedWebhookIntegrationAlone(t *testing.T) {
	var deleteConfigCalls int
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		actionMap, _ := body["action"].(map[string]any)
		switch actionMap["name"] {
		case "integrations_create_config":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "int-123",
				"configs": []map[string]any{{"name": "token", "value": "secret-token-123"}},
			})
		case "integrations_delete_config":
			deleteConfigCalls++
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	config.Config.ServiceEndpoint = mockServer.URL
	config.Config.ServiceApiServerToken = "test-token"

	service, mockStore, mockTemporal := newRollbackTestService(t)
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	wf := model.Workflow{
		Name: "rollback-webhook-probe",
		Definition: model.WorkflowDefinition{
			Triggers: []model.Trigger{
				{Type: model.WorkflowTriggerWebhook, Params: map[string]any{"integration_name": "shared-hook"}},
				{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 * * * *"}},
			},
			Tasks: []model.Task{
				{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo 'hello'"}},
			},
		},
	}

	expectCreateThenFailingScheduleRegistration(mockStore, mockTemporal, wf.Name)
	// The webhook secret is injected through the internal (non-audit) persist path.
	mockStore.On("UpdateInternal", mock.Anything, "test-tenant", "test-account", mock.Anything, mock.Anything).Return(nil)
	mockTemporal.On("List", mock.Anything, mock.Anything).
		Return(&MockScheduleListIterator{Schedules: []*client.ScheduleListEntry{}}, nil)
	mockStore.On("Delete", mock.Anything, "test-tenant", "test-account", "stored-id").Return(nil)

	_, _, err := service.CreateWorkflow(sc, "test-account", wf)

	// Assert on the specific failure: an earlier validation error would otherwise
	// satisfy assert.Error while never reaching the rollback this test is about.
	assert.ErrorContains(t, err, "temporal unavailable")
	mockStore.AssertCalled(t, "Delete", mock.Anything, "test-tenant", "test-account", "stored-id")
	assert.Zero(t, deleteConfigCalls, "a user-managed webhook integration must survive the rollback")
}
