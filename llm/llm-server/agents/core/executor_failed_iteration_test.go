package core

import "testing"

func TestShouldCountFailedIteration(t *testing.T) {
	tests := []struct {
		name                  string
		steps                 []NBAgentPlannerToolActionStep
		countNonEmptyFailures bool
		want                  bool
	}{
		{
			name: "no actions with gate disabled retains legacy behavior",
			want: true,
		},
		{
			name:                  "no actions with gate enabled",
			countNonEmptyFailures: true,
			want:                  true,
		},
		{
			name: "all actions failed with gate disabled",
			steps: []NBAgentPlannerToolActionStep{
				{Status: ToolStatusFailure},
				{Status: ToolStatusFailure},
			},
			want: false,
		},
		{
			name: "all actions failed with gate enabled",
			steps: []NBAgentPlannerToolActionStep{
				{Status: ToolStatusFailure},
				{Status: ToolStatusFailure},
			},
			countNonEmptyFailures: true,
			want:                  true,
		},
		{
			name: "mixed success and failure",
			steps: []NBAgentPlannerToolActionStep{
				{Status: ToolStatusFailure},
				{Status: ToolStatusSuccess},
			},
			countNonEmptyFailures: true,
			want:                  false,
		},
		{
			name: "circuit-open failure",
			steps: []NBAgentPlannerToolActionStep{
				{Status: ToolStatusFailure},
				{Status: ToolStatusFailure, IsCircuitOpen: true},
			},
			countNonEmptyFailures: true,
			want:                  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldCountFailedIteration(tt.steps, tt.countNonEmptyFailures); got != tt.want {
				t.Fatalf("shouldCountFailedIteration() = %v, want %v", got, tt.want)
			}
		})
	}
}
