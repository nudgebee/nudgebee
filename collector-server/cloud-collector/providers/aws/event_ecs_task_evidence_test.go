package aws

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AWS/ECS has no TaskId dimension, so a per-task query always came back empty.
// The OOM graph asks for the memory of the task's service instead.
func TestECSTaskOOM_MemoryGraphQueriesTheService(t *testing.T) {
	api := processECSTaskSample(t, "ECS_Task_Stopped_OutOfMemory_EventBridge",
		ecsTaskStoppedSample("EssentialContainerExited", "OutOfMemoryError: Container killed due to memory usage", `"exitCode": 137`))

	require.Len(t, api.queryMetricesCalls, 1)
	call := api.queryMetricesCalls[0]
	assert.Equal(t, "AWS/ECS", call.MetricNamespace)
	assert.Equal(t, []string{"MemoryUtilization"}, call.MetricNames)
	dims := fmt.Sprint(call.Dimensions)
	assert.Contains(t, dims, "ServiceName")
	assert.Contains(t, dims, "checkout")
	assert.Contains(t, dims, "prod-cluster")
	assert.NotContains(t, dims, "TaskId")
}

// A standalone task (group "family:...") has no service; its empty ServiceName
// is dropped, so the graph is the cluster's memory rather than a query on the
// made-up dimension Name=ServiceName.
func TestECSTaskOOM_StandaloneTaskGetsClusterMemory(t *testing.T) {
	body := strings.Replace(
		ecsTaskStoppedSample("EssentialContainerExited", "OutOfMemoryError: Container killed due to memory usage", `"exitCode": 137`),
		`"group": "service:checkout"`, `"group": "family:checkout"`, 1)
	api := processECSTaskSample(t, "ECS_Task_Stopped_OutOfMemory_EventBridge", body)

	require.Len(t, api.queryMetricesCalls, 1)
	assert.Equal(t, []map[string]string{{"Name": "ClusterName", "Value": "prod-cluster"}}, api.queryMetricesCalls[0].Dimensions)
}

func TestDropEmptyDimensions(t *testing.T) {
	in := []map[string]string{
		{"Name": "ClusterName", "Value": "prod-cluster"},
		{"Name": "ServiceName", "Value": ""},
		{"Name": "TaskId", "Value": "  "},
		{"ServiceName": "checkout"}, // {dimName: value} form is left alone
	}
	assert.Equal(t, []map[string]string{
		{"Name": "ClusterName", "Value": "prod-cluster"},
		{"ServiceName": "checkout"},
	}, dropEmptyDimensions(in))
}

// The repository is everything after the registry host, up to :tag or
// @digest. It used to render as "team # Basic parsing" (a note inside the
// block scalar) and kept only the first path segment.
func TestECSTaskCannotPull_ECRLookupUsesFullRepositoryName(t *testing.T) {
	for _, tc := range []struct{ image, wantRepo string }{
		{"123456789012.dkr.ecr.us-east-1.amazonaws.com/team/web:v9", "team/web"},
		{"123456789012.dkr.ecr.us-east-1.amazonaws.com/web@sha256:0123abcd", "web"},
		{"123456789012.dkr.ecr.us-east-1.amazonaws.com/team/web", "team/web"},
		{"nginx:latest", ""},
		{"docker.io/library/nginx:1.25", ""},
	} {
		t.Run(tc.image, func(t *testing.T) {
			body := strings.Replace(
				ecsTaskStoppedSample("TaskFailedToStart", "CannotPullContainerError: pull image manifest has been retried 5 time(s): not found", ``),
				"123456789012.dkr.ecr.us-east-1.amazonaws.com/checkout:v9", tc.image, 1)
			api := processECSTaskSample(t, "ECS_Task_Stopped_CannotPullContainerError_EventBridge", body)

			if tc.wantRepo == "" {
				assert.NotEqual(t, "ecr", api.listResourcesLastArgs.serviceName, "no ECR lookup for a non-ECR image")
				return
			}
			assert.Equal(t, "ecr", api.listResourcesLastArgs.serviceName)
			assert.Equal(t, []string{tc.wantRepo}, api.listResourcesLastArgs.resourceIds)
		})
	}
}

// The catch-all runs on every task state change; its per-task CPU query was
// always empty.
func TestECSTaskAllEvents_NoMetricFetch(t *testing.T) {
	api := processECSTaskSample(t, "ECS_Task_All_Events_EventBridge",
		ecsTaskStoppedSample("ServiceSchedulerInitiated", "Scaling activity initiated by (deployment ecs-svc/123)", `"exitCode": 0`))

	assert.Zero(t, api.queryMetricesCallCount)
}

func processECSTaskSample(t *testing.T, wantRule, body string) *dummyAwsProvider {
	t.Helper()
	seedAccountTenantForTest(t)
	rules, err := GetEventRules("")
	require.NoError(t, err)

	api := &dummyAwsProvider{}
	processor := NewTemplatedEventBridgeProcessor(rules, api)
	require.Equal(t, wantRule, emittedRule(t, processor, rules, body))
	return api
}
