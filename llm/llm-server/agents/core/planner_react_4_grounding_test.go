package core

import (
	"github.com/stretchr/testify/assert"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"testing"
)

func TestReAct4GroundingScope(t *testing.T) {
	original := config.Config.OrchestratorGroundingEnabled
	t.Cleanup(func() { config.Config.OrchestratorGroundingEnabled = original })
	for _, tc := range []struct {
		name    string
		enabled bool
		parent  string
		agent   NBAgent
		want    bool
	}{
		{"top level", true, "", &MockAgent{}, true},
		{"self parent", true, "top", &MockAgent{}, true},
		{"disabled", false, "", &MockAgent{}, false},
		{"child", true, "parent", &MockAgent{}, false},
		{"specialist", true, "", notebookOptOutAgent{}, false},
		{"custom", true, "", &nbCustomAgent{agent: AgentDto{Config: map[string]any{}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config.Config.OrchestratorGroundingEnabled = tc.enabled
			out := renderReact4Base(security.NewRequestContextForSuperAdmin(), NBAgentRequest{AgentId: "top", ParentAgentId: tc.parent}, tc.agent, nil)
			assert.NotEmpty(t, out)
			header := "GROUND THE REQUEST BEFORE COMMITTING TO AN INVESTIGATION PATH"
			if tc.want {
				assert.Contains(t, out, header)
				assert.Contains(t, out, "ask one focused clarification")
				assert.Contains(t, out, "A failed or empty tool result does not confirm the symptom")
				assert.Contains(t, out, "independent checks may run in parallel")
			} else {
				assert.NotContains(t, out, header)
			}
		})
	}
}
