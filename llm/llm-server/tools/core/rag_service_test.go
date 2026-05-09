package core

import (
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAgentRag(t *testing.T) {

	testAgentName := "nb-test-rag"
	testAccountId := os.Getenv("TEST_ACCOUNT")
	testTenantId := os.Getenv("TEST_TENANT")
	testUserId := os.Getenv("TEST_USER")
	sc := security.NewRequestContextForTenantAccountAdmin(testTenantId, testUserId, []string{testAccountId})

	testRagData := `
		question: How do you list all Compute Engine instances in a specific project and zone using the gcloud CLI?
		answer: gcloud compute instances list --project=[YOUR_PROJECT_ID] --zone=[YOUR_ZONE]
		question: Get All projects
		answer: gcloud compute instances list --project=my-awesome-project --zone=us-central1-a	
	`

	err := DeleteAgentRags(sc, testAccountId, testAgentName)
	assert.Nil(t, err)

	rags, err := ListAgentRags(sc, testAccountId, testAgentName)
	assert.Nil(t, err)
	assert.Empty(t, rags)

	agentRag, err := CreateAgentRag(sc, testAccountId, testAgentName, testRagData, "", "sample.txt")
	assert.Nil(t, err)
	assert.NotEmpty(t, agentRag.AgentId)

	rags, err = ListAgentRags(sc, testAccountId, testAgentName)
	assert.Nil(t, err)
	assert.NotEmpty(t, rags)
}

func TestQueryRAG(t *testing.T) {

	testAgentName := "k8s_debug_react"
	testAccountId := os.Getenv("TEST_ACCOUNT")
	testTenantId := os.Getenv("TEST_TENANT")
	testUserId := os.Getenv("TEST_USER")
	sc := security.NewRequestContextForTenantAccountAdmin(testTenantId, testUserId, []string{testAccountId})

	testRagData := `
	question: How do you list all Compute Engine instances in a specific project and zone using the gcloud CLI?
	answer: gcloud compute instances list --project=[YOUR_PROJECT_ID] --zone=[YOUR_ZONE]
	question: Get All projects
	answer: gcloud compute instances list --project=my-awesome-project --zone=us-central1-a	
	`

	agentRag, err := CreateAgentRag(sc, testAccountId, testAgentName, testRagData, "", "sample.txt")
	assert.Nil(t, err)
	assert.NotEmpty(t, agentRag.AgentId)

	// Test query with all parameters
	result := QueryRAG(testUserId, testAccountId, "Get All projects", testAgentName, 5, "conv-123", "msg-456", agentRag.AgentId, false)
	assert.NotNil(t, result, "QueryRAG should return an initialized (non-nil) slice, even if empty")
	assert.Empty(t, result)
}
