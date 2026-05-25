package crawl

import (
	"context"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAwsCrawl(t *testing.T) {
	ctxt := security.NewRequestContextForUserTenant("af4cb6af-1254-421d-bfa5-ffcfe649017e", "0053b816-4b45-4dcd-a612-19545110f8aa", nil, nil, nil)
	ec2PricingData, err := currentPriceByService(ctxt, "Compute", "us-west-2", "", "")
	assert.Nil(t, err)
	found := false
	for _, data := range ec2PricingData {
		assert.NotNil(t, data)
		if data.ResourceType == "c7a.xlarge" {
			assert.Equal(t, 0.22581, data.ResourceCost)
			found = true
		}
		assert.Equal(t, "us-west-2", data.ResourceRegion)

	}
	assert.True(t, found, "c7a.xlarge not found")
	assert.NotNil(t, ec2PricingData)
}

func TestAwsResourceMeta(t *testing.T) {
	ctxt := security.NewRequestContextForUserTenant("af4cb6af-1254-421d-bfa5-ffcfe649017e", "0053b816-4b45-4dcd-a612-19545110f8aa", nil, nil, nil)
	err := AWsResourceMeta(ctxt)
	assert.Nil(t, err)
}

func TestSpotPricing(t *testing.T) {
	instanceTypes := []string{"c5.xlarge", "c5.large"}
	data, err := getSpotInstancePricing(context.Background(), "us-west-2", instanceTypes)
	assert.Nil(t, err)
	assert.Equal(t, 2, len(data))
	assert.Equal(t, "us-west-2", data[0].Region)
	assert.NotNil(t, data)
}
