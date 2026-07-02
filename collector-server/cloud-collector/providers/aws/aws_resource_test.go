package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAwsResources(t *testing.T) {
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		t.Skip("Skipping integration test that requires AWS credentials")
	}
	service, _ := GetAwsService("AmazonEC2")
	resource, err := service.GetResources(providers.NewCloudProviderContext(context.Background()), providers.Account{}, "us-east-1")
	assert.Nil(t, err)
	assert.NotNil(t, resource)

	service, _ = GetAwsService("AmazonS3")
	resource, err = service.GetResources(providers.NewCloudProviderContext(context.Background()), providers.Account{}, "us-east-1")
	assert.Nil(t, err)
	assert.NotNil(t, resource)

	service, _ = GetAwsService("AmazonRDS")
	resource, err = service.GetResources(providers.NewCloudProviderContext(context.Background()), providers.Account{}, "us-east-1")
	assert.Nil(t, err)
	assert.NotNil(t, resource)
}
