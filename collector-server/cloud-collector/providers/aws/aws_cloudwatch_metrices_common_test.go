package aws

import (
	"context"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/stretchr/testify/assert"
)

func TestGetAwsRdsMetrics(t *testing.T) {
	startDate := time.Now().Add(-time.Hour * 24)
	metrics, err := getAwsCloudwatchMetrics(providers.NewCloudProviderContext(context.Background()), providers.Account{
		AccountNumber: testAWSAccountNumber,
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
		AccountNumber: testAWSAccountNumber,
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
		AccountNumber: testAWSAccountNumber,
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

func TestBuildAvailableMetricsWithDimensions(t *testing.T) {
	mk := func(name string, dims map[string]string) types.Metric {
		m := types.Metric{MetricName: strPtr(name)}
		for k, v := range dims {
			kk, vv := k, v
			m.Dimensions = append(m.Dimensions, types.Dimension{Name: &kk, Value: &vv})
		}
		return m
	}

	metrics := []types.Metric{
		mk("CPUUtilization", map[string]string{"InstanceId": "i-1"}),
		mk("CPUUtilization", map[string]string{"InstanceId": "i-2"}),
		mk("CPUUtilization", map[string]string{"InstanceId": "i-1"}), // dup combo
		mk("NetworkIn", map[string]string{"InstanceId": "i-1", "AutoScalingGroupName": "asg-a"}),
		{MetricName: strPtr("NoDims")}, // metric with no dimensions
	}

	out := buildAvailableMetricsWithDimensions(metrics, "AWS/EC2", 100)

	// sorted by name: CPUUtilization, NetworkIn, NoDims
	assert.Equal(t, []string{"CPUUtilization", "NetworkIn", "NoDims"}, []string{out[0].Name, out[1].Name, out[2].Name})
	for _, m := range out {
		assert.Equal(t, "AWS/EC2", m.Namespace)
	}
	// CPUUtilization: two distinct InstanceId combos, dup collapsed
	assert.Len(t, out[0].Dimensions, 2)
	// NetworkIn: one combo with two dimension keys
	assert.Len(t, out[1].Dimensions, 1)
	assert.Equal(t, "asg-a", out[1].Dimensions[0]["AutoScalingGroupName"])
	// NoDims: no dimension sets
	assert.Empty(t, out[2].Dimensions)
}

func TestBuildAvailableMetricsWithDimensions_CapsSets(t *testing.T) {
	var metrics []types.Metric
	for i := 0; i < 10; i++ {
		v := fmt.Sprintf("i-%d", i)
		metrics = append(metrics, types.Metric{
			MetricName: strPtr("CPUUtilization"),
			Dimensions: []types.Dimension{{Name: strPtr("InstanceId"), Value: &v}},
		})
	}
	out := buildAvailableMetricsWithDimensions(metrics, "AWS/EC2", 3)
	assert.Len(t, out, 1)
	assert.Len(t, out[0].Dimensions, 3, "dimension sets capped at maxSets")
}
