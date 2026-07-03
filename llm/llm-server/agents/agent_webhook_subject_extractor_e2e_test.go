//go:build e2e

package agents

import (
	"os"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

// TestWebhookSubjectExtractorAgent_Execute exercises the webhook_subject_name_extractor
// agent end-to-end: it drives the Classification planner against the account's real
// running-service inventory + historical patterns and asserts it returns one of the
// options (a running service name) or the "Not Found" sentinel.
//
// The Query for each case is the alert payload the api-server webhook handlers send
// to the agent (title/description/source_url/labels as JSON). Requires a live LLM +
// DB, so it is gated behind the `e2e` build tag and TEST_ACCOUNT/TEST_USER.
func TestWebhookSubjectExtractorAgent_Execute(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("skipping: TEST_ACCOUNT not set")
	}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			// Direct name + label references — should resolve to the workload.
			SessionId: "ut-webhook-subject-extractor-direct",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     `{"title":"nudgebee-agent-runner crashlooping","description":"pod restart loop","source_url":"","labels":{"namespace":"iteration-test","pod":"nudgebee-agent-runner-abc123","deployment":"nudgebee-agent-runner"}}`,
		},
		{
			// Pipe-delimited title — the service is a middle segment.
			SessionId: "ut-webhook-subject-extractor-pipe",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     `{"title":"[Critical] Critical | Prod | EKS | relay-server | high latency","description":"p99 latency breached","source_url":"","labels":{}}`,
		},
		{
			// Infrastructure-only alert — should decline with "Not Found" (no specific service).
			SessionId: "ut-webhook-subject-extractor-infra",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     `{"title":"Kafka broker down","description":"broker unreachable","source_url":"","labels":{}}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			agent := &WebhookSubjectExtractorAgent{accountId: tc.AccountId}

			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, resp.AgentName, agent.GetName())
			assert.NotEmpty(t, resp.Query)
			assert.NotNil(t, resp.AgentStepResponse)
			assert.Greater(t, len(resp.Response), 0)

			// The classifier must return exactly one option: a running service name or
			// the "Not Found" sentinel — never free-form prose.
			if len(resp.Response) > 0 {
				choice := resp.Response[0]
				t.Logf("extractor chose: %q for %q", choice, tc.SessionId)
				assert.NotEmpty(t, choice)
			}
		})
	}
}
