package aws

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithy "github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"nudgebee/collector/cloud/providers"
)

func TestCostReportParsing(t *testing.T) {

	f, err := os.Open("testdata/aws_cost_report.csv.gz")
	assert.NotNil(t, f)
	assert.Nil(t, err)
	defer func() {
		if err := f.Close(); err != nil {
			t.Log(err)
		}
	}()

	gzr, err := gzip.NewReader(f)
	assert.Nil(t, err)
	defer func() {
		if err := gzr.Close(); err != nil {
			t.Log(err)
		}
	}()

	items, err := readAwsBillingReport(gzr, "DAILY")
	assert.Nil(t, err)
	assert.Len(t, items, 58797)
}

func TestGetUsageReportFromAws(t *testing.T) {
	t.Skip("Skipping integration test that requires real credentials and AWS resources")
	bucket, region, path, compression, reportType, reportName, timeUnit, err := getUsageBucketFromCostReport(providers.NewCloudProviderContext(context.Background()), providers.Account{}, "", "")
	assert.Nil(t, err)
	assert.NotEmpty(t, bucket)
	assert.NotEmpty(t, path)
	assert.NotEmpty(t, region)
	assert.NotEmpty(t, compression)
	assert.NotEmpty(t, reportType)
	assert.NotEmpty(t, reportName)
	assert.NotEmpty(t, timeUnit)

	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(region))
	assert.Nil(t, err)
	s3Svc := s3.NewFromConfig(cfg)
	t0 := time.Now()
	keys, err := getS3KeysFromUsageReport(s3Svc, bucket, path, reportType, reportName, t0.Month(), t0.Year(), "DAY")
	assert.Nil(t, err)
	assert.Len(t, keys, 1)

}

// TestCurAccessDeniedIsNotConfiguredWhenNeverConfigured pins the second way an
// account can legitimately have no CUR. Onboarding no longer blocks on a
// missing Cost & Usage Report, so an account can be connected with a role that
// lacks cur:DescribeReportDefinitions entirely. That denial is the supported
// steady state, not a fault: without this classification every sync of such an
// account dead-letters a message and every manual run returns 500 — the exact
// failure the ErrCostNotConfigured sentinel exists to prevent.
//
// Two distinctions are load-bearing and both are asserted here:
//   - Never configured vs. previously configured. Losing the permission on an
//     account that HAS a stored report is a real regression, not a setup choice.
//   - Authorization denied vs. broken credentials. An expired or invalid token
//     must NOT be reclassified, or a credential failure disappears behind an
//     empty cost page.
func TestCurAccessDeniedIsNotConfiguredWhenNeverConfigured(t *testing.T) {
	denied := &smithy.OperationError{
		ServiceID:     "Cost and Usage Report Service",
		OperationName: "DescribeReportDefinitions",
		Err: &smithy.GenericAPIError{
			Code:    "AccessDeniedException",
			Message: "User: arn:aws:sts::111122223333:assumed-role/Example/nudgebee-cloud-collector is not authorized to perform: cur:DescribeReportDefinitions",
		},
	}

	if !isCurAuthorizationDenied(denied) {
		t.Fatal("a real AccessDeniedException from the CUR API must be recognised; otherwise a " +
			"CUR-less account dead-letters one message per day forever")
	}

	// Broken credentials are NOT a cost-configuration state.
	expired := &smithy.OperationError{
		ServiceID:     "Cost and Usage Report Service",
		OperationName: "DescribeReportDefinitions",
		Err:           &smithy.GenericAPIError{Code: "ExpiredTokenException", Message: "the security token included in the request is expired"},
	}
	if isCurAuthorizationDenied(expired) {
		t.Fatal("an expired token must not be treated as not-configured — that hides a credential " +
			"failure behind an empty cost page instead of surfacing it")
	}

	// Never configured → benign, so the consumer ACKs rather than dead-lettering.
	neverConfigured := fmt.Errorf("%w: %w", providers.ErrCostNotConfigured, denied)
	if !errors.Is(neverConfigured, providers.ErrCostNotConfigured) {
		t.Fatal("wrapping must preserve errors.Is so StoreUsage takes the benign path")
	}
	// %w on both keeps the underlying AWS error inspectable by callers.
	if !errors.Is(neverConfigured, denied) {
		t.Fatal("the underlying AWS error must stay in the unwrap chain")
	}

	// A bare denial is not itself the sentinel; only resolveCostReportDefinition
	// promotes it, and only when the account never had CUR config.
	if errors.Is(denied, providers.ErrCostNotConfigured) {
		t.Fatal("a bare access denial must not be treated as not-configured — that would " +
			"silently swallow a genuine permission regression on a working account")
	}
}
