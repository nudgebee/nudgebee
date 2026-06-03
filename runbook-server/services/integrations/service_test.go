package integrations

import (
	"nudgebee/runbook/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCreateWorkflowWebhookTrigger(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_USER_ID"), []string{os.Getenv("TEST_ACCOUNT_ID")})
	triggerResponse, err := CreateWorkflowWebhookTrigger(ctx, os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_ACCOUNT_ID"), "integration-test-trigger")
	assert.NoError(t, err)
	assert.NotEmpty(t, triggerResponse.Token)
	assert.NotEmpty(t, triggerResponse.IntegrationId)

	err = DeleteWorkflowWebhookTrigger(ctx, "test-account", "test-integration")
	assert.NoError(t, err)
}
