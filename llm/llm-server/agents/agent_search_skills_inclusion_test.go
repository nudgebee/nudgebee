package agents

import (
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// Every orchestrator's DEFAULT tool list must carry the knowledge-search tool
// (#34819): retrieval before planning covers the eager path, but an agent that
// discovers mid-investigation it needs knowledge it was not given had no way
// to search for it by query — only exact-name loading, which fails when the
// agent does not know the name. User-pinned tool lists are configured through
// a different branch and are deliberately NOT extended.
func TestOrchestrators_IncludeSearchSkillsTool(t *testing.T) {
	const testAccountId = "test-search-skills-inclusion"
	sc := security.NewRequestContextForSuperAdmin()

	tests := []struct {
		name     string
		getTools func() []toolcore.NBTool
	}{
		{"k8s_orchestrator", func() []toolcore.NBTool {
			return getSupportedTools(sc, testAccountId, "k8s_debug", KubectlAgentName)
		}},
		{"gcp_orchestrator", func() []toolcore.NBTool {
			return getGcpPlannerSupportedTools(sc, testAccountId)
		}},
		{"azure_orchestrator", func() []toolcore.NBTool {
			return getAzurePlannerSupportedTools(sc, testAccountId)
		}},
		{"aws_orchestrator", func() []toolcore.NBTool {
			return getAwsPlannerSupportedTools(sc, testAccountId, "aws_orchestrator", false)
		}},
		{"datadog_orchestrator", func() []toolcore.NBTool {
			return getDatadogPlannerSupportedTools(sc, testAccountId)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			names := make([]string, 0)
			for _, tool := range tc.getTools() {
				names = append(names, tool.Name())
			}
			assert.Contains(t, names, tools.SearchSkillsToolName)
		})
	}
}
