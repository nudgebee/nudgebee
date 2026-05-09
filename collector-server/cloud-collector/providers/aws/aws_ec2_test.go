package aws

import (
	"context"
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAvailableEc2Instances(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")
	cfg, err := getAwsConfigFromAccount(context.Background(), providers.Account{
		AccountNumber: "280501305789",
	})
	assert.Nil(t, err)

	instances, err := getAvailableEc2Instances(cfg, "us-east-1", 16, 4, "", "SUSE", "EBS Only")
	assert.Nil(t, err)
	assert.NotNil(t, instances)

	data, err := common.MarshalJson(instances)
	assert.Nil(t, err)
	fmt.Println(string(data))
	t.Log(string(data))
}

func TestPricing(t *testing.T) {
	price, err := getEc2PricesBasedOnPriceList("us-east-1", "m4.xlarge")
	assert.Nil(t, err)
	assert.NotNil(t, price)
}

func TestParseAwsMemoryGiB(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		{"1 GiB", 1.0},
		{"16 GiB", 16.0},
		{"192 GiB", 192.0},
		{"1,952 GiB", 1952.0},
		{"24,576 GiB", 24576.0},
		{"512 MiB", 0.5},
		{"1024 MiB", 1.0},
		{"24 TiB", 24576.0},
		{"16", 16.0}, // bare number assumed GiB
		{"", 0.0},
		{"  ", 0.0},
		{"N/A", 0.0},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := parseAwsMemoryGiB(tc.input)
			assert.Equal(t, tc.expected, got)
		})
	}
}
