package core

import (
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/prompts"
	"nudgebee/llm/config"
	"testing"
)

func TestClaimFeedbackDecision(t *testing.T) {
	for _, tc := range []struct {
		name, baseline, status, audit, want string
		enabled                             bool
	}{
		{"off", "accept", "ok", "refine", "accept", false},
		{"reject", "accept", "ok", "refine", "refine", true},
		{"preserve rejection", "refine", "ok", "accept", "refine", true},
		{"both reject", "refine", "ok", "refine", "refine", true},
		{"padded rejection", " REFINE ", "ok", "refine", "refine", true},
		{"invalid", "accept", "invalid_output", "refine", "accept", true},
		{"timeout", "accept", "generation_error", "refine", "accept", true},
		{"unknown baseline", "", "ok", "refine", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, f := claimFeedbackDecision(tc.enabled, tc.baseline, "original", claimShadowResult{Status: tc.status, Decision: tc.audit, Feedback: "claim gap"})
			require.Equal(t, tc.want, d)
			if tc.enabled && tc.status == "ok" && tc.audit == "refine" && tc.baseline != "" {
				require.Contains(t, f, "claim gap")
			} else {
				require.Equal(t, "original", f)
			}
			if tc.baseline == "refine" {
				require.Contains(t, f, "original")
			}
		})
	}
}

func TestClaimFeedbackBothPlanners(t *testing.T) {
	pinClaimAuditOnly(t)
	previousFeedback := config.Config.ClaimCritiqueFeedbackEnabled
	t.Cleanup(func() {
		config.Config.ClaimCritiqueFeedbackEnabled = previousFeedback
	})
	config.Config.ClaimCritiqueFeedbackEnabled = true
	for _, planner := range []string{"react3", "react4"} {
		for _, failWrite := range []int{0, 1, 2} {
			t.Run(planner+string(rune('0'+failWrite)), func(t *testing.T) {
				dao := &claimCaptureDAO{failWrite: failWrite}
				installClaimCaptureDAO(t, dao)
				dao.IConversationDao = &claimTestDAO{IConversationDao: dao.IConversationDao}
				fake := &fakeLLMModel{turns: []fakeTurn{{content: "<decision>accept</decision><feedback>legacy</feedback>"}, {content: validClaimAudit}}}
				withFakeLLMModel(t, fake)
				ctx := llmOverrideContext()
				req := NBAgentRequest{Query: "why is CPU high"}
				steps := []NBAgentPlannerToolActionStep{{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: "CPU high"}}
				var d, f string
				if planner == "react4" {
					p := &NBReActPlanner4{ctx: ctx, request: req, nbAgent: notebookOptOutAgent{}}
					d, f = p.runCritique(req.Query, "metrics", "answer", steps, nil)
				} else {
					p := &NBReActPlanner3{ctx: ctx, request: req, nbAgent: notebookOptOutAgent{}}
					d, f = p.runCritique(req.Query, "metrics", "answer", steps, ctx.GetLogger())
				}
				if failWrite == 0 {
					require.Equal(t, "refine", d)
					require.Contains(t, f, "material evidence gaps")
					require.True(t, dao.rows[1].Detail.FeedbackSelected)
					require.Equal(t, d, dao.rows[1].Detail.EffectiveDecision)
					require.Equal(t, "accept", dao.rows[1].BaselineDecision)
				} else {
					require.Equal(t, "accept", d)
					require.Equal(t, "legacy", f)
				}
				if failWrite == 1 {
					require.Equal(t, 1, fake.callCount())
				} else {
					require.Equal(t, 2, fake.callCount())
				}
			})
		}
	}
}

func TestClaimFeedbackReAct4UsesExistingBudget(t *testing.T) {
	pinClaimAuditOnly(t)
	previous := config.Config.ClaimCritiqueFeedbackEnabled
	config.Config.ClaimCritiqueFeedbackEnabled = true
	t.Cleanup(func() { config.Config.ClaimCritiqueFeedbackEnabled = previous })
	dao := &claimCaptureDAO{}
	installClaimCaptureDAO(t, dao)
	fake := &fakeLLMModel{turns: []fakeTurn{
		{content: "candidate one"}, {content: "<decision>accept</decision><feedback></feedback>"}, {content: validClaimAudit},
		{content: "candidate two"}, {content: "<decision>accept</decision><feedback></feedback>"}, {content: validClaimAudit},
		{content: "candidate three"},
	}}
	withFakeLLMModel(t, fake)
	ctx := llmOverrideContext()
	p := &NBReActPlanner4{ctx: ctx, request: NBAgentRequest{Query: "why is CPU high"}, nbAgent: notebookOptOutAgent{}, maxRefinementAttempts: 2, enableCritique: true}
	actions, finish, err := p.Plan(ctx.GetContext(), []NBAgentPlannerToolActionStep{{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: "CPU high"}}, "why is CPU high")
	require.NoError(t, err)
	require.Empty(t, actions)
	require.NotNil(t, finish)
	require.Equal(t, "candidate three", finish.Data)
	require.Equal(t, 7, fake.callCount(), "three candidates, two incumbent critiques, two claim audits")
	require.Len(t, dao.rows, 5)
	require.Equal(t, "budget_exhausted", dao.rows[4].Status)
	require.Empty(t, dao.rows[4].Decision, "budget exhaustion is not an audit acceptance")
	require.Equal(t, "candidate three", dao.rows[4].Answer)
	require.Empty(t, p.refinementData)
}

func TestClaimFeedbackReAct3ResumeKeepsBudget(t *testing.T) {
	previous := config.Config.ClaimCritiqueFeedbackEnabled
	config.Config.ClaimCritiqueFeedbackEnabled = true
	t.Cleanup(func() { config.Config.ClaimCritiqueFeedbackEnabled = previous })
	dao := &claimCaptureDAO{}
	installClaimCaptureDAO(t, dao)
	fake := &fakeLLMModel{turns: []fakeTurn{{content: "<final_answer>bounded final answer</final_answer>"}}}
	withFakeLLMModel(t, fake)
	ctx := llmOverrideContext()
	original := &NBReActPlanner3{refinementAttempts: 2, refinementData: []refinementRecord{{Answer: "first", Feedback: "first gap"}, {Answer: "second", Feedback: "second gap"}}}
	data, err := original.Marshal()
	require.NoError(t, err)
	p := &NBReActPlanner3{ctx: ctx, request: NBAgentRequest{Query: "why is CPU high"}, nbAgent: notebookOptOutAgent{}, maxRefinementAttempts: 2, prompt: prompts.NewPromptTemplate("{{.input}}", []string{"input"})}
	require.NoError(t, p.Unmarshal(data))
	actions, finish, err := p.Plan(ctx.GetContext(), []NBAgentPlannerToolActionStep{{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: "CPU high"}}, "why is CPU high")
	require.NoError(t, err)
	require.Empty(t, actions)
	require.NotNil(t, finish)
	require.Equal(t, "bounded final answer", finish.Data)
	require.Equal(t, 1, fake.callCount(), "resumed budget must not call either critic again")
	require.Equal(t, 2, p.refinementAttempts)
	require.Len(t, p.refinementData, 2)
	require.Len(t, dao.rows, 1)
	require.Equal(t, "budget_exhausted", dao.rows[0].Status)
}

func TestClaimFeedbackSkipsChildAgent(t *testing.T) {
	previous := config.Config.ClaimCritiqueFeedbackEnabled
	config.Config.ClaimCritiqueFeedbackEnabled = true
	t.Cleanup(func() { config.Config.ClaimCritiqueFeedbackEnabled = previous })
	dao := &claimCaptureDAO{}
	installClaimCaptureDAO(t, dao)
	fake := &fakeLLMModel{}
	withFakeLLMModel(t, fake)
	d, f := runClaimCritiqueShadow(llmOverrideContext(), NBAgentRequest{AgentId: "child", ParentAgentId: "parent"}, "react4", "test", "accept", "legacy", "why", "answer", "", nil, false, nil)
	require.Equal(t, "accept", d)
	require.Equal(t, "legacy", f)
	require.Zero(t, fake.callCount())
	require.Empty(t, dao.rows)
}

func TestClaimFeedbackReAct3ToolContinuation(t *testing.T) {
	pinClaimAuditOnly(t)
	previous := config.Config.ClaimCritiqueFeedbackEnabled
	config.Config.ClaimCritiqueFeedbackEnabled = true
	t.Cleanup(func() { config.Config.ClaimCritiqueFeedbackEnabled = previous })
	dao := &claimCaptureDAO{}
	installClaimCaptureDAO(t, dao)
	dao.IConversationDao = &claimTestDAO{IConversationDao: dao.IConversationDao}
	fake := &fakeLLMModel{turns: []fakeTurn{
		{content: "<final_answer>candidate one</final_answer>"},
		{content: "<decision>accept</decision><feedback></feedback>"}, {content: validClaimAudit},
		{content: "<thought_action><thought>check evidence</thought><action><tool_name>kubectl_execute</tool_name><tool_input>kubectl get pods</tool_input></action></thought_action>"},
		{content: "<final_answer>candidate two</final_answer>"},
		{content: "<decision>accept</decision><feedback></feedback>"}, {content: validClaimAudit},
		{content: "<final_answer>candidate three</final_answer>"},
	}}
	withFakeLLMModel(t, fake)
	ctx := llmOverrideContext()
	p := &NBReActPlanner3{ctx: ctx, request: NBAgentRequest{Query: "why is CPU high"}, nbAgent: notebookOptOutAgent{}, maxRefinementAttempts: 2, prompt: prompts.NewPromptTemplate("{{.input}}", []string{"input"})}
	steps := []NBAgentPlannerToolActionStep{{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: "CPU high"}}
	actions, finish, err := p.Plan(ctx.GetContext(), steps, "why is CPU high")
	require.NoError(t, err)
	require.Nil(t, finish)
	require.Len(t, actions, 1)
	require.Equal(t, 1, p.refinementAttempts)
	steps = append(steps, NBAgentPlannerToolActionStep{Action: actions[0], Observation: "pods running"})
	actions, finish, err = p.Plan(ctx.GetContext(), steps, "why is CPU high")
	require.NoError(t, err)
	require.Empty(t, actions)
	require.NotNil(t, finish)
	require.Equal(t, "candidate three", finish.Data)
	require.Equal(t, 8, fake.callCount())
	require.Equal(t, 2, p.refinementAttempts)
	require.Len(t, dao.rows, 5)
	require.Equal(t, "budget_exhausted", dao.rows[4].Status)
}

func TestClaimFeedbackDisabledResumeIgnoresCarry(t *testing.T) {
	previous := config.Config.ClaimCritiqueFeedbackEnabled
	config.Config.ClaimCritiqueFeedbackEnabled = false
	t.Cleanup(func() { config.Config.ClaimCritiqueFeedbackEnabled = previous })
	original := &NBReActPlanner3{refinementAttempts: 1, refinementData: []refinementRecord{{Answer: "old", Feedback: "gap"}}}
	data, err := original.Marshal()
	require.NoError(t, err)
	require.Contains(t, string(data), "gap", "serialization must retain history regardless of flag")
	restored := &NBReActPlanner3{}
	require.NoError(t, restored.Unmarshal(data))
	require.Empty(t, restored.refinementData, "disabled resume must not suppress direct summarization before the legacy reset")
}
