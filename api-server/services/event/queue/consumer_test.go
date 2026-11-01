package queue

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
)

// postProcessSpy records which path the post-process consumer took, by event id.
type postProcessSpy struct {
	postProcessed, workflowsReEmitted, resolvedNotified, returnedNotified []string
	// returnedErr is what the returned notification reports.
	returnedErr error
}

// swapPostProcessSeams replaces the consumer's seams for one test, so no path
// reaches a database or a broker, and restores them afterwards.
func swapPostProcessSeams(t *testing.T) *postProcessSpy {
	t.Helper()
	spy := &postProcessSpy{}

	origLoad, origPostProcess, origReEmit := loadEventMapFn, postProcessEventFn, reEmitForWorkflowsFn
	origResolved, origReturned := processEventResolvedFn, processEventReturnedFn
	t.Cleanup(func() {
		loadEventMapFn, postProcessEventFn, reEmitForWorkflowsFn = origLoad, origPostProcess, origReEmit
		processEventResolvedFn, processEventReturnedFn = origResolved, origReturned
	})

	id := func(e map[string]any) string { return e["id"].(string) }
	loadEventMapFn = func(_ context.Context, eventID string, _ *slog.Logger) (*security.RequestContext, map[string]any, error) {
		return nil, map[string]any{"id": eventID, "source": "anomaly"}, nil
	}
	postProcessEventFn = func(_ *security.RequestContext, e map[string]any) {
		spy.postProcessed = append(spy.postProcessed, id(e))
	}
	reEmitForWorkflowsFn = func(_ *security.RequestContext, e map[string]any) {
		spy.workflowsReEmitted = append(spy.workflowsReEmitted, id(e))
	}
	processEventResolvedFn = func(_ *security.RequestContext, e map[string]any) error {
		spy.resolvedNotified = append(spy.resolvedNotified, id(e))
		return nil
	}
	processEventReturnedFn = func(_ *security.RequestContext, e map[string]any) error {
		spy.returnedNotified = append(spy.returnedNotified, id(e))
		return spy.returnedErr
	}
	return spy
}

// A returned event gets its notification and nothing else: the event went
// through triage, the investigation and event.created when it was first
// inserted. The second message is the one the anomaly producer sends; its
// workflow_only is there for a consumer that predates phase and must not win
// here.
func TestProcessEventPostProcessMessage_ReturnedPhaseSendsOnlyTheReturnedNotification(t *testing.T) {
	for _, data := range []string{
		`{"event_id":"e-returned","phase":"returned"}`,
		`{"event_id":"e-returned","workflow_only":true,"phase":"returned"}`,
	} {
		t.Run(data, func(t *testing.T) {
			spy := swapPostProcessSeams(t)

			err := processEventPostProcessMessage(context.Background(), []byte(data))

			assert.NoError(t, err)
			assert.Equal(t, []string{"e-returned"}, spy.returnedNotified)
			assert.Empty(t, spy.postProcessed, "the full pipeline must not run again for a returned event")
			assert.Empty(t, spy.workflowsReEmitted, "event.created must not be emitted again")
			assert.Empty(t, spy.resolvedNotified)
		})
	}
}

// The queue has no dead-letter exchange, so an error returned here would
// redeliver the message forever.
func TestProcessEventPostProcessMessage_ReturnedPublishFailureIsAcked(t *testing.T) {
	spy := swapPostProcessSeams(t)
	spy.returnedErr = errors.New("broker is down")

	err := processEventPostProcessMessage(context.Background(), []byte(`{"event_id":"e-returned","phase":"returned"}`))

	assert.NoError(t, err)
	assert.Equal(t, []string{"e-returned"}, spy.returnedNotified)
	assert.Empty(t, spy.postProcessed)
}

// Every message published before phase existed carries no phase and must take
// the path it always took.
func TestProcessEventPostProcessMessage_MessagesWithoutPhaseAreHandledAsBefore(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		want       postProcessSpy
	}{
		{
			name: "new event runs the full pipeline",
			data: `{"event_id":"e-1"}`,
			want: postProcessSpy{postProcessed: []string{"e-1"}},
		},
		{
			name: "re-fire goes to workflows only",
			data: `{"event_id":"e-1","workflow_only":true}`,
			want: postProcessSpy{workflowsReEmitted: []string{"e-1"}},
		},
		{
			name: "resolve sends the resolved notification only",
			data: `{"event_id":"e-1","notify_resolved":true}`,
			want: postProcessSpy{resolvedNotified: []string{"e-1"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := swapPostProcessSeams(t)

			err := processEventPostProcessMessage(context.Background(), []byte(tc.data))

			assert.NoError(t, err)
			assert.Equal(t, tc.want, *spy)
		})
	}
}
