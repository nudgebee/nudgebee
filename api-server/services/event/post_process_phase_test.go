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

func TestReEmitForWorkflows_WorkflowOnlyNoPipeline(t *testing.T) {
	origTriage, origLLM, origEmit, origWf := triageStep, llmStep, emitLifecycle, emitWorkflowOnly
	t.Cleanup(func() {
		triageStep, llmStep, emitLifecycle, emitWorkflowOnly = origTriage, origLLM, origEmit, origWf
	})

	called := &[]string{}
	triageStep = func(_ *security.RequestContext, _ map[string]any) error {
		*called = append(*called, "triage")
		return nil
	}
	llmStep = func(_ *security.RequestContext, _ map[string]any) error { *called = append(*called, "llm"); return nil }
	emitLifecycle = func(_ *security.RequestContext, phase lifecycle.Phase, _ map[string]any, _ map[string]any) {
		*called = append(*called, "emit:"+string(phase))
	}
	emitWorkflowOnly = func(_ *security.RequestContext, phase lifecycle.Phase, _ map[string]any) {
		*called = append(*called, "wf:"+string(phase))
	}

	ReEmitForWorkflows(testCtx(), map[string]any{})

	// A re-fire re-delivers to event-trigger workflows at the event.created phase
	// ONLY — the full pipeline (triage/llm/notification) ran on first insert and
	// must not re-run on every repeat occurrence.
	assert.Equal(t, []string{"wf:event.created"}, *called)
}

func TestPagerdutyCommentIfNotInvestigated_SkipsWhenEnqueued(t *testing.T) {
	// When an investigation was enqueued, the created-phase pagerduty hook must
	// be a no-op (the "report ready" link is posted at investigation.completed
	// instead). With no marker it falls through to processPagerDutyComment,
	// which self-gates on source/finding_id and returns nil for an empty event.
	assert.NoError(t, pagerdutyCommentIfNotInvestigated(testCtx(), map[string]any{llm.EventInvestigationEnqueuedKey: true}))
	assert.NoError(t, pagerdutyCommentIfNotInvestigated(testCtx(), map[string]any{}))
}

func TestPostProcessEvent_RecordOnlyIsTriagedButNotActedOn(t *testing.T) {
	shapes := map[string]any{
		"labels as decoded from the queue": map[string]any{recordOnlyLabel: "true"},
		"labels as a string map":           map[string]string{recordOnlyLabel: "true"},
		"labels as stored JSON":            `{"nb_record_only":"true"}`,
	}
	for name, labels := range shapes {
		t.Run(name, func(t *testing.T) {
			called := stubPipeline(t, false)
			PostProcessEvent(testCtx(), map[string]any{"source": "AWS_CloudWatch_Alarm", "labels": labels})
			// Triage still counts it; no investigation, notification or workflow.
			assert.Equal(t, []string{"triage"}, *called)
		})
	}
}

func TestPostProcessEvent_OtherLabelsAreActedOn(t *testing.T) {
	for name, event := range map[string]map[string]any{
		"no labels":            {"source": "AWS_CloudWatch_Alarm"},
		"label set to false":   {"source": "AWS_CloudWatch_Alarm", "labels": map[string]any{recordOnlyLabel: "false"}},
		"unrelated label only": {"source": "AWS_CloudWatch_Alarm", "labels": map[string]any{"aws_region": "us-east-1"}},
		// A label the customer's own alert rule can set must not mute it.
		"label from a customer-controlled source": {"source": "prometheus", "labels": map[string]any{recordOnlyLabel: "true"}},
		"label with no source":                    {"labels": map[string]any{recordOnlyLabel: "true"}},
	} {
		t.Run(name, func(t *testing.T) {
			called := stubPipeline(t, false)
			PostProcessEvent(testCtx(), event)
			assert.Equal(t, []string{"triage", "llm", "emit:event.created"}, *called)
		})
	}
}
