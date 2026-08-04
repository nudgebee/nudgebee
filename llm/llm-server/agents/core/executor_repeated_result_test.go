package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Replays the shape of the production loop: the model rewrote its aws_execute
// command on every iteration while carrying the same mis-cased `--query` field,
// so every call returned the identical null-filled result. The turn cache keys on
// the input and never fired.
func TestCountIdenticalPriorObservations(t *testing.T) {
	const nullResult = "null\tavailable\taurora-postgresql\t17.7\n"

	step := func(tool, input, observation string) NBAgentPlannerToolActionStep {
		return NBAgentPlannerToolActionStep{
			Action:      NBAgentPlannerToolAction{Tool: tool, ToolInput: input},
			Observation: observation,
		}
	}

	tests := []struct {
		name     string
		steps    []NBAgentPlannerToolActionStep
		action   NBAgentPlannerToolAction
		obs      string
		expected int
	}{
		{
			name:     "no prior steps",
			action:   NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "a"},
			obs:      nullResult,
			expected: 0,
		},
		{
			name:     "different input, identical result",
			steps:    []NBAgentPlannerToolActionStep{step("aws_execute", "query with jmespath", nullResult)},
			action:   NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "same query via jq"},
			obs:      nullResult,
			expected: 1,
		},
		{
			name:     "same input is the turn cache's job, not ours",
			steps:    []NBAgentPlannerToolActionStep{step("aws_execute", "a", nullResult)},
			action:   NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "a"},
			obs:      nullResult,
			expected: 0,
		},
		{
			name:     "different tool",
			steps:    []NBAgentPlannerToolActionStep{step("shell_execute", "a", nullResult)},
			action:   NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "b"},
			obs:      nullResult,
			expected: 0,
		},
		{
			name:     "different result means progress",
			steps:    []NBAgentPlannerToolActionStep{step("aws_execute", "a", nullResult)},
			action:   NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "b"},
			obs:      "database-1-instance-1\tavailable\n",
			expected: 0,
		},
		{
			// Sweeping regions/namespaces where most come back empty is correct
			// behaviour — flagging it would push the planner off a working scan.
			name:     "repeated empty results are not flagged",
			steps:    []NBAgentPlannerToolActionStep{step("aws_execute", "a", "")},
			action:   NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "b"},
			obs:      "",
			expected: 0,
		},
		{
			name:     "repeated no-data sentinel is not flagged",
			steps:    []NBAgentPlannerToolActionStep{step("aws_execute", "a", plannerToolNoData)},
			action:   NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "b"},
			obs:      plannerToolNoData,
			expected: 0,
		},
		{
			name:     "repeated empty JSON array is not flagged",
			steps:    []NBAgentPlannerToolActionStep{step("aws_execute", "a", "[]")},
			action:   NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "b"},
			obs:      "[]",
			expected: 0,
		},
		{
			name:     "repeated empty JSON object is not flagged",
			steps:    []NBAgentPlannerToolActionStep{step("aws_execute", "a", "{}")},
			action:   NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "b"},
			obs:      "{}",
			expected: 0,
		},
		{
			name:     "repeated bare null is not flagged",
			steps:    []NBAgentPlannerToolActionStep{step("aws_execute", "a", "null")},
			action:   NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "b"},
			obs:      "null",
			expected: 0,
		},
		{
			name:     "trivial containers are matched after trimming",
			steps:    []NBAgentPlannerToolActionStep{step("aws_execute", "a", " []\n")},
			action:   NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "b"},
			obs:      " []\n",
			expected: 0,
		},
		{
			// Guards the trivial-observation exclusions against over-reach: the
			// production symptom was `jq` printing one null PER INSTANCE, which is
			// not a trivial container and must still be flagged.
			name:     "multi-line nulls from the real incident are still flagged",
			steps:    []NBAgentPlannerToolActionStep{step("aws_execute", "jmespath variant", "null\nnull\nnull\n")},
			action:   NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "jq variant"},
			obs:      "null\nnull\nnull\n",
			expected: 1,
		},
		{
			// The count is what escalates the notice, so it has to be a count.
			name: "counts every prior occurrence",
			steps: []NBAgentPlannerToolActionStep{
				step("aws_execute", "variant 1", nullResult),
				step("aws_execute", "variant 2", nullResult),
				step("aws_execute", "variant 3", "unrelated output"),
				step("shell_execute", "variant 4", nullResult),
			},
			action:   NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "variant 5"},
			obs:      nullResult,
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &plannerExecutor{steps: tt.steps}
			assert.Equal(t, tt.expected, e.countIdenticalPriorObservations(tt.action, tt.obs))
		})
	}
}

// The static-hint version of this notice was demonstrably ignorable — a production run
// took the same "File not found" hint 16 times. The escalated form must therefore be
// visibly different from the advisory form, and must state the count.
func TestRepeatedResultNotice_EscalatesWithCount(t *testing.T) {
	advisory := repeatedResultNotice(2)
	assert.Contains(t, advisory, "SAME RESULT AS AN EARLIER CALL")
	assert.NotContains(t, advisory, "STOP")

	escalated := repeatedResultNotice(repeatedResultEscalateAt)
	assert.Contains(t, escalated, "STOP")
	assert.Contains(t, escalated, "3 TIMES", "the occurrence count must be stated, not implied")
	assert.NotEqual(t, advisory, escalated)

	assert.Contains(t, repeatedResultNotice(16), "16 TIMES")
}

// countTrivialResultsForTool complements countIdenticalPriorObservations: the
// byte-identical check deliberately short-circuits on trivial output to
// preserve legit multi-region sweeps, but a specific retry pattern still slips
// through — same tool, varied filter/format args, all returning `[]` because
// the underlying query is (say) mis-cased. Threshold-gated at the call site so
// only "clearly stuck" runs are named; the notice text handles both loops and
// sweeps correctly (naming "no matches" as a valid conclusion is what both
// need to hear).
func TestCountTrivialResultsForTool(t *testing.T) {
	step := func(tool, input, observation string) NBAgentPlannerToolActionStep {
		return NBAgentPlannerToolActionStep{
			Action:      NBAgentPlannerToolAction{Tool: tool, ToolInput: input},
			Observation: observation,
		}
	}

	tests := []struct {
		name     string
		steps    []NBAgentPlannerToolActionStep
		action   NBAgentPlannerToolAction
		expected int
	}{
		{
			name:     "no prior steps",
			action:   NBAgentPlannerToolAction{Tool: "aws_execute"},
			expected: 0,
		},
		{
			name: "counts trivial results for same tool only",
			steps: []NBAgentPlannerToolActionStep{
				step("aws_execute", "region us-east-1", "[]"),
				step("aws_execute", "region us-west-2", "null"),
				step("aws_execute", "region eu-west-1", plannerToolNoData),
				step("shell_execute", "unrelated", "[]"),      // different tool — skip
				step("aws_execute", "region ap-1", "one row"), // non-trivial — skip
			},
			action:   NBAgentPlannerToolAction{Tool: "aws_execute"},
			expected: 3,
		},
		{
			name: "empty JSON containers count as trivial",
			steps: []NBAgentPlannerToolActionStep{
				step("kubectl_execute", "get pods -n ns-a", "[]"),
				step("kubectl_execute", "get pods -n ns-b", "{}"),
				step("kubectl_execute", "get pods -n ns-c", ""),
			},
			action:   NBAgentPlannerToolAction{Tool: "kubectl_execute"},
			expected: 3,
		},
		{
			name: "tool-agnostic — works uniformly for shell/postgres/etc",
			steps: []NBAgentPlannerToolActionStep{
				step("postgres_query_execute", "SELECT * WHERE id='x'", "[]"),
				step("postgres_query_execute", "SELECT * WHERE ID='x'", "[]"),
				step("postgres_query_execute", "SELECT * WHERE Id='x'", "null"),
			},
			action:   NBAgentPlannerToolAction{Tool: "postgres_query_execute"},
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &plannerExecutor{steps: tt.steps}
			assert.Equal(t, tt.expected, e.countTrivialResultsForTool(tt.action))
		})
	}
}

// trivialResultNotice must name (a) the count so the model reads repetition as
// a signal not noise, (b) "no matches" as a valid conclusion so a legit
// multi-region sweep terminates correctly on it, and (c) "field name casing"
// as a diagnosis path since that was the root cause in every 7d-sweep loop
// where the model was actively wrong (versus scanning correctly).
func TestTrivialResultNotice_ContainsRequiredSignals(t *testing.T) {
	n := trivialResultNotice("aws_execute", 4)
	assert.Contains(t, n, "4 TIMES", "occurrence count must be stated, not implied")
	assert.Contains(t, n, "aws_execute", "tool name must be in the notice so the model can attribute")
	assert.Contains(t, n, "STOP", "notice must be visibly a stop directive, not advisory")
	assert.Contains(t, n, "no matching", "must name empty-as-valid so multi-region sweeps terminate correctly")
	assert.Contains(t, n, "CASING", "must name field-casing as recovery for the retry-loop case")
}
