package events

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockWorkflowExecutor is a mock implementation of WorkflowExecutor
type MockWorkflowExecutor struct {
	mock.Mock
}

func (m *MockWorkflowExecutor) ExecuteWorkflow(ctx *security.RequestContext, accountId, id string, triggerType model.WorkflowTrigger, inputs map[string]any) (string, error) {
	args := m.Called(ctx, accountId, id, triggerType, inputs)
	return args.String(0), args.Error(1)
}

func TestConsumer_ProcessMessage(t *testing.T) {
	// Setup Registry
	mockStore := new(MockTriggerStore)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := NewEventRegistry(mockStore, logger)

	rules := []model.WorkflowEventTriggerRule{
		{WorkflowID: "wf-1", EventType: "test.event", Filter: "{{ event.foo == 'bar' }}", AccountID: "acc-1", TenantID: "tenant-1"},
	}
	mockStore.On("FindEventTriggers", mock.Anything).Return(rules, nil)
	_ = registry.Refresh(context.Background())

	// Setup Consumer
	mockExecutor := new(MockWorkflowExecutor)
	consumer := NewConsumer(registry, mockExecutor, logger)

	// 1. Success Case
	payload := map[string]any{
		"event_type": "test.event",
		"account_id": "acc-1",
		"foo":        "bar",
	}
	data, _ := json.Marshal(payload)

	// Expect the executor to receive the payload wrapped in an "event" key
	expectedInputs := map[string]any{
		"event": payload,
	}
	mockExecutor.On("ExecuteWorkflow", mock.Anything, "acc-1", "wf-1", model.WorkflowTriggerEvent, expectedInputs).Return("run-1", nil)

	err := consumer.ProcessMessage(data)
	assert.NoError(t, err)
	mockExecutor.AssertExpectations(t)

	// 2. No Match Case
	payloadNoMatch := map[string]any{
		"event_type": "test.event",
		"account_id": "acc-1",
		"foo":        "baz",
	}
	dataNoMatch, _ := json.Marshal(payloadNoMatch)
	// Executor should NOT be called
	err = consumer.ProcessMessage(dataNoMatch)
	assert.NoError(t, err)

	// 3. Invalid JSON Case
	err = consumer.ProcessMessage([]byte("{invalid-json"))
	assert.NoError(t, err) // Should return nil (ack) to avoid poison loop

	// 4. Missing Event Type
	payloadNoType := map[string]any{"foo": "bar"}
	dataNoType, _ := json.Marshal(payloadNoType)
	err = consumer.ProcessMessage(dataNoType)
	assert.NoError(t, err)
}

func TestConsumer_ProcessMessage_UsesRuleTriggerType(t *testing.T) {
	mockStore := new(MockTriggerStore)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := NewEventRegistry(mockStore, logger)

	rules := []model.WorkflowEventTriggerRule{
		{
			WorkflowID:  "wf-opt",
			EventType:   "optimization.recommendation",
			Filter:      "",
			AccountID:   "acc-1",
			TenantID:    "tenant-1",
			TriggerType: model.WorkflowTriggerOptimization,
		},
	}
	mockStore.On("FindEventTriggers", mock.Anything).Return(rules, nil)
	_ = registry.Refresh(context.Background())

	mockExecutor := new(MockWorkflowExecutor)
	consumer := NewConsumer(registry, mockExecutor, logger)

	payload := map[string]any{
		"event_type": "optimization.recommendation",
		"account_id": "acc-1",
	}
	data, _ := json.Marshal(payload)

	// Should use rule.TriggerType (optimization), not infer from event name
	mockExecutor.On("ExecuteWorkflow", mock.Anything, "acc-1", "wf-opt", model.WorkflowTriggerOptimization, mock.Anything).Return("run-1", nil)

	err := consumer.ProcessMessage(data)
	assert.NoError(t, err)
	mockExecutor.AssertExpectations(t)
}
