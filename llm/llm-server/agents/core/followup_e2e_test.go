//go:build e2e

package core

import (
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestFollowupFollowupOnMultipleResources(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId         string
			Query             string
			AccountId         string
			UserId            string
			FollowupQuery     string
			AdditionalContext string
		}{
			{
				SessionId:         "ut-followup-chain-1",
				AccountId:         os.Getenv("TEST_ACCOUNT"),
				UserId:            os.Getenv("TEST_USER"),
				Query:             "show me logs of services-server",
				FollowupQuery:     "namespace: nudgebee",
				AdditionalContext: "",
			},
		}

	llmAgent := ClarificationAgent{}
	for _, tc := range testCases {
		err := DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := HandleConversationSessionRequest(sc, llmAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)

		// check response
		assert.Equal(t, llmAgent.GetName(), resp.AgentName)
		assert.Equal(t, ConversationStatusWaiting, resp.Status)

		// based on response return followup response and wait for processing
		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		resp, err = HandleConversationSessionRequest(sc, llmAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.FollowupQuery, ConversationSessionRequestWithMessageId(uuid.NullUUID{
			UUID:  messageId,
			Valid: true,
		}), ConversationSessionRequestWithAgentId(uuid.NullUUID{
			UUID:  agentId,
			Valid: true,
		}))

		assert.Nil(t, err)
		assert.Equal(t, ConversationStatusCompleted, resp.Status)

		// review refiend question

		// review ui how it looks

	}
}

func TestRefineFollowup2(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	testCases :=
		[]struct {
			SessionId         string
			Query             string
			AccountId         string
			UserId            string
			AdditionalContext string
		}{
			{
				SessionId:         "ut-followup-chain-5",
				AccountId:         os.Getenv("TEST_ACCOUNT"),
				UserId:            os.Getenv("TEST_USER"),
				Query:             "I have done helm installation sometime back on one of my namespace..recently observed that help is complaining that no installation found.. even thougn i can see workloads running any idea ?",
				AdditionalContext: "",
			},
		}
	for _, tc := range testCases {
		request := NBAgentRequest{
			Query:                 tc.Query,
			AccountId:             tc.AccountId,
			ConversationId:        uuid.NewString(),
			UserId:                tc.UserId,
			MessageId:             uuid.NewString(),
			EnableQueryRefinement: true,
		}
		agent := LLMAgent{}
		followup, err := FollowupRequestForMissingInformation(sc, request, agent)
		assert.Nil(t, err)
		assert.NotNil(t, followup)
		assert.NotEmpty(t, followup.Question)
	}

}
