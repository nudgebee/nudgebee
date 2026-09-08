//go:build e2e

package agents

import (
	"encoding/json"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUnifiedSearchAgent_Execute validates the end-to-end unified search flow.
//
// Bot-block / CAPTCHA validation note:
// The isCrawlContentUsable check (bot-blocks, login walls, short pages) cannot be
// exercised from a local Mac because most sites only block requests that originate
// from known cloud-provider CIDR ranges (AWS, GCP, etc.). To validate that behaviour,
// run this test from a staging environment deployed on AWS/GCP where the egress IP
// is recognized as a datacenter IP and triggers Cloudflare/DDoS-Guard challenges.
func TestUnifiedSearchAgent_Execute(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		name      string
		SessionId string
		Query     string
		AccountId string
		UserId    string
		AgentName string
		WantTool  string
		WantURL   string
	}{
		{
			name:      "Web Search Query",
			SessionId: "ut-unified-search-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "release notes of latest version of kubernetes",
			WantTool:  "web_search",
		},
		{
			name:      "Direct URL Crawl",
			SessionId: "ut-unified-search-2",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Can you review the content of https://kubernetes.io/docs/concepts/overview/what-is-kubernetes/",
			WantTool:  "crawl",
			WantURL:   "https://kubernetes.io/docs/concepts/overview/what-is-kubernetes/",
		},
		{
			name:      "Ambiguous Query (Internal + External)",
			SessionId: "ut-unified-search-3",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "how to configure loki and what agents can help me with it?",
		},
		{
			name:      "Legacy Name Error Message Search",
			SessionId: "ut-unified-search-legacy-error",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			AgentName: "websearch_old",
			Query:     `Kubernetes "pull access denied" "authorization failed" docker image loki secrets`,
			WantTool:  "web_search",
		},
		{
			name:      "Nudgebee Documentation Page Review",
			SessionId: "ut-unified-search-doc-review",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Can you review docs in https://docs.nudgebee.com/docs/features/api/ and let us know if you find issues from usability perspective",
			WantTool:  "crawl",
			WantURL:   "https://docs.nudgebee.com/docs/features/api/",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.AccountId == "" || tc.UserId == "" {
				t.Skip("TEST_ACCOUNT and TEST_USER are required for live search tests")
			}

			agentName := tc.AgentName
			if agentName == "" {
				agentName = WebSearchAgentName
			}
			agent, ok := core.GetNBAgent(sc, agentName, tc.AccountId, "")
			require.True(t, ok)
			require.IsType(t, UnifiedSearchAgent{}, agent)

			// Clean up previous session
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			require.NoError(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

			require.NoError(t, err)
			assert.Equal(t, WebSearchAgentName, resp.AgentName)
			assert.NotEmpty(t, resp.Query)
			require.NotEmpty(t, resp.Response)

			// Verify that citations/references are present (manually appended if needed)
			assert.True(t, strings.Contains(strings.ToLower(resp.Response[0]), "references") || strings.Contains(resp.Response[0], "http"), "Response should contain references or URLs")

			// Check the selected branch, not just a non-empty answer.
			require.NotEmpty(t, resp.AgentStepResponse)
			if tc.WantTool != "" {
				var found bool
				for _, step := range resp.AgentStepResponse {
					call := step.Call.FunctionCall
					if call == nil || call.Name != tc.WantTool {
						continue
					}
					found = true
					if tc.WantURL != "" {
						var args struct {
							URL string `json:"url"`
						}
						require.NoError(t, json.Unmarshal([]byte(call.Arguments), &args))
						assert.Equal(t, tc.WantURL, args.URL)
					}
				}
				assert.True(t, found, "expected a %s invocation", tc.WantTool)
			}
		})
	}
}
