package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestOrchestratingConstantValue pins the wire/DB value so a future accidental
// edit can't silently desync it from the llm_agents_check_executortype CHECK
// constraint (V779 restricts executor_type to tools/react/orchestrating/custom).
func TestOrchestratingConstantValue(t *testing.T) {
	assert.Equal(t, AgentPlannerType("orchestrating"), AgentPlannerTypeOrchestrating)
}
