package tools

import (
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToolAlias_ResolvesRetiredAgentHandles pins the Phase 3d alias contract
// for the four retired CLI-wrapper agents: `delegate_agent(tools=["gcp"])`,
// `["azure"]`, `["aws"]`, `["kubectl"]` — the short handles the retired
// agents used — must still resolve to the canonical `*_execute` tool.
//
// DB check on dev the day of retirement showed 4 real calls in 7 days using
// these handles (3× aws, 1× gcp) — the alias is what keeps stored history +
// straggler LLM turns from breaking silently.
func TestToolAlias_ResolvesRetiredAgentHandles(t *testing.T) {
	cases := []struct {
		alias, canonical string
	}{
		{"aws", ToolExecuteAwsCliCommand},
		{"gcp", ToolExecuteGcpCliCommand},
		{"azure", ToolExecuteAzureCliCommand},
		{"kubectl", ToolExecuteKubectlCommand},
	}
	for _, c := range cases {
		t.Run(c.alias, func(t *testing.T) {
			tool, ok := core.GetNBTool("fake-account", c.alias)
			require.True(t, ok, "alias %q must resolve after Phase 3d retirement", c.alias)
			assert.Equal(t, c.canonical, tool.Name(),
				"alias %q must map to canonical %q, not surface a shadow entry",
				c.alias, c.canonical)
		})
	}
}

// TestToolAlias_NotEnumerated pins the anti-double-listing invariant: the
// aliases must resolve but MUST NOT appear as their own entries in the
// enumerable tool list. If they did, search_tools would surface both e.g.
// "aws" and "aws_execute" — the exact duplication problem retiring the agents
// was meant to solve.
func TestToolAlias_NotEnumerated(t *testing.T) {
	names := core.ListRegisteredSystemToolNames()
	forbidden := map[string]struct{}{"aws": {}, "gcp": {}, "azure": {}, "kubectl": {}}
	for _, n := range names {
		if _, blocked := forbidden[n]; blocked {
			t.Errorf("alias %q must not appear in enumerated tool list — was the alias registered as a factory by mistake?", n)
		}
	}
}

// Compile-time interface checks so a renamed or unexported ToolPrompt method
// fails the build instead of silently dropping delegate-context safety rules.
var (
	_ core.NBToolPromptProvider = AwsCliTool{}
	_ core.NBToolPromptProvider = GcpCliTool{}
	_ core.NBToolPromptProvider = AzureCliTool{}
	_ core.NBToolPromptProvider = KubectlExecuteTool{}
)

// Content guards on each of the 4 tools' slim ToolPrompts. These are the
// safety-critical rules that MUST survive delegate context (where the
// orchestrator's *_lean.yaml prompt isn't loaded). Regressions here are
// silent quality drops in delegated cloud/k8s investigations.
func TestAwsCliTool_ToolPromptSafetyRules(t *testing.T) {
	joined := joinLines(AwsCliTool{}.ToolPrompt())
	assert.Contains(t, joined, "IAM", "aws ToolPrompt must warn against self-IAM-modify")
	assert.Contains(t, joined, "--filters", "aws ToolPrompt must call out the plural --filters gotcha")
	assert.Contains(t, joined, "Cost Explorer", "aws ToolPrompt must cover credit/refund exclusion")
	assert.Contains(t, joined, "Evidence-based", "aws ToolPrompt must include evidence-based invariant")
}
func TestGcpCliTool_ToolPromptSafetyRules(t *testing.T) {
	joined := joinLines(GcpCliTool{}.ToolPrompt())
	assert.Contains(t, joined, "IAM", "gcp ToolPrompt must warn against self-IAM-modify")
	assert.Contains(t, joined, "gcloud config set project", "gcp ToolPrompt must call out the project-preconfigured gotcha")
	assert.Contains(t, joined, "gcloud_execute", "gcp ToolPrompt must state the canonical tool name (planner-fail guard)")
	assert.Contains(t, joined, "Evidence-based", "gcp ToolPrompt must include evidence-based invariant")
}
func TestAzureCliTool_ToolPromptSafetyRules(t *testing.T) {
	joined := joinLines(AzureCliTool{}.ToolPrompt())
	assert.Contains(t, joined, "role assignment", "azure ToolPrompt must warn against self-role-modify")
	assert.Contains(t, joined, "az monitor metrics list", "azure ToolPrompt must cover monitoring disambiguation")
	assert.Contains(t, joined, "run-command", "azure ToolPrompt must cover OS-detect-before-run-command")
	assert.Contains(t, joined, "Evidence-based", "azure ToolPrompt must include evidence-based invariant")
}
func TestKubectlExecuteTool_ToolPromptSafetyRules(t *testing.T) {
	joined := joinLines(KubectlExecuteTool{}.ToolPrompt())
	assert.Contains(t, joined, "namespace", "kubectl ToolPrompt must enforce namespace discipline")
	assert.Contains(t, joined, "RBAC", "kubectl ToolPrompt must warn against self-RBAC-modify")
	assert.Contains(t, joined, "--all-namespaces", "kubectl ToolPrompt must cover the -o json/-o yaml + all-namespaces context-saturation gotcha")
	assert.Contains(t, joined, "--previous", "kubectl ToolPrompt must retain the empty-logs recovery hint")
}

// joinLines is defined in tool_prompt_test.go (added for helm/redis/rabbit).
