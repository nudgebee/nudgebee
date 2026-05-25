package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAwsResources(t *testing.T) {
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
