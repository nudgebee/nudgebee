//go:build integration
// +build integration

package gcloud

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

// GCP Compute Engine ApplyCommand integration test.
//
// Drives (*gcloudProvider).ApplyCommand directly against a real instance.
//
// Required env vars:
//
//	GCP_INTEGRATION_INSTANCE_ID  = numeric instance id, e.g. 38172937967952219
//	GCP_INTEGRATION_REGION       = e.g. us-central1
//	GCP_INTEGRATION_PROJECT      = GCP project id (used as account.AccountNumber)
//	GCP_INTEGRATION_COMMANDS     = comma-separated (default: "start") — start/stop/reboot
//	GOOGLE_APPLICATION_CREDENTIALS = path to a service-account JSON with
//	  compute.instances.{start,stop,reset} permissions on the target.
//
// Skipped when GCP_INTEGRATION_INSTANCE_ID is unset. Run with:
// go test -tags integration -run GcpComputeApplyCommand ./providers/gcloud/...
func TestGcpComputeApplyCommand_LiveInstance(t *testing.T) {
	instanceID := os.Getenv("GCP_INTEGRATION_INSTANCE_ID")
	if instanceID == "" {
		t.Skip("GCP_INTEGRATION_INSTANCE_ID not set — skipping live integration test")
	}
	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
		t.Skip("GOOGLE_APPLICATION_CREDENTIALS not set — skipping live integration test")
	}

	region := envOrGcp("GCP_INTEGRATION_REGION", "us-central1")
	commands := splitCSVGcp(envOrGcp("GCP_INTEGRATION_COMMANDS", "start"))
	projectID := os.Getenv("GCP_INTEGRATION_PROJECT")

	provider := &gcloudProvider{}
	ctx := newGcpTestContext(t)
	// AccountNumber overrides the project_id baked into the credentials JSON.
	// If GCP_INTEGRATION_PROJECT is unset, we leave it empty so the credentials
	// file's own project is used.
	account := providers.Account{AccountNumber: projectID}

	for _, cmd := range commands {
		t.Run(cmd, func(t *testing.T) {
			req := providers.ApplyCommandRequest{
				ServiceName: "Compute Engine",
				Region:      region,
				ResourceId:  instanceID,
				Command:     cmd,
			}
			resp, err := provider.ApplyCommand(ctx, account, req)
			require.NoError(t, err, "ApplyCommand returned error: %v / response=%+v", err, resp)
			require.True(t, resp.Success, "ApplyCommand reported failure: %s", resp.Message)
			t.Logf("gcp %s → %s", cmd, resp.Message)
		})
	}
}

func envOrGcp(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitCSVGcp(s string) []string {
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

type gcpTestContext struct {
	ctx    context.Context
	logger *slog.Logger
	sec    *security.SecurityContext
}

func newGcpTestContext(t *testing.T) *gcpTestContext {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t.Cleanup(cancel)
	return &gcpTestContext{
		ctx:    ctx,
		logger: slog.Default(),
	}
}

func (c *gcpTestContext) GetContext() context.Context                   { return c.ctx }
func (c *gcpTestContext) GetLogger() *slog.Logger                       { return c.logger }
func (c *gcpTestContext) GetSecurityContext() *security.SecurityContext { return c.sec }
