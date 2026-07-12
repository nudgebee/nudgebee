package core

import (
	"strings"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// step is a tiny helper to build a ToolInvocation with a tool name, input args,
// and output content.
func step(tool, input, output string) ToolInvocation {
	return ToolInvocation{
		Call:     llms.ToolCall{Type: "function", FunctionCall: &llms.FunctionCall{Name: tool, Arguments: input}},
		Response: llms.ToolCallResponse{Name: tool, Content: output},
	}
}

func TestBuildSubAgentEvidence(t *testing.T) {
	t.Run("empty steps → empty string", func(t *testing.T) {
		assert.Equal(t, "", buildSubAgentEvidence(nil, 2048))
		assert.Equal(t, "", buildSubAgentEvidence([]ToolInvocation{}, 2048))
	})

	t.Run("basic: surfaces tool, input, and output, fenced", func(t *testing.T) {
		out := buildSubAgentEvidence([]ToolInvocation{
			step("postgres_query_execute", "EXPLAIN ANALYZE SELECT 1", "Seq Scan ... 1147ms"),
		}, 2048)
		assert.True(t, strings.HasPrefix(out, "<sub_agent_evidence>"))
		assert.True(t, strings.HasSuffix(out, "</sub_agent_evidence>"))
		assert.Contains(t, out, "postgres_query_execute")
		assert.Contains(t, out, "EXPLAIN ANALYZE SELECT 1")
		assert.Contains(t, out, "1147ms")
	})

	t.Run("multi-line input is collapsed to fit compactly", func(t *testing.T) {
		out := buildSubAgentEvidence([]ToolInvocation{
			step("shell_execute", "grep -c \\\n  'Failed to load'\n  file.txt", "48"),
		}, 2048)
		assert.NotContains(t, out, "\n  ", "multi-line input should be whitespace-collapsed")
		assert.Contains(t, out, "48")
	})

	t.Run("hard total budget is never exceeded", func(t *testing.T) {
		big := strings.Repeat("x", 100_000)
		steps := make([]ToolInvocation, 0, 20)
		for i := 0; i < 20; i++ {
			steps = append(steps, step("fetch_logs", big, big))
		}
		out := buildSubAgentEvidence(steps, 2048)
		assert.LessOrEqual(t, len(out), 2048, "manifest must respect the hard total budget")
	})

	t.Run("the '48' scenario: distilled grep survives, multi-MB dump does not bloat", func(t *testing.T) {
		// Chronological order: a huge raw fetch first, then small distilling greps.
		multiMB := strings.Repeat("log line noise ", 250_000) // ~3.7MB
		steps := []ToolInvocation{
			step("fetch_logs", "all logs for cost-server last 24h", multiMB),
			step("shell_execute", "grep -c 'Failed to load cluster info' logs.txt", "48"),
			step("shell_execute", "grep 'Failed to load' logs.txt | tail -1", "2026-07-10T06:41:44Z ... Failed to load cluster info"),
		}
		out := buildSubAgentEvidence(steps, 2048)
		assert.LessOrEqual(t, len(out), 2048)
		// The distilled findings the parent needs are present...
		assert.Contains(t, out, "48")
		assert.Contains(t, out, "06:41:44")
		// ...and the multi-MB dump did not cross the boundary verbatim.
		assert.NotContains(t, out, multiMB)
	})

	t.Run("sub-budget floor is applied", func(t *testing.T) {
		out := buildSubAgentEvidence([]ToolInvocation{step("t", "in", "out")}, 1)
		// floored to subAgentEvidenceMinBudget, so a small entry still renders.
		assert.Contains(t, out, "- t")
	})
}

// TestBuildSubAgentEvidenceForTool covers the shared helper the bespoke
// agent-tool wrappers (logs/metrics/traces/delegate) call so they don't drop
// evidence at their own tool boundary the way they did before the fix.
func TestBuildSubAgentEvidenceForTool(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	steps := []ToolInvocation{
		step("shell_execute", "grep -c 'Failed to load' logs.txt", "48"),
	}

	prev := config.Config.LlmServerSubAgentEvidenceEnabled
	prevMax := config.Config.LlmServerSubAgentEvidenceMaxChars
	defer func() {
		config.Config.LlmServerSubAgentEvidenceEnabled = prev
		config.Config.LlmServerSubAgentEvidenceMaxChars = prevMax
	}()
	config.Config.LlmServerSubAgentEvidenceMaxChars = 2048

	t.Run("feature off → empty, regardless of steps", func(t *testing.T) {
		config.Config.LlmServerSubAgentEvidenceEnabled = false
		assert.Equal(t, "", BuildSubAgentEvidenceForTool(ctx, "logs", steps))
	})

	t.Run("feature on → fenced manifest with the distilled step", func(t *testing.T) {
		config.Config.LlmServerSubAgentEvidenceEnabled = true
		out := BuildSubAgentEvidenceForTool(ctx, "logs", steps)
		assert.True(t, strings.HasPrefix(out, "<sub_agent_evidence>"))
		assert.Contains(t, out, "shell_execute")
		assert.Contains(t, out, "48")
	})

	t.Run("feature on but no steps → empty", func(t *testing.T) {
		config.Config.LlmServerSubAgentEvidenceEnabled = true
		assert.Equal(t, "", BuildSubAgentEvidenceForTool(ctx, "logs", nil))
	})
}

// TestFormatObservationAppendsEvidence verifies the render seam: the evidence
// manifest is appended verbatim AFTER the </observation> close (so it is exempt
// from observation summarization), and absent steps are byte-for-byte unchanged.
func TestFormatObservationAppendsEvidence(t *testing.T) {
	o := &NBReActPlanner3{}
	base := NBAgentPlannerToolActionStep{Action: NBAgentPlannerToolAction{Tool: "logs", DisplayID: "E1"}}

	t.Run("no evidence → observation unchanged", func(t *testing.T) {
		out := o.formatObservation(base, "raw log output", true)
		assert.Contains(t, out, "raw log output")
		assert.NotContains(t, out, "<sub_agent_evidence>")
		assert.True(t, strings.HasSuffix(out, "</observation>"))
	})

	t.Run("evidence rendered after the observation on a recent step", func(t *testing.T) {
		withEv := base
		withEv.SubAgentEvidence = "<sub_agent_evidence>\n- shell_execute: grep -c err → 48\n</sub_agent_evidence>"
		out := o.formatObservation(withEv, "raw log output", true)
		assert.Contains(t, out, "grep -c err → 48")
		// Evidence comes AFTER the observation close, not inside it.
		obsClose := strings.Index(out, "</observation>")
		evOpen := strings.Index(out, "<sub_agent_evidence>")
		assert.Greater(t, evOpen, obsClose, "evidence must be appended after </observation>")
	})

	t.Run("evidence dropped on a non-recent step under compression", func(t *testing.T) {
		withEv := base
		withEv.SubAgentEvidence = "<sub_agent_evidence>\n- shell_execute: grep -c err → 48\n</sub_agent_evidence>"
		// includeEvidence=false simulates an aged step once compression is active:
		// the evidence ages out with the observation it distilled, keeping the
		// scratchpad bounded instead of carrying every sub-agent's trail forever.
		out := o.formatObservation(withEv, "[summary] logs looked noisy", false)
		assert.Contains(t, out, "[summary] logs looked noisy")
		assert.NotContains(t, out, "<sub_agent_evidence>")
		assert.True(t, strings.HasSuffix(out, "</observation>"))
	})
}
