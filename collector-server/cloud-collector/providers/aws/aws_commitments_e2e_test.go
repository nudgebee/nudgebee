//go:build e2e
// +build e2e

package aws

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/require"
)

// TestCommitmentsAgainstRealAccount reads commitments from a real AWS account
// through the same functions the recommendation pass uses, and prints what came
// back so it can be compared with the AWS console.
//
// It makes Cost Explorer calls, which AWS bills per request, so it only runs
// when AWS_COMMITMENTS_E2E_ACCOUNT names the account the ambient credentials
// belong to:
//
//	AWS_COMMITMENTS_E2E_ACCOUNT=123456789012 go test -tags e2e ./providers/aws/ -run TestCommitmentsAgainstRealAccount -v
func TestCommitmentsAgainstRealAccount(t *testing.T) {
	accountNumber := os.Getenv("AWS_COMMITMENTS_E2E_ACCOUNT")
	if accountNumber == "" {
		t.Skip("Skipping: set AWS_COMMITMENTS_E2E_ACCOUNT to the AWS account number to run.")
	}
	ctx := &targetedFetchTestCtx{ctx: context.Background()}
	account := providers.Account{AccountNumber: accountNumber, CloudProvider: "AWS"}
	now := time.Now().UTC()

	clients, err := newCommitmentClients(ctx, account)
	require.NoError(t, err)

	started := time.Now()
	commitments, utilizations, coverages, err := collectCommitments(ctx, clients, account, now, savingsPlanUtilizationLookbackDays)
	require.NoError(t, err)
	t.Logf("collected in %s: %d commitments, %d utilizations, %d coverage rows", time.Since(started).Round(time.Millisecond), len(commitments), len(utilizations), len(coverages))

	printed, err := json.MarshalIndent(map[string]any{
		"commitments":  commitments,
		"utilizations": utilizations,
		"coverage":     coverages,
	}, "", "  ")
	require.NoError(t, err)
	t.Logf("read from AWS:\n%s", printed)

	recommendations := providers.CommitmentRecommendations(commitments, utilizations, now)
	recommendations = append(recommendations, providers.CoverageRecommendations(coverages, commitments)...)
	printed, err = json.MarshalIndent(recommendations, "", "  ")
	require.NoError(t, err)
	t.Logf("recommendations:\n%s", printed)

	// Every commitment must carry the identity its recommendation row is keyed
	// on, and a utilization must belong to a commitment that was listed.
	listed := map[string]bool{}
	for _, c := range commitments {
		require.NotEmpty(t, c.ID)
		require.Equal(t, accountNumber, c.AccountID)
		listed[c.ID] = true
	}
	for _, u := range utilizations {
		require.True(t, listed[u.CommitmentID], "utilization for a commitment that was not listed: %s", u.CommitmentID)
	}
}
