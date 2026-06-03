package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPostgresDebugAgent_TestRag(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-postgres-chain-4",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "can you optimize - select id,resourse_id from cloud_resourses where resourse_id = 'my-app/pod/example-pod-abc12-xyz34' and tenant = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa' and account = 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb'",
			},
		}
	for _, tc := range testCases {
		postgresChain := newPostgresAgent(tc.AccountId)

		prompt := postgresChain.GetSystemPrompt(sc, core.NBAgentRequest{
			Query:          tc.Query,
			AccountId:      tc.AccountId,
			ConversationId: tc.SessionId,
			UserId:         tc.UserId,
		})

		assert.NotNil(t, prompt)
	}

}
