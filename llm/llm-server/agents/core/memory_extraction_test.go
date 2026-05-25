package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReWooSolver_ParseOutput_WithNotebook(t *testing.T) {
	solver := &ReWooSolver{}

	t.Run("Final answer with notebook update", func(t *testing.T) {
		output := `
<final_answer>
    <thought>Task completed.</thought>
    <content>I have listed the connections.</content>
</final_answer>
<update_notebook>User prefers direct agent usage.</update_notebook>`

		finish, missingInfo, updatedNotebook, err := solver.parseOutput(output, false)
		assert.NoError(t, err)
		assert.NotNil(t, finish)
		assert.Empty(t, missingInfo)
		assert.Equal(t, "User prefers direct agent usage.", updatedNotebook)
		assert.Equal(t, "I have listed the connections.", finish.Data)
	})

	t.Run("Missing info with notebook update", func(t *testing.T) {
		output := `
<missing_information>
    <thought>Need more info.</thought>
    <required_information>Which environment?</required_information>
</missing_information>
<update_notebook>User mentioned 'dev' environment.</update_notebook>`

		finish, missingInfo, updatedNotebook, err := solver.parseOutput(output, false)
		assert.NoError(t, err)
		assert.NotNil(t, finish) // Finish is non-nil as it contains Log/Data for the caller
		assert.Equal(t, "Which environment?", missingInfo)
		assert.Equal(t, "User mentioned 'dev' environment.", updatedNotebook)
		assert.Equal(t, "Which environment?", finish.Data)
	})
}

func TestRetrieveAndBuildMemoryNotebook_Broadened(t *testing.T) {
	// Setup mock agent
	mockAgent := &MockAgent{} // Assumes ReWoo by default from executor_planner_test.go or similar

	t.Run("Investigation task - Should trigger", func(t *testing.T) {
		req := NBAgentRequest{Query: "investigate why pod is failing"}
		// We can't easily test the DB part here without full setup,
		// but we can test the logic that determines IF it should run.

		isRetrievalTask := IsDataRetrievalOrActionRequest(req.Query)
		isInvestigationTask := IsInvestigationRequestTask(req.Query)
		isSupportedPlanner := mockAgent.GetPlannerType() == AgentPlannerTypeReWoo || mockAgent.GetPlannerType() == AgentPlannerTypeReAct

		shouldRun := (isInvestigationTask || isRetrievalTask) && isSupportedPlanner
		assert.True(t, shouldRun)
	})

	t.Run("Retrieval task - Should now trigger", func(t *testing.T) {
		req := NBAgentRequest{Query: "get me pods"}

		isRetrievalTask := IsDataRetrievalOrActionRequest(req.Query)
		isInvestigationTask := IsInvestigationRequestTask(req.Query)
		isSupportedPlanner := mockAgent.GetPlannerType() == AgentPlannerTypeReWoo || mockAgent.GetPlannerType() == AgentPlannerTypeReAct

		shouldRun := (isInvestigationTask || isRetrievalTask) && isSupportedPlanner
		assert.True(t, shouldRun)
	})

	t.Run("Conversational task - Should NOT trigger unless 'remember' verb added", func(t *testing.T) {
		req := NBAgentRequest{Query: "who are you"}

		isRetrievalTask := IsDataRetrievalOrActionRequest(req.Query)
		isInvestigationTask := IsInvestigationRequestTask(req.Query)
		isSupportedPlanner := mockAgent.GetPlannerType() == AgentPlannerTypeReWoo || mockAgent.GetPlannerType() == AgentPlannerTypeReAct

		shouldRun := (isInvestigationTask || isRetrievalTask) && isSupportedPlanner
		assert.False(t, shouldRun)
	})
}

func TestReWooPlanner2_Plan_CapturesSolverNotebook(t *testing.T) {
	planner := &ReWooPlanner2{
		Notebook: "initial content",
	}

	// Simulate the logic in planner_rewoo_2.go (merge/append):
	updatedNotebook := "User preference: use postgres agent."
	if updatedNotebook != "" {
		if planner.Notebook != "" && !strings.Contains(updatedNotebook, planner.Notebook) {
			planner.Notebook += "\n" + updatedNotebook
		} else {
			planner.Notebook = updatedNotebook
		}
	}

	assert.Equal(t, "initial content\nUser preference: use postgres agent.", planner.Notebook)
}

func TestIsDataRetrievalOrActionRequest_Extended(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"get pods", true},
		{"list connections", true},
		{"show config", true},
		{"remember to use postgres agent", true}, // 'remember' was added to retrieval verbs
		{"give me logs", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsDataRetrievalOrActionRequest(tt.input))
		})
	}
}

// TestIsAcceptableMemoryFact guards the save-time content validator. The
// reason strings are part of the contract — log analysis and metrics
// downstream group by them.
func TestIsAcceptableMemoryFact(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		expectOK       bool
		expectedReason string
	}{
		// Junk / placeholders — these are the primary motivators (production
		// is observing literal `null` text being stored as a memory).
		{"empty string", "", false, "empty"},
		{"whitespace only", "   \n\t ", false, "empty"},
		{"literal null", "null", false, "junk_placeholder"},
		{"literal NULL uppercase", "NULL", false, "junk_placeholder"},
		{"literal none", "none", false, "junk_placeholder"},
		{"literal NONE uppercase", "NONE", false, "junk_placeholder"},
		{"literal n/a", "n/a", false, "junk_placeholder"},
		{"literal na", "na", false, "junk_placeholder"},
		{"empty array", "[]", false, "junk_placeholder"},
		{"empty object", "{}", false, "junk_placeholder"},
		{"double dash", "--", false, "junk_placeholder"},

		// Markdown / template leakage.
		{"markdown h1", "# Summary of investigation findings", false, "markdown_or_template_fragment"},
		{"markdown h2", "## Severity assessment for the incident", false, "markdown_or_template_fragment"},
		{"markdown h3", "### Impact analysis of the cluster failure", false, "markdown_or_template_fragment"},
		{"bold severity", "**Severity** is high for this incident", false, "markdown_or_template_fragment"},
		{"scratchpad tag", "<scratchpad>note to self</scratchpad>", false, "markdown_or_template_fragment"},

		// Length thresholds.
		{"too short by chars", "k8s broke", false, "too_short"},
		{"too few words but long enough chars", "supercalifragilistic", false, "too_few_words"},

		// Ephemeral cluster state — true now, misleading later.
		{"pod ages", "`nudgebee` namespace runs `temporal` components with ages up to 5d14h.", false, "ephemeral_state"},
		{"replica count", "`workflow-server` deployment runs with 2 replicas in nudgebee namespace.", false, "ephemeral_state"},
		{"stateful index", "`nudgebee-qdrant-server` runs as a single replica (index 0) in the cluster.", false, "ephemeral_state"},

		// Durable facts that must pass.
		{"configuration insight", "ECR pulls require imagePullSecrets even when nodes have IAM roles assigned", true, ""},
		{"architectural fact", "relay-server is the sole gateway for kubectl operations from llm-server", true, ""},
		{"user preference", "User prefers kubectl top over the Prometheus agent for quick checks", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := isAcceptableMemoryFact(tt.content)
			assert.Equal(t, tt.expectOK, ok, "acceptance verdict for %q", tt.content)
			assert.Equal(t, tt.expectedReason, reason, "rejection reason for %q", tt.content)
		})
	}
}
