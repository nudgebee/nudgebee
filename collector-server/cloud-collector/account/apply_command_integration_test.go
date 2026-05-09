//go:build integration
// +build integration

package account

import (
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// account.ApplyCommand integration test.
//
// Drives the same code path as the HTTP /v1/cloud/apply_command handler, but
// in-process: (a) loads the cloud account record from the metastore using the
// configured DB URL, (b) decrypts the stored credentials with the configured
// encryption key, (c) routes through the provider's ApplyCommand. This is the
// fullest end-to-end provider test we can run without bringing up Hasura.
//
// Run from the cloud-collector directory so viper picks up the local .env:
//
//	cd collector-server/cloud-collector
//	TEST_INTEGRATION_AWS_RESOURCE=i-0455574dd895c0849 \
//	  go test -tags integration -v -count=1 -run TestAccountApplyCommand_Live ./account/...
//
// Per-provider env vars (any subset — providers without all three vars set are
// skipped, so you can test one cloud at a time):
//
//	AWS:
//	  TEST_INTEGRATION_AWS_ACCOUNT   (cloud_accounts.id; default: $TEST_ACCOUNT)
//	  TEST_INTEGRATION_AWS_TENANT    (default: $TEST_TENANT)
//	  TEST_INTEGRATION_AWS_RESOURCE  (e.g. i-0455574dd895c0849)
//	  TEST_INTEGRATION_AWS_REGION    (default: us-east-1)
//	  TEST_INTEGRATION_AWS_SERVICE   (default: AmazonEC2)
//	Azure:
//	  TEST_INTEGRATION_AZURE_ACCOUNT
//	  TEST_INTEGRATION_AZURE_TENANT  (default: $TEST_TENANT)
//	  TEST_INTEGRATION_AZURE_RESOURCE (full /subscriptions/.../virtualmachines/<vm>)
//	  TEST_INTEGRATION_AZURE_REGION  (default: eastus2)
//	GCP:
//	  TEST_INTEGRATION_GCP_ACCOUNT   (default: $TEST_GCP_ACCOUNT)
//	  TEST_INTEGRATION_GCP_TENANT    (default: $TEST_TENANT)
//	  TEST_INTEGRATION_GCP_RESOURCE  (numeric instance id)
//	  TEST_INTEGRATION_GCP_REGION    (default: us-central1)
//
//	TEST_INTEGRATION_COMMANDS — comma-separated, default "start". start/stop/reboot.

func TestAccountApplyCommand_LiveAWS(t *testing.T) {
	runApplyCommandIntegration(t, "aws",
		envOrEnv("TEST_INTEGRATION_AWS_ACCOUNT", "TEST_ACCOUNT"),
		envOrEnv("TEST_INTEGRATION_AWS_TENANT", "TEST_TENANT"),
		os.Getenv("TEST_INTEGRATION_AWS_RESOURCE"),
		envOr("TEST_INTEGRATION_AWS_REGION", "us-east-1"),
		envOr("TEST_INTEGRATION_AWS_SERVICE", "AmazonEC2"),
	)
}

func TestAccountApplyCommand_LiveAzure(t *testing.T) {
	runApplyCommandIntegration(t, "azure",
		os.Getenv("TEST_INTEGRATION_AZURE_ACCOUNT"),
		envOrEnv("TEST_INTEGRATION_AZURE_TENANT", "TEST_TENANT"),
		os.Getenv("TEST_INTEGRATION_AZURE_RESOURCE"),
		envOr("TEST_INTEGRATION_AZURE_REGION", "eastus2"),
		"microsoft.compute/virtualmachines",
	)
}

func TestAccountApplyCommand_LiveGCP(t *testing.T) {
	runApplyCommandIntegration(t, "gcp",
		envOrEnv("TEST_INTEGRATION_GCP_ACCOUNT", "TEST_GCP_ACCOUNT"),
		envOrEnv("TEST_INTEGRATION_GCP_TENANT", "TEST_TENANT"),
		os.Getenv("TEST_INTEGRATION_GCP_RESOURCE"),
		envOr("TEST_INTEGRATION_GCP_REGION", "us-central1"),
		"Compute Engine",
	)
}

func runApplyCommandIntegration(t *testing.T, label, accountID, tenantID, resourceID, region, serviceName string) {
	if accountID == "" || tenantID == "" || resourceID == "" {
		t.Skipf("%s: account/tenant/resource env not set — skipping (account=%q tenant=%q resource=%q)",
			label, accountID, tenantID, resourceID)
	}

	commands := splitCSV(envOr("TEST_INTEGRATION_COMMANDS", "start"))
	ctx := security.NewRequestContextForTenantAdmin(tenantID)

	for _, cmd := range commands {
		t.Run(cmd, func(t *testing.T) {
			req := providers.ApplyCommandRequest{
				ServiceName: serviceName,
				Region:      region,
				ResourceId:  resourceID,
				Command:     cmd,
			}
			resp, err := ApplyCommand(ctx, accountID, req)
			require.NoError(t, err, "%s ApplyCommand error: %v / response=%+v", label, err, resp)
			require.True(t, resp.Success, "%s ApplyCommand reported failure: %s", label, resp.Message)
			t.Logf("%s %s → %s", label, cmd, resp.Message)
		})
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envOrEnv returns the value of `primary` if set, else falls back to the value
// of `fallbackEnv` (NOT a literal default — used to inherit from TEST_ACCOUNT
// etc. that's already in the project .env).
func envOrEnv(primary, fallbackEnv string) string {
	if v := os.Getenv(primary); v != "" {
		return v
	}
	return os.Getenv(fallbackEnv)
}

func splitCSV(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
