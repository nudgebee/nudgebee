package account

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestListMetrics(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := QueryMetrics(ctx, os.Getenv("TEST_ACCOUNT"), providers.QueryMetricsRequest{
		ServiceName:  "AmazonECS",
		Region:       "us-east-1",
		ResourceType: "cluster",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestListMetricsForResourceId(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := QueryMetrics(ctx, os.Getenv("TEST_ACCOUNT"), providers.QueryMetricsRequest{
		ServiceName:  "AmazonECS",
		Region:       "us-east-1",
		ResourceType: "task",
		ResourceIds:  []string{fmt.Sprintf("arn:aws:ecs:us-east-1:%s:task/%s/%s", testAWSAccountNumber, os.Getenv("TEST_AWS_ECS_CLUSTER"), os.Getenv("TEST_AWS_ECS_TASK_ID"))},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestListMetricsUsingDimensions(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := QueryMetrics(ctx, os.Getenv("TEST_ACCOUNT"), providers.QueryMetricsRequest{
		ServiceName:  "AmazonECS",
		Region:       "us-east-1",
		ResourceType: "task",
		ResourceIds:  []string{os.Getenv("TEST_AWS_ECS_CLUSTER")},
		Dimensions: []map[string]string{
			{"Name": "ClusterName", "Value": os.Getenv("TEST_AWS_ECS_CLUSTER")}, {"Name": "ServiceName", "Value": os.Getenv("TEST_AWS_ECS_SERVICE")},
		},
		MetricNames:     []string{"RunningCount"},
		MetricNamespace: "AWS/ECS",
		Statistics:      []string{"Minimum"},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestListMetricsGCloud(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := QueryMetrics(ctx, os.Getenv("TEST_ACCOUNT"), providers.QueryMetricsRequest{
		ServiceName:  "GCEInstance",
		Region:       "us-central1",
		ResourceType: "instance",
		ResourceIds:  []string{"instance-1"},
		MetricNames:  []string{"instance/cpu/utilization"},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestListMetricsAzure(t *testing.T) {
	azureVMResourceID := os.Getenv("TEST_AZURE_VM_RESOURCE_ID")
	if azureVMResourceID == "" {
		t.Skip("Skipping test - TEST_AZURE_VM_RESOURCE_ID must be set (full /subscriptions/.../virtualmachines/<name> ARM resource ID)")
	}

	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	endTime := time.Now()
	startTime := endTime.Add(-24 * time.Hour)

	request := providers.QueryMetricsRequest{
		ServiceName:  "microsoft.compute/virtualmachines",
		Region:       "eastus2",
		ResourceType: "virtualmachine",
		ResourceIds:  []string{azureVMResourceID},
		MetricNames:  []string{"Percentage CPU"},
		StartDate:    &startTime,
		EndDate:      &endTime,
		Step:         time.Minute, // explicitly set a positive duration
	}

	response, err := QueryMetrics(ctx, os.Getenv("TEST_ACCOUNT"), request)
	fmt.Printf("response: Items=%d, StartDate=%v, EndDate=%v, Step=%v\n",
		len(response.Items), response.StartDate, response.EndDate, response.Step)
	if len(response.Items) > 0 {
		fmt.Printf("First item: Name=%s, Statistics=%s, ValueCount=%d\n",
			response.Items[0].Name, response.Items[0].Statistics, len(response.Items[0].Values))
	}
	assert.Nil(t, err)
	assert.NotNil(t, response)

}

func TestIsMetriclessService(t *testing.T) {
	// Control-plane / governance types → skipped (no metrics).
	metricless := []string{
		"microsoft.authorization/policyassignments",
		"microsoft.authorization/roleassignments",
		"microsoft.security/pricings",
		"microsoft.insights/metricalerts",
		"microsoft.network/networkwatchers",
		"Microsoft.Authorization/PolicyAssignments", // case-insensitive
		"  microsoft.security/pricings  ",           // trimmed
	}
	for _, s := range metricless {
		assert.Truef(t, isMetriclessService(s), "expected %q to be metricless", s)
	}

	// Types that DO expose metrics must never be skipped — including the two
	// known missing-mapping gaps we explicitly chose not to denylist.
	withMetrics := []string{
		"microsoft.compute/virtualmachines",
		"microsoft.sql/servers/databases",
		"microsoft.storage/storageaccounts",
		"microsoft.insights/components",      // App Insights — has metrics
		"microsoft.network/privateendpoints", // has PEBytesIn/Out
		"aws/ec2",
		"",
	}
	for _, s := range withMetrics {
		assert.Falsef(t, isMetriclessService(s), "expected %q to NOT be metricless", s)
	}
}
