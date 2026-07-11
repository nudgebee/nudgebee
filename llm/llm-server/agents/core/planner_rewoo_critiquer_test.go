package core

import (
	"testing"

	"nudgebee/llm/security"

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

// TestReWooCritiquer_ParseOutput characterizes the critiquer output parser:
// markdown-fenced vs raw XML, the Gemini <thought> preamble strip, case-folded
// decisions, CDATA/special-char resilience, and the no-XML error. parseOutput is
// pure given an output string; the only dependency is the logger reached via ctx
// on the regex-fallback path.
func TestReWooCritiquer_ParseOutput(t *testing.T) {
	cases := []struct {
		name             string
		output           string
		wantErr          bool
		wantRefine       bool
		feedbackContains string
	}{
		{
			name:             "fenced XML, refine",
			output:           "```xml\n<critique_response><decision>refine</decision><feedback>add the logs step</feedback></critique_response>\n```",
			wantRefine:       true,
			feedbackContains: "add the logs step",
		},
		{
			name:             "fenced XML, accept",
			output:           "```xml\n<critique_response><decision>accept</decision><feedback>plan looks complete</feedback></critique_response>\n```",
			wantRefine:       false,
			feedbackContains: "plan looks complete",
		},
		{
			name:             "raw XML (no fence), refine",
			output:           "<critique_response><decision>refine</decision><feedback>needs a metrics check</feedback></critique_response>",
			wantRefine:       true,
			feedbackContains: "needs a metrics check",
		},
		{
			name:       "Gemini <thought> preamble is stripped before the root",
			output:     "<thought>let me reason about this plan</thought>\n<critique_response><decision>accept</decision><feedback>ok</feedback></critique_response>",
			wantRefine: false,
		},
		{
			name:       "decision is case-insensitive",
			output:     "<critique_response><decision>REFINE</decision><feedback>x</feedback></critique_response>",
			wantRefine: true,
		},
		{
			name:             "CDATA feedback with special chars round-trips",
			output:           "<critique_response><decision>refine</decision><feedback><![CDATA[scale <ns> & retry]]></feedback></critique_response>",
			wantRefine:       true,
			feedbackContains: "retry",
		},
		{
			name:       "bare ampersand under accept still resolves the decision",
			output:     "<critique_response><decision>accept</decision><feedback>cpu & mem within budget</feedback></critique_response>",
			wantRefine: false,
		},
		{
			// accept discards feedback, so this also asserts the feedback survives
			// the regex fallback when XML parsing trips on the bare ampersand.
			name:             "bare ampersand under refine preserves feedback",
			output:           "<critique_response><decision>refine</decision><feedback>scale & retry</feedback></critique_response>",
			wantRefine:       true,
			feedbackContains: "scale & retry",
		},
		{
			name:    "empty output is a parse error",
			output:  "",
			wantErr: true,
		},
		{
			name:    "prose with no XML markers is a parse error",
			output:  "I think the plan is fine, no changes needed.",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &ReWooCritiquer{ctx: security.NewRequestContextForSuperAdmin()}
			refine, feedback, err := c.parseOutput(tc.output)

			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.wantRefine, refine)
			if tc.feedbackContains != "" {
				assert.Contains(t, feedback, tc.feedbackContains)
			}
		})
	}
}
