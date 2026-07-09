package tools

import (
	"testing"
	"unicode/utf8"

	core "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

func TestThinkTool_EchoesReasoning(t *testing.T) {
	tool := &thinkTool{}
	resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{
		Arguments: map[string]any{
			"reasoning": "Logs show connection refused but metrics show low CPU. This suggests the issue is not resource exhaustion but rather a network partition or misconfigured service endpoint.",
		},
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Contains(t, resp.Data, "network partition")
}

func TestThinkTool_FallbackToCommand(t *testing.T) {
	tool := &thinkTool{}
	resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{
		Command: "Need to reconsider the approach, either retry or pivot to a different tool",
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Contains(t, resp.Data, "reconsider")
}

func TestThinkTool_EmptyArgsFallsBackToCommand(t *testing.T) {
	tool := &thinkTool{}
	resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{
		Command:   "fallback reasoning",
		Arguments: map[string]any{},
	})
	assert.NoError(t, err)
	assert.Equal(t, "fallback reasoning", resp.Data)
}

func TestThinkTool_Metadata(t *testing.T) {
	tool := &thinkTool{}
	assert.Equal(t, "think", tool.Name())
	assert.Equal(t, core.NBToolTypeTool, tool.GetType())
	assert.Contains(t, tool.Description(), "conflicting evidence")

	schema := tool.InputSchema()
	assert.Equal(t, core.ToolSchemaTypeObject, schema.Type)
	assert.Contains(t, schema.Properties, "reasoning")
	assert.Equal(t, []string{"reasoning"}, schema.Required,
		"Required stays honest so the LLM sees the correct spec; per-tool "+
			"actionable messages flow through OnMissingRequiredFields instead")
}

// TestTruncateForError_UTF8Safe pins the rune-boundary walk-back so a
// cut in the middle of a multi-byte rune never glues the ellipsis onto
// a half-rune (would render as a replacement char downstream). Uses the
// nudgebee brand + a mixed-script sample so the test is robust to future
// edits.
func TestTruncateForError_UTF8Safe(t *testing.T) {
	// Each of these characters is 3 bytes in UTF-8 — cutting at a
	// non-boundary byte would produce invalid output.
	s := "α β γ δ ε ζ η θ ι κ λ μ ν ξ ο π ρ σ τ υ φ χ ψ ω"
	for maxBytes := 1; maxBytes < len(s); maxBytes++ {
		got := truncateForError(s, maxBytes)
		// The truncated prefix (before ellipsis) must be a complete UTF-8
		// sequence — no half-runes — else the message is corrupt.
		prefix := got
		if len(got) >= len("…") && got[len(got)-len("…"):] == "…" {
			prefix = got[:len(got)-len("…")]
		}
		if !utf8.ValidString(prefix) {
			t.Fatalf("maxBytes=%d produced invalid UTF-8 prefix: %q (full: %q)", maxBytes, prefix, got)
		}
	}
	// Under-cap: returns unchanged.
	assert.Equal(t, s, truncateForError(s, len(s)+10))
	assert.Equal(t, s, truncateForError(s, len(s)))
	// ASCII short input: no ellipsis.
	assert.Equal(t, "hello", truncateForError("hello", 100))
}

// TestThinkTool_OnMissingRequiredFields pins the escape-hatch that
// intercepts the planner-level "missing required fields — reasoning" line
// so the model sees actionable guidance instead of the generic message.
// Schema-Required stays honest; this handler runs when it fails.
func TestThinkTool_OnMissingRequiredFields(t *testing.T) {
	tool := &thinkTool{}

	t.Run("empty command → empty-input reject with final_answer pointer", func(t *testing.T) {
		resp := tool.OnMissingRequiredFields(core.NBToolCallRequest{}, []string{"reasoning"})
		if assert.NotNil(t, resp) {
			assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
			assert.Contains(t, resp.Data, "requires a non-empty 'reasoning'")
			assert.Contains(t, resp.Data, "<final_answer>")
		}
	})

	t.Run("whitespace-only command → treated as empty", func(t *testing.T) {
		resp := tool.OnMissingRequiredFields(core.NBToolCallRequest{Command: "   \n\t  "}, []string{"reasoning"})
		if assert.NotNil(t, resp) {
			assert.Contains(t, resp.Data, "requires a non-empty 'reasoning'")
		}
	})

	t.Run("narration text on Command → narration reject with wrong-field nudge", func(t *testing.T) {
		// Sample verbatim from production 2026-07-07 — narration landed on
		// Command instead of Arguments["reasoning"], so the schema-Required
		// check rejected it before Call() could see it. The responder must
		// catch this: narration guard fires AND names the wrong-field
		// misplacement.
		resp := tool.OnMissingRequiredFields(core.NBToolCallRequest{
			Command: "I have all the evidence needed. I will now generate the final answer explaining why scaling down ingress-nginx is not safe yet.",
		}, []string{"reasoning"})
		if assert.NotNil(t, resp) {
			assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
			assert.Contains(t, resp.Data, "narration of an upcoming action")
			assert.Contains(t, resp.Data, "<final_answer>")
			assert.Contains(t, resp.Data, "'reasoning' field",
				"must nudge model to move reasoning to the correct field")
		}
	})

	t.Run("real reasoning on wrong field → nudge to correct field, no narration reject", func(t *testing.T) {
		resp := tool.OnMissingRequiredFields(core.NBToolCallRequest{
			Command: "Retry with a longer timeout vs. pivot to fetch_logs — pivoting because the pod may be unreachable.",
		}, []string{"reasoning"})
		if assert.NotNil(t, resp) {
			assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
			assert.Contains(t, resp.Data, "'reasoning' field")
			assert.NotContains(t, resp.Data, "narration of an upcoming action",
				"real reasoning shape shouldn't get the narration rejection")
		}
	})
}

// TestThinkTool_EmptyInputRejectedInCall verifies the belt-and-suspenders
// empty-check inside Call() — the planner-level validator normally catches
// this via schema-Required, but Call() is called directly by tests and
// custom invokers, so the defensive check keeps its contract.
func TestThinkTool_EmptyInputRejectedInCall(t *testing.T) {
	tool := &thinkTool{}
	cases := []struct {
		name string
		req  core.NBToolCallRequest
	}{
		{"nil arguments and empty command", core.NBToolCallRequest{}},
		{"whitespace-only command", core.NBToolCallRequest{Command: "   \n\t  "}},
		{"empty reasoning in arguments", core.NBToolCallRequest{Arguments: map[string]any{"reasoning": ""}}},
		{"whitespace-only reasoning in arguments", core.NBToolCallRequest{Arguments: map[string]any{"reasoning": "  \n"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := tool.Call(core.NbToolContext{}, c.req)
			assert.NoError(t, err)
			assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
			assert.Contains(t, resp.Data, "requires a non-empty 'reasoning'")
			assert.Contains(t, resp.Data, "<final_answer>")
		})
	}
}

// TestThinkTool_DescriptionForbidsNarration pins the narration-misuse
// guardrails. The description must name the forbidden phrasings explicitly
// so the LLM has something concrete to match against, must call out the
// notebook/hypothesis-tree overlap, AND must announce the programmatic
// guard so the rejection is treated as spec, not a system fault.
func TestThinkTool_DescriptionForbidsNarration(t *testing.T) {
	tool := &thinkTool{}
	desc := tool.Description()

	// Pin ALL forbidden phrasings listed in the description so a future
	// rewrite cannot silently drop one (per Gemini PR #32697 review).
	for _, banned := range []string{
		"ready to provide (the) final answer",
		"ready to give (the) answer",
		"will provide (the) final answer",
		"investigation is complete",
		"notebook updated",
		"i have enough information to answer",
		"consolidated findings: ... ready to ...",
	} {
		assert.Contains(t, desc, banned,
			"description must explicitly name the %q narration pattern so the LLM can match against it", banned)
	}

	// Hypothesis-mode redundancy must be called out so the agent stops
	// using `think` as a notebook-update proxy when the hypothesis tree
	// already provides structured reasoning.
	assert.Contains(t, desc, "hypothesis-mode",
		"description must call out think/notebook overlap (think is redundant on top of the hypothesis tree)")
	assert.Contains(t, desc, "[SUPPORTED]",
		"description must point at the notebook's hypothesis-status markers as the canonical reasoning surface")

	// Positive guidance must still be present.
	assert.Contains(t, desc, "names a SPECIFIC choice",
		"description must define what a valid think call looks like, not just forbid bad ones")

	// The description must announce the programmatic guard so the LLM
	// knows the rejection is intentional, not a bug.
	assert.Contains(t, desc, "rejected at the tool boundary",
		"description must announce the Call()-level reject so the model treats it as the spec, not a system fault")
}

// TestThinkTool_NarrationGuardRejects pins the Call()-level reject path —
// the programmatic backstop added after two rounds of prompt-only fixes
// (May 2026 and #32697 / June 2026) both failed to move the narration
// share. Each case is sampled verbatim from a production think call in
// the 2026-06-25 → 2026-06-27 window (post-#32697-deploy data, where
// narration share remained at ~85-90%).
func TestThinkTool_NarrationGuardRejects(t *testing.T) {
	tool := &thinkTool{}

	productionNarrationSamples := []struct {
		name   string
		sample string
	}{
		{
			name:   "I have enough evidence + I will now generate",
			sample: "I have enough evidence to conclude that cluster-wide DNS resolution timeouts are the root cause of the extreme latency. I will now generate the final answer.",
		},
		{
			name:   "I have sufficient information",
			sample: "The pod payment-85cd547d44-msbj4 is healthy and producing 'info' level logs. No errors or warnings were found in the last hour. I have sufficient information to answer the user.",
		},
		{
			name:   "I have gathered all evidence",
			sample: "I have gathered all evidence: the container 'nginx' does not exist in pod 'payment-85cd547d44-msbj4', and the actual container 'payment' shows no error logs in the last 2 hours, only successful transaction logs.",
		},
		{
			name:   "I am ready to finalize",
			sample: "I have identified the root causes for the 400, 429, and MCP errors. The 400 errors correlate with large request lengths (~520KB), the 429 errors are from the 'googleai' provider, and the MCP errors are due to an offline ngrok endpoint. I am ready to finalize.",
		},
		{
			name:   "I am now ready to provide",
			sample: "I have confirmed that the container 'nginx' does not exist in pod 'payment-85cd547d44-msbj4' in namespace 'demo'. The pod only contains a container named 'payment'. I checked the logs for the 'payment' container and found no error logs for the last 2 hours, only 'info' level logs indicating successful transactions. I am now ready to provide the final answer.",
		},
		{
			name:   "investigation is complete",
			sample: "The investigation is complete. The 'failure' is a simulation hardcoded into the pod's startup command. I have all the evidence needed to explain this to the user.",
		},
		{
			name:   "Ready to provide final answer (short)",
			sample: "Ready to provide final answer.",
		},
		{
			name:   "Notebook updated + ready",
			sample: "Notebook updated. Ready to provide final answer.",
		},
		{
			name:   "Finalizing the investigation",
			sample: "Finalizing the investigation. The workload is a test fixture designed to fail with ImagePullBackOff. I have all the evidence needed.",
		},
		{
			name:   "I will update notebook + final answer narration",
			sample: "I will update the notebook to mark the resource usage check as DONE and the plan as complete, then proceed to the final answer.",
		},
		{
			// Regression guard for Gemini #33080 review: `consolidated
			// findings:` ends with `:` (non-word), so a trailing `\b`
			// on the regex group failed to match when followed by a
			// space. Fix attaches `\b` per-alternative; this case pins
			// it.
			name:   "consolidated findings followed by space (trailing-boundary bug)",
			sample: "Consolidated findings: ready to provide the final answer.",
		},
		{
			// 2026-07-02 recheck leak class: the pre-fix regex only knew
			// `i have (enough|sufficient|gathered|confirmed|identified|
			// verified|completed)` — the "necessary information" stem is
			// a distinct sentence structure the model adopted after the
			// first-order stems started rejecting. Adds
			// `i have all (the )?(necessary|required|needed)
			// (info|information|data|evidence|context)`.
			name:   "I have all the necessary information + I will format (post-33080 leak)",
			sample: "I have all the necessary information to provide the final answer. I will format it according to the RESOURCE DRILL-DOWN requirements.",
		},
		{
			name:   "I have all the necessary information (bare)",
			sample: "I have all the necessary information from the previous steps. The node health check is complete. All nodes are Ready.",
		},
		{
			// 2026-07-02 recheck leak class: `i will format this into
			// the required investigation summary` — `format` was in the
			// "ready to X" list but missing from the "i will X"
			// verb-action list. Adds `format` there plus optional
			// object slots so "into", "it", "this", "the report", etc.
			// all match.
			name:   "I will format this into (post-33080 leak — verb missing from I-will list)",
			sample: "The user has provided a complete incident report. No further tool calls are needed. I will format this into the required investigation summary.",
		},
		{
			// Trailing-space / word-boundary bug regression: the original
			// pattern had trailing spaces INSIDE the optional groups
			// (`(the |a |this |it )?`), so a bare phrase ending with `.` or
			// end-of-string could match up to a trailing space and then fail
			// the `\b` check (no boundary between space and non-word char).
			// New pattern moves the space INSIDE the group as a leading
			// character so the match always ends on a word char.
			// Regression for the Gemini high-priority finding on PR #33390.
			name:   "I will format this. (trailing space bug)",
			sample: "I will format this.",
		},
		{
			name:   "I will format it. (trailing space bug)",
			sample: "I will format it.",
		},
		{
			name:   "I will format. (trailing space bug, no object at all)",
			sample: "I will format.",
		},
		{
			// 2026-07-08 sweep leak class: swapped word order — the
			// qualifier ("needed") lands AFTER the noun. Original
			// pattern only caught qualifier-BEFORE-noun. Real sample
			// from production convos on account a2a30b02.
			name:   "I have all the evidence needed (post-33390 swapped order)",
			sample: "I have all the evidence needed. I will now generate the final answer explaining why scaling down ingress-nginx is not safe yet.",
		},
		{
			// 2026-07-08 same leak class — swapped order with a
			// different noun (data) and no "all" quantifier.
			name:   "I have the data needed to provide (post-33390 swapped order, no all)",
			sample: "I have the data needed to provide the final answer. I will format the top 3 PVCs into a table.",
		},
		{
			// 2026-07-08 swapped-order coverage for information/context
			// as sanity for the noun alternation.
			name:   "I have the information needed (post-33390 swapped order)",
			sample: "I have the information needed to answer. All nodes are healthy.",
		},
	}

	for _, c := range productionNarrationSamples {
		t.Run(c.name, func(t *testing.T) {
			resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{
				Arguments: map[string]any{"reasoning": c.sample},
			})
			assert.NoError(t, err)
			assert.Equal(t, core.NBToolResponseStatusError, resp.Status,
				"narration-shape input must be rejected at the tool boundary")
			assert.Contains(t, resp.Data, "narration of an upcoming action",
				"rejection error must explain the misuse")
			assert.Contains(t, resp.Data, "<final_answer>",
				"rejection error must point at the correct path (emit final_answer directly)")
		})
	}
}

// TestThinkTool_NarrationGuardAllowsRealDecisions pins the false-positive
// bound. The guard biases toward false-negatives (some narration slips
// through) over false-positives (real reasoning gets blocked). These cases
// are the legitimate decision shapes from the pre-tighten baseline that
// MUST continue to flow.
func TestThinkTool_NarrationGuardAllowsRealDecisions(t *testing.T) {
	tool := &thinkTool{}

	legitimateDecisionSamples := []struct {
		name   string
		sample string
	}{
		{
			name:   "weighing what to check first (real legacy sample)",
			sample: "The traces show that the latency is internal to the services-server (specifically /rpc/query and HTTP POST endpoints) and not caused by downstream dependencies. I need to check if resource constraints (CPU throttling or memory pressure) are the cause before looking at the code.",
		},
		{
			name:   "exhaustive search + decision to ask (real legacy sample)",
			sample: "I have searched for 'sync' across events, AWS resources, and CloudWatch logs/alarms, but found nothing. I will ask the user for more details.",
		},
		{
			name:   "notebook bookkeeping with hypothesis status (real legacy sample)",
			sample: "Hypothesis H2 is now SUPPORTED. Evidence E3 shows Poetry's caret syntax and E6 confirms the use of [tool.poetry.dependencies] in the affected directories, which Dependabot's uv integration cannot parse.",
		},
		{
			name:   "weighing retry vs pivot",
			sample: "The kubectl call timed out. Should I retry with a longer timeout, pivot to fetch_logs, or check if the cluster is reachable first?",
		},
		{
			name:   "either-or alternatives",
			sample: "Either the secret was rotated and the pod hasn't picked it up, or the secret was never created in this namespace. I need to check the events for the pod before deciding.",
		},
		{
			name:   "instead-of pivot",
			sample: "Instead of running another kubectl exec, I will use fetch_logs to get the same data without touching the pod's filesystem.",
		},
		{
			name:   "conflicts in evidence",
			sample: "The metrics show 0 errors but the logs show 5xx responses. These conflict — I need to check whether the metric is sampled or filtered.",
		},
		{
			// Includes the dotted abbreviation `vs.` to pin the
			// Gemini #33080 review fix: original regex `\bvs\.?\b`
			// required a word-char after `.` so `vs.` followed by a
			// space failed to match. Fix is `\bvs\b\.?`.
			name:   "vs. comparison with dot",
			sample: "The cause is application-side connection leak vs. database-side connection limit. Let me check pg_stat_activity to disambiguate.",
		},
	}

	for _, c := range legitimateDecisionSamples {
		t.Run(c.name, func(t *testing.T) {
			resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{
				Arguments: map[string]any{"reasoning": c.sample},
			})
			assert.NoError(t, err)
			assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status,
				"legitimate decision-language input must NOT be rejected by the narration guard")
			assert.Equal(t, c.sample, resp.Data,
				"successful think call must echo the reasoning back verbatim")
		})
	}
}

// TestThinkTool_NarrationGuardEdgeCases covers boundary inputs the guard
// must not crash on or misclassify.
func TestThinkTool_NarrationGuardEdgeCases(t *testing.T) {
	tool := &thinkTool{}

	t.Run("empty reasoning is now handled by Call()-level empty-check, not narration guard", func(t *testing.T) {
		// Pre-2026-07-08 the schema-Required validator caught this before
		// Call() ran, so the narration guard test asserted empty→SUCCESS
		// as documentation of what happens "if we ever get here". Now the
		// schema-Required guard is gone (see InputSchema comment) and
		// Call() itself rejects empty input with the actionable message.
		// See TestThinkTool_EmptyInputRejected for the full matrix.
		resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{
			Arguments: map[string]any{"reasoning": ""},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "requires a non-empty 'reasoning'")
	})

	t.Run("very short random text passes", func(t *testing.T) {
		resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{
			Arguments: map[string]any{"reasoning": "foo"},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	})

	t.Run("isNarrationOnly helper handles whitespace-only input", func(t *testing.T) {
		assert.False(t, isNarrationOnly("   \n\t  "),
			"whitespace-only input is the same failure mode as empty — let it through")
	})

	t.Run("isNarrationOnly rejects bare narration without trailing facts", func(t *testing.T) {
		assert.True(t, isNarrationOnly("I am ready to finalize the answer."))
		assert.True(t, isNarrationOnly("The investigation is complete."))
	})

	t.Run("isNarrationOnly allows narration shape when paired with 'but' (real pivot)", func(t *testing.T) {
		assert.False(t, isNarrationOnly("I have confirmed the pod exists but the logs are empty, so I need to check the previous container instance."))
	})
}
