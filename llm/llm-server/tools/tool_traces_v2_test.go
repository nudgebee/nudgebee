package tools

import (
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// TestNBTraceToolV2_Metadata pins the canonical v2 trace tool's identity and input
// contract. The Call round-trip goes through services-server (QueryTraces) and is
// covered by the gated live e2e; the empty-provider behaviour lives in
// executeFetchTraceCanonical.
func TestNBTraceToolV2_Metadata(t *testing.T) {
	tool := NBTraceToolV2{accountId: "acct"}
	assert.Equal(t, ToolTracesExecuteV2, tool.Name())
	assert.Equal(t, "traces_execute_v2", ToolTracesExecuteV2)
	assert.Equal(t, core.NBToolTypeTool, tool.GetType())

	schema := tool.InputSchema()
	assert.Equal(t, []string{"command"}, schema.Required)
	for _, key := range []string{"command", "start_time", "end_time", "range"} {
		_, ok := schema.Properties[key]
		assert.True(t, ok, "input schema must expose %q", key)
	}
}
