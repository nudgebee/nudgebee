package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestParseExecutorType is the dual-read guard for the V776 executor_type value
// rename ("rewoo" -> "orchestrating", #33515). Rows written before the migration
// — or by an old pod mid rolling-deploy — still carry "rewoo" and MUST resolve to
// Orchestrating; every other value passes through verbatim.
func TestParseExecutorType(t *testing.T) {
	cases := []struct {
		stored string
		want   AgentPlannerType
	}{
		{"rewoo", AgentPlannerTypeOrchestrating},         // legacy value
		{"orchestrating", AgentPlannerTypeOrchestrating}, // new value
		{"react", AgentPlannerTypeReAct},
		{"tools", AgentPlannerTypeTool},
		{"custom", AgentPlannerTypeCustom},
		{"react_3", AgentPlannerTypeReAct3},
		{"classification", AgentPlannerTypeClassification},
	}
	for _, c := range cases {
		t.Run(c.stored, func(t *testing.T) {
			assert.Equal(t, c.want, ParseExecutorType(c.stored))
		})
	}
}

// TestOrchestratingConstantValue pins the wire/DB value so a future accidental
// edit can't silently desync it from the V776 CHECK constraint.
func TestOrchestratingConstantValue(t *testing.T) {
	assert.Equal(t, AgentPlannerType("orchestrating"), AgentPlannerTypeOrchestrating)
}
