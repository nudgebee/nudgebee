package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAvailableRdsInstances(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")
	cfg, err := getAwsConfigFromAccount(context.Background(), providers.Account{
		AccountNumber: testAWSAccountNumber,
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

func TestNormalizeRdsEngineForPricing(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"mysql", "MySQL"},
		{"MYSQL", "MySQL"},
		{"mariadb", "MariaDB"},
		{"postgres", "PostgreSQL"},
		{"postgresql", "PostgreSQL"},
		{"aurora-mysql", "Aurora MySQL"},
		{"aurora-postgresql", "Aurora PostgreSQL"},
		{"oracle-ee", "Oracle"},
		{"oracle-se2", "Oracle"},
		{"oracle-ee-cdb", "Oracle"},
		{"sqlserver-ee", "SQL Server"},
		{"sqlserver-se", "SQL Server"},
		{"sqlserver-ex", "SQL Server"},
		{"sqlserver-web", "SQL Server"},
		{"db2-ae", "Db2"},
		{"db2-se", "Db2"},
		{"", ""},
		// Pre-canonicalized values are also accepted.
		{"PostgreSQL", "PostgreSQL"},
		{"MySQL", "MySQL"},
		// Unknown engines fall through unchanged.
		{"some-future-engine", "some-future-engine"},
	}
	for _, c := range cases {
		got := normalizeRdsEngineForPricing(c.in)
		assert.Equal(t, c.want, got, "input=%q", c.in)
	}
}

func TestSynthesizeRdsInstanceTypeDetailsRoundtripsThroughGetPricingValue(t *testing.T) {
	attrs := map[string]any{
		"databaseEngine":   "MySQL",
		"deploymentOption": "Single-AZ",
		"instanceType":     "db.t4g.micro",
		"memory":           "1 GiB",
		"vcpu":             "2",
	}
	details := synthesizeRdsInstanceTypeDetails(0.016, attrs)

	// product.attributes round-trip
	product, ok := details["product"].(map[string]any)
	assert.True(t, ok)
	a, ok := product["attributes"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "1 GiB", a["memory"])

	// terms shape — must be readable by getPricingValue
	price, err := getPricingValue(details)
	assert.Nil(t, err)
	// Float formatting may not be exact; compare as string for determinism.
	assert.Equal(t, "0.016", strconv.FormatFloat(price, 'f', -1, 64))
}

func TestSynthesizeRdsInstanceTypeDetailsHandlesEmptyAttributes(t *testing.T) {
	details := synthesizeRdsInstanceTypeDetails(0, map[string]any{})
	price, err := getPricingValue(details)
	assert.Nil(t, err)
	assert.Equal(t, 0.0, price)
}
