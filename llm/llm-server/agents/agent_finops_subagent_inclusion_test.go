package agents

import (
	"testing"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// Unmentioned cost/spend questions default-route to the K8s debug agents (the
// router reaches finops only via an @mention on a fresh conversation), so both
// the full orchestrator and the lean core must preload finops as a callable
// sub-agent — otherwise cost questions get answered with kubectl/metrics
// guesswork or from the recommendations list alone. Cloud orchestrators are
// deliberately not covered here; extending the mount to them is a separate
// decision.
func TestK8sDebugAgents_PreloadFinops(t *testing.T) {
	const testAccountId = "test-finops-subagent-inclusion"
	sc := security.NewRequestContextForSuperAdmin()

	resolvedNames := func(nbTools []toolcore.NBTool) []string {
		names := make([]string, 0, len(nbTools))
		for _, tool := range nbTools {
			names = append(names, tool.Name())
		}
		return names
	}

	t.Run("lean core names include finops", func(t *testing.T) {
		assert.True(t, containsTool(trimmedK8sCoreToolNames(), FinOpsAgentName),
			"k8s_lean must preload finops — cost questions default-route here and nothing steers on-demand search to it")
	})

	t.Run("lean resolved set includes finops", func(t *testing.T) {
		names := resolvedNames(getTrimmedK8sSupportedTools(sc, testAccountId, "k8s_lean_finops_inclusion"))
		assert.Contains(t, names, FinOpsAgentName)
	})

	t.Run("full orchestrator resolved set includes finops", func(t *testing.T) {
		names := resolvedNames(getSupportedTools(sc, testAccountId, "k8s_debug_finops_inclusion", KubectlAgentName))
		assert.Contains(t, names, FinOpsAgentName)
	})
}
