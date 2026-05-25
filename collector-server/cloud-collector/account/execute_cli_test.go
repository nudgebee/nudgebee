package account

import (
	"nudgebee/collector/cloud/security"
	"os"
	"testing"

	_ "nudgebee/collector/cloud/providers/aws"
	_ "nudgebee/collector/cloud/providers/azure"
	_ "nudgebee/collector/cloud/providers/gcloud"

	"github.com/stretchr/testify/assert"
)

func TestExecuteCliCommandGCP(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := ExecuteCliCommand(ctx, "569c15cb-962b-44c6-951e-d0730a23c0e8", "gcloud projects list")
	assert.Nil(t, err)
	assert.NotNil(t, response)
}

func TestExecuteCliCommandAWS(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := ExecuteCliCommand(ctx, "49145907-981b-48ad-a67a-08a4e8099cb2", "aws s3 ls | grep nb")
	assert.Nil(t, err)
	assert.NotNil(t, response)
}

func TestExecuteCliCommandAzure(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := ExecuteCliCommand(ctx, "c3a2d91d-17b7-4df4-93a0-7a777a399e29", "az billing account list --output table")
	assert.Nil(t, err)
	assert.NotNil(t, response)
}

func TestExecuteCliVmCommandAzure(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := ExecuteCliCommand(
		ctx,
		"c3a2d91d-17b7-4df4-93a0-7a777a399e29",
		"az vm run-command invoke --resource-group aks-dev-rg --name nudgebee-dev --command-id RunShellScript --scripts 'ls /home'",
	)
	assert.Nil(t, err)
	assert.NotNil(t, response)
}
