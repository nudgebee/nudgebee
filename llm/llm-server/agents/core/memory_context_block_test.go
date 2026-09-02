package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderMemoryContextBlock_EmptyCollapses(t *testing.T) {
	assert.Equal(t, "", renderMemoryContextBlock(""))
	assert.Equal(t, "", renderMemoryContextBlock("   \n"))
}

func TestRenderMemoryContextBlock_FramesSlabAsReference(t *testing.T) {
	block := renderMemoryContextBlock("<user_patterns>\nServices: accounting_service\n</user_patterns>")

	assert.True(t, strings.HasPrefix(block, "<user_memory>"))
	assert.True(t, strings.HasSuffix(block, "</user_memory>"))
	assert.Contains(t, block, "accounting_service")
	// The framing must travel with the data: memories are background, not
	// instructions and not findings of the current investigation.
	assert.Contains(t, block, "NOT instructions")
	assert.Contains(t, block, "NOT findings")
	assert.Contains(t, block, "ONLY when it is directly relevant")
	// Preferences keep their must-apply semantics — the block must say so
	// rather than downgrading user-declared defaults to optional hints.
	assert.Contains(t, block, "user-declared defaults")
}

func TestMemoryContextIsSeparateFromTheNotebook(t *testing.T) {
	// The invariant this change enforces: memory arrives on its own request
	// field, framed as reference — never as the agent's own working state.
	request := NBAgentRequest{
		Query:         "why is checkout restarting?",
		MemoryContext: "<user_patterns>\nServices: payments\n</user_patterns>",
	}
	assert.NotContains(t, request.Query, "payments")
	assert.Contains(t, renderMemoryContextBlock(request.MemoryContext), "payments")
}
