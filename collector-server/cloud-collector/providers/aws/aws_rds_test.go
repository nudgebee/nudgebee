package aws

import (
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
)

import "context"

func TestGetAvailableRdsInstances(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")
	cfg, err := getAwsConfigFromAccount(context.Background(), providers.Account{
		AccountNumber: "280501305789",
	})
	assert.Nil(t, err)

	instances, err := getAvailableRdsInstances(cfg, "us-east-1", "PostgreSQL", "2 GiB", "1", "", "Single-AZ")
	assert.Nil(t, err)
	assert.NotNil(t, instances)

	instances, err = getAvailableRdsInstances(cfg, "us-east-1", "PostgreSQL", "8 GiB", "2", "", "Single-AZ")
	assert.Nil(t, err)
	assert.NotNil(t, instances)

	alternateInsatnces, err := alternateInstancesBasedOnPricing(instances, instances[0])
	assert.Nil(t, err)
	assert.NotNil(t, alternateInsatnces)
}
