//go:build e2e

package agents

import (
	agentasserts "nudgebee/llm/agents/asserts"
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
			EnforceToolOrder             bool
			RequireExternalReference     bool
			ExpectedFinalAnswerFragments []string
			ForbiddenFinalFragments      []string
			ExpectedLLMClaims            []string
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
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 1}},
				MaxToolCalls:                 1,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"account"},
			},
			{
				SessionId: "ut-nudgebee-chain-55",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "What is an integration in Nudgebee, and how many GitHub integrations do I have?",
				ExpectedToolCalls: []expectedNudgebeeToolCall{
					{Name: "nudgebee_docs_search", MaxOccurrences: 1},
					{Name: "nudgebee_integrations_count", ParameterFragment: `"type":"github"`},
				},
				MaxToolCalls:                 2,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"integration", "github"},
			},
			{
				SessionId:                    "ut-nudgebee-chain-66",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "Why is my Datadog integration not connected?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_integration_get_status", ParameterFragment: `"name":"Datadog"`}},
				MaxToolCalls:                 1,
				ExpectedFinalAnswerFragments: []string{"datadog", "integration"},
			},
			{
				SessionId: "ut-nudgebee-chain-67",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Why is my datadog-dev-alert integration not connected?",
				ExpectedToolCalls: []expectedNudgebeeToolCall{
					{Name: "nudgebee_integration_get_status", ParameterFragment: `"name":"datadog-dev-alert"`},
					{Name: "nudgebee_integration_diagnose", ResponseFragment: `"reason_code"`},
				},
				MaxToolCalls:                 2,
				EnforceToolOrder:             true,
				ExpectedFinalAnswerFragments: []string{"datadog-dev-alert", "integration"},
				ForbiddenFinalFragments:      []string{"was disabled on", "has remained disabled"},
				ExpectedLLMClaims: []string{
					"The answer does not use an integration record's updated_at value as evidence of when the integration entered its current status.",
				},
			},
			{
				SessionId:                    "ut-nudgebee-chain-68",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "Check Nudgebee agent health",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_agent_health_get"}},
				MaxToolCalls:                 1,
				ExpectedFinalAnswerFragments: []string{"agent"},
			},
		}
	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
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
			for _, fragment := range tc.ForbiddenFinalFragments {
				assert.NotContains(t, responseLower, strings.ToLower(fragment))
			}
			if len(tc.ExpectedLLMClaims) > 0 {
				agentasserts.LLMClaims(t, sc, tc.ExpectedLLMClaims, strings.Join(resp.Response, "\n"))
			}
			if tc.RequireExternalReference {
				require.NotEmpty(t, resp.References, "documentation answers must propagate source references")
				assert.NotEmpty(t, resp.References[0].Url)
			}

			detail, err := core.GetConversationDao().GetConversationAgentDetail(tc.SessionId, tc.AccountId, resp.AgentId, "")
			require.NoError(t, err)
			require.NotEmpty(t, detail.ToolCalls, "Nubi must ground live-state answers in a tool call")
			assert.LessOrEqual(t, len(detail.ToolCalls), tc.MaxToolCalls)

			matchedExpectedCalls := make(map[int]int, len(tc.ExpectedToolCalls))
			allowedToolNames := make(map[string]bool, len(tc.ExpectedToolCalls))
			expectationSignatures := make(map[string]bool, len(tc.ExpectedToolCalls))
			for _, expected := range tc.ExpectedToolCalls {
				signature := strings.Join([]string{expected.Name, expected.ParameterFragment, expected.ResponseFragment}, "\x00")
				require.False(t, expectationSignatures[signature],
					"duplicate expectation for tool %q; express identical repeated calls with MaxOccurrences", expected.Name)
				expectationSignatures[signature] = true
				allowedToolNames[expected.Name] = true
			}
			lastExpectedIndex := -1
			for _, toolCall := range detail.ToolCalls {
				assert.True(t, strings.HasPrefix(toolCall.ToolName, "nudgebee_"), "unexpected tool: %s", toolCall.ToolName)
				assert.True(t, allowedToolNames[toolCall.ToolName], "unexpected Nudgebee tool for query: %s", toolCall.ToolName)
				assert.Equal(t, "success", toolCall.Status, "tool %s failed: %s", toolCall.ToolName, toolCall.Response)
				assert.False(t, toolCall.IsError)
				assert.NotEmpty(t, toolCall.Response)

				matches := matchingExpectedNudgebeeToolCalls(tc.ExpectedToolCalls, toolCall.ToolName, toolCall.Parameters, toolCall.Response)
				require.LessOrEqual(t, len(matches), 1,
					"tool call ambiguously matched multiple expectations; use non-overlapping parameter or response fragments: %v", matches)
				if len(matches) == 1 {
					i := matches[0]
					matchedExpectedCalls[i]++
					if tc.EnforceToolOrder && matchedExpectedCalls[i] == 1 {
						require.Greater(t, i, lastExpectedIndex, "expected dependent Nudgebee tools in declared order")
						lastExpectedIndex = i
					}
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
		})
	}
}

func matchingExpectedNudgebeeToolCalls(expectedCalls []expectedNudgebeeToolCall, name, parameters, response string) []int {
	matches := make([]int, 0, 1)
	for i, expected := range expectedCalls {
		if name != expected.Name {
			continue
		}
		if expected.ParameterFragment != "" && !strings.Contains(parameters, expected.ParameterFragment) {
			continue
		}
		if expected.ResponseFragment != "" && !strings.Contains(response, expected.ResponseFragment) {
			continue
		}
		matches = append(matches, i)
	}
	return matches
}

func TestMatchingExpectedNudgebeeToolCallsDetectsAmbiguity(t *testing.T) {
	expected := []expectedNudgebeeToolCall{
		{Name: "nudgebee_integration_get_status", ParameterFragment: `"name":"datadog"`},
		{Name: "nudgebee_integration_get_status", ParameterFragment: `"name":"datadog-dev-alert"`},
	}
	assert.Equal(t, []int{0}, matchingExpectedNudgebeeToolCalls(expected,
		"nudgebee_integration_get_status", `{"name":"datadog"}`, "response"))
	assert.Equal(t, []int{1}, matchingExpectedNudgebeeToolCalls(expected,
		"nudgebee_integration_get_status", `{"name":"datadog-dev-alert"}`, "response"))

	overlapping := []expectedNudgebeeToolCall{
		{Name: "nudgebee_integration_get_status"},
		{Name: "nudgebee_integration_get_status", ParameterFragment: `"name":"datadog"`},
	}
	assert.Equal(t, []int{0, 1}, matchingExpectedNudgebeeToolCalls(overlapping,
		"nudgebee_integration_get_status", `{"name":"datadog"}`, "response"))
}
