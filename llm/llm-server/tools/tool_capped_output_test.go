package tools

import (
	"strings"
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixedOutputTool struct {
	MetricsListTool
	output string
}

func (f fixedOutputTool) Call(core.NbToolContext, core.NBToolCallRequest) (core.NBToolResponse, error) {
	return core.NBToolResponse{Data: f.output}, nil
}

type unclassifiedTool struct{ core.NBTool }

type classifiedTool struct{ core.NBTool }

func (classifiedTool) InferToolRequestType(*security.RequestContext, string, string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func TestCappedOutputToolCapsResult(t *testing.T) {
	long := CappedOutputTool{NBTool: fixedOutputTool{output: strings.Repeat("x", 100)}, MaxChars: 10, Hint: "narrow it"}
	out, err := long.Call(core.NbToolContext{}, core.NBToolCallRequest{})
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("x", 10)+"\n... (truncated - narrow it)", out.Data)

	short := CappedOutputTool{NBTool: fixedOutputTool{output: "ok"}, MaxChars: 10}
	out, err = short.Call(core.NbToolContext{}, core.NBToolCallRequest{})
	require.NoError(t, err)
	assert.Equal(t, "ok", out.Data)
}

// The wrapper must not hide a read-only tool's classification: unclassified
// calls are logged as such by the approval check (#39880 C7).
func TestCappedOutputToolForwardsClassification(t *testing.T) {
	read, err := CappedOutputTool{NBTool: classifiedTool{}}.InferToolRequestType(nil, "metrics_list", "{}")
	require.NoError(t, err)
	assert.Equal(t, core.ToolRequestTypeRead, read)

	undecided, err := CappedOutputTool{NBTool: unclassifiedTool{}}.InferToolRequestType(nil, "x", "{}")
	require.NoError(t, err)
	assert.Equal(t, core.ToolRequestType(""), undecided)
}
