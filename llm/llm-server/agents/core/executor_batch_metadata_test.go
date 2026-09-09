package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlannerSupportsExecutionBatches_ReAct3AndReAct4(t *testing.T) {
	assert.True(t, plannerSupportsExecutionBatches(&NBReActPlanner3{}))
	assert.True(t, plannerSupportsExecutionBatches(&NBReActPlanner4{}))
	assert.False(t, plannerSupportsExecutionBatches(nil))
}

func TestAnnotateExecutionBatch_UsesOneIdentifierForAllActions(t *testing.T) {
	actions := []NBAgentPlannerToolAction{{ToolID: "E1"}, {ToolID: "E2"}, {ToolID: "E3"}}
	annotateExecutionBatch(actions, "batch-shared", executionModeParallelDispatch, "", 2)

	for _, action := range actions {
		assert.Equal(t, "batch-shared", action.ExecutionBatchID)
		assert.Equal(t, executionModeParallelDispatch, action.ExecutionMode)
		assert.Equal(t, 3, action.ExecutionBatchSize)
		assert.Equal(t, 2, action.ExecutionParallelismLimit)
		assert.Empty(t, action.SequentialFallbackReason)
	}
}

func TestAnnotatePlannerIteration_UsesOneBasedIterationForAllActions(t *testing.T) {
	actions := []NBAgentPlannerToolAction{{ToolID: "E1"}, {ToolID: "E2"}}
	annotatePlannerIteration(actions, 3)

	for _, action := range actions {
		assert.Equal(t, 3, action.PlannerIteration)
	}
}

func TestAnnotateExecutionBatch_SequentialDoesNotExposeParallelLimit(t *testing.T) {
	actions := []NBAgentPlannerToolAction{{ToolID: "E1"}, {ToolID: "E2"}}
	annotateExecutionBatch(actions, "batch-fallback", executionModeSequentialDispatch, "potential_write", 4)

	for _, action := range actions {
		assert.Equal(t, "batch-fallback", action.ExecutionBatchID)
		assert.Equal(t, executionModeSequentialDispatch, action.ExecutionMode)
		assert.Equal(t, 2, action.ExecutionBatchSize)
		assert.Zero(t, action.ExecutionParallelismLimit)
		assert.Equal(t, "potential_write", action.SequentialFallbackReason)
	}
}
