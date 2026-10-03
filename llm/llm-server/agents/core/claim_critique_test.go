package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/llm/config"
	nbprompts "nudgebee/llm/prompts"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/require"
)

func TestClaimShadowNeverAuditsTruncatedEvidence(t *testing.T) {
	previous := config.Config.LlmServerPreflightMaxMessageBytes
	t.Cleanup(func() { config.Config.LlmServerPreflightMaxMessageBytes = previous })
	config.Config.LlmServerPreflightMaxMessageBytes = 100
	fake := &fakeLLMModel{response: validClaimAudit}
	withFakeLLMModel(t, fake)
	base := llmOverrideContext()
	ctx := security.NewRequestContext(context.WithValue(base.GetContext(), contextKeyPreserveInput, true),
		base.GetSecurityContext(), base.GetLogger(), base.GetTracer(), base.GetMeter())
	result := evaluateClaimShadow(ctx, NBAgentRequest{}, "why", "answer", "",
		[]NBAgentPlannerToolActionStep{{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: strings.Repeat("CPU high ", 100)}})
	require.Equal(t, "input_limit", result.Status)
	require.Empty(t, result.Decision)
	require.Zero(t, fake.callCount(), "truncated evidence must never reach inference")
}

func TestClaimShadowDoesNotContinueTruncatedOutput(t *testing.T) {
	fake := &fakeLLMModel{turns: []fakeTurn{{content: validClaimAudit, stopReason: "length"}}}
	withFakeLLMModel(t, fake)
	base := llmOverrideContext()
	ctx := security.NewRequestContext(context.WithValue(base.GetContext(), contextKeyPreserveInput, true),
		base.GetSecurityContext(), base.GetLogger(), base.GetTracer(), base.GetMeter())
	result := evaluateClaimShadow(ctx, NBAgentRequest{}, "why", "answer", "",
		[]NBAgentPlannerToolActionStep{{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: "CPU high"}})
	require.Equal(t, "incomplete_response", result.Status)
	require.Empty(t, result.Decision)
	require.Equal(t, 1, fake.callCount(), "audit output must not enter continuation or repair loops")
}

const validClaimAudit = `{"claims":[{"claim":"CPU caused health failure","support":"inferred","asserted_as_fact":true,"material":true,"evidence_refs":[1]}],"missing_required":[]}`

func TestClaimAuditValidationAndVerdict(t *testing.T) {
	cases := []struct{ name, input, decision string }{
		{"causal leap", validClaimAudit, "refine"},
		{"honest hypothesis", strings.Replace(validClaimAudit, `"asserted_as_fact":true`, `"asserted_as_fact":false`, 1), "accept"},
		{"direct evidence", strings.Replace(validClaimAudit, `"inferred"`, `"observed"`, 1), "accept"},
		{"contradicted hypothesis", strings.Replace(strings.Replace(validClaimAudit, `"inferred"`, `"contradicted"`, 1), `"asserted_as_fact":true`, `"asserted_as_fact":false`, 1), "refine"},
		{"immaterial", strings.Replace(validClaimAudit, `"material":true`, `"material":false`, 1), "accept"},
		{"missing fact", strings.Replace(strings.Replace(validClaimAudit, `"inferred"`, `"observed"`, 1), `"missing_required":[]`, `"missing_required":["Requested region not reported"]`, 1), "refine"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			audit, err := parseClaimAudit(tc.input, 1)
			require.NoError(t, err)
			decision, feedback := audit.decision()
			require.Equal(t, tc.decision, decision)
			if decision == "refine" {
				require.NotEmpty(t, feedback)
			} else {
				require.Empty(t, feedback)
			}
		})
	}
}

func TestClaimAuditInvalidOutputHasNoVerdict(t *testing.T) {
	cases := map[string]string{
		"invalid log escape":   strings.Replace(validClaimAudit, "CPU caused health failure", `Redis it\'s full`, 1),
		"missing boolean":      strings.Replace(validClaimAudit, `"material":true,`, "", 1),
		"null boolean":         strings.Replace(validClaimAudit, `"material":true`, `"material":null`, 1),
		"duplicate boolean":    strings.Replace(validClaimAudit, `"material":true`, `"material":true,"material":false`, 1),
		"unknown field":        strings.Replace(validClaimAudit, `"claims":`, `"verdict":"accept","claims":`, 1),
		"invalid support":      strings.Replace(validClaimAudit, `"inferred"`, `"probably"`, 1),
		"missing requirements": strings.Replace(validClaimAudit, `,"missing_required":[]`, "", 1),
		"null requirements":    strings.Replace(validClaimAudit, `"missing_required":[]`, `"missing_required":null`, 1),
		"case variant field":   strings.Replace(validClaimAudit, `"material":true`, `"material":true,"Material":false`, 1),
		"null ref":             strings.Replace(validClaimAudit, `[1]`, `[null]`, 1),
		"unknown ref":          strings.Replace(validClaimAudit, `[1]`, `[2]`, 1),
		"duplicate ref":        strings.Replace(validClaimAudit, `[1]`, `[1,1]`, 1),
		"absent ref":           strings.Replace(validClaimAudit, `"evidence_refs":[1]`, `"evidence_refs":null`, 1),
		"observed without ref": strings.Replace(strings.Replace(validClaimAudit, `"inferred"`, `"observed"`, 1), `[1]`, `[]`, 1),
		"trailing JSON":        validClaimAudit + "{}",
		"markdown":             "```json\n" + validClaimAudit + "\n```",
		"empty claims":         `{"claims":[],"missing_required":[]}`,
		"nested bomb":          strings.Repeat("[", 20) + strings.Repeat("]", 20),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseClaimAudit(input, 1)
			require.Error(t, err)
		})
	}
}

func TestClaimCritiquePacketPreservesEvidenceWithoutThoughts(t *testing.T) {
	steps := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: "shell_execute", Log: "unsupported reasoning", ToolInput: "read logs"}, Observation: "permission denied"},
		{Action: NBAgentPlannerToolAction{Tool: "update_notebook"}, Observation: "notes"},
		{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: "CPU 100%, health zero"},
	}
	packet, err := claimCritiquePacket("why", "answer", "hypothesis only", steps)
	require.NoError(t, err)
	require.True(t, json.Valid(packet))
	require.NotContains(t, string(packet), "unsupported reasoning")
	require.Contains(t, string(packet), "permission denied")
	require.Contains(t, string(packet), `"id":2`)
	require.Contains(t, string(packet), "hypothesis only")
	_, err = claimCritiquePacket(strings.Repeat("x", claimCritiqueInputLimit), "answer", "", nil)
	require.Error(t, err, "oversize evidence must be skipped, never silently truncated")
}

func TestClaimShadowDisabledAndSubagentDoNotCallLLM(t *testing.T) {
	previous := config.Config.ClaimCritiqueShadowEnabled
	t.Cleanup(func() { config.Config.ClaimCritiqueShadowEnabled = previous })
	fake := &fakeLLMModel{response: validClaimAudit}
	withFakeLLMModel(t, fake)
	ctx := llmOverrideContext()
	config.Config.ClaimCritiqueShadowEnabled = false
	runClaimCritiqueShadow(ctx, NBAgentRequest{}, "react4", "test_agent", "accept", "legacy", "why", "answer", "", nil)
	config.Config.ClaimCritiqueShadowEnabled = true
	runClaimCritiqueShadow(ctx, NBAgentRequest{AgentId: "child", ParentAgentId: "parent"}, "react3", "test_agent", "accept", "legacy", "why", "answer", "", nil)
	require.Zero(t, fake.callCount())
}

func TestClaimShadowEvaluator(t *testing.T) {
	cases := []struct{ name, output, stop, status, decision string }{
		{"refine", validClaimAudit, "stop", "ok", "refine"},
		{"vertex", validClaimAudit, "FinishReasonStop", "ok", "refine"},
		{"malformed", `{"claims":[`, "stop", "invalid_output", ""},
		{"missing", "", "stop", "empty_response", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeLLMModel{turns: []fakeTurn{{content: tc.output, stopReason: tc.stop}}}
			withFakeLLMModel(t, fake)
			result := evaluateClaimShadow(llmOverrideContext(), NBAgentRequest{}, "why", "answer", "",
				[]NBAgentPlannerToolActionStep{{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: "CPU high"}})
			// Empty generations may be rejected by the shared generation layer first.
			if tc.name == "missing" {
				require.NotEqual(t, "ok", result.Status)
			} else {
				require.Equal(t, tc.status, result.Status)
			}
			require.Equal(t, tc.decision, result.Decision)
		})
	}
}

func TestClaimCritiquePromptRegistered(t *testing.T) {
	prompt, err := nbprompts.GetPromptStrict(llmOverrideContext().GetContext(), nbprompts.PromptReactClaimCritiquer, "")
	require.NoError(t, err)
	require.Contains(t, prompt, "evidence_refs")
	require.Contains(t, prompt, "Clearly labeled hypotheses")
}

type claimTestDAO struct{ IConversationDao }

func (d *claimTestDAO) SaveClaimCritiqueAudit(_ context.Context, _ *ClaimCritiqueAuditRecord) error {
	return nil
}

func (d *claimTestDAO) SaveCritique(_ *ConversationCritique) error { return nil }

func TestClaimShadowBothPlannersPreserveLegacyResult(t *testing.T) {
	previous := config.Config.ClaimCritiqueShadowEnabled
	t.Cleanup(func() { config.Config.ClaimCritiqueShadowEnabled = previous })
	config.Config.ClaimCritiqueShadowEnabled = true
	previousDAO := GetConversationDao()
	conversationDaoMutex.Lock()
	SetConversationDao(&claimTestDAO{IConversationDao: previousDAO})
	conversationDaoMutex.Unlock()
	t.Cleanup(func() {
		conversationDaoMutex.Lock()
		defer conversationDaoMutex.Unlock()
		SetConversationDao(previousDAO)
	})
	for _, planner := range []string{"react3", "react4"} {
		for _, output := range []string{validClaimAudit, `{"claims":[`} {
			t.Run(planner+output, func(t *testing.T) {
				fake := &fakeLLMModel{turns: []fakeTurn{
					{content: "<decision>accept</decision><feedback>legacy feedback</feedback>"},
					{content: output},
				}}
				withFakeLLMModel(t, fake)
				ctx := llmOverrideContext()
				request := NBAgentRequest{Query: "why is CPU high"}
				steps := []NBAgentPlannerToolActionStep{{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: "CPU high"}}
				var decision, feedback string
				if planner == "react4" {
					p := &NBReActPlanner4{ctx: ctx, request: request, nbAgent: notebookOptOutAgent{}}
					decision, feedback = p.runCritique(request.Query, "metrics", "answer", steps)
				} else {
					p := &NBReActPlanner3{ctx: ctx, request: request, nbAgent: notebookOptOutAgent{}}
					decision, feedback = p.runCritique(request.Query, "metrics", "answer", steps, ctx.GetLogger())
				}
				require.Equal(t, "accept", decision)
				require.Equal(t, "legacy feedback", feedback)
				require.Equal(t, 2, fake.callCount())
			})
		}
	}
}
