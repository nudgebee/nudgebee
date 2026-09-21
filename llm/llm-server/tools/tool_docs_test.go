package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
	"nudgebee/llm/tools/core"
)

func TestSearchDocsBlocksWhenPolicyDisabled(t *testing.T) {
	for _, policy := range []string{"disabled", "llm_only"} {
		t.Run("searchDocumentation/"+policy, func(t *testing.T) {
			tc := core.NbToolContext{
				AccountId:               "acct-123",
				KnowledgePolicy:         policy,
				KnowledgePolicyResolved: true,
			}
			resp, refs, err := searchDocumentation(tc, "checkout triage runbook")
			require.NoError(t, err)
			require.Empty(t, refs)
			require.Equal(t, "No documentation found for your query. Please try a different search term.", resp)
		})

		t.Run("DocsAgentTool.Call/"+policy, func(t *testing.T) {
			tool := DocsAgentTool{}
			tc := core.NbToolContext{
				AccountId:               "acct-123",
				KnowledgePolicy:         policy,
				KnowledgePolicyResolved: true,
			}
			resp, err := tool.Call(tc, core.NBToolCallRequest{Command: "checkout triage runbook"})
			require.NoError(t, err)
			require.Empty(t, resp.References)
			require.Equal(t, "No documentation found for your query. Please try a different search term.", resp.Data)
		})
	}
}

func TestSearchDocsEmptyQuery(t *testing.T) {
	tc := core.NbToolContext{AccountId: "acct-123"}
	_, _, err := searchDocumentation(tc, "   ")
	require.Error(t, err)
	require.Contains(t, err.Error(), "query cannot be empty")
}
