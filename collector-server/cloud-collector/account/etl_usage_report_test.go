package account

import (
	"nudgebee/collector/cloud/security"
	"os"
	"testing"
	"time"

	_ "nudgebee/collector/cloud/providers/aws"
	_ "nudgebee/collector/cloud/providers/gcloud"

	"github.com/stretchr/testify/assert"
)

func TestStoreUsageAws(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreUsage(ctx, "6c008cf8-4d79-4999-8447-573a697d0652", time.January, 2026)
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreUsageAzure(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreUsage(ctx, "c3a2d91d-17b7-4df4-93a0-7a777a399e29", time.October, 2025)
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreUsageAzure2(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := discoverAndStoreAccountResources(ctx, "c3a2d91d-17b7-4df4-93a0-7a777a399e29")
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreUsageGCloud(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreUsage(ctx, os.Getenv("TEST_ACCOUNT"), time.November, 2025)
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}
