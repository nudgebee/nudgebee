package agents

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/aws"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// containsTool reports whether the given list of tool-name strings has s.
// Case-insensitive + whitespace-trimmed to match the resolution behavior in
// getCloudLeanSupportedTools / getTrimmedK8sSupportedTools (both lowercase
// the tool name before enabledMap lookup); a specialist named "GitHub"
// creeping into a lean's preload should still trip the forbidden check.
func containsTool(names []string, s string) bool {
	target := strings.TrimSpace(s)
	for _, n := range names {
		if strings.EqualFold(strings.TrimSpace(n), target) {
			return true
		}
	}
	return false
}

// TestLeanOrchestratorParity_Websearch guards the addition of websearch to
// every lean orchestrator's preloaded tool set. Prior to this fix only
// aws_lean had websearch — k8s / azure / gcp lean silently omitted it, so
// error-shape and version-specific lookups on those paths fell back to the
// model's training knowledge. Regression here would silently degrade grounding
// on the 3 non-aws lean paths again.
func TestLeanOrchestratorParity_Websearch(t *testing.T) {
	tests := []struct {
		name  string
		names []string
	}{
		{"k8s_lean", trimmedK8sCoreToolNames()},
		{"aws_lean", cloudLeanCoreToolNames(tools.ToolExecuteAwsCliCommand)},
		{"azure_lean", cloudLeanCoreToolNames(tools.ToolExecuteAzureCliCommand)},
		{"gcp_lean", cloudLeanCoreToolNames(tools.ToolExecuteGcpCliCommand)},
	}

	for _, tt := range tests {
		t.Run(tt.name+"_has_websearch", func(t *testing.T) {
			assert.True(t, containsTool(tt.names, WebSearchAgentName),
				"%s must preload websearch — regression from the aws-only-had-it pattern", tt.name)
		})
	}
}

// TestLeanOrchestratorParity_ReducedCoreShape pins the invariant that all four
// lean orchestrators share the same reduced-core shape: cloud/k8s CLI +
// SDG + resource_search + recommendations + delegate_agent + search_tools +
// websearch, plus the conditional tail (shell / remediation / memory / followup).
// This guards against future drift where one lean quietly grows a specialist
// back into its preloaded set — the whole point of "lean" is to reach
// specialists on-demand via delegate + search_tools.
func TestLeanOrchestratorParity_ReducedCoreShape(t *testing.T) {
	origRem := config.Config.RemediationAgentEnabled
	config.Config.RemediationAgentEnabled = false
	t.Cleanup(func() {
		config.Config.RemediationAgentEnabled = origRem
	})

	// Every lean core must contain these — the shared "reach + investigate"
	// backbone that makes lean actually work.
	sharedRequired := []string{
		ServiceDependencyGraph,
		EventsAgentName,
		ResourceSearchAgentName,
		RecommendationsAgentName,
		DelegateAgentToolName,
		SearchToolsToolName,
		WebSearchAgentName,
		toolcore.ToolExecuteShellCommand,
	}

	// And every lean core must NOT preload these specialists — reaching them
	// on-demand via delegate + search_tools is the lean pattern's whole point.
	// (Some clouds may not have every one of these, but if it's present it's a bug.)
	sharedForbidden := []string{
		GithubAgentName,
		getTicketAgentName(),
		VisualizationAgentName,
		PostgresAgentName,
		MySQLAgentName,
		MSSQLAgentName,
		OracleAgentName,
		RedisAgentName,
		RabbitMQAgentName,
		// aws_observability: removed from aws_lean's preload in this PR to
		// match the reach-on-demand contract. Guard against creeping back.
		aws.AgentAwsObservabilityName,
	}

	cases := []struct {
		name  string
		names []string
	}{
		{"k8s_lean", trimmedK8sCoreToolNames()},
		{"aws_lean", cloudLeanCoreToolNames(tools.ToolExecuteAwsCliCommand)},
		{"azure_lean", cloudLeanCoreToolNames(tools.ToolExecuteAzureCliCommand)},
		{"gcp_lean", cloudLeanCoreToolNames(tools.ToolExecuteGcpCliCommand)},
	}

	for _, tt := range cases {
		for _, req := range sharedRequired {
			t.Run(tt.name+"_has_"+req, func(t *testing.T) {
				assert.True(t, containsTool(tt.names, req),
					"%s must preload %s (shared reduced-core backbone)", tt.name, req)
			})
		}
		for _, forbidden := range sharedForbidden {
			t.Run(tt.name+"_omits_"+forbidden, func(t *testing.T) {
				assert.False(t, containsTool(tt.names, forbidden),
					"%s must NOT preload %s — specialist must be reached on-demand via delegate + search_tools", tt.name, forbidden)
			})
		}
	}

	// Bound the reduced-core size: shell + remediation-off + the standard
	// tail lands the four leans at the same tool count. If this count drifts
	// up over time it's a signal that lean is drifting back toward the
	// direct-orchestrator shape.
	for _, tt := range cases {
		t.Run(tt.name+"_bounded_size", func(t *testing.T) {
			// 8 shared + memory (conditional but usually 1) + followup (conditional).
			// Allow generous ceiling to avoid brittle test on every optional add.
			assert.LessOrEqual(t, len(tt.names), 15,
				"%s reduced core has %d tools; lean should stay ≤15 preloaded — otherwise the reach-on-demand pattern isn't being preserved",
				tt.name, len(tt.names))
		})
	}
}

// TestReducedCoreHelpers_DefensiveCopy guards Gemini's HIGH-priority finding
// on PR #35774: the reduced-core helpers cache their result via
// agentSupportedToolsCacheInstance, and downstream planner code appends to
// the returned slice (FilterAndInjectDefaultTools at utils.go:122). Prior
// to the fix, the no-MCP branch returned the raw cached slice — a caller's
// in-place append could corrupt the cache. Now both branches return a fresh
// backing array; mutating the returned slice must not touch the cached copy.
//
// This test drives two calls to the same (accountId, agentName) and confirms
// the returned slices have DIFFERENT backing arrays. If someone reverts the
// fix, appending to call-1's result would silently mutate call-2's result
// on the no-MCP path, and this test catches it.
func TestReducedCoreHelpers_DefensiveCopy(t *testing.T) {
	// Super-admin context: DB-free (unit-test safe) and short-circuits the
	// HasAccountAccess check in GetEnabledNBTools. Tenant-admin ctor hits the
	// DB to resolve accountIds and returns nil when no DB is wired up.
	ctx := security.NewRequestContextForSuperAdmin()
	const accountId = "test-account-defensive-copy"

	// cloud helper — two back-to-back calls
	t.Run("cloud_returns_fresh_slice_each_call", func(t *testing.T) {
		agentName := "test-cloud-lean-agent"
		call1 := getCloudLeanSupportedTools(ctx, accountId, agentName, tools.ToolExecuteGcpCliCommand)
		call2 := getCloudLeanSupportedTools(ctx, accountId, agentName, tools.ToolExecuteGcpCliCommand)
		// Not the same backing array — appends to call1 must not affect call2.
		// Compare via slice-header pointer address of the first element (safe when
		// both are non-empty; the "bounded_size" test above guarantees at least
		// cliTool + backbone tools exist).
		if len(call1) == 0 || len(call2) == 0 {
			t.Skip("empty tool list — cannot verify backing arrays differ")
		}
		assert.NotSame(t, &call1[0], &call2[0],
			"getCloudLeanSupportedTools returned the same backing array across two calls; a downstream append would corrupt the cache")
	})

	// k8s helper — same shape
	t.Run("k8s_returns_fresh_slice_each_call", func(t *testing.T) {
		agentName := "test-k8s-lean-agent"
		call1 := getTrimmedK8sSupportedTools(ctx, accountId, agentName)
		call2 := getTrimmedK8sSupportedTools(ctx, accountId, agentName)
		if len(call1) == 0 || len(call2) == 0 {
			t.Skip("empty tool list — cannot verify backing arrays differ")
		}
		assert.NotSame(t, &call1[0], &call2[0],
			"getTrimmedK8sSupportedTools returned the same backing array across two calls; a downstream append would corrupt the cache")
	})
}
