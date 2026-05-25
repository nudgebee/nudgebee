package events

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockTriggerStore is a mock implementation of TriggerStore
type MockTriggerStore struct {
	mock.Mock
}

func (m *MockTriggerStore) FindEventTriggers(ctx context.Context) ([]model.WorkflowEventTriggerRule, error) {
	args := m.Called(ctx)
	return args.Get(0).([]model.WorkflowEventTriggerRule), args.Error(1)
}

func TestEventRegistry_Refresh(t *testing.T) {
	mockStore := new(MockTriggerStore)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := NewEventRegistry(mockStore, logger)

	ctx := context.Background()

	// Define test rules with Jinja syntax
	rules := []model.WorkflowEventTriggerRule{
		{
			WorkflowID: "wf-1",
			EventType:  "deployment.success",
			Filter:     "", // No filter
		},
		{
			WorkflowID: "wf-2",
			EventType:  "alert.fired",
			Filter:     "{{ event.severity == 'critical' }}",
		},
		{
			WorkflowID: "wf-3",
			EventType:  "deployment.success",
			Filter:     "{{ event.env == 'prod' }}",
		},
		{
			WorkflowID: "wf-invalid",
			EventType:  "deployment.success",
			Filter:     "{{ unclosed variable", // Invalid Jinja syntax (unclosed variable)
		},
		{
			WorkflowID: "wf-false",
			EventType:  "deployment.success",
			Filter:     "{{ event.value == false }}",
		},
		{
			WorkflowID: "wf-true-string",
			EventType:  "deployment.success",
			Filter:     "{{ 'true' }}",
		},
		{
			WorkflowID: "wf-1-number",
			EventType:  "deployment.success",
			Filter:     "{{ 1 }}",
		},
	}

	mockStore.On("FindEventTriggers", ctx).Return(rules, nil)

	// Act
	err := registry.Refresh(ctx)

	// Assert
	assert.NoError(t, err)
	// Expecting 5 valid rules after refresh:
	// deployment.success: wf-1, wf-3, wf-false, wf-true-string, wf-1-number (5 rules)
	// alert.fired: wf-2 (1 rule)
	// wf-invalid should be skipped due to syntax error
	assert.Equal(t, 6, len(registry.triggers["deployment.success"])+len(registry.triggers["alert.fired"]))

	// Verify compiled rules
	assert.Equal(t, 5, len(registry.triggers["deployment.success"]))
	assert.Equal(t, 1, len(registry.triggers["alert.fired"]))
	// wf-invalid should be skipped
}

func TestEventRegistry_Refresh_Error(t *testing.T) {
	mockStore := new(MockTriggerStore)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := NewEventRegistry(mockStore, logger)

	ctx := context.Background()
	mockStore.On("FindEventTriggers", ctx).Return([]model.WorkflowEventTriggerRule{}, errors.New("db error"))

	err := registry.Refresh(ctx)
	assert.Error(t, err)
}

func TestEventRegistry_Match(t *testing.T) {
	mockStore := new(MockTriggerStore)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := NewEventRegistry(mockStore, logger)

	// Setup registry state directly for matching test
	// Note: In a real integration we would call Refresh, but here we can assume Refresh works (tested above)
	// However, Refresh compiles the rules. So we MUST call Refresh to populate the registry correctly with compiled queries.
	rules := []model.WorkflowEventTriggerRule{
		{WorkflowID: "wf-all-deploy", EventType: "deployment.success", Filter: "", AccountID: "acc-1"},
		{WorkflowID: "wf-prod-deploy", EventType: "deployment.success", Filter: "{{ event.env == 'prod' }}", AccountID: "acc-1"},
		{WorkflowID: "wf-staging-deploy", EventType: "deployment.success", Filter: "{{ event.env == 'staging' }}", AccountID: "acc-1"},
		{WorkflowID: "wf-critical-alert", EventType: "alert.fired", Filter: "{{ event.severity == 'critical' }}", AccountID: "acc-1"},
		{WorkflowID: "wf-templated-bool", EventType: "generic.event", Filter: "{{ event.status == 'ok' }}", AccountID: "acc-1"},
		{WorkflowID: "wf-templated-true", EventType: "generic.event", Filter: "{{ true }}", AccountID: "acc-1"},
		{WorkflowID: "wf-templated-false", EventType: "generic.event", Filter: "{{ false }}", AccountID: "acc-1"},
		{WorkflowID: "wf-templated-string-true", EventType: "generic.event", Filter: "{{ 'true' }}", AccountID: "acc-1"},
		{WorkflowID: "wf-templated-string-1", EventType: "generic.event", Filter: "{{ '1' }}"}, // Missing AccountID to test isolation
		{WorkflowID: "wf-templated-number-1", EventType: "generic.event", Filter: "{{ 1 }}", AccountID: "acc-1"},
	}
	mockStore.On("FindEventTriggers", mock.Anything).Return(rules, nil)
	_ = registry.Refresh(context.Background())

	tests := []struct {
		name      string
		eventType string
		accountID string
		payload   map[string]any
		wantIDs   []string
	}{
		{
			name:      "Match All + Filter Match (prod)",
			eventType: "deployment.success",
			accountID: "acc-1",
			payload:   map[string]any{"env": "prod", "version": "v1"},
			wantIDs:   []string{"wf-all-deploy", "wf-prod-deploy"},
		},
		{
			name:      "Match All + Filter Match (staging)",
			eventType: "deployment.success",
			accountID: "acc-1",
			payload:   map[string]any{"env": "staging", "version": "v1"},
			wantIDs:   []string{"wf-all-deploy", "wf-staging-deploy"},
		},
		{
			name:      "Match All + Filter No Match",
			eventType: "deployment.success",
			accountID: "acc-1",
			payload:   map[string]any{"env": "dev", "version": "v1"},
			wantIDs:   []string{"wf-all-deploy"},
		},
		{
			name:      "Match Specific Event (critical)",
			eventType: "alert.fired",
			accountID: "acc-1",
			payload:   map[string]any{"severity": "critical", "msg": "down"},
			wantIDs:   []string{"wf-critical-alert"},
		},
		{
			name:      "No Match Specific Event (info)",
			eventType: "alert.fired",
			accountID: "acc-1",
			payload:   map[string]any{"severity": "info", "msg": "info"},
			wantIDs:   nil,
		},
		{
			name:      "Unknown Event Type",
			eventType: "unknown.event",
			accountID: "acc-1",
			payload:   map[string]any{},
			wantIDs:   nil,
		},
		{
			name:      "Generic Event Match (boolean true)",
			eventType: "generic.event",
			accountID: "acc-1",
			payload:   map[string]any{"status": "ok"},
			wantIDs:   []string{"wf-templated-bool", "wf-templated-true", "wf-templated-string-true", "wf-templated-number-1"},
		},
		{
			name:      "Generic Event Match (boolean false)",
			eventType: "generic.event",
			accountID: "acc-1",
			payload:   map[string]any{"status": "not_ok"},
			wantIDs:   []string{"wf-templated-true", "wf-templated-string-true", "wf-templated-number-1"},
		},
		{
			name:      "Tenant Isolation Check",
			eventType: "deployment.success",
			accountID: "acc-2", // Different account
			payload:   map[string]any{"env": "prod"},
			wantIDs:   nil, // Should match nothing as all rules are for acc-1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := registry.Match(tt.eventType, tt.accountID, tt.payload)
			var gotIDs []string
			for _, m := range matches {
				gotIDs = append(gotIDs, m.WorkflowID)
			}
			assert.ElementsMatch(t, tt.wantIDs, gotIDs)
		})
	}
}

func TestEventRegistry_Wildcard(t *testing.T) {
	mockStore := new(MockTriggerStore)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := NewEventRegistry(mockStore, logger)

	rules := []model.WorkflowEventTriggerRule{
		// Wildcard (no event_type), filter on cluster
		{WorkflowID: "wf-any-prod-cluster", EventType: "", Filter: "{{ event.cluster == 'prod' }}", AccountID: "acc-1"},
		// Wildcard, filter on severity
		{WorkflowID: "wf-any-critical", EventType: "", Filter: "{{ event.severity == 'critical' }}", AccountID: "acc-1"},
		// Wildcard for a different tenant
		{WorkflowID: "wf-other-tenant", EventType: "", Filter: "{{ event.cluster == 'prod' }}", AccountID: "acc-2"},
		// Scoped rule coexists with wildcards
		{WorkflowID: "wf-scoped-alert", EventType: "alert.fired", Filter: "", AccountID: "acc-1"},
		// Wildcard without a filter — must be dropped at refresh time.
		{WorkflowID: "wf-wildcard-no-filter", EventType: "", Filter: "", AccountID: "acc-1"},
	}
	mockStore.On("FindEventTriggers", mock.Anything).Return(rules, nil)
	_ = registry.Refresh(context.Background())

	// The wildcard without a filter must have been dropped defensively.
	assert.Equal(t, 3, len(registry.wildcard), "wildcard should hold 3 filtered rules (the no-filter one is dropped)")

	tests := []struct {
		name      string
		eventType string
		accountID string
		payload   map[string]any
		wantIDs   []string
	}{
		{
			name:      "Wildcard matches on cluster",
			eventType: "deployment.success",
			accountID: "acc-1",
			payload:   map[string]any{"cluster": "prod", "severity": "info"},
			wantIDs:   []string{"wf-any-prod-cluster"},
		},
		{
			name:      "Wildcard matches on severity, any event_type",
			eventType: "random.type",
			accountID: "acc-1",
			payload:   map[string]any{"cluster": "staging", "severity": "critical"},
			wantIDs:   []string{"wf-any-critical"},
		},
		{
			name:      "Wildcard + scoped rule both match",
			eventType: "alert.fired",
			accountID: "acc-1",
			payload:   map[string]any{"cluster": "prod", "severity": "critical"},
			wantIDs:   []string{"wf-scoped-alert", "wf-any-prod-cluster", "wf-any-critical"},
		},
		{
			name:      "Wildcard respects tenant isolation",
			eventType: "anything",
			accountID: "acc-2",
			payload:   map[string]any{"cluster": "prod"},
			wantIDs:   []string{"wf-other-tenant"},
		},
		{
			name:      "No match when filter fails",
			eventType: "anything",
			accountID: "acc-1",
			payload:   map[string]any{"cluster": "staging", "severity": "info"},
			wantIDs:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := registry.Match(tt.eventType, tt.accountID, tt.payload)
			var gotIDs []string
			for _, m := range matches {
				gotIDs = append(gotIDs, m.WorkflowID)
			}
			assert.ElementsMatch(t, tt.wantIDs, gotIDs)
		})
	}
}
