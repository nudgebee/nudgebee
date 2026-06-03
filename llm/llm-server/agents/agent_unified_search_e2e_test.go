//go:build e2e

package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
	}{
		{
			name:      "Web Search Query",
			SessionId: "ut-unified-search-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "release notes of latest version of kubernetes",
		},
		{
			name:      "Direct URL Crawl",
			SessionId: "ut-unified-search-2",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Can you review the content of https://kubernetes.io/docs/concepts/overview/what-is-kubernetes/",
		},
		{
			name:      "Ambiguous Query (Internal + External)",
			SessionId: "ut-unified-search-3",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "how to configure loki and what agents can help me with it?",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.AccountId == "" {
				t.Skip("Skipping test as TEST_ACCOUNT is not set")
			}

			agent := newUnifiedSearchAgent(tc.AccountId)

			// Clean up previous session
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

			assert.Nil(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, WebSearchAgentName, resp.AgentName)
			assert.NotEmpty(t, resp.Query)
			assert.Greater(t, len(resp.Response), 0)

			// Verify that citations/references are present (manually appended if needed)
			assert.True(t, strings.Contains(strings.ToLower(resp.Response[0]), "references") || strings.Contains(resp.Response[0], "http"), "Response should contain references or URLs")

			// Verify that parallel tool invocations were tracked
			assert.NotNil(t, resp.AgentStepResponse)
			assert.Greater(t, len(resp.AgentStepResponse), 0)
		})
	}
}
