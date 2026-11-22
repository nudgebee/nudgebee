package agents

import (
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/tools"

	"github.com/stretchr/testify/require"
)

// The events agent trims get_event_by_id rows to known columns before the
// model sees them; the note that the event is on another account must
// survive, or the model cannot say so or target its follow-ups.
func TestEventsPlannerViewKeepsTheAccountNote(t *testing.T) {
	raw := `[{"id":"e1","title":"Error log spike","description":"d","aggregation_key":"k","` + tools.EventAccountNoteKey + `":"Event e1 belongs to the Kubernetes cluster \"k8s-prod\""}]`
	got := AgentEvents{}.UpdateToolResponseForPlanner(core.NBAgentPlannerToolAction{Tool: tools.ToolGetEventById}, raw)
	require.Contains(t, got, tools.EventAccountNoteKey)
	require.Contains(t, got, `k8s-prod`)
}
