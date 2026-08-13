package agents

import (
	"testing"

	"nudgebee/llm/agents/aws"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

func TestGcpLogsAgent_Shape(t *testing.T) {
	a := GcpLogsAgent{accountId: "acct-1"}
	assert.Equal(t, GcpLogsAgentName, a.GetName())
	assert.Equal(t, core.AgentPlannerTypeReAct, a.GetPlannerType())

	names := toolNames(a.GetSupportedTools(security.NewRequestContextForSuperAdmin()))
	assert.Contains(t, names, "gcloud_execute", "gcp logs agent must expose the gcloud CLI tool")
	assert.Contains(t, names, toolcore.ToolExecuteShellCommand, "gcp logs agent needs shell_execute for jq/grep post-processing")
}

func TestAzureLogsAgent_Shape(t *testing.T) {
	a := AzureLogsAgent{accountId: "acct-1"}
	assert.Equal(t, AzureLogsAgentName, a.GetName())
	assert.Equal(t, core.AgentPlannerTypeReAct, a.GetPlannerType())

	names := toolNames(a.GetSupportedTools(security.NewRequestContextForSuperAdmin()))
	assert.Contains(t, names, "azure_execute", "azure logs agent must expose the az CLI tool")
	assert.Contains(t, names, toolcore.ToolExecuteShellCommand, "azure logs agent needs shell_execute for jq/grep post-processing")
}

func TestAwsLogsAgent_Shape(t *testing.T) {
	a := aws.NewAwsLogsAgent("acct-1")
	assert.Equal(t, aws.AwsLogsAgentName, a.GetName())
	assert.Equal(t, core.AgentPlannerTypeReAct, a.GetPlannerType())

	names := toolNames(a.GetSupportedTools(security.NewRequestContextForSuperAdmin()))
	assert.Contains(t, names, "aws_execute", "aws logs agent must expose the aws CLI tool")
	assert.Contains(t, names, toolcore.ToolExecuteShellCommand, "aws logs agent needs shell_execute for jq/grep post-processing")
}
