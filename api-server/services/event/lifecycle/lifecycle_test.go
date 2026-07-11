package lifecycle

import (
	"log/slog"
	"testing"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
)

func resetHooks() {
	mu.Lock()
	defer mu.Unlock()
	hooks = map[Phase][]namedHandler{}
}

func testCtx() *security.RequestContext {
	return security.NewRequestContextForTenantAdmin("t1", slog.Default(), nil, nil)
}

// PhaseInvestigationEnqueued is not workflow-eligible, so Emit runs only the
// in-process hooks (no MQ publish) — ideal for exercising the registry.

func TestEmitRunsHooksInRegistrationOrder(t *testing.T) {
	resetHooks()
	var order []string
	RegisterLifecycleHook(PhaseInvestigationEnqueued, "first", func(_ *security.RequestContext, _ map[string]any) error {
		order = append(order, "first")
		return nil
	})
	RegisterLifecycleHook(PhaseInvestigationEnqueued, "second", func(_ *security.RequestContext, _ map[string]any) error {
		order = append(order, "second")
		return nil
	})

	Emit(testCtx(), PhaseInvestigationEnqueued, map[string]any{}, nil)
	assert.Equal(t, []string{"first", "second"}, order)
}

func TestEmitRunsOnlyMatchingPhaseHooks(t *testing.T) {
	resetHooks()
	ran := map[string]bool{}
	RegisterLifecycleHook(PhaseInvestigationEnqueued, "a", func(_ *security.RequestContext, _ map[string]any) error {
		ran["a"] = true
		return nil
	})
	RegisterLifecycleHook(PhaseInvestigationWaiting, "b", func(_ *security.RequestContext, _ map[string]any) error {
		ran["b"] = true
		return nil
	})

	Emit(testCtx(), PhaseInvestigationEnqueued, map[string]any{}, nil)
	assert.True(t, ran["a"], "phase A hook should run")
	assert.False(t, ran["b"], "phase B hook must not run for phase A")
}

func TestEmitMergesExtraAndStampsPhase(t *testing.T) {
	resetHooks()
	var seen map[string]any
	RegisterLifecycleHook(PhaseInvestigationEnqueued, "capture", func(_ *security.RequestContext, ev map[string]any) error {
		seen = ev
		return nil
	})

	ev := map[string]any{"id": "e1"}
	Emit(testCtx(), PhaseInvestigationEnqueued, ev, map[string]any{"analysis_summary": "s"})

	assert.Equal(t, "s", seen["analysis_summary"], "extra merged into event")
	assert.Equal(t, string(PhaseInvestigationEnqueued), seen[LifecyclePhaseKey], "phase stamped on event")
}

func TestEmitHookErrorDoesNotStopOthers(t *testing.T) {
	resetHooks()
	var ran []string
	RegisterLifecycleHook(PhaseInvestigationEnqueued, "boom", func(_ *security.RequestContext, _ map[string]any) error {
		ran = append(ran, "boom")
		return assert.AnError
	})
	RegisterLifecycleHook(PhaseInvestigationEnqueued, "after", func(_ *security.RequestContext, _ map[string]any) error {
		ran = append(ran, "after")
		return nil
	})

	Emit(testCtx(), PhaseInvestigationEnqueued, map[string]any{}, nil)
	assert.Equal(t, []string{"boom", "after"}, ran, "a hook error must not stop later hooks")
}
