package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestExtractToolsInvoked locks down the signal shape consumed by the
// critic prompts: ordered, deduplicated, "(none)" when empty.
func TestExtractToolsInvoked(t *testing.T) {
	cases := []struct {
		name  string
		steps []NBAgentPlannerToolActionStep
		want  string
	}{
		{
			name:  "empty steps → (none) sentinel",
			steps: nil,
			want:  "(none)",
		},
		{
			name: "all blank tool names → (none) sentinel",
			steps: []NBAgentPlannerToolActionStep{
				{Action: NBAgentPlannerToolAction{Tool: ""}},
				{Action: NBAgentPlannerToolAction{Tool: "   "}},
			},
			want: "(none)",
		},
		{
			name: "single tool",
			steps: []NBAgentPlannerToolActionStep{
				{Action: NBAgentPlannerToolAction{Tool: "kubectl"}},
			},
			want: "kubectl",
		},
		{
			name: "preserves order, deduplicates",
			steps: []NBAgentPlannerToolActionStep{
				{Action: NBAgentPlannerToolAction{Tool: "kubectl"}},
				{Action: NBAgentPlannerToolAction{Tool: "kubectl_execute"}},
				{Action: NBAgentPlannerToolAction{Tool: "kubectl"}}, // dup
				{Action: NBAgentPlannerToolAction{Tool: "events"}},
				{Action: NBAgentPlannerToolAction{Tool: "kubectl_execute"}}, // dup
			},
			want: "kubectl, kubectl_execute, events",
		},
		{
			name: "trims whitespace from tool names",
			steps: []NBAgentPlannerToolActionStep{
				{Action: NBAgentPlannerToolAction{Tool: " logs "}},
				{Action: NBAgentPlannerToolAction{Tool: "metrics"}},
			},
			want: "logs, metrics",
		},
		{
			name: "73a regression case — only status-check tools, no evidence tools",
			steps: []NBAgentPlannerToolActionStep{
				{Action: NBAgentPlannerToolAction{Tool: "kubectl"}},
				{Action: NBAgentPlannerToolAction{Tool: "kubectl_execute"}},
			},
			// Critic prompt rule: investigation question + this list (no logs/events/metrics) + "no issues" answer → REJECT
			want: "kubectl, kubectl_execute",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractToolsInvoked(tc.steps)
			assert.Equal(t, tc.want, got)
		})
	}
}
