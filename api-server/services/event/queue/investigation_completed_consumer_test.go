package queue

import (
	"database/sql"
	"log/slog"
	"testing"

	"nudgebee/services/event/lifecycle"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type emittedCall struct {
	phase lifecycle.Phase
	event map[string]any
	extra map[string]any
}

// withStubbedSeams swaps the loadEventMapFn / emitLifecycleFn seams for the
// duration of the test and restores them afterward. The emit stub records the
// (phase, event, extra) it was called with.
func withStubbedSeams(t *testing.T, loadErr error) *[]emittedCall {
	t.Helper()
	origLoad := loadEventMapFn
	origEmit := emitLifecycleFn
	t.Cleanup(func() {
		loadEventMapFn = origLoad
		emitLifecycleFn = origEmit
	})

	calls := &[]emittedCall{}
	loadEventMapFn = func(eventID string, logger *slog.Logger) (*security.RequestContext, map[string]any, error) {
		if loadErr != nil {
			return nil, nil, loadErr
		}
		return nil, map[string]any{"id": eventID}, nil
	}
	emitLifecycleFn = func(ctx *security.RequestContext, phase lifecycle.Phase, event map[string]any, extra map[string]any) {
		*calls = append(*calls, emittedCall{phase: phase, event: event, extra: extra})
	}
	return calls
}

func TestProcessInvestigationCompleted_MalformedJSON(t *testing.T) {
	calls := withStubbedSeams(t, nil)
	err := processInvestigationCompleted([]byte("{not json"))
	assert.NoError(t, err)
	assert.Empty(t, *calls, "no phase must be emitted on malformed payload")
}

func TestProcessInvestigationCompleted_MissingIDs(t *testing.T) {
	calls := withStubbedSeams(t, nil)
	err := processInvestigationCompleted([]byte(`{"status":"COMPLETED"}`))
	assert.NoError(t, err)
	assert.Empty(t, *calls)
}

func TestProcessInvestigationCompleted_NonTerminalSkipped(t *testing.T) {
	calls := withStubbedSeams(t, nil)
	err := processInvestigationCompleted([]byte(`{"event_id":"e-nonterminal","account_id":"a1","status":"IN_PROGRESS"}`))
	assert.NoError(t, err)
	assert.Empty(t, *calls, "non-terminal status must not emit a phase")
}

func TestProcessInvestigationCompleted_CompletedEmitsCompletedPhase(t *testing.T) {
	calls := withStubbedSeams(t, nil)
	err := processInvestigationCompleted([]byte(`{"event_id":"e-completed","account_id":"a1","status":"COMPLETED","summary":"the summary","investigation":"the rca","log_summary":"ls","log_analysis":"la"}`))
	require.NoError(t, err)
	require.Len(t, *calls, 1)
	c := (*calls)[0]
	assert.Equal(t, lifecycle.PhaseInvestigationCompleted, c.phase)
	assert.Equal(t, "COMPLETED", c.extra["analysis_status"])
	assert.Equal(t, "the summary", c.extra["analysis_summary"])
	assert.Equal(t, "the rca", c.extra["analysis_investigation"])
	assert.Equal(t, "ls", c.extra["analysis_log_summary"])
	assert.Equal(t, "la", c.extra["analysis_log_analysis"])
}

func TestProcessInvestigationCompleted_FailedEmitsFailedPhase(t *testing.T) {
	calls := withStubbedSeams(t, nil)
	err := processInvestigationCompleted([]byte(`{"event_id":"e-failed","account_id":"a1","status":"FAILED","status_reason":"boom"}`))
	require.NoError(t, err)
	require.Len(t, *calls, 1, "FAILED must still emit (investigation.failed) so the event isn't dropped")
	c := (*calls)[0]
	assert.Equal(t, lifecycle.PhaseInvestigationFailed, c.phase)
	assert.Equal(t, "FAILED", c.extra["analysis_status"])
	assert.Equal(t, "boom", c.extra["analysis_status_reason"])
	assert.Empty(t, c.extra["analysis_summary"])
}

func TestProcessInvestigationCompleted_DedupRunsOnce(t *testing.T) {
	calls := withStubbedSeams(t, nil)
	payload := []byte(`{"event_id":"e-dedup","account_id":"a1","status":"COMPLETED","summary":"s"}`)
	require.NoError(t, processInvestigationCompleted(payload))
	require.NoError(t, processInvestigationCompleted(payload))
	assert.Len(t, *calls, 1, "duplicate completion envelopes must emit at most once per event")
}

func TestProcessInvestigationCompleted_TokenBearingDropped(t *testing.T) {
	calls := withStubbedSeams(t, nil)
	// A token-bearing envelope belongs to runbook-server's consumer; api-server
	// must drop it so it doesn't emit a phase for events it never deferred (a
	// runbook investigation bypasses api-server's lifecycle).
	err := processInvestigationCompleted([]byte(`{"task_token":"dGhpcy1pcy1hLXRva2Vu","event_id":"e-token","account_id":"a1","status":"COMPLETED","summary":"s"}`))
	assert.NoError(t, err)
	assert.Empty(t, *calls, "token-bearing envelopes must not emit a lifecycle phase")
}

func TestProcessInvestigationCompleted_NotFoundAcks(t *testing.T) {
	// A missing event is permanent — the message must be ACK'd (no error
	// returned) so it isn't requeued forever, and no phase is emitted.
	calls := withStubbedSeams(t, sql.ErrNoRows)
	err := processInvestigationCompleted([]byte(`{"event_id":"e-missing","account_id":"a1","status":"COMPLETED"}`))
	assert.NoError(t, err)
	assert.Empty(t, *calls, "no phase must be emitted when the event does not exist")
}

func TestProcessInvestigationCompleted_TransientLoadErrorRequeues(t *testing.T) {
	// A transient DB / network error must be returned so RabbitMQ requeues and
	// the lifecycle phase still fires once the event loads — not silently dropped.
	calls := withStubbedSeams(t, assert.AnError)
	err := processInvestigationCompleted([]byte(`{"event_id":"e-loaderr","account_id":"a1","status":"COMPLETED"}`))
	assert.Error(t, err, "a transient load error must requeue (return an error)")
	assert.Empty(t, *calls, "no phase must be emitted when the event can't be loaded")
}
