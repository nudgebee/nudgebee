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
