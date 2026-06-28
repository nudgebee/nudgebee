package tools

import (
	"nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTektonTool_Name(t *testing.T) {
	tool := TektonExecuteTool{}
	assert.Equal(t, ToolExecuteTektonCommand, tool.Name())
	assert.Equal(t, "tekton_execute", tool.Name())
}

func TestTektonTool_Description(t *testing.T) {
	tool := TektonExecuteTool{}
	desc := tool.Description()
	assert.Contains(t, desc, "tkn")
	assert.Contains(t, desc, "pipeline")
	assert.Contains(t, desc, "CI")
	assert.Contains(t, desc, "pipelinerun logs")
	assert.Contains(t, desc, "taskrun")
}

func TestTektonTool_InputSchema(t *testing.T) {
	tool := TektonExecuteTool{}
	schema := tool.InputSchema()
	assert.Contains(t, schema.Properties, "command")
	assert.Contains(t, schema.Required, "command")
}

func TestTektonTool_ConfigSchema(t *testing.T) {
	tool := TektonExecuteTool{}
	schema := tool.ConfigSchema(nil)
	assert.Equal(t, "tekton", schema.ConfigType)
	assert.Equal(t, core.ToolConfigSourceIntegration, schema.ConfigSource)
	assert.Contains(t, schema.Properties, "namespace")
	assert.Contains(t, schema.Properties, "timeout")
	assert.Equal(t, "", schema.Properties["namespace"].Default)
	assert.Equal(t, "30", schema.Properties["timeout"].Default)
}

func TestTektonTool_ParseError(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		origErr  string
		contains string
	}{
		{
			name:     "tekton not installed",
			output:   "error: the server doesn't have a resource type \"pipelines.tekton.dev\"",
			contains: "not installed",
		},
		{
			name:     "tkn not found",
			output:   "bash: tkn: command not found",
			contains: "tkn CLI is not available",
		},
		{
			name:     "permission denied",
			output:   "Error: forbidden: User \"system:serviceaccount\" cannot list resource",
			contains: "authorization failed",
		},
		{
			name:     "pipelinerun not found",
			output:   "Error: pipelinerun.tekton.dev \"my-run\" not found",
			contains: "resource not found",
		},
		{
			name:     "connection refused",
			output:   "dial tcp 10.0.0.1:443: connection refused",
			contains: "Cannot connect",
		},
		{
			name:     "timeout",
			output:   "error: context deadline exceeded",
			contains: "timed out",
		},
		{
			name:     "no such host",
			output:   "dial tcp: lookup kubernetes.default.svc: no such host",
			contains: "Cannot resolve",
		},
		{
			name:     "fallback to original error",
			output:   "",
			origErr:  "some unknown error",
			contains: "some unknown error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseTektonError(tt.output, tt.origErr)
			assert.Contains(t, result, tt.contains)
		})
	}
}

func TestTektonTool_InferRequestTypePrompt(t *testing.T) {
	tool := TektonExecuteTool{}
	prompt, err := tool.InferToolRequestTypePrompt(nil, "tekton_execute", "tkn pipelinerun list")
	assert.NoError(t, err)
	assert.Contains(t, prompt, "read")
	assert.Contains(t, prompt, "create")
	assert.Contains(t, prompt, "delete")
	assert.Contains(t, prompt, "pipelinerun list")
}
