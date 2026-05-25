package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestAwsAgentReWoo_S3BucketPublic(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_AWS_ACCOUNT"), nil)
	tests := []struct {
		UserId    string
		AccountId string
		Query     string
		SessionId string
	}{
		{
			UserId:    os.Getenv("TEST_USER"),
			AccountId: os.Getenv("TEST_AWS_ACCOUNT"),
			Query:     "Can you check all the buckets and identify if there are any public buckets",
			SessionId: "ut-aws-chain-s3-rewoo",
		},
	}

	for _, tc := range tests {

		awsDebugAgentent := newAwsDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, awsDebugAgentent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, awsDebugAgentent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Greater(t, len(resp.AgentStepResponse), 0)
	}
}

func TestAwsAgentReWoo_CostBreakDown(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_AWS_ACCOUNT"), nil)
	tests := []struct {
		UserId    string
		AccountId string
		Query     string
		SessionId string
	}{
		{
			UserId:    os.Getenv("TEST_USER"),
			AccountId: os.Getenv("TEST_AWS_ACCOUNT"),
			Query:     "Can you get me cost breakdown on last month ?",
			SessionId: "ut-aws-chain-ec2-rewoo",
		},
	}

	for _, tc := range tests {
		awsDebugAgentent := newAwsDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, awsDebugAgentent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, awsDebugAgentent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Greater(t, len(resp.AgentStepResponse), 0)
	}
}

func TestAwsAgentReWoo_ECSDetails(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_AWS_ACCOUNT"), nil)
	tests := []struct {
		UserId    string
		AccountId string
		Query     string
		SessionId string
	}{
		{
			UserId:    os.Getenv("TEST_USER"),
			AccountId: os.Getenv("TEST_AWS_ACCOUNT"),
			Query:     "How many ECS clusters and I have, and how many services they are running and how many tasks each service has?",
			SessionId: "ut-aws-chain-lambda-rewoo",
		},
	}

	for _, tc := range tests {
		awsDebugAgentent := newAwsDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, awsDebugAgentent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, awsDebugAgentent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Greater(t, len(resp.AgentStepResponse), 0)
	}
}

func TestAwsAgentReWoo_CloudCost(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), "6c008cf8-4d79-4999-8447-573a697d0652", nil)
	tests := []struct {
		UserId    string
		AccountId string
		Query     string
		SessionId string
	}{
		{
			UserId:    os.Getenv("TEST_USER"),
			AccountId: "6c008cf8-4d79-4999-8447-573a697d0652",
			Query:     "our AWS cost has increased again.. review with respect to last months call and what all things are increasing user: Can you cross check billing differences in last 2 months and tell where cost increase is",
			SessionId: "ut-aws-chain-cost-rewoo",
		},
	}

	for _, tc := range tests {
		awsDebugAgentent := newAwsDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, awsDebugAgentent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, awsDebugAgentent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Greater(t, len(resp.AgentStepResponse), 0)
	}
}

func TestAwsAgentReWoo_CloudCost2(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), "6c008cf8-4d79-4999-8447-573a697d0652", nil)
	tests := []struct {
		UserId    string
		AccountId string
		Query     string
		SessionId string
	}{
		{
			UserId:    os.Getenv("TEST_USER"),
			AccountId: "6c008cf8-4d79-4999-8447-573a697d0652",
			Query:     "our AWS cost has increased again.. Can you cross check billing differences in May and June months and tell where cost increase is",
			SessionId: "ut-aws-chain-cost-rewoo-2",
		},
	}

	for _, tc := range tests {
		awsDebugAgentent := newAwsDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, awsDebugAgentent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, awsDebugAgentent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Greater(t, len(resp.AgentStepResponse), 0)
	}
}

func TestAwsAgentReWoo_AWSDebug(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), nil)
	tests := []struct {
		UserId    string
		AccountId string
		Query     string
		SessionId string
	}{
		{
			UserId:    os.Getenv("TEST_USER"),
			AccountId: "49145907-981b-48ad-a67a-08a4e8099cb2",
			Query:     "I am getting 500 error on my ELB - xray-ecs-dev-1733299795.us-east-1.elb.amazonaws.com. Can you investigate why that is happening",
			SessionId: "ut-aws-chain-cost-rewoo-3",
		},
	}

	for _, tc := range tests {
		awsDebugAgentent := newAwsDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, awsDebugAgentent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, awsDebugAgentent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Greater(t, len(resp.AgentStepResponse), 0)
	}
}

func TestAWSInvestigation(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), nil)
	tests := []struct {
		UserId    string
		AccountId string
		Query     string
		SessionId string
	}{
		{
			UserId:    os.Getenv("TEST_USER"),
			AccountId: os.Getenv("TEST_AWS_ACCOUNT"),
			Query:     "i am having issue with http://demo-frontend-alb-1435077682.us-east-1.elb.amazonaws.com/, I see 500 error. can you investigat?",
			SessionId: "ut-aws-11",
		},
	}

	for _, tc := range tests {
		awsDebugAgentent := newAwsDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, awsDebugAgentent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, awsDebugAgentent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Greater(t, len(resp.AgentStepResponse), 0)
	}
}

func TestAWSInvestigationKubectlInvocation(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), nil)
	tests := []struct {
		UserId    string
		AccountId string
		Query     string
		SessionId string
	}{
		{
			UserId:    os.Getenv("TEST_USER"),
			AccountId: os.Getenv("TEST_AWS_ACCOUNT"),
			Query:     "list pods in nudgebee namespace",
			SessionId: "ut-aws-k8s-12",
		},
	}

	for _, tc := range tests {
		awsDebugAgentent := newAwsDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, awsDebugAgentent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		if resp.Status == core.ConversationStatusWaiting {
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			resp, err = core.HandleConversationSessionRequest(sc, awsDebugAgentent, tc.UserId, tc.AccountId, tc.SessionId, "k8s-dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)

		}

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, awsDebugAgentent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Greater(t, len(resp.AgentStepResponse), 0)
	}
}
