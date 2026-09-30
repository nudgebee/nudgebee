package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/security"
)

// get_event_by_id's inline text sends the model to get_event_evidence for
// anything it does not inline. The events agent must be able to call it, and
// has no shell_execute to fall back on.
func TestAgentEvents_CanFetchOffloadedEvidence(t *testing.T) {
	names := toolNames(AgentEvents{}.GetSupportedTools(security.NewRequestContextForSuperAdmin()))
	assert.Contains(t, names, "get_event_by_id")
	assert.Contains(t, names, "get_event_evidence")
	assert.NotContains(t, names, "shell_execute")
}
