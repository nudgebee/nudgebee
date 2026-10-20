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
	stats := extractLongTermMemory(ctx, nil, writeOffRequest(), "a response", "notebook", 3, false)
	assert.Zero(t, stats, "no extraction may run, so no stats")
}

// Only person chats and alert investigations save memory; anything else,
// including a source added later, does not.
func TestGate_IsChatOrAlertSource(t *testing.T) {
	for _, s := range []ConversationSource{"", ConversationSourceUserInvestigation, ConversationSourceUserInvestigationCLI,
		ConversationSourceInvestigation, ConversationSourceInstantNotification} {
		assert.True(t, isChatOrAlertSource(s), "%q", s)
	}
	for _, s := range []ConversationSource{ConversationSourceAutomation, ConversationSourcePRLifecycle,
		ConversationSourceTriageClassification, ConversationSourceWorkflowBuilder, "SomeFutureSource"} {
		assert.False(t, isChatOrAlertSource(s), "%q", s)
	}
}

// Switching writing off for an unattended run keeps its reading and reranking,
// and never mutates the caller's overrides.
func TestGate_WithMemoryWriteOff(t *testing.T) {
	q := toolcore.NBQueryConfig{Memory: withMemoryWriteOff(nil)}
	assert.True(t, q.MemoryReadAllowed())
	assert.True(t, q.MemoryRerankAllowed())
	assert.False(t, q.MemoryWriteAllowed())

	in := &toolcore.MemoryOverrides{Rerank: offPtr()}
	out := withMemoryWriteOff(in)
	assert.Nil(t, in.Write)
	assert.False(t, *out.Write)
	assert.False(t, *out.Rerank)
}

// Write off must leave reading alone — that is the combination the grouped
// shape exists to express.
func TestGate_WriteOffLeavesReadingEnabled(t *testing.T) {
	q := writeOffRequest().QueryConfig
	assert.True(t, q.MemoryReadAllowed())
	assert.True(t, q.MemoryRerankAllowed())
	assert.False(t, q.MemoryWriteAllowed())
}
