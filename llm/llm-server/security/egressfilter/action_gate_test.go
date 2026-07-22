package egressfilter

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// TestResolveAction_NilGateFallback locks the contract that when no
// ActionGate is registered, every Mode resolves to ActionAudit — including
// ModeEnforce. This is the safety floor: a missing policy provider must
// never silently turn ModeEnforce into a panic or undefined behaviour.
func TestResolveAction_NilGateFallback(t *testing.T) {
	clearActionGateForTest(t)
	empty := Result{}
	assert.Equal(t, ActionAudit, resolveAction(ModeDetect, empty))
	assert.Equal(t, ActionAudit, resolveAction(ModeEnforce, empty))
}

// TestResolveAction_GateOverrides locks that a registered gate's decision
// is the one used, even when Mode-derived logic would have returned
// something else.
func TestResolveAction_GateOverrides(t *testing.T) {
	prev := ActionGate
	t.Cleanup(func() { ActionGate = prev })
	// Force-everything-blocks gate — to prove resolveAction defers to the
	// registered function rather than inspecting Mode itself.
	ActionGate = func(_ Mode, _ Result) Action { return ActionBlock }
	assert.Equal(t, ActionBlock, resolveAction(ModeDetect, Result{}))
	assert.Equal(t, ActionBlock, resolveAction(ModeEnforce, Result{}))
}

// TestWrapModel_EnforceWithoutGate_DegradesToDetect is the end-to-end
// version of the fallback: with no gate registered, even ModeEnforce
// passes the LLM call through (the inner model is called) and no
// ErrSecretsBlocked is returned. Operators see a one-shot WARN log noting
// the missing gate.
func TestWrapModel_EnforceWithoutGate_DegradesToDetect(t *testing.T) {
	clearActionGateForTest(t)
	resetEnforceWarnOnce()

	inner := &fakeModel{respText: "passed through"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeEnforce)
	_, err := wrapped.GenerateContent(context.Background(),
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeHuman, "leaked: ghp_Abcdefghijklmnopqrstuvwxyz0123456789"),
		})
	require.NoError(t, err, "no gate registered → call must NOT be blocked")
	assert.Equal(t, 1, inner.callCount(), "inner model must be called when no gate blocks")
}

// resetEnforceWarnOnce lets tests exercise the one-shot warning across
// multiple cases in a single `go test` invocation. Production code has no
// reason to reset this; the sync.Once is intentionally per-process.
func resetEnforceWarnOnce() {
	enforceWithoutGateOnce = sync.Once{}
}
