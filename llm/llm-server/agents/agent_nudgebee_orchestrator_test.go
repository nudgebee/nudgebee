package agents

import (
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools"

	"github.com/stretchr/testify/assert"
)

func TestDefaultOrchestratorsMountNudgebeeAgent(t *testing.T) {
	assert.Contains(t, trimmedK8sCoreToolNames(), NudgebeeAgentName)
	for _, cliTool := range []string{
		tools.ToolExecuteAwsCliCommand,
		tools.ToolExecuteGcpCliCommand,
		tools.ToolExecuteAzureCliCommand,
	} {
		assert.Contains(t, cloudLeanCoreToolNames(cliTool), NudgebeeAgentName)
	}

	native := &K8sNativeAgent{accountId: "account-1"}
	nativeNames := make([]string, 0)
	for _, tool := range native.GetSupportedTools(security.NewRequestContextForSuperAdmin()) {
		nativeNames = append(nativeNames, tool.Name())
	}
	assert.Contains(t, nativeNames, NudgebeeAgentName)
}

func TestNudgebeeDescriptionProvidesOrchestratorRoutingBoundary(t *testing.T) {
	description := newNudgebeeAgent("account-1").GetDescription()
	assert.Contains(t, description, "account inventory")
	assert.Contains(t, description, "configured integrations")
	assert.Contains(t, description, "Do not use for workloads")
	assert.Contains(t, description, "troubleshooting")
}
