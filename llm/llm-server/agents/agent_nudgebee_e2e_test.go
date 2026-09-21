//go:build e2e

package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type expectedNudgebeeToolCall struct {
	Name              string
	ParameterFragment string
	ResponseFragment  string
	MaxOccurrences    int
}

func TestNudgebeeDebugAgent_Execute(t *testing.T) {
	if os.Getenv("TEST_TENANT") == "" || os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" {
		t.Skip("integration test requires TEST_TENANT, TEST_ACCOUNT and TEST_USER")
	}

	testCases :=
		[]struct {
			SessionId                    string
			Query                        string
			AccountId                    string
			UserId                       string
			ExpectedToolCalls            []expectedNudgebeeToolCall
			MaxToolCalls                 int
			ExpectedFinalAnswerFragments []string
		}{
			{
				SessionId:                    "ut-nudgebee-chain-11",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "show me active accounts",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_accounts_list", ParameterFragment: `"status":"active"`}},
				MaxToolCalls:                 1,
				ExpectedFinalAnswerFragments: []string{"active", "account"},
			},
			{
				SessionId:                    "ut-nudgebee-chain-22",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "show me active k8s accounts",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_accounts_list", ParameterFragment: `"cloud_provider":"K8s"`}},
				MaxToolCalls:                 1,
				ExpectedFinalAnswerFragments: []string{"active", "kubernetes", "account"},
			},
			{
				SessionId:                    "ut-nudgebee-chain-33",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "How nany github integrations",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_integrations_count", ParameterFragment: `"type":"github"`}},
				MaxToolCalls:                 1,
				ExpectedFinalAnswerFragments: []string{"github", "integration"},
			},
			{
				SessionId:                    "ut-nudgebee-chain-44",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "What is a Nudgebee account?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 4}},
				MaxToolCalls:                 4,
				ExpectedFinalAnswerFragments: []string{"account"},
			},
			{
				SessionId: "ut-nudgebee-chain-55",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "What is an integration in Nudgebee, and how many GitHub integrations do I have?",
				ExpectedToolCalls: []expectedNudgebeeToolCall{
					{Name: "nudgebee_docs_search", MaxOccurrences: 3},
					{Name: "nudgebee_integrations_count", ParameterFragment: `"type":"github"`},
				},
				MaxToolCalls:                 4,
				ExpectedFinalAnswerFragments: []string{"integration", "github"},
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(
			os.Getenv("TEST_TENANT"),
			tc.UserId,
			[]string{tc.AccountId},
		)
		agent := &NudgebeeAgent{accountId: tc.AccountId}

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		require.NoError(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		require.NoError(t, err)

		assert.Equal(t, agent.GetName(), resp.AgentName)
		assert.Equal(t, tc.Query, resp.Query)
		require.NotEmpty(t, resp.Response)
		responseLower := strings.ToLower(strings.Join(resp.Response, "\n"))
		assert.NotContains(t, responseLower, "requesting user is required")
		assert.NotContains(t, responseLower, "i will check")
		assert.NotContains(t, responseLower, "i am unable to retrieve")
		for _, fragment := range tc.ExpectedFinalAnswerFragments {
			assert.Contains(t, responseLower, fragment)
		}

		detail, err := core.GetConversationDao().GetConversationAgentDetail(tc.SessionId, tc.AccountId, resp.AgentId, "")
		require.NoError(t, err)
		require.NotEmpty(t, detail.ToolCalls, "Nubi must ground live-state answers in a tool call")
		assert.LessOrEqual(t, len(detail.ToolCalls), tc.MaxToolCalls)

		matchedExpectedCalls := make(map[int]int, len(tc.ExpectedToolCalls))
		allowedToolNames := make(map[string]bool, len(tc.ExpectedToolCalls))
		for _, expected := range tc.ExpectedToolCalls {
			allowedToolNames[expected.Name] = true
		}
		for _, toolCall := range detail.ToolCalls {
			assert.True(t, strings.HasPrefix(toolCall.ToolName, "nudgebee_"), "unexpected tool: %s", toolCall.ToolName)
			assert.True(t, allowedToolNames[toolCall.ToolName], "unexpected Nudgebee tool for query: %s", toolCall.ToolName)
			assert.Equal(t, "success", toolCall.Status, "tool %s failed: %s", toolCall.ToolName, toolCall.Response)
			assert.False(t, toolCall.IsError)
			assert.NotEmpty(t, toolCall.Response)

			for i, expected := range tc.ExpectedToolCalls {
				if toolCall.ToolName != expected.Name {
					continue
				}
				if expected.ParameterFragment != "" && !strings.Contains(toolCall.Parameters, expected.ParameterFragment) {
					continue
				}
				if expected.ResponseFragment != "" && !strings.Contains(toolCall.Response, expected.ResponseFragment) {
					continue
				}
				matchedExpectedCalls[i]++
			}
		}
		for i, expected := range tc.ExpectedToolCalls {
			assert.GreaterOrEqual(t, matchedExpectedCalls[i], 1, "expected grounded %s call was not recorded", expected.Name)
			maxOccurrences := expected.MaxOccurrences
			if maxOccurrences == 0 {
				maxOccurrences = 1
			}
			assert.LessOrEqual(t, matchedExpectedCalls[i], maxOccurrences, "tool %s was called redundantly", expected.Name)
		}
	}
}
