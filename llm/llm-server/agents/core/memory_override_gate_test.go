package core

import (
	"context"
	"testing"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

func offPtr() *bool { b := false; return &b }

func writeOffRequest() NBAgentRequest {
	return NBAgentRequest{
		AccountId:   "acct",
		QueryConfig: toolcore.NBQueryConfig{Memory: &toolcore.MemoryOverrides{Write: offPtr()}},
	}
}

// extractLongTermMemory must return before it touches the security context or
// runs an extractor. Proving it by passing a context with no security context:
// the function dereferences one immediately after the gate, so reaching that
// line would panic and fail the test.
func TestGate_ExtractLongTermMemorySkipsWhenWriteDisabled(t *testing.T) {
	ctx := security.NewRequestContext(context.TODO(), nil, nil, nil, nil)
	stats := extractLongTermMemory(ctx, nil, writeOffRequest(), "a response", "notebook", 3)
	assert.Zero(t, stats, "no extraction may run, so no stats")
}

// Write off must leave reading alone — that is the combination the grouped
// shape exists to express.
func TestGate_WriteOffLeavesReadingEnabled(t *testing.T) {
	q := writeOffRequest().QueryConfig
	assert.True(t, q.MemoryReadAllowed())
	assert.True(t, q.MemoryRerankAllowed())
	assert.False(t, q.MemoryWriteAllowed())
}
