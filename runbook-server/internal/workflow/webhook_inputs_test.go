package workflow

import (
	"testing"

	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fanOutAndCaptureInputs fans payload out to one webhook subscriber filtered on
// action == "go" and returns the inputs the run was started with (nil if none).
func fanOutAndCaptureInputs(t *testing.T, payload string) (map[string]any, FanOutResult) {
	t.Helper()
	service, mockTemporal, mockStore, sc := newExecuteTestService()

	wf := model.Workflow{
		ID:            "wf-hook",
		AccountID:     "test-account",
		Name:          "wf-hook",
		Status:        model.WorkflowStatusActive,
		LiveVersionID: strPtrLocal("v-live"),
		Definition: model.WorkflowDefinition{
			Triggers: []model.Trigger{{Type: model.WorkflowTriggerWebhook, Params: map[string]any{
				"integration_name": "hook",
				"filter":           `{{ webhook_payload.action == "go" }}`,
			}}},
			Tasks: []model.Task{{ID: "t1", Type: "scripting.run_script"}},
		},
	}
	mockStore.On("ListByIntegrationName", mock.Anything, "test-tenant", "hook").Return([]model.Workflow{wf}, nil).Once()
	mockStore.On("Find", mock.Anything, "test-tenant", "test-account", "wf-hook").Return(&wf, nil).Maybe()
	mockStore.On("GetLiveWorkflowVersion", mock.Anything, "wf-hook").
		Return(&model.WorkflowVersion{ID: "v-live", WorkflowID: "wf-hook", VersionNumber: 1, Definition: wf.Definition}, nil).Maybe()

	var captured map[string]any
	mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(4).(map[string]any) }).
		Return(&MockWorkflowRun{}, nil).Maybe()

	res, err := service.FanOutWebhookEvent(sc, "hook", []byte(payload))
	require.NoError(t, err)
	return captured, res
}

func TestFanOutWebhookExposesParsedJSON(t *testing.T) {
	raw := `{"action":"go","nested":{"k":"v"}}`
	inputs, res := fanOutAndCaptureInputs(t, raw)
	require.Equal(t, 1, res.Fired)
	require.NotNil(t, inputs)

	assert.Equal(t, raw, inputs["webhook_payload"], "raw body must stay a string")
	tplCtx := NewTemplateContextWithEngine(nil, inputs, "jinja")
	nested, err := Render("{{ Inputs.webhook_json.nested.k }}", tplCtx)
	require.NoError(t, err)
	assert.Equal(t, "v", nested)
	top, err := Render("{{ Inputs.webhook_json.action }}", tplCtx)
	require.NoError(t, err)
	assert.Equal(t, "go", top)
	rawOut, err := Render("{{ Inputs.webhook_payload }}", tplCtx)
	require.NoError(t, err)
	assert.Equal(t, raw, rawOut)
}

func TestFanOutWebhookFilterStillGatesRun(t *testing.T) {
	inputs, res := fanOutAndCaptureInputs(t, `{"action":"stop"}`)
	assert.Equal(t, 0, res.Fired)
	assert.Nil(t, inputs)
}

func TestFanOutWebhookNonJSONBodyLeavesParsedUnset(t *testing.T) {
	service, mockTemporal, mockStore, sc := newExecuteTestService()
	wf := model.Workflow{
		ID: "wf-text", AccountID: "test-account", Name: "wf-text", Status: model.WorkflowStatusActive,
		LiveVersionID: strPtrLocal("v-live"),
		Definition: model.WorkflowDefinition{
			Triggers: []model.Trigger{{Type: model.WorkflowTriggerWebhook, Params: map[string]any{"integration_name": "hook"}}},
			Tasks:    []model.Task{{ID: "t1", Type: "scripting.run_script"}},
		},
	}
	mockStore.On("ListByIntegrationName", mock.Anything, "test-tenant", "hook").Return([]model.Workflow{wf}, nil).Once()
	mockStore.On("Find", mock.Anything, "test-tenant", "test-account", "wf-text").Return(&wf, nil).Once()
	mockStore.On("GetLiveWorkflowVersion", mock.Anything, "wf-text").
		Return(&model.WorkflowVersion{ID: "v-live", WorkflowID: "wf-text", VersionNumber: 1, Definition: wf.Definition}, nil).Once()
	var captured map[string]any
	mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(4).(map[string]any) }).
		Return(&MockWorkflowRun{}, nil).Once()

	res, err := service.FanOutWebhookEvent(sc, "hook", []byte("plain text body"))
	require.NoError(t, err)
	require.Equal(t, 1, res.Fired)
	assert.Equal(t, "plain text body", captured["webhook_payload"])
	_, has := captured["webhook_json"]
	assert.False(t, has, "a non-JSON body must not set webhook_json")
}

func TestWebhookRunInputs(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantSet bool
	}{
		{"object", `{"a":1}`, true},
		{"array", `[1,2]`, true},
		{"plain text", `hello`, false},
		{"json null", `null`, false},
		{"json scalar", `"just a string"`, false},
		{"empty body", ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inputs := WebhookRunInputs([]byte(tc.body))
			assert.Equal(t, tc.body, inputs["webhook_payload"])
			_, has := inputs["webhook_json"]
			assert.Equal(t, tc.wantSet, has)
		})
	}
}

// A workflow that declares webhook_payload as a required string input keeps validating.
func TestWebhookRunInputsKeepsDeclaredStringInputValid(t *testing.T) {
	service, _, _, _ := newExecuteTestService()
	defs := []model.Input{{ID: "webhook_payload", Type: "string", Required: true}}
	assert.NoError(t, service.validateWorkflowInputs(defs, WebhookRunInputs([]byte(`{"a":{"b":1}}`))))
}
