//go:build e2e

package agents

import (
	"os"
	"strings"
	"testing"

	agentasserts "nudgebee/llm/agents/asserts"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/analytics"
	"nudgebee/llm/security"

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
				Query:     "Why is my current account's datadog-dev-alert integration not connected?",
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
			// Product-docs answers. Each pins a fact that NuBi previously got wrong or
			// that a docs page now states; a failure means retrieval or the docs drifted.
			{
				SessionId:                    "ut-nudgebee-docs-71",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "Is Azure Cosmos DB supported in NudgeBee cloud accounts?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"microsoft.documentdb"},
			},
			{
				SessionId:                    "ut-nudgebee-docs-72",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "How do I re-sync a Confluence knowledge base in NudgeBee?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"retrigger"},
			},
			{
				SessionId:                    "ut-nudgebee-docs-73",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "In the NudgeBee knowledge base, what is the difference between SOP and Fact?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"sop", "fact"},
			},
			{
				SessionId:                    "ut-nudgebee-docs-74",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "How do I make one NudgeBee agent always follow a specific short runbook on every run?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"prioritize knowledge", "always load"},
			},
			{
				SessionId:                    "ut-nudgebee-docs-75",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "What is the Soul tab in b-Cortex used for?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"tone"},
				ExpectedLLMClaims:            []string{"The answer describes Soul as a style profile the user writes (tone, verbosity, reasoning), not as a memory or persona NuBi learns."},
			},
			{
				SessionId:                    "ut-nudgebee-docs-77",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "Where does a NudgeBee custom tool actually run?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"pod"},
			},
			{
				SessionId:                    "ut-nudgebee-docs-78",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "Why can't I see the Create Rule button on the tenant-wide Triage Rules page?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"account"},
				ExpectedLLMClaims:            []string{"The answer says triage rules are created from a specific cluster or cloud account's Triage Rules tab, not the tenant-wide page."},
			},
			{
				SessionId:                    "ut-nudgebee-docs-79",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "How does NudgeBee decide whether an alert is P0, P1, P2 or P3?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"80", "60", "40"},
			},
			{
				SessionId:                    "ut-nudgebee-docs-80",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "In a NudgeBee automation, how do I use the output of a previous task and read a secret?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"tasks[", "secrets."},
			},
			{
				SessionId:                    "ut-nudgebee-docs-81",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "How do I read a field from the JSON body of an automation webhook in a task?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"webhook_json"},
			},
			{
				SessionId:                    "ut-nudgebee-docs-82",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "My AWS account was connected in read-only mode. Can I still get real-time EventBridge events into NudgeBee?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"connect eventbridge"},
			},
			{
				SessionId:                    "ut-nudgebee-docs-83",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "How do I stop or delete an Auto Optimize rule in NudgeBee?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"disable"},
				ExpectedLLMClaims:            []string{"The answer says Auto Optimize rules cannot be deleted and are stopped by disabling them, which does not revert changes already applied."},
			},
			{
				SessionId:                    "ut-nudgebee-docs-84",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "I added a self-hosted VM account and installed the proxy agent, but no VMs show up in Infra > VM. Why?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"discovery"},
			},
			{
				SessionId:                    "ut-nudgebee-docs-85",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "My self-hosted NudgeBee install fails with Unknown key in nudgebee_secret. What does that mean?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"additional_env_vars"},
			},
			{
				SessionId:                    "ut-nudgebee-docs-86",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "If I close a Jira ticket that was created from a NudgeBee recommendation, what happens to the recommendation?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"closed"},
				ExpectedLLMClaims:            []string{"The answer says the linked recommendation is closed when the ticket reaches a done or closed status, and reopened when the ticket is rejected or cancelled."},
			},
			{
				SessionId:                    "ut-nudgebee-docs-87",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "Can a NudgeBee application group include workloads from more than one cluster?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{},
				ExpectedLLMClaims:            []string{"The answer says an application group is limited to a single cluster."},
			},
			{
				SessionId:                    "ut-nudgebee-docs-88",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "If I turn on Memory across conversations in NudgeBee, where do I edit the account context?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"b-cortex"},
				ExpectedLLMClaims:            []string{"The answer says account context is edited under b-Cortex Knowledge, not under Admin AI & Tools, when memory is on."},
			},
			{
				SessionId:                    "ut-nudgebee-docs-89",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "Which chat tools can I talk to NuBi from?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 3}},
				MaxToolCalls:                 3,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"slack", "teams"},
				ExpectedLLMClaims:            []string{"The answer says you can chat with NuBi from Slack and Microsoft Teams."},
			},
			// #42036: support questions must not turn a missing doc match or a
			// partial list (configured integrations, event filters) into "not supported".
			{
				SessionId:                    "ut-nudgebee-docs-90",
				AccountId:                    os.Getenv("TEST_ACCOUNT"),
				UserId:                       os.Getenv("TEST_USER"),
				Query:                        "Does Nudgebee support Azure Cosmos DB?",
				ExpectedToolCalls:            []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 2}},
				MaxToolCalls:                 2,
				RequireExternalReference:     true,
				ExpectedFinalAnswerFragments: []string{"microsoft.documentdb"},
				ForbiddenFinalFragments:      []string{"not supported", "does not support", "doesn't support"},
				ExpectedLLMClaims:            []string{"The answer says Nudgebee supports Azure Cosmos DB."},
			},
			{
				SessionId:               "ut-nudgebee-docs-91",
				AccountId:               os.Getenv("TEST_ACCOUNT"),
				UserId:                  os.Getenv("TEST_USER"),
				Query:                   "Does Nudgebee support AWS DynamoDB?",
				ExpectedToolCalls:       []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 2}},
				MaxToolCalls:            2,
				ForbiddenFinalFragments: []string{"not supported", "does not support", "doesn't support"},
				ExpectedLLMClaims:       []string{"The answer does not say that Nudgebee lacks support for AWS DynamoDB, and does not use the tenant's configured integrations as the list of supported services."},
			},
			{
				SessionId:               "ut-nudgebee-docs-92",
				AccountId:               os.Getenv("TEST_ACCOUNT"),
				UserId:                  os.Getenv("TEST_USER"),
				Query:                   "Does Nudgebee support Azure Cache for Redis?",
				ExpectedToolCalls:       []expectedNudgebeeToolCall{{Name: "nudgebee_docs_search", MaxOccurrences: 2}},
				MaxToolCalls:            2,
				ForbiddenFinalFragments: []string{"not supported", "does not support", "doesn't support"},
				ExpectedLLMClaims:       []string{"The answer says Nudgebee supports Azure Cache for Redis."},
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

			detail, err := analytics.GetStore().GetConversationAgentDetail(tc.SessionId, tc.AccountId, resp.AgentId, "")
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

// TEST_AGENTLESS_ACCOUNT must identify an agentless account whose normalized
// health is unknown while raw compatibility fields report CONNECTED and a
// last_connected_at value. This fixture reproduces the deployed precedence bug.
func TestNudgebeeAgent_AgentlessHealthPrecedence(t *testing.T) {
	accountID := os.Getenv("TEST_AGENTLESS_ACCOUNT")
	userID := os.Getenv("TEST_USER")
	if os.Getenv("TEST_TENANT") == "" || accountID == "" || userID == "" {
		t.Skip("requires TEST_TENANT, TEST_USER, and TEST_AGENTLESS_ACCOUNT")
	}

	const sessionID = "ut-nudgebee-agentless-precedence-1"
	query := "Troubleshoot this selected account health. Stay within this account."
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userID, []string{accountID})
	agent := &NudgebeeAgent{accountId: accountID}
	require.NoError(t, core.DeleteConversationBySession(sessionID, accountID, userID))

	resp, err := core.HandleConversationSessionRequest(sc, agent, userID, accountID, sessionID, query)
	require.NoError(t, err)
	require.NotEmpty(t, resp.Response)

	detail, err := analytics.GetStore().GetConversationAgentDetail(sessionID, accountID, resp.AgentId, "")
	require.NoError(t, err)
	require.Len(t, detail.ToolCalls, 1)
	call := detail.ToolCalls[0]
	assert.Equal(t, "nudgebee_agent_health_get", call.ToolName)
	assert.JSONEq(t, `{}`, call.Parameters)
	for _, fragment := range []string{
		`"deployment_model":"agentless_cloud"`, `"overall_health":"unknown"`,
		`"status":"CONNECTED"`, `"last_connected_at":`,
	} {
		assert.Contains(t, call.Response, fragment)
	}
	agentasserts.LLMClaims(t, sc, []string{
		"The answer reports agentless synchronization health as unknown despite the raw connected status and last_connected_at compatibility fields, and it does not invent a root cause.",
	}, strings.Join(resp.Response, "\n"))
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
