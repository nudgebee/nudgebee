package agents

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.Contains(t, description, "Nudgebee-recorded collector")
	assert.Contains(t, description, "Do not use it alone for arbitrary workloads")
	assert.Contains(t, description, "troubleshooting")
}

func TestOrchestratorPromptsGateNudgebeeHealthBeforeRuntimeInvestigation(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	for _, promptName := range []string{
		prompts.PromptK8sLean,
		prompts.PromptK8sNative,
		prompts.PromptAwsLean,
		prompts.PromptGcpLean,
		prompts.PromptAzureLean,
	} {
		promptName := promptName
		t.Run(promptName, func(t *testing.T) {
			promptText, err := prompts.GetPromptStrict(ctx.GetContext(), promptName, "account-1")
			require.NoError(t, err)
			prompt := core.ParsePromptToNBAgentPrompt(promptText)
			text := strings.Join(prompt.Instructions, "\n") + "\n" + strings.Join(prompt.Constraints, "\n")
			lowerText := strings.ToLower(text)
			assert.Contains(t, text, "call the available `nudgebee` agent before")
			assert.Contains(t, text, "before any infrastructure, cluster, telemetry, resource-search, or shell action")
			assert.Contains(t, text, "exact installation namespace")
			assert.Contains(t, lowerText, "never guess")
			assert.Contains(t, lowerText, "read-only")
			assert.Contains(t, lowerText, "do not create temporary resources")
		})
	}
}
