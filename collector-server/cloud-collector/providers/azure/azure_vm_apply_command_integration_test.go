//go:build integration
// +build integration

package azure

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

// Azure VM ApplyCommand integration test.
//
// Drives (*azureProvider).ApplyCommand directly against a real VM.
//
// Required env vars:
//
//	AZURE_INTEGRATION_RESOURCE_ID = full resource ID, e.g.
//	  /subscriptions/.../resourcegroups/.../providers/microsoft.compute/virtualmachines/<vm>
//	AZURE_INTEGRATION_REGION      = e.g. eastus2
//	AZURE_INTEGRATION_COMMANDS    = comma-separated (default: "start") — start/stop/reboot
//	AZURE_CLIENT_ID, AZURE_CLIENT_SECRET, AZURE_TENANT_ID, AZURE_SUBSCRIPTION_ID
//	  — picked up via the env-var fallback in getAzureSessionFromAccount.
//
// Skipped when AZURE_INTEGRATION_RESOURCE_ID is unset. Run with:
// go test -tags integration -run AzureVMApplyCommand ./providers/azure/...
func TestAzureVMApplyCommand_LiveVM(t *testing.T) {
	resourceID := os.Getenv("AZURE_INTEGRATION_RESOURCE_ID")
	if resourceID == "" {
		t.Skip("AZURE_INTEGRATION_RESOURCE_ID not set — skipping live integration test")
	}
	if os.Getenv("AZURE_CLIENT_ID") == "" || os.Getenv("AZURE_CLIENT_SECRET") == "" ||
		os.Getenv("AZURE_TENANT_ID") == "" || os.Getenv("AZURE_SUBSCRIPTION_ID") == "" {
		t.Skip("AZURE_CLIENT_ID/SECRET/TENANT_ID/SUBSCRIPTION_ID required for live test")
	}

	region := envOrAzure("AZURE_INTEGRATION_REGION", "eastus2")
	commands := splitCSVAzure(envOrAzure("AZURE_INTEGRATION_COMMANDS", "start"))

	provider := &azureProvider{}
	ctx := newAzureTestContext(t)
	// Empty Account triggers the env-var fallback in getAzureSessionFromAccount.
	account := providers.Account{}

	for _, cmd := range commands {
		t.Run(cmd, func(t *testing.T) {
			req := providers.ApplyCommandRequest{
				ServiceName: "microsoft.compute/virtualmachines",
				Region:      region,
				ResourceId:  resourceID,
				Command:     cmd,
			}
			resp, err := provider.ApplyCommand(ctx, account, req)
			require.NoError(t, err, "ApplyCommand returned error: %v / response=%+v", err, resp)
			require.True(t, resp.Success, "ApplyCommand reported failure: %s", resp.Message)
			t.Logf("azure %s → %s", cmd, resp.Message)
		})
	}
}

func envOrAzure(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitCSVAzure(s string) []string {
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

type azureTestContext struct {
	ctx    context.Context
	logger *slog.Logger
	sec    *security.SecurityContext
}

func newAzureTestContext(t *testing.T) *azureTestContext {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t.Cleanup(cancel)
	return &azureTestContext{
		ctx:    ctx,
		logger: slog.Default(),
	}
}

func (c *azureTestContext) GetContext() context.Context                   { return c.ctx }
func (c *azureTestContext) GetLogger() *slog.Logger                       { return c.logger }
func (c *azureTestContext) GetSecurityContext() *security.SecurityContext { return c.sec }
