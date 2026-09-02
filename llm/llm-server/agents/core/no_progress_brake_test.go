package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestStepIsStuck covers the outcome-counter threshold check the no-progress
// brake uses — it keys on the executor's unified NoProgressRepeatCount (the
// consolidated per-tool consecutive no-progress counter maintained by
// countConsecutiveNoProgressForTool), not on command sameness.
func TestStepIsStuck(t *testing.T) {
	assert.False(t, stepIsStuck(&NBAgentPlannerToolActionStep{}, 3), "fresh step is not stuck")
	assert.False(t, stepIsStuck(&NBAgentPlannerToolActionStep{NoProgressRepeatCount: 2}, 3), "below threshold")
	assert.True(t, stepIsStuck(&NBAgentPlannerToolActionStep{NoProgressRepeatCount: 3}, 3), "at threshold")
	assert.True(t, stepIsStuck(&NBAgentPlannerToolActionStep{NoProgressRepeatCount: 4}, 3), "past threshold")
	assert.False(t, stepIsStuck(&NBAgentPlannerToolActionStep{NoProgressRepeatCount: 0}, 3), "zero counter is not stuck")
}
