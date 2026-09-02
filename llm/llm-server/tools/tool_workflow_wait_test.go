package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runbookStub stands in for runbook-server's execution-detail endpoint, serving
// one scripted response per poll so a run can be walked through states.
func runbookStub(t *testing.T, responses []map[string]any) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(atomic.AddInt32(&calls, 1)) - 1
		if i >= len(responses) {
			i = len(responses) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(responses[i])
	}))
	t.Cleanup(srv.Close)

	original := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = srv.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = original })

	// Poll fast so the suite does not spend real seconds sleeping.
	originalInterval := executionPollInterval
	executionPollInterval = time.Millisecond
	t.Cleanup(func() { executionPollInterval = originalInterval })

	return srv, &calls
}

func TestWaitForExecutionTerminalStates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
	}{
		{"completed", "COMPLETED"},
		{"failed", "FAILED"},
		{"canceled", "CANCELED"},
		{"terminated", "TERMINATED"},
		{"timed out", "TIMED_OUT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runbookStub(t, []map[string]any{
				{"status": tc.status, "workflow_result": map[string]any{"ok": true}},
			})

			outcome := waitForExecution(context.Background(), "wf-1", "exec-1", "acct", "tenant", "user")
			assert.Equal(t, tc.status, outcome.Status)
			assert.Empty(t, outcome.Note, "a terminal run needs no explanatory note")
		})
	}
}

func TestWaitForExecutionPollsUntilTerminal(t *testing.T) {
	_, calls := runbookStub(t, []map[string]any{
		{"status": "RUNNING"},
		{"status": "RUNNING"},
		{"status": "COMPLETED", "workflow_result": map[string]any{"restarted": 3}},
	})

	outcome := waitForExecution(context.Background(), "wf-1", "exec-1", "acct", "tenant", "user")
	assert.Equal(t, "COMPLETED", outcome.Status)
	assert.NotNil(t, outcome.Result)
	assert.GreaterOrEqual(t, int(atomic.LoadInt32(calls)), 3, "should have polled past the RUNNING responses")
}

func TestWaitForExecutionReturnsErrorFromRun(t *testing.T) {
	runbookStub(t, []map[string]any{
		{"status": "FAILED", "error": "task restart failed: pod not found"},
	})

	outcome := waitForExecution(context.Background(), "wf-1", "exec-1", "acct", "tenant", "user")
	assert.Equal(t, "FAILED", outcome.Status)
	assert.Contains(t, outcome.Error, "pod not found")
}

func TestWaitForExecutionParkedOnApproval(t *testing.T) {
	// A run waiting on a human may sit for hours; it must not hold the turn open.
	runbookStub(t, []map[string]any{
		{
			"status": "RUNNING",
			"tasks": []map[string]any{
				{"type": "core.print", "status": "COMPLETED"},
				{"type": approvalTaskType, "status": "SCHEDULED"},
			},
		},
	})

	outcome := waitForExecution(context.Background(), "wf-1", "exec-1", "acct", "tenant", "user")
	assert.Equal(t, "RUNNING", outcome.Status)
	assert.Contains(t, outcome.Note, "approval")
	assert.Equal(t, "exec-1", outcome.ExecutionID, "the run must stay findable")
}

func TestWaitForExecutionIgnoresCompletedApprovalTask(t *testing.T) {
	// An already-answered approval is not a reason to stop waiting.
	_, calls := runbookStub(t, []map[string]any{
		{
			"status": "RUNNING",
			"tasks":  []map[string]any{{"type": approvalTaskType, "status": "COMPLETED"}},
		},
		{"status": "COMPLETED"},
	})

	outcome := waitForExecution(context.Background(), "wf-1", "exec-1", "acct", "tenant", "user")
	assert.Equal(t, "COMPLETED", outcome.Status)
	assert.Empty(t, outcome.Note)
	assert.GreaterOrEqual(t, int(atomic.LoadInt32(calls)), 2)
}

func TestWaitForExecutionTimesOutWithoutClaimingSuccess(t *testing.T) {
	runbookStub(t, []map[string]any{{"status": "RUNNING"}})

	// A cancelled context stands in for the wait ceiling so the test is fast.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcome := waitForExecution(ctx, "wf-1", "exec-1", "acct", "tenant", "user")
	assert.NotEqual(t, "COMPLETED", outcome.Status, "must never report success it did not observe")
	// Assert the actual message, not merely that there is one: an earlier version
	// of this test only checked NotEmpty, which is why it stayed green while a
	// timeout was being reported as an unreadable status.
	assert.Equal(t, "still running; check back with workflow_execution_get", outcome.Note)
	assert.Equal(t, "exec-1", outcome.ExecutionID)
}

// TestWaitForExecutionTimeoutMidReadStillReportsStillRunning covers the deadline
// expiring while a poll is in flight, rather than between polls. The read then
// fails with a context error, and treating that as an ordinary read failure told
// the user their automation was in an unknown state when it was simply still
// going — the exact confusion this helper exists to prevent.
func TestWaitForExecutionTimeoutMidReadStillReportsStillRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Outlive the caller's deadline, so the failure is the context's.
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "RUNNING"})
	}))
	t.Cleanup(srv.Close)
	originalURL := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = srv.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = originalURL })
	originalInterval := executionPollInterval
	executionPollInterval = time.Millisecond
	t.Cleanup(func() { executionPollInterval = originalInterval })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	outcome := waitForExecution(ctx, "wf-1", "exec-1", "acct", "tenant", "user")
	assert.Equal(t, "still running; check back with workflow_execution_get", outcome.Note)
	assert.NotContains(t, outcome.Note, "could not be read")
	assert.Equal(t, "RUNNING", outcome.Status)
}

func TestWaitForExecutionSurvivesATransientReadFailure(t *testing.T) {
	// A single blip mid-run must not end the watch. Seen live: one transient 401
	// about halfway through a healthy run made the assistant tell the user the
	// automation had failed, when it completed moments later.
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch atomic.AddInt32(&calls, 1) {
		case 1:
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "RUNNING"})
		case 2:
			w.WriteHeader(http.StatusUnauthorized) // the blip
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "COMPLETED"})
		}
	}))
	t.Cleanup(srv.Close)
	originalURL := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = srv.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = originalURL })
	originalInterval := executionPollInterval
	executionPollInterval = time.Millisecond
	t.Cleanup(func() { executionPollInterval = originalInterval })

	outcome := waitForExecution(context.Background(), "wf-1", "exec-1", "acct", "tenant", "user")
	assert.Equal(t, "COMPLETED", outcome.Status, "one failed poll must not end the watch")
	assert.Empty(t, outcome.Note)
}

func TestWaitForExecutionGivesUpAfterRepeatedReadFailures(t *testing.T) {
	// Persistent unreachability should still end quickly rather than burning the
	// whole timeout.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	originalURL := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = srv.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = originalURL })
	originalInterval := executionPollInterval
	executionPollInterval = time.Millisecond
	t.Cleanup(func() { executionPollInterval = originalInterval })

	outcome := waitForExecution(context.Background(), "wf-1", "exec-1", "acct", "tenant", "user")
	assert.Contains(t, outcome.Note, "status could not be read")
	assert.NotEqual(t, "FAILED", outcome.Status)
}

func TestWaitForExecutionUnreadableStatusStillReportsTheRunStarted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	original := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = srv.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = original })

	originalInterval := executionPollInterval
	executionPollInterval = time.Millisecond
	t.Cleanup(func() { executionPollInterval = originalInterval })

	outcome := waitForExecution(context.Background(), "wf-1", "exec-1", "acct", "tenant", "user")
	// The run WAS started — only the observation failed. Saying "failed" here
	// would be a lie that could prompt someone to run it a second time.
	assert.NotEqual(t, "FAILED", outcome.Status)
	assert.Contains(t, outcome.Note, "status could not be read")
	assert.Equal(t, "exec-1", outcome.ExecutionID)
}

func TestMarshalOutcomeAlwaysKeepsIdentifiers(t *testing.T) {
	out := marshalOutcome(executionOutcome{
		WorkflowID:  "wf-1",
		ExecutionID: "exec-1",
		Status:      "RUNNING",
		Result:      make(chan int), // unmarshalable, forces the fallback path
	})
	assert.Contains(t, out, "wf-1")
	assert.Contains(t, out, "exec-1")

	var round map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &round), "fallback must still be valid JSON")
}

func TestTriggerSourceHeaderIsSentOnEveryRunbookCall(t *testing.T) {
	var gotSource, gotUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSource = r.Header.Get(headerTriggerSource)
		gotUser = r.Header.Get("X-User-ID")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	original := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = srv.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = original })

	_, err := DoRunbookRequest("GET", "workflows", nil, "acct", "tenant", "user-1")
	require.NoError(t, err)
	assert.Equal(t, triggerSourceAI, gotSource, "runbook-server's AI gate depends on this header")
	assert.Equal(t, "user-1", gotUser)
}
