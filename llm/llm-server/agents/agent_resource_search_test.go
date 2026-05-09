package agents

import (
	"encoding/json"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceSearchAgent_Execute(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-resource-search-agent-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "find pods for the llm-server app in the nudgebee namespace",
		},
		{
			SessionId: "ut-resource-search-agent-2",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "I'm looking for a service, maybe I misspelled it as 'servce'",
		},
		{
			SessionId: "ut-resource-search-cross-platform",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "find all postgres instances across my k8s cluster and cloud accounts",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			agent := newResourceSearchAgent(tc.AccountId)
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.NoError(t, err, "Cleanup failed for session %s", tc.SessionId)

			resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.NoError(t, err, "Failed to handle conversation session")
			assert.NotNil(t, resp, "Response should not be nil")

			assert.Equal(t, resp.AgentName, agent.GetName(), "Agent name mismatch")
			assert.NotEmpty(t, resp.Query, "Query should not be empty")
			assert.NotNil(t, resp.AgentStepResponse, "Agent step response should not be nil")
			assert.Greater(t, len(resp.Response), 0, "Response should not be empty")
		})
	}
}

func TestResourceSearchAgent_Execute2(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-resource-search-agent-3",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     `{"namespace": "od", "search_type": "suggestions"}`,
		},
		{
			SessionId: "ut-resource-search-agent-4",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Find the resource named 'llm server' and identify its type (Deployment, Pod, etc.) and namespace.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			agent := newResourceSearchAgent(tc.AccountId)
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.NoError(t, err, "Cleanup failed for session %s", tc.SessionId)

			resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.NoError(t, err, "Failed to handle conversation session")
			assert.NotNil(t, resp, "Response should not be nil")

			assert.Equal(t, resp.AgentName, agent.GetName(), "Agent name mismatch")
			assert.NotEmpty(t, resp.Query, "Query should not be empty")
			assert.NotNil(t, resp.AgentStepResponse, "Agent step response should not be nil")
			assert.Greater(t, len(resp.Response), 0, "Response should not be empty")
		})
	}
}

// ---------------------------------------------------------------------------
// Pure-logic unit tests for agent relevance helpers.
// These tests do NOT require a live cluster or TEST_ACCOUNT.
// ---------------------------------------------------------------------------

func TestExtractResourceQueryTerms(t *testing.T) {
	tests := []struct {
		query    string
		wantAll  []string // terms that MUST be present
		wantNone []string // terms that MUST NOT be present
	}{
		{
			query:    "find pods for the llm-server app",
			wantAll:  []string{"llm-server", "llm"},
			wantNone: []string{"find", "for", "the", "pod", "pods", "app"},
		},
		{
			query:    "search for llm server deployment",
			wantAll:  []string{"llm", "deployment"},
			wantNone: []string{"search", "for", "server"}, // "server" is generic
		},
		{
			query:    "find all postgres instances across my cluster",
			wantAll:  []string{"postgres"},
			wantNone: []string{"find", "all", "instances", "across", "cluster"},
		},
		{
			query:    "",
			wantAll:  []string{},
			wantNone: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := extractResourceQueryTerms(tt.query)
			gotSet := make(map[string]bool, len(got))
			for _, g := range got {
				gotSet[g] = true
			}
			for _, want := range tt.wantAll {
				assert.True(t, gotSet[want], "expected term %q to be present in %v", want, got)
			}
			for _, notWant := range tt.wantNone {
				assert.False(t, gotSet[notWant], "expected term %q to be absent from %v", notWant, got)
			}
		})
	}
}

func TestResourceNameMatchesTerms(t *testing.T) {
	tests := []struct {
		name     string
		terms    []string
		expected bool
	}{
		{"llm-server-abc123", []string{"llm", "llm-server"}, true},
		{"system:controller:resourcequota-controller", []string{"llm", "llm-server"}, false},
		{"system:resource-tracker", []string{"llm", "llm-server"}, false},
		{"postgres-primary-0", []string{"postgres"}, true},
		{"my-api-server", []string{"llm", "llm-server"}, false},
		{"", []string{"llm"}, false},
		{"anything", []string{}, false},
		// Cloud results that were incorrectly returned for "llm-server"
		{"lipi-games-resources-mobile-application-public", []string{"llm", "llm-server"}, false},
		{"resource-observer-scheduler", []string{"llm", "llm-server"}, false},
		{"gcp_billing_export_resource_v1_01766B", []string{"llm", "llm-server"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resourceNameMatchesTerms(tt.name, tt.terms)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestResourceSearchAgent_SearchTypeSelection(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" {
		t.Skip("TEST_ACCOUNT and TEST_USER must be set for this test")
	}

	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")

	testCases := []struct {
		name               string
		query              string
		expectedSearchType string
	}{
		{
			name:               "standard_service_search",
			query:              "find services related to elasticsearch or opensearch in the cluster",
			expectedSearchType: "suggestions",
		},
		{
			name:               "standard_daemonset_search",
			query:              "Find the DaemonSet named fluent-bit in all namespaces",
			expectedSearchType: "suggestions",
		},
		{
			name:               "explicit_typo_fuzzy",
			query:              "I think I misspelled it, find podss",
			expectedSearchType: "fuzzy",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			agent := newResourceSearchAgent(accountId)
			sessionId := "test-search-type-" + tc.name

			// Clean up previous conversation
			_ = core.DeleteConversationBySession(sessionId, accountId, userId)

			resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId, tc.query)
			require.NoError(t, err)
			require.NotNil(t, resp)

			// Check tool calls in AgentStepResponse
			foundExpectedCall := false
			for _, step := range resp.AgentStepResponse {
				if step.Call.FunctionCall != nil && step.Call.FunctionCall.Name == tools.ToolResourceSearch {
					var input map[string]interface{}
					err := json.Unmarshal([]byte(step.Call.FunctionCall.Arguments), &input)
					require.NoError(t, err)

					searchType, ok := input["search_type"].(string)
					if ok && searchType == tc.expectedSearchType {
						foundExpectedCall = true
						break
					}
				}
			}

			assert.True(t, foundExpectedCall, "Expected to find a call to %s with search_type '%s' for query: %s", tools.ToolResourceSearch, tc.expectedSearchType, tc.query)
		})
	}
}
