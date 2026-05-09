package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGetAwsRdsMetrics(t *testing.T) {
	startDate := time.Now().Add(-time.Hour * 24)
	metrics, err := getAwsCloudwatchMetrics(providers.NewCloudProviderContext(context.Background()), providers.Account{
		AccountNumber: "280501305789",
	}, providers.QueryMetricsRequest{
		StartDate:   &startDate,
		ServiceName: "AmazonRDS",
		Region:      "us-east-1",
		Step:        time.Second * 60,
	})
	assert.Nil(t, err)
	assert.NotNil(t, metrics)
}

func TestGetAwsS3Metrics(t *testing.T) {
	startDate := time.Now().Add(-time.Hour * 24 * 10)
	metrics, err := getAwsCloudwatchMetrics(providers.NewCloudProviderContext(context.Background()), providers.Account{
		AccountNumber: "280501305789",
	}, providers.QueryMetricsRequest{
		StartDate:   &startDate,
		ServiceName: "AmazonS3",
		Region:      "us-east-1",
		Step:        time.Second * 60,
	})
	assert.Nil(t, err)
	assert.NotNil(t, metrics)
}

func TestListAwsCloudwatchMetrics_RDS(t *testing.T) {
	resp, err := listAwsCloudwatchMetrics(providers.ListMetricsRequest{
		ServiceName: "AmazonRDS",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Metrics)
	for _, m := range resp.Metrics {
		assert.NotEmpty(t, m.Name)
		assert.Equal(t, "AWS/RDS", m.Namespace)
	}
}

func TestListAwsCloudwatchMetrics_EC2(t *testing.T) {
	resp, err := listAwsCloudwatchMetrics(providers.ListMetricsRequest{
		ServiceName: "AmazonEC2",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Metrics)
	for _, m := range resp.Metrics {
		assert.NotEmpty(t, m.Name)
		assert.Equal(t, "AWS/EC2", m.Namespace)
	}
}

func TestListAwsCloudwatchMetrics_S3(t *testing.T) {
	resp, err := listAwsCloudwatchMetrics(providers.ListMetricsRequest{
		ServiceName: "AmazonS3",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Metrics)
	for _, m := range resp.Metrics {
		assert.NotEmpty(t, m.Name)
		assert.Equal(t, "AWS/S3", m.Namespace)
	}
}

func TestListAwsCloudwatchMetrics_UnknownService(t *testing.T) {
	resp, err := listAwsCloudwatchMetrics(providers.ListMetricsRequest{
		ServiceName: "NonExistentService",
	})
	assert.Nil(t, err)
	assert.Empty(t, resp.Metrics)
}

func TestListAwsCloudwatchMetrics_CaseInsensitive(t *testing.T) {
	resp1, _ := listAwsCloudwatchMetrics(providers.ListMetricsRequest{ServiceName: "AmazonRDS"})
	resp2, _ := listAwsCloudwatchMetrics(providers.ListMetricsRequest{ServiceName: "amazonrds"})
	assert.Equal(t, len(resp1.Metrics), len(resp2.Metrics))
}

func TestListAwsCloudwatchMetrics_WithResourceType(t *testing.T) {
	resp, err := listAwsCloudwatchMetrics(providers.ListMetricsRequest{
		ServiceName:  "AmazonRDS",
		ResourceType: "db",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Metrics)
}

func TestListAwsCloudwatchMetrics_HasStatistics(t *testing.T) {
	resp, err := listAwsCloudwatchMetrics(providers.ListMetricsRequest{
		ServiceName: "AmazonECS",
	})
	assert.Nil(t, err)
	hasStats := false
	for _, m := range resp.Metrics {
		if len(m.Statistics) > 0 {
			hasStats = true
			break
		}
	}
	assert.True(t, hasStats, "at least some ECS metrics should have statistics")
}

func TestGetNamespaceForService(t *testing.T) {
	tests := []struct {
		service   string
		namespace string
	}{
		{"AmazonEC2", "AWS/EC2"},
		{"amazonec2", "AWS/EC2"},
		{"AmazonRDS", "AWS/RDS"},
		{"AmazonS3", "AWS/S3"},
		{"AWSLambda", "AWS/Lambda"},
		{"AmazonECS", "AWS/ECS"},
		{"AWSELB", "AWS/ELB"},
		{"AWSQueueService", "AWS/SQS"},
		{"AmazonSNS", "AWS/SNS"},
		{"AmazonEKS", "AWS/EKS"},
		{"AmazonDynamoDB", "AWS/DynamoDB"},
		{"AmazonRedshift", "AWS/Redshift"},
		{"AmazonCloudFront", "AWS/CloudFront"},
		{"AmazonEFS", "AWS/EFS"},
		{"AmazonES", "AWS/ES"},
		{"NonExistentService", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.service, func(t *testing.T) {
			got := getNamespaceForService(tt.service)
			assert.Equal(t, tt.namespace, got)
		})
	}
}

func TestListAwsCloudwatchMetrics_NewServices(t *testing.T) {
	newServices := []struct {
		name      string
		namespace string
	}{
		{"AWSELB", "AWS/ELB"},
		{"AWSQueueService", "AWS/SQS"},
		{"AmazonSNS", "AWS/SNS"},
		{"AmazonEKS", "AWS/EKS"},
		{"AmazonDynamoDB", "AWS/DynamoDB"},
		{"AmazonRedshift", "AWS/Redshift"},
		{"AmazonCloudFront", "AWS/CloudFront"},
		{"AmazonEFS", "AWS/EFS"},
		{"AmazonES", "AWS/ES"},
	}

	for _, svc := range newServices {
		t.Run(svc.name, func(t *testing.T) {
			resp, err := listAwsCloudwatchMetrics(providers.ListMetricsRequest{
				ServiceName: svc.name,
			})
			assert.Nil(t, err)
			assert.NotEmpty(t, resp.Metrics, "expected metrics for %s", svc.name)
			for _, m := range resp.Metrics {
				assert.NotEmpty(t, m.Name)
				assert.Equal(t, svc.namespace, m.Namespace)
			}
		})
	}
}

// TestGetAwsRdsMetricsWithoutResourceType tests that metrics are auto-detected
// when only service_name is provided (without explicit resource_type)
// This is a regression test for the bug where filter.ResourceType was checked
// instead of the auto-populated resourceType variable
func TestGetAwsRdsMetricsWithoutResourceType(t *testing.T) {
	startDate := time.Now().Add(-time.Hour * 24)
	endDate := time.Now()

	// Test with only service_name, no resource_type - should auto-detect
	metrics, err := getAwsCloudwatchMetrics(providers.NewCloudProviderContext(context.Background()), providers.Account{
		AccountNumber: "740395098545",
	}, providers.QueryMetricsRequest{
		StartDate:   &startDate,
		EndDate:     &endDate,
		ServiceName: "amazonrds",
		Region:      "us-east-1",
		ResourceIds: []string{"main"}, // Specific resource ID
		Step:        time.Second * 60,
		// ResourceType intentionally not specified - should auto-detect as "db"
		// MetricNames intentionally not specified - should auto-detect from serviceConfig
	})

	assert.Nil(t, err)
	assert.NotNil(t, metrics)
	// If the bug exists, this test would fail with "MetricDataQueries is required" error
}
