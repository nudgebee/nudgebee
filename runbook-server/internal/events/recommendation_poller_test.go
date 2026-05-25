package events

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockRecommendationStore is a mock implementation of RecommendationStore
type MockRecommendationStore struct {
	mock.Mock
}

func (m *MockRecommendationStore) FindNewRecommendations(ctx context.Context, since time.Time) ([]storage.RecommendationEvent, error) {
	args := m.Called(ctx, since)
	return args.Get(0).([]storage.RecommendationEvent), args.Error(1)
}

func setupPollerTest(t *testing.T, rules []model.WorkflowEventTriggerRule) (*MockRecommendationStore, *MockWorkflowExecutor, *RecommendationPoller) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Build a registry with the provided rules
	mockTriggerStore := new(MockTriggerStore)
	registry := NewEventRegistry(mockTriggerStore, logger)
	mockTriggerStore.On("FindEventTriggers", mock.Anything).Return(rules, nil)
	_ = registry.Refresh(context.Background())

	mockRecStore := new(MockRecommendationStore)
	mockExecutor := new(MockWorkflowExecutor)

	poller := NewRecommendationPoller(mockRecStore, registry, mockExecutor, logger, time.Minute)
	return mockRecStore, mockExecutor, poller
}

func TestRecommendationPoller_NoNewRecommendations(t *testing.T) {
	mockRecStore, mockExecutor, poller := setupPollerTest(t, nil)

	mockRecStore.On("FindNewRecommendations", mock.Anything, mock.Anything).
		Return([]storage.RecommendationEvent{}, nil)

	poller.poll(context.Background())

	mockRecStore.AssertExpectations(t)
	mockExecutor.AssertNotCalled(t, "ExecuteWorkflow")
}

func TestRecommendationPoller_RecommendationMatchesTrigger(t *testing.T) {
	rules := []model.WorkflowEventTriggerRule{
		{
			WorkflowID: "wf-opt-1",
			EventType:  "optimization.recommendation",
			Filter:     "{{ event.category in ['PodRightSizing'] }}",
			AccountID:  "acc-1",
			TenantID:   "tenant-1",
		},
	}
	mockRecStore, mockExecutor, poller := setupPollerTest(t, rules)

	recs := []storage.RecommendationEvent{
		{
			ID:               "rec-1",
			TenantID:         "tenant-1",
			CloudAccountID:   "acc-1",
			ResourceID:       "pod-abc",
			Category:         "PodRightSizing",
			RuleName:         "vertical_rightsize",
			Severity:         "medium",
			EstimatedSavings: 50.0,
			Status:           "Open",
		},
	}

	mockRecStore.On("FindNewRecommendations", mock.Anything, mock.Anything).
		Return(recs, nil)
	mockExecutor.On("ExecuteWorkflow", mock.Anything, "acc-1", "wf-opt-1", model.WorkflowTriggerOptimization, mock.Anything).
		Return("run-1", nil)

	poller.poll(context.Background())

	mockRecStore.AssertExpectations(t)
	mockExecutor.AssertExpectations(t)

	// Verify the event payload passed to ExecuteWorkflow
	call := mockExecutor.Calls[0]
	inputs := call.Arguments.Get(4).(map[string]any)
	event := inputs["event"].(map[string]any)
	assert.Equal(t, "optimization.recommendation", event["event_type"])
	assert.Equal(t, "PodRightSizing", event["category"])
	assert.Equal(t, "vertical_rightsize", event["rule_name"])
	assert.Equal(t, "rec-1", event["recommendation_id"])
	assert.Equal(t, 50.0, event["estimated_savings"])
}

func TestRecommendationPoller_NoMatchingTrigger(t *testing.T) {
	rules := []model.WorkflowEventTriggerRule{
		{
			WorkflowID: "wf-opt-1",
			EventType:  "optimization.recommendation",
			Filter:     "{{ event.category in ['Security'] }}",
			AccountID:  "acc-1",
			TenantID:   "tenant-1",
		},
	}
	mockRecStore, mockExecutor, poller := setupPollerTest(t, rules)

	recs := []storage.RecommendationEvent{
		{
			ID:             "rec-1",
			TenantID:       "tenant-1",
			CloudAccountID: "acc-1",
			Category:       "PodRightSizing", // Does not match 'Security'
			RuleName:       "vertical_rightsize",
			Status:         "Open",
		},
	}

	mockRecStore.On("FindNewRecommendations", mock.Anything, mock.Anything).
		Return(recs, nil)

	poller.poll(context.Background())

	mockRecStore.AssertExpectations(t)
	mockExecutor.AssertNotCalled(t, "ExecuteWorkflow")
}

func TestRecommendationPoller_AccountIsolation(t *testing.T) {
	rules := []model.WorkflowEventTriggerRule{
		{
			WorkflowID: "wf-opt-1",
			EventType:  "optimization.recommendation",
			Filter:     "", // Match all
			AccountID:  "acc-1",
			TenantID:   "tenant-1",
		},
	}
	mockRecStore, mockExecutor, poller := setupPollerTest(t, rules)

	recs := []storage.RecommendationEvent{
		{
			ID:             "rec-1",
			TenantID:       "tenant-2",
			CloudAccountID: "acc-2", // Different account - should not match
			Category:       "PodRightSizing",
			Status:         "Open",
		},
	}

	mockRecStore.On("FindNewRecommendations", mock.Anything, mock.Anything).
		Return(recs, nil)

	poller.poll(context.Background())

	mockRecStore.AssertExpectations(t)
	mockExecutor.AssertNotCalled(t, "ExecuteWorkflow")
}

func TestRecommendationPoller_MultipleRecommendationsAndRules(t *testing.T) {
	rules := []model.WorkflowEventTriggerRule{
		{
			WorkflowID: "wf-opt-pod",
			EventType:  "optimization.recommendation",
			Filter:     "{{ event.category in ['PodRightSizing'] }}",
			AccountID:  "acc-1",
			TenantID:   "tenant-1",
		},
		{
			WorkflowID: "wf-opt-all",
			EventType:  "optimization.recommendation",
			Filter:     "", // Match all
			AccountID:  "acc-1",
			TenantID:   "tenant-1",
		},
	}
	mockRecStore, mockExecutor, poller := setupPollerTest(t, rules)

	recs := []storage.RecommendationEvent{
		{
			ID:             "rec-1",
			TenantID:       "tenant-1",
			CloudAccountID: "acc-1",
			Category:       "PodRightSizing",
			RuleName:       "vertical_rightsize",
			Status:         "Open",
		},
		{
			ID:             "rec-2",
			TenantID:       "tenant-1",
			CloudAccountID: "acc-1",
			Category:       "Security",
			RuleName:       "unused_role",
			Status:         "Open",
		},
	}

	mockRecStore.On("FindNewRecommendations", mock.Anything, mock.Anything).
		Return(recs, nil)

	// rec-1 matches both wf-opt-pod (category filter) and wf-opt-all (no filter)
	mockExecutor.On("ExecuteWorkflow", mock.Anything, "acc-1", "wf-opt-pod", model.WorkflowTriggerOptimization, mock.Anything).
		Return("run-1", nil).Once()
	mockExecutor.On("ExecuteWorkflow", mock.Anything, "acc-1", "wf-opt-all", model.WorkflowTriggerOptimization, mock.Anything).
		Return("run-2", nil).Times(2) // Once for rec-1, once for rec-2

	poller.poll(context.Background())

	mockRecStore.AssertExpectations(t)
	mockExecutor.AssertExpectations(t)

	// Total calls: rec-1 → wf-opt-pod + wf-opt-all, rec-2 → wf-opt-all = 3
	assert.Equal(t, 3, len(mockExecutor.Calls))
}

func TestRecommendationPoller_DBErrorDoesNotAdvanceLastPoll(t *testing.T) {
	mockRecStore, mockExecutor, poller := setupPollerTest(t, nil)

	initialLastPoll := poller.lastPoll

	mockRecStore.On("FindNewRecommendations", mock.Anything, mock.Anything).
		Return([]storage.RecommendationEvent{}, errors.New("connection refused"))

	poller.poll(context.Background())

	// lastPoll should NOT have advanced on error
	assert.Equal(t, initialLastPoll, poller.lastPoll)
	mockExecutor.AssertNotCalled(t, "ExecuteWorkflow")
}

func TestRecommendationPoller_ExecutorErrorDoesNotBlockOthers(t *testing.T) {
	rules := []model.WorkflowEventTriggerRule{
		{
			WorkflowID: "wf-opt-1",
			EventType:  "optimization.recommendation",
			Filter:     "",
			AccountID:  "acc-1",
			TenantID:   "tenant-1",
		},
	}
	mockRecStore, mockExecutor, poller := setupPollerTest(t, rules)

	recs := []storage.RecommendationEvent{
		{
			ID: "rec-1", TenantID: "tenant-1", CloudAccountID: "acc-1",
			Category: "PodRightSizing", Status: "Open",
		},
		{
			ID: "rec-2", TenantID: "tenant-1", CloudAccountID: "acc-1",
			Category: "Security", Status: "Open",
		},
	}

	mockRecStore.On("FindNewRecommendations", mock.Anything, mock.Anything).
		Return(recs, nil)

	// First call fails, second succeeds
	mockExecutor.On("ExecuteWorkflow", mock.Anything, "acc-1", "wf-opt-1", model.WorkflowTriggerOptimization, mock.MatchedBy(func(inputs map[string]any) bool {
		event := inputs["event"].(map[string]any)
		return event["recommendation_id"] == "rec-1"
	})).Return("", errors.New("temporal unavailable")).Once()

	mockExecutor.On("ExecuteWorkflow", mock.Anything, "acc-1", "wf-opt-1", model.WorkflowTriggerOptimization, mock.MatchedBy(func(inputs map[string]any) bool {
		event := inputs["event"].(map[string]any)
		return event["recommendation_id"] == "rec-2"
	})).Return("run-2", nil).Once()

	poller.poll(context.Background())

	// Both calls were made despite the first one failing
	assert.Equal(t, 2, len(mockExecutor.Calls))
	mockRecStore.AssertExpectations(t)
	mockExecutor.AssertExpectations(t)
}

func TestRecommendationPoller_LastPollAdvancesOnSuccess(t *testing.T) {
	mockRecStore, _, poller := setupPollerTest(t, nil)

	before := time.Now().UTC()
	poller.lastPoll = before.Add(-5 * time.Minute)

	mockRecStore.On("FindNewRecommendations", mock.Anything, mock.Anything).
		Return([]storage.RecommendationEvent{}, nil)

	poller.poll(context.Background())

	// lastPoll should have advanced to ~now
	assert.True(t, poller.lastPoll.After(before) || poller.lastPoll.Equal(before))
}

func TestRecommendationPoller_StartStopsOnContextCancel(t *testing.T) {
	mockRecStore, _, poller := setupPollerTest(t, nil)
	poller.interval = 50 * time.Millisecond

	mockRecStore.On("FindNewRecommendations", mock.Anything, mock.Anything).
		Return([]storage.RecommendationEvent{}, nil).Maybe()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		poller.Start(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Start returned after context cancellation - success
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancellation")
	}
}
