//go:build integration
// +build integration

package aws

import (
	"context"
	"log/slog"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// AWS ApplyCommand integration test.
//
// Drives (*awsProvider).ApplyCommand directly against a real AWS account, the
// same path the cloud-collector takes when called from /v1/cloud/apply_command.
// Use this instead of the curl-via-Hasura loop when validating provider-side
// changes locally.
//
// Required env vars:
//
//	AWS_INTEGRATION_INSTANCE_ID  = e.g. i-0455574dd895c0849
//	AWS_INTEGRATION_REGION       = e.g. us-east-1
//	AWS_INTEGRATION_SERVICE      = AmazonEC2 (default; set to override)
//	AWS_INTEGRATION_COMMANDS     = comma-separated list (default: "start") — any of start/stop/reboot
//	AWS_PROFILE or AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY = standard AWS auth
//
// Skipped when AWS_INTEGRATION_INSTANCE_ID is unset, so go test ./... stays
// fast and offline. Run with: go test -tags integration -run AwsApplyCommand
// ./providers/aws/...
func TestAwsApplyCommand_LiveInstance(t *testing.T) {
	instanceID := os.Getenv("AWS_INTEGRATION_INSTANCE_ID")
	if instanceID == "" {
		t.Skip("AWS_INTEGRATION_INSTANCE_ID not set — skipping live integration test")
	}

	region := envOr("AWS_INTEGRATION_REGION", "us-east-1")
	serviceName := envOr("AWS_INTEGRATION_SERVICE", "AmazonEC2")
	commands := splitCSV(envOr("AWS_INTEGRATION_COMMANDS", "start"))

	provider := &awsProvider{}
	ctx := newAwsTestContext(t)
	account := providers.Account{
		Region: &region,
		// AccessKey/AccessSecret intentionally nil — getAwsConfigFromAccount
		// then falls through to AWS_PROFILE / default chain.
	}

	for _, cmd := range commands {
		t.Run(cmd, func(t *testing.T) {
			req := providers.ApplyCommandRequest{
				ServiceName: serviceName,
				Region:      region,
				ResourceId:  instanceID,
				Command:     cmd,
			}
			resp, err := provider.ApplyCommand(ctx, account, req)
			require.NoError(t, err, "ApplyCommand returned error: %v / response=%+v", err, resp)
			require.True(t, resp.Success, "ApplyCommand reported failure: %s", resp.Message)
			t.Logf("aws %s → %s", cmd, resp.Message)
		})
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitCSV(s string) []string {
	out := []string{}
	current := ""
	for _, r := range s {
		if r == ',' {
			if current != "" {
				out = append(out, current)
			}
			current = ""
			continue
		}
		current += string(r)
	}
	if current != "" {
		out = append(out, current)
	}
	return out
}

type awsTestContext struct {
	ctx    context.Context
	logger *slog.Logger
	sec    *security.SecurityContext
}

func newAwsTestContext(t *testing.T) *awsTestContext {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	return &awsTestContext{
		ctx:    ctx,
		logger: slog.Default(),
		sec:    nil,
	}
}

func (c *awsTestContext) GetContext() context.Context                   { return c.ctx }
func (c *awsTestContext) GetLogger() *slog.Logger                       { return c.logger }
func (c *awsTestContext) GetSecurityContext() *security.SecurityContext { return c.sec }
