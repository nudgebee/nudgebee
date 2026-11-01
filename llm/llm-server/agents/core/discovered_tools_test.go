package core

import (
	"testing"

	"github.com/stretchr/testify/assert"

	toolcore "nudgebee/llm/tools/core"
)

func TestDiscoveredTools_RecordAndCheck(t *testing.T) {
	RecordDiscoveredTools("conv-A", []string{"postgres_query_execute", "Redis"})

	// Recorded names are directly-callable, case-insensitively.
	assert.True(t, IsDiscoveredTool("conv-A", "postgres_query_execute"))
	assert.True(t, IsDiscoveredTool("conv-A", "redis"), "match must be case-insensitive")
	assert.True(t, IsDiscoveredTool("conv-A", "REDIS"))

	// Canonical casing is preserved so the auth gate resolves against the
	// case-sensitive tool/agent registries even when the planner uses a different case.
	canonical, ok := DiscoveredToolCanonicalName("conv-A", "redis")
	assert.True(t, ok)
	assert.Equal(t, "Redis", canonical)

	// A tool that was never surfaced is NOT callable (anti-hallucination guardrail).
	assert.False(t, IsDiscoveredTool("conv-A", "kubectl_execute"))

	// Discovery is scoped to the conversation.
	assert.False(t, IsDiscoveredTool("conv-B", "postgres_query_execute"))
}

func TestDiscoveredTools_EmptyInputsAreSafeNoOps(t *testing.T) {
	// Empty conversation id / tool names must be no-ops and never panic.
	RecordDiscoveredTools("", []string{"x"})
	RecordDiscoveredTools("conv-C", nil)
	assert.False(t, IsDiscoveredTool("", "x"))
	assert.False(t, IsDiscoveredTool("conv-C", "x"))
	assert.False(t, IsDiscoveredTool("conv-C", ""))
}

func TestDiscoveredTools_AccumulatesAcrossCalls(t *testing.T) {
	// Multiple search_tools calls in one conversation union their discoveries.
	RecordDiscoveredTools("conv-D", []string{"helm"})
	RecordDiscoveredTools("conv-D", []string{"rabbitmq"})
	assert.True(t, IsDiscoveredTool("conv-D", "helm"))
	assert.True(t, IsDiscoveredTool("conv-D", "rabbitmq"))
}

func TestQueryConfigDiscoveredToolCanonicalName(t *testing.T) {
	qc := toolcore.NBQueryConfig{
		DiscoveredTools: []string{"postgres_query_execute", "Redis_Command"},
		ToolConfigs: map[string]string{
			"kubectl_execute": "k8s-dev",
		},
	}

	// Tool in DiscoveredTools matches case-insensitively and returns canonical
	canonical, ok := QueryConfigDiscoveredToolCanonicalName(qc, "postgres_query_execute")
	assert.True(t, ok)
	assert.Equal(t, "postgres_query_execute", canonical)

	canonical, ok = QueryConfigDiscoveredToolCanonicalName(qc, "redis_command")
	assert.True(t, ok)
	assert.Equal(t, "Redis_Command", canonical)

	// Tool in ToolConfigs matches case-insensitively and returns canonical
	canonical, ok = QueryConfigDiscoveredToolCanonicalName(qc, "kubectl_execute")
	assert.True(t, ok)
	assert.Equal(t, "kubectl_execute", canonical)

	canonical, ok = QueryConfigDiscoveredToolCanonicalName(qc, "KUBECTL_EXECUTE")
	assert.True(t, ok)
	assert.Equal(t, "kubectl_execute", canonical)

	// Tool neither discovered nor configured returns false
	canonical, ok = QueryConfigDiscoveredToolCanonicalName(qc, "unauthorized_tool")
	assert.False(t, ok)
	assert.Empty(t, canonical)

	// Empty inputs return false
	canonical, ok = QueryConfigDiscoveredToolCanonicalName(qc, "")
	assert.False(t, ok)
	assert.Empty(t, canonical)
}
