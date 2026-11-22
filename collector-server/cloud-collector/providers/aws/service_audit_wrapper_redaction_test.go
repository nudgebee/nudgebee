package aws

import (
	"context"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/assert"
)

// fakeLambdaService returns a Lambda resource whose env carries a secret; only
// the two resource fetch methods are used, the rest of awsService stays nil.
type fakeLambdaService struct{ awsService }

func lambdaWithSecret() []providers.Resource {
	return []providers.Resource{{Meta: map[string]any{
		"Environment": map[string]any{"Variables": map[string]any{
			"DB_PASSWORD": "hunter2",
			"QUEUE_URL":   "https://sqs.us-east-1.amazonaws.com/123456789012/orders",
		}},
	}}}
}

func (fakeLambdaService) GetResources(providers.CloudProviderContext, providers.Account, string) ([]providers.Resource, error) {
	return lambdaWithSecret(), nil
}

func (fakeLambdaService) GetResourcesByIds(providers.CloudProviderContext, providers.Account, string, []string) ([]providers.Resource, error) {
	return lambdaWithSecret(), nil
}

// Every AWS service is wrapped (main.go), so both full syncs and realtime
// lookups get redacted meta before anything else reads it.
func TestAuditedServiceRedactsResourceMeta(t *testing.T) {
	svc := &auditedAwsService{inner: fakeLambdaService{}, serviceName: "lambda"}
	ctx := providers.NewCloudProviderContext(context.Background())

	full, err := svc.GetResources(ctx, providers.Account{}, "us-east-1")
	assert.NoError(t, err)
	byID, err := svc.GetResourcesByIds(ctx, providers.Account{}, "us-east-1", []string{"fn"})
	assert.NoError(t, err)

	for _, resources := range [][]providers.Resource{full, byID} {
		vars := resources[0].Meta["Environment"].(map[string]any)["Variables"].(map[string]any)
		assert.Equal(t, providers.RedactedValue, vars["DB_PASSWORD"])
		assert.Equal(t, "https://sqs.us-east-1.amazonaws.com/123456789012/orders", vars["QUEUE_URL"])
	}
}
