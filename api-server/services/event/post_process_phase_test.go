package event

import (
	"log/slog"
	"testing"

	"nudgebee/services/event/lifecycle"
	"nudgebee/services/llm"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
)

// stubPipeline swaps the triage/llm/emit seams for recording stubs and restores
// them afterward. setMarker controls whether the llm stub marks an investigation
// as enqueued. Returns a pointer to the ordered list of step names invoked.
func stubPipeline(t *testing.T, setMarker bool) *[]string {
	t.Helper()
	origTriage, origLLM, origEmit := triageStep, llmStep, emitLifecycle
	t.Cleanup(func() {
		triageStep, llmStep, emitLifecycle = origTriage, origLLM, origEmit
	})

	called := &[]string{}
	triageStep = func(_ *security.RequestContext, _ map[string]any) error {
		*called = append(*called, "triage")
		return nil
	}
	llmStep = func(_ *security.RequestContext, ev map[string]any) error {
		*called = append(*called, "llm")
		if setMarker {
			ev[llm.EventInvestigationEnqueuedKey] = true
		}
		return nil
	}
	emitLifecycle = func(_ *security.RequestContext, phase lifecycle.Phase, _ map[string]any, _ map[string]any) {
		*called = append(*called, "emit:"+string(phase))
	}
	return called
}

func testCtx() *security.RequestContext {
	return security.NewRequestContextForTenantAdmin("t1", slog.Default(), nil, nil)
}

func TestPostProcessEvent_TriageThenLLMThenEmitCreated(t *testing.T) {
	called := stubPipeline(t, false)
	PostProcessEvent(testCtx(), map[string]any{})

	// triage must run first, llm second, then the event.created emit — the
	// ordering downstream gates (notification suppressed/priority, llm
	// suppressed-skip) depend on.
	assert.Equal(t, []string{"triage", "llm", "emit:event.created"}, *called)
}

func TestPostProcessEvent_EmitsCreatedEvenWhenInvestigationEnqueued(t *testing.T) {
	called := stubPipeline(t, true)
	PostProcessEvent(testCtx(), map[string]any{})

	// The primary event.created emit fires regardless of whether an
	// investigation was enqueued — notification (a created-phase hook) is never
	// gated on the LLM (augment, don't gate).
	assert.Equal(t, []string{"triage", "llm", "emit:event.created"}, *called)
}

func TestPagerdutyCommentIfNotInvestigated_SkipsWhenEnqueued(t *testing.T) {
	// When an investigation was enqueued, the created-phase pagerduty hook must
	// be a no-op (the "report ready" link is posted at investigation.completed
	// instead). With no marker it falls through to processPagerDutyComment,
	// which self-gates on source/finding_id and returns nil for an empty event.
	assert.NoError(t, pagerdutyCommentIfNotInvestigated(testCtx(), map[string]any{llm.EventInvestigationEnqueuedKey: true}))
	assert.NoError(t, pagerdutyCommentIfNotInvestigated(testCtx(), map[string]any{}))
}
