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

func TestQueryLogsForResourceId(t *testing.T) {
	endTime := time.Now()
	startTime := endTime.Add(-24 * time.Hour)
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := QueryLogs(ctx, os.Getenv("TEST_ACCOUNT"), providers.QueryLogsRequest{
		LogGroupName: "/ecs/awsxray-task-defination",
		QueryString: `fields @timestamp, @message, @logStream, @log
| sort @timestamp desc`,
		Region:    "us-east-1",
		StartTime: &startTime,
		EndTime:   &endTime,
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response.Results)
}

func TestQueryMetrics(t *testing.T) {
	response, err := QueryMetrics(security.NewRequestContextForSuperAdmin(), os.Getenv("TEST_ACCOUNT"), providers.QueryMetricsRequest{
		ServiceName:     "amazonec2",
		MetricNamespace: "AWS/EC2",
		ResourceIds:     []string{"i-0695d9d318b7bbf30"},
		MetricNames:     []string{"CPUUtilization"},
		Region:          "us-east-1",
		Statistics:      []string{"Average"},
	})
	assert.Nil(t, err)
	assert.NotNil(t, response)
}

func TestQueryMetrics2(t *testing.T) {
	response, err := QueryMetrics(security.NewRequestContextForSuperAdmin(), os.Getenv("TEST_ACCOUNT"), providers.QueryMetricsRequest{
		ServiceName:     "amazonec2",
		MetricNamespace: "aws/ec2",
		Dimensions: []map[string]string{
			{
				"InstanceId": "i-0695d9d318b7bbf30",
			},
		},
		MetricNames: []string{"CPUUtilization"},
		Region:      "us-east-1",
		Statistics:  []string{"Average"},
	})
	assert.Nil(t, err)
	assert.NotNil(t, response)
}

func TestQueryResources(t *testing.T) {
	response, err := ListResources(security.NewRequestContextForSuperAdmin(), os.Getenv("TEST_ACCOUNT"), providers.ListResourceRequest{
		ServiceName: "amazonelb",
		Regions:     []string{"us-east-1"},
		ResourceIds: []string{os.Getenv("TEST_AWS_ALB_RESOURCE_ID")},
	})
	assert.Nil(t, err)
	assert.NotNil(t, response)
}

func TestQueryServiceMapEc2(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := QueryServiceMap(ctx, os.Getenv("TEST_ACCOUNT"), providers.QueryServiceMapRequest{
		Region: "us-east-1",
		Resources: []providers.QueryServiceMapResourceRequest{
			{
				ServiceName: "amazonec2",
				Resource:    "i-0695d9d318b7bbf30",
			},
		},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestQueryServiceMapELB(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := QueryServiceMap(ctx, os.Getenv("TEST_ACCOUNT"), providers.QueryServiceMapRequest{
		Region: "us-east-1",
		Resources: []providers.QueryServiceMapResourceRequest{
			{
				ServiceName: "awselb",
				Resource:    os.Getenv("TEST_AWS_ELB_NAME"),
			},
		},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestQueryServiceMapECS(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := QueryServiceMap(ctx, os.Getenv("TEST_ACCOUNT"), providers.QueryServiceMapRequest{
		Region: "us-east-1",
		Resources: []providers.QueryServiceMapResourceRequest{
			{
				ServiceName: "amazonecs",
				Resource:    os.Getenv("TEST_AWS_ECS_RESOURCE_ID"),
			},
		},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestQueryServiceMapALB(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := QueryServiceMap(ctx, os.Getenv("TEST_ACCOUNT"), providers.QueryServiceMapRequest{
		Region: "us-east-1",
		Resources: []providers.QueryServiceMapResourceRequest{
			{
				ServiceName: "AWSELB",
				Resource:    os.Getenv("TEST_AWS_ALB_RESOURCE_ID"),
			},
		},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestQueryLogECSContainer(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := QueryLogs(ctx, os.Getenv("TEST_ACCOUNT"), providers.QueryLogsRequest{
		Region:      "us-east-1",
		QueryString: "",
		ServiceName: "amazonecs",
		ResourceId:  os.Getenv("TEST_AWS_ECS_RESOURCE_ID"),
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestQueryLogALB(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := QueryLogs(ctx, os.Getenv("TEST_ACCOUNT"), providers.QueryLogsRequest{
		Region:      "us-east-1",
		QueryString: "",
		ServiceName: "AWSELB",
		ResourceId:  os.Getenv("TEST_AWS_ALB_RESOURCE_ID"),
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestListEventRules(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := ListEventRules(ctx, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestQueryLogIAM(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := QueryLogs(ctx, os.Getenv("TEST_ACCOUNT"), providers.QueryLogsRequest{
		Region:      "us-east-1",
		QueryString: "",
		ServiceName: "AWSIAM",
		ResourceId:  fmt.Sprintf("arn:aws:iam::%s:user/test-user", testAWSAccountNumber),
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestQueryServiceMapIAM(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := QueryServiceMap(ctx, os.Getenv("TEST_ACCOUNT"), providers.QueryServiceMapRequest{
		Region: "us-east-1",
		Resources: []providers.QueryServiceMapResourceRequest{
			{
				ServiceName: "AWSIAM",
				Resource:    fmt.Sprintf("arn:aws:iam::%s:user/test-user", testAWSAccountNumber),
			},
		},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}
