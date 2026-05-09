package aws

import (
	"compress/gzip"
	"context"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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
