package account

import (
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
		ResourceIds: []string{"app/xray-ecs-dev/ad37a0b66e936f4f"},
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
				Resource:    "xray-ecs-dev",
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
				Resource:    "xray-ecs-dev-ecs-cluster/xray-ecs-dev-bff/bff-app",
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
				Resource:    "app/xray-ecs-dev/229c8c476f5f5f1b",
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
		ResourceId:  "xray-ecs-dev-ecs-cluster/xray-ecs-dev-bff/bff-app",
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
		ResourceId:  "app/xray-ecs-dev/229c8c476f5f5f1b",
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
		ResourceId:  "arn:aws:iam::864186153326:user/abhay",
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
				Resource:    "arn:aws:iam::864186153326:user/abhay",
			},
		},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}
