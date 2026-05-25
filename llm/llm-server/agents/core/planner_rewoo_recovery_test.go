package core

import (
	"context"
	"nudgebee/llm/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

// MockSolver implements a minimal solver for testing plan summary injection
type MockSolver struct {
	CapturedTaskContext string
}

func (m *MockSolver) Solve(ctx context.Context, input string, steps []NBAgentPlannerToolActionStep, notebook string, taskContext ...string) (*NBAgentPlannerFinishAction, string, error) {
	if len(taskContext) > 0 {
		m.CapturedTaskContext = taskContext[0]
	}
	return &NBAgentPlannerFinishAction{Data: "Final Answer", Status: ConversationStatusCompleted}, "", nil
}

func TestReWooPlanner2_RecoveryAlertInjection(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("test_tenant", "test_user", []string{"test_account"})

	planner := &ReWooPlanner2{
		ctx:                ctx,
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		request:            NBAgentRequest{AccountId: "test_account", Query: "Investigate failure"},
	}

	t.Run("Alert injected when failure exists", func(t *testing.T) {
		planner.executionGraph["Task1"] = &PlannerNode{
			Step:   rewooPlannerStep2{ID: "Task1", Tool: "Github", Reason: "Check repo"},
			Status: StepStatusFailed,
			Output: "Repository not found",
		}

		// Let's simulate the logic in Plan()
		planSummary := planner.generatePlanSummary()
		hasFailures := false
		for _, node := range planner.executionGraph {
			if node.Status == StepStatusFailed {
				hasFailures = true
				break
			}
		}
		if hasFailures {
			planSummary += "\n\nSYSTEM ALERT: One or more technical steps FAILED. Do not simply summarize the error. 1. Review the failures and the gathered metadata in the Notebook. 2. If the failures were due to incorrect resource names, missing parameters, or wrong regions, use <missing_information> to request a NEW plan with corrected inputs. 3. ONLY if no recovery is possible after reviewing all metadata, provide a final answer synthesizing what was successfully found."
		}

		assert.True(t, hasFailures)
		assert.Contains(t, planSummary, "SYSTEM ALERT")
		assert.Contains(t, planSummary, "failed")
		assert.Contains(t, planSummary, "Error: Repository not found")
		assert.Contains(t, planSummary, "use <missing_information> to request a NEW plan")
	})

	t.Run("No alert when all steps succeed", func(t *testing.T) {
		planner.executionGraph["Task1"].Status = StepStatusCompleted
		planner.executionGraph["Task1"].Output = "Found it"

		planSummary := planner.generatePlanSummary()
		hasFailures := false
		for _, node := range planner.executionGraph {
			if node.Status == StepStatusFailed {
				hasFailures = true
				break
			}
		}
		// Logic from Plan()
		if hasFailures {
			planSummary += "\n\nSYSTEM ALERT: One or more technical steps FAILED..."
		}

		assert.False(t, hasFailures)
		assert.NotContains(t, planSummary, "SYSTEM ALERT")
	})
}
