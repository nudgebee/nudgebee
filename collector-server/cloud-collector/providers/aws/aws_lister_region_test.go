package aws

import (
	"context"
	"net/http"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/assert"
)

// A function that is given a region must call that region. These built their
// clients on the account's default region instead: the Lambda and RDS scans
// returned the default region's resources for every region and stored them
// again under the region being scanned, and the lookups reported "not found"
// for anything living outside the default region.
func TestCallsTargetTheRequestedRegion(t *testing.T) {
	const otherRegion = "eu-west-1" // anything but the us-east-1 default
	account := providers.Account{AccountNumber: fakeAwsAccount}
	tests := []struct {
		name string
		call func(ctx providers.CloudProviderContext)
	}{
		{"lambda scan", func(ctx providers.CloudProviderContext) {
			_, _ = (&awsLambda{}).GetResources(ctx, account, otherRegion)
		}},
		{"rds scan", func(ctx providers.CloudProviderContext) {
			_, _ = (&amazonRds{}).GetResources(ctx, account, otherRegion)
		}},
		{"rds lookup", func(ctx providers.CloudProviderContext) {
			_, _ = (&amazonRds{}).DescribeResource(ctx, account, otherRegion, "orders-db")
		}},
		{"load balancer lookup", func(ctx providers.CloudProviderContext) {
			_, _ = (&awsElb{}).DescribeResource(ctx, account, otherRegion, "app/orders/50dc6c495c0c9188")
		}},
		{"load balancer service map", func(ctx providers.CloudProviderContext) {
			_, _ = (&awsElb{}).GetServiceMap(ctx, account, otherRegion, "app/orders/50dc6c495c0c9188")
		}},
		{"kms service map", func(ctx providers.CloudProviderContext) {
			_, _ = (&awsKms{}).GetServiceMap(ctx, account, otherRegion, "1234abcd-12ab-34cd-56ef-1234567890ab")
		}},
		{"secrets manager service map", func(ctx providers.CloudProviderContext) {
			_, _ = (&awsSecretsManager{}).GetServiceMap(ctx, account, otherRegion, "orders/db-password")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var signedFor []string
			startFakeAws(t, func(req fakeAwsRequest) (int, string) {
				signedFor = append(signedFor, req.Region)
				if action := req.Params["Action"]; action != "" {
					return http.StatusOK, awsQueryResponse(action, "", "")
				}
				// One JSON body that every JSON-protocol call here can read: an empty
				// function list, and the ARN the secrets lookup dereferences.
				return http.StatusOK, `{"Functions":[],"ARN":"arn:aws:secretsmanager:eu-west-1:123456789012:secret:orders/db-password"}`
			})

			tt.call(&targetedFetchTestCtx{ctx: context.Background()})

			assert.NotEmpty(t, signedFor, "expected at least one AWS call")
			for _, region := range signedFor {
				assert.Equal(t, otherRegion, region, "every call must target the region asked for")
			}
		})
	}
}
