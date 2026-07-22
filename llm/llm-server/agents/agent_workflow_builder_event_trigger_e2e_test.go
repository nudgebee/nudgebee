package agents

import (
	"os"
	"strings"
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

// TestWorkflowBuilderAgent_GetEventTriggerSchema_E2E exercises the get_event_trigger_schema tool and
// its live DB-backed augmentation against a real events store. Gated on TEST_ACCOUNT (a live metastore
// seeded with events). It verifies the tool always returns the authoritative reference and, when the
// account has recent events, surfaces the real label keys — the whole point of the tool (grounding the
// agent in reality instead of guessing).
func TestWorkflowBuilderAgent_GetEventTriggerSchema_E2E(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("requires a live DB seeded with events; set TEST_ACCOUNT (and TEST_TENANT/TEST_USER) to run")
	}

	agent := newWorkflowBuilderAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})

	out := agent.toolGetEventTriggerSchema(sc)
	t.Logf("get_event_trigger_schema output:\n%s", out)

	// The authoritative reference is ALWAYS present (degrades gracefully if the DB is unreachable).
	assert.Contains(t, out, "EVENT TRIGGER FIELD REFERENCE")
	assert.Contains(t, out, "computed_priority")
	assert.Contains(t, out, "LIVE", "tool must always include a LIVE section (real keys or the graceful fallback)")

	// Exercise the live query path directly. If the account has recent events, real label keys must
	// appear; if it has none, the helper returns "" and the tool falls back to the static reference.
	live := agent.fetchLiveEventTriggerHints(sc)
	if strings.TrimSpace(live) == "" {
		t.Log("no live hints (account has no recent events / metastore unavailable) — static reference still returned")
		return
	}
	assert.Contains(t, out, live, "live hints must be embedded in the tool output")
	assert.True(t,
		strings.Contains(live, "label keys") || strings.Contains(live, "event_type values"),
		"live hints should surface real label keys and/or event_type values: %q", live)
}
