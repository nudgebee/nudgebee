package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestStepIsStuck covers the outcome-counter threshold check the no-progress
// brake uses — it keys on the executor's existing RepeatedResultCount (same
// result via different inputs) and TrivialResultRepeatCount (trivial/empty
// output this turn), not on command sameness.
func TestStepIsStuck(t *testing.T) {
	assert.False(t, stepIsStuck(&NBAgentPlannerToolActionStep{}, 3), "fresh step is not stuck")
	assert.False(t, stepIsStuck(&NBAgentPlannerToolActionStep{RepeatedResultCount: 2}, 3), "below threshold")
	assert.True(t, stepIsStuck(&NBAgentPlannerToolActionStep{RepeatedResultCount: 3}, 3), "identical result at threshold")
	assert.True(t, stepIsStuck(&NBAgentPlannerToolActionStep{TrivialResultRepeatCount: 4}, 3), "trivial repeats past threshold")
	assert.False(t, stepIsStuck(&NBAgentPlannerToolActionStep{TrivialResultRepeatCount: 2}, 3), "trivial below threshold")
}
