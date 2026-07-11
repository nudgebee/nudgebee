package service

import (
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetResourceID(t *testing.T) {
	testutils.RequireEnv(t, "TEST_TENANT_ID", "TEST_ACCOUNT_ID", "TEST_USER_ID")
	ctx := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_USER_ID"), []string{os.Getenv("TEST_ACCOUNT_ID")})
	resourceId, err := GetResourceID(ctx, os.Getenv("TEST_ACCOUNT_ID"), "nudgebee", "ticket-server", "")
	assert.Nil(t, err)
	assert.NotEmpty(t, resourceId)
}
