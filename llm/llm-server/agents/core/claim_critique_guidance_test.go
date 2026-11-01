package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"

	"nudgebee/llm/config"
	nbprompts "nudgebee/llm/prompts"
)

const validClaimGuidance = `{"guidance":[{"index":0,"why_weak":"Observation 1 shows CPU high but no health-check failure.","alternative":"The health check timed out on a dependency.","discriminating_check":"logs: health endpoint errors for the failure window"}]}`

const legacyClaimFeedback = "Correct these material evidence gaps using existing observations, or one permitted discriminating read. " +
	"Clearly label hypotheses; do not repeat denied evidence paths.\n- inferred: CPU caused health failure"

func claimGuidanceDetail(t *testing.T) claimCaptureDetail {
	t.Helper()
	packet, err := claimCritiquePacket("why", "answer", "", []NBAgentPlannerToolActionStep{{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: "CPU high"}})
	require.NoError(t, err)
	return claimCaptureDetail{InputPacket: packet, ObservationCount: 1}
}

func TestClaimGuidanceFeedback(t *testing.T) {
	audit, err := parseClaimAudit(validClaimAudit, 1)
	require.NoError(t, err)
	for _, tc := range []struct{ name, output, status string }{
		{"guided", validClaimGuidance, "ok"},
		{"malformed", `{"guidance":[`, "invalid_output"},
		{"index out of range", strings.Replace(validClaimGuidance, `"index":0`, `"index":5`, 1), "invalid_output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeLLMModel{turns: []fakeTurn{{content: tc.output}}}
			withFakeLLMModel(t, fake)
			feedback, capture := claimGuidanceFeedback(llmOverrideContext(), NBAgentRequest{}, claimGuidanceDetail(t), audit, false, nil)
			require.Equal(t, tc.status, capture.Status)
			require.Equal(t, 1, fake.callCount())
			if tc.status != "ok" {
				require.Empty(t, feedback, "a failed step 2 must leave the legacy feedback in place")
				return
			}
			require.True(t, strings.HasPrefix(feedback, claimGuidanceHeader))
			require.Contains(t, feedback, "- inferred: CPU caused health failure")
			require.Contains(t, feedback, "why: Observation 1 shows CPU high")
			require.Contains(t, feedback, "alternative: The health check timed out")
			require.Contains(t, feedback, "check: logs: health endpoint errors")
			// The flagged claims are sent to the guidance call with the original evidence.
			sent := fake.lastSent()
			require.Len(t, sent, 2)
			require.Contains(t, partsText(sent[1].Parts), `"flagged_claims"`)
			require.Contains(t, partsText(sent[1].Parts), `"CPU high"`)
		})
	}
}

// The last refinement's answer is not reviewed again, so its feedback says so.
func TestClaimGuidanceFinalRound(t *testing.T) {
	audit, err := parseClaimAudit(validClaimAudit, 1)
	require.NoError(t, err)
	for _, final := range []bool{false, true} {
		fake := &fakeLLMModel{turns: []fakeTurn{{content: validClaimGuidance}}}
		withFakeLLMModel(t, fake)
		feedback, capture := claimGuidanceFeedback(llmOverrideContext(), NBAgentRequest{}, claimGuidanceDetail(t), audit, final, nil)
		require.Equal(t, "ok", capture.Status)
		require.Equal(t, final, capture.FinalRound)
		require.Contains(t, feedback, "A check that fails or is denied is not evidence for the alternative")
		require.Equal(t, final, strings.Contains(feedback, claimGuidanceFinalRound))
	}
}

func TestClaimGuidanceSkipsRejectionWithoutFlaggedClaims(t *testing.T) {
	// A rejection driven only by missing_required has no claim to explain.
	audit, err := parseClaimAudit(`{"claims":[{"claim":"CPU is high","support":"observed","asserted_as_fact":true,"material":true,"evidence_refs":[1]}],"missing_required":["check the health endpoint logs"]}`, 1)
	require.NoError(t, err)
	fake := &fakeLLMModel{response: validClaimGuidance}
	withFakeLLMModel(t, fake)
	feedback, capture := claimGuidanceFeedback(llmOverrideContext(), NBAgentRequest{}, claimGuidanceDetail(t), audit, false, nil)
	require.Empty(t, feedback)
	require.Equal(t, "no_flagged_claims", capture.Status)
	require.Zero(t, fake.callCount())
}

func TestClaimGuidanceRejectsEmptyPacket(t *testing.T) {
	audit, err := parseClaimAudit(validClaimAudit, 1)
	require.NoError(t, err)
	fake := &fakeLLMModel{response: validClaimGuidance}
	withFakeLLMModel(t, fake)
	feedback, capture := claimGuidanceFeedback(llmOverrideContext(), NBAgentRequest{}, claimCaptureDetail{InputPacket: []byte("null")}, audit, false, nil)
	require.Empty(t, feedback)
	require.Equal(t, "prepare_error", capture.Status)
	require.Zero(t, fake.callCount())
}

func TestClaimGuidanceTruncatesLongFields(t *testing.T) {
	audit, err := parseClaimAudit(validClaimAudit, 1)
	require.NoError(t, err)
	long := strings.Repeat("x", 3*claimGuidanceLimit)
	fake := &fakeLLMModel{turns: []fakeTurn{{content: strings.Replace(validClaimGuidance, "Observation 1 shows CPU high but no health-check failure.", long, 1)}}}
	withFakeLLMModel(t, fake)
	feedback, capture := claimGuidanceFeedback(llmOverrideContext(), NBAgentRequest{}, claimGuidanceDetail(t), audit, false, nil)
	require.Equal(t, "ok", capture.Status)
	require.Less(t, len(feedback), 2*claimGuidanceLimit+1000)
}

func TestClaimGuidancePromptRegistered(t *testing.T) {
	prompt, err := nbprompts.GetPromptStrict(llmOverrideContext().GetContext(), nbprompts.PromptReactClaimCritiqueGuidance, "")
	require.NoError(t, err)
	for _, want := range []string{"flagged_claims", "why_weak", "alternative", "discriminating_check", "Do not re-judge"} {
		require.Contains(t, prompt, want)
	}
}

// End to end through both planners: step 2 runs only with the flag on and a
// rejection, replaces only the feedback text, and falls back on failure.
func TestClaimGuidanceBothPlanners(t *testing.T) {
	previousFeedback := config.Config.ClaimCritiqueFeedbackEnabled
	previousGuidance := config.Config.ClaimCritiqueActionableFeedbackEnabled
	t.Cleanup(func() {
		config.Config.ClaimCritiqueFeedbackEnabled = previousFeedback
		config.Config.ClaimCritiqueActionableFeedbackEnabled = previousGuidance
	})
	config.Config.ClaimCritiqueFeedbackEnabled = true
	previousDAO := GetConversationDao()
	conversationDaoMutex.Lock()
	SetConversationDao(&claimTestDAO{IConversationDao: previousDAO})
	conversationDaoMutex.Unlock()
	t.Cleanup(func() {
		conversationDaoMutex.Lock()
		defer conversationDaoMutex.Unlock()
		SetConversationDao(previousDAO)
	})
	accepted := strings.Replace(validClaimAudit, `"inferred"`, `"observed"`, 1)
	cases := []struct {
		name            string
		flag            bool
		audit, guidance string
		decision        string
		calls           int
		guided          bool
		usedRefinements int
	}{
		{"flag off", false, validClaimAudit, "", "refine", 2, false, 0},
		{"guided", true, validClaimAudit, validClaimGuidance, "refine", 3, true, 0},
		{"guided final round", true, validClaimAudit, validClaimGuidance, "refine", 3, true, 1},
		{"guidance fails", true, validClaimAudit, `not json`, "refine", 3, false, 0},
		{"accepted", true, accepted, "", "accept", 2, false, 0},
	}
	for _, planner := range []string{"react3", "react4"} {
		for _, tc := range cases {
			t.Run(planner+" "+tc.name, func(t *testing.T) {
				config.Config.ClaimCritiqueActionableFeedbackEnabled = tc.flag
				turns := []fakeTurn{{content: "<decision>accept</decision><feedback>legacy feedback</feedback>"}, {content: tc.audit}}
				if tc.guidance != "" {
					turns = append(turns, fakeTurn{content: tc.guidance})
				}
				fake := &fakeLLMModel{turns: turns}
				withFakeLLMModel(t, fake)
				ctx := llmOverrideContext()
				request := NBAgentRequest{Query: "why is CPU high"}
				steps := []NBAgentPlannerToolActionStep{{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: "CPU high"}}
				var decision, feedback string
				if planner == "react4" {
					p := &NBReActPlanner4{ctx: ctx, request: request, nbAgent: notebookOptOutAgent{},
						maxRefinementAttempts: 2, refinementData: make([]refinementRecord, tc.usedRefinements)}
					decision, feedback = p.runCritique(request.Query, "metrics", "answer", steps, nil)
				} else {
					p := &NBReActPlanner3{ctx: ctx, request: request, nbAgent: notebookOptOutAgent{},
						maxRefinementAttempts: 2, refinementAttempts: tc.usedRefinements}
					decision, feedback = p.runCritique(request.Query, "metrics", "answer", steps, ctx.GetLogger())
				}
				require.Equal(t, tc.decision, decision)
				require.Equal(t, tc.calls, fake.callCount())
				switch {
				case tc.decision == "accept":
					require.Equal(t, "legacy feedback", feedback)
				case tc.guided:
					require.True(t, strings.HasPrefix(feedback, claimGuidanceHeader))
					require.Contains(t, feedback, "check: logs: health endpoint errors")
					// Only the refinement that spends the last attempt says the next answer is final.
					require.Equal(t, tc.usedRefinements == 1, strings.Contains(feedback, claimGuidanceFinalRound))
				default:
					require.Equal(t, legacyClaimFeedback, feedback)
				}
			})
		}
	}
}

func partsText(parts []llms.ContentPart) string {
	var b strings.Builder
	for _, p := range parts {
		if text, ok := p.(llms.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}
