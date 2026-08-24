package core

import (
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

// TestEvaluateConditions_Expression covers the pure (no-LLM) branches of
// evaluateConditions: the no-condition fast path, boolean expression results,
// the non-boolean and evaluation-error guards, and dependency-output wiring.
// All hermetic — only e.ctx (for logging) is needed.
func TestEvaluateConditions_Expression(t *testing.T) {
	exec := &plannerExecutor{ctx: security.NewRequestContextForSuperAdmin()}

	cases := []struct {
		name    string
		action  NBAgentPlannerToolAction
		steps   []NBAgentPlannerToolActionStep
		want    bool
		wantErr bool
	}{
		{
			name:   "no condition is always allowed",
			action: NBAgentPlannerToolAction{},
			want:   true,
		},
		{
			name:   "expression evaluating true allows the action",
			action: NBAgentPlannerToolAction{Condition: NBAgentPlannerToolActionCondition{Expression: "1 == 1"}},
			want:   true,
		},
		{
			name:   "expression evaluating false skips the action",
			action: NBAgentPlannerToolAction{Condition: NBAgentPlannerToolActionCondition{Expression: "1 == 2"}},
			want:   false,
		},
		{
			name:    "non-boolean expression is an error",
			action:  NBAgentPlannerToolAction{Condition: NBAgentPlannerToolActionCondition{Expression: "1 + 1"}},
			want:    false,
			wantErr: true,
		},
		{
			name:    "expression referencing an unknown variable is an error",
			action:  NBAgentPlannerToolAction{Condition: NBAgentPlannerToolActionCondition{Expression: "missingvar == 1"}},
			want:    false,
			wantErr: true,
		},
		{
			name: "expression reads a dependency's observation",
			action: NBAgentPlannerToolAction{
				Dependency: []string{"d1"},
				Condition:  NBAgentPlannerToolActionCondition{Expression: `d1 == "yes"`},
			},
			steps: []NBAgentPlannerToolActionStep{
				{Action: NBAgentPlannerToolAction{ToolID: "d1"}, Observation: "yes"},
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := exec.evaluateConditions(tc.action, tc.steps)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestEvaluateConditions_LLM covers the LLM-backed condition branch via the
// fake-LLM seam: the model's answer is matched against AllowedResponses and
// ExpectedResponse. No live provider needed.
func TestEvaluateConditions_LLM(t *testing.T) {
	newExec := func() *plannerExecutor {
		return &plannerExecutor{
			ctx: llmOverrideContext(),
			agentRequest: NBAgentRequest{
				AccountId:      "acct-1",
				UserId:         "user-1",
				ConversationId: "conv-1",
				MessageId:      "msg-1",
			},
		}
	}
	condition := func() NBAgentPlannerToolActionCondition {
		return NBAgentPlannerToolActionCondition{
			Prompt:           "Is the service healthy?",
			AllowedResponses: []string{"true", "false"},
			ExpectedResponse: "true",
		}
	}

	cases := []struct {
		name     string
		response string
		fakeErr  bool
		want     bool
		wantErr  bool
	}{
		{name: "matching expected response is met", response: "true", want: true},
		{name: "allowed but unexpected response is not met", response: "false", want: false},
		{name: "response outside allowed set is not met", response: "maybe", want: false},
		{name: "LLM failure is an error", response: "", fakeErr: true, want: false, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeLLMModel{response: tc.response}
			if tc.fakeErr {
				fake.err = assert.AnError
			}
			withFakeLLMModel(t, fake)

			action := NBAgentPlannerToolAction{Condition: condition()}
			got, err := newExec().evaluateConditions(action, nil)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.want, got)
		})
	}
}
