package account

import (
	"errors"
	"fmt"

	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"os"
	"testing"
	"time"

	_ "nudgebee/collector/cloud/providers/aws"
	_ "nudgebee/collector/cloud/providers/gcloud"

	"github.com/stretchr/testify/assert"
)

func TestStoreUsageAws(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreUsage(ctx, "6c008cf8-4d79-4999-8447-573a697d0652", time.January, 2026)
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreUsageAzure(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreUsage(ctx, "c3a2d91d-17b7-4df4-93a0-7a777a399e29", time.October, 2025)
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreUsageAzure2(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := discoverAndStoreAccountResources(ctx, "c3a2d91d-17b7-4df4-93a0-7a777a399e29")
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreUsageGCloud(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreUsage(ctx, os.Getenv("TEST_ACCOUNT"), time.November, 2025)
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

// TestAgentStatusForUsageSync pins that an account with no cost reporting is
// not reported as a disconnected agent.
//
// Onboarding accepts an AWS account with no usable Cost & Usage Report, so that
// account is working as intended — events, resources and recommendations all
// sync, and only spend is empty. Marking the agent disconnected for it showed
// customers a red "The Agent is not connected" banner sitting directly above a
// feature table where every row read Connected, and would trip agent-health
// alerting for a supported configuration.
//
// A genuine failure must still mark the agent disconnected.
func TestAgentStatusForUsageSync(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want AgentStatus
	}{
		{
			name: "clean sync stays connected",
			want: AgentStatusConnected,
		},
		{
			name: "cost simply not configured stays connected",
			err:  providers.ErrCostNotConfigured,
			want: AgentStatusConnected,
		},
		{
			name: "unsupported provider stays connected",
			err:  errors.ErrUnsupported,
			want: AgentStatusConnected,
		},
		{
			// The sentinel is wrapped in practice (resolveCostReportDefinition
			// wraps the AWS denial), so matching must survive unwrapping.
			name: "wrapped not-configured stays connected",
			err:  fmt.Errorf("resolve cost report: %w", providers.ErrCostNotConfigured),
			want: AgentStatusConnected,
		},
		{
			name: "a real failure disconnects",
			err:  errors.New("failed to fetch cost report manifest: AccessDenied on the CUR bucket"),
			want: AgentStatusDisconnected,
		},
		{
			// Azure: throttled, or failed for an unrecognised reason, after the
			// provider confirmed the credential can still read the subscription.
			name: "cost fetch failed with a working credential stays connected",
			err:  fmt.Errorf("%w: rate limited: %w", providers.ErrCostFetchFailedCredentialOK, errors.New("RESPONSE 429")),
			want: AgentStatusConnected,
		},
		{
			// Azure: the credential can no longer read the subscription. This is
			// returned unwrapped, so it must still disconnect.
			name: "broken credential disconnects",
			err:  errors.New("The service principal can no longer read subscription X: RESPONSE 403"),
			want: AgentStatusDisconnected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentStatusForUsageSync(tt.err); got != tt.want {
				t.Fatalf("agentStatusForUsageSync() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCostFetchFailedCredentialOKIsNotBenign pins that a cost failure with a
// working credential keeps the account connected but is still a failure for
// the queue: the job is dead-lettered and "Sync now" reports the error, so a
// month that fails outside the daily window leaves a trace.
func TestCostFetchFailedCredentialOKIsNotBenign(t *testing.T) {
	err := fmt.Errorf("%w: throttled", providers.ErrCostFetchFailedCredentialOK)
	if isBenignCostOutcome(err) {
		t.Fatal("isBenignCostOutcome() = true, want false: the job must still fail and dead-letter")
	}
	if got := agentStatusForUsageSync(err); got != AgentStatusConnected {
		t.Fatalf("agentStatusForUsageSync() = %q, want %q", got, AgentStatusConnected)
	}
}
