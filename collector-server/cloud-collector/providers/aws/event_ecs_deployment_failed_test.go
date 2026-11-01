package aws

import (
	"context"
	"fmt"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AWS puts the service only in resources[0]; detail has no serviceName or
// clusterName. Reading them from detail gave every failed deployment the same
// blank title and the same fingerprint, so failures merged into one alert.
func TestECSDeploymentFailed_NamesServiceAndSeparatesDeployments(t *testing.T) {
	seedAccountTenantForTest(t)
	rules, err := GetEventRules("")
	require.NoError(t, err)

	api := &dummyAwsProvider{}
	processor := NewTemplatedEventBridgeProcessor(rules, api)
	pCtx := providers.NewCloudProviderContext(context.Background())

	first, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, deploymentFailedSample("checkout", "ecs-svc/111"), processor, &dummyProcessedEventHandler{})
	require.NoError(t, err)
	assert.Equal(t, "ECS Deployment FAILED for service checkout", first.Title)
	assert.Contains(t, first.Description, "service checkout in cluster prod-cluster")
	assert.Equal(t, "arn:aws:ecs:us-east-1:123456789012:service/prod-cluster/checkout", first.ResourceId)
	assert.Equal(t, "ecs-deploy-failed:prod-cluster:checkout:ecs-svc/111", first.EventId)

	assert.Equal(t, 1, api.getECSServiceDetailsCallCount, "service details should be fetched once")
	assert.Equal(t, "prod-cluster", api.getECSServiceDetailsLastArgs.clusterIdentifier)
	assert.Equal(t, "checkout", api.getECSServiceDetailsLastArgs.serviceIdentifier)
	assert.Zero(t, api.queryLogsCallCount, "no log query: ECS service events are not in CloudWatch Logs")

	other, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, deploymentFailedSample("payments", "ecs-svc/222"), processor, &dummyProcessedEventHandler{})
	require.NoError(t, err)
	assert.NotEqual(t, first.EventId, other.EventId, "different services must not merge")

	retry, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, deploymentFailedSample("checkout", "ecs-svc/333"), processor, &dummyProcessedEventHandler{})
	require.NoError(t, err)
	assert.NotEqual(t, first.EventId, retry.EventId, "a second failed deployment of the same service must not hide in the first")
}

// Services created before the long ARN format have service/<service>, with no
// cluster in the ARN.
func TestECSDeploymentFailed_ShortServiceARN(t *testing.T) {
	seedAccountTenantForTest(t)
	rules, err := GetEventRules("")
	require.NoError(t, err)

	processor := NewTemplatedEventBridgeProcessor(rules, &dummyAwsProvider{})
	pCtx := providers.NewCloudProviderContext(context.Background())
	body := `{"id": "evt-deploy-short", "detail-type": "ECS Deployment State Change", "source": "aws.ecs", "account": "123456789012", "time": "2026-09-28T10:00:00Z", "region": "us-east-1",
		"resources": ["arn:aws:ecs:us-east-1:123456789012:service/legacy-svc"],
		"detail": {"eventType": "ERROR", "eventName": "SERVICE_DEPLOYMENT_FAILED", "deploymentId": "ecs-svc/999", "reason": "ECS deployment circuit breaker: tasks failed to start."}}`

	ev, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, body, processor, &dummyProcessedEventHandler{})
	require.NoError(t, err)
	assert.Equal(t, "ECS Deployment FAILED for service legacy-svc", ev.Title)
	assert.Contains(t, ev.Description, "ECS deployment for service legacy-svc (Account")
	assert.Equal(t, "ecs-deploy-failed::legacy-svc:ecs-svc/999", ev.EventId)
}

func deploymentFailedSample(service, deploymentID string) string {
	return fmt.Sprintf(`{"id": "evt-deploy-%s", "detail-type": "ECS Deployment State Change", "source": "aws.ecs", "account": "123456789012", "time": "2026-09-28T10:00:00Z", "region": "us-east-1",
		"resources": ["arn:aws:ecs:us-east-1:123456789012:service/prod-cluster/%s"],
		"detail": {"eventType": "ERROR", "eventName": "SERVICE_DEPLOYMENT_FAILED", "deploymentId": %q, "reason": "ECS deployment circuit breaker: tasks failed to start."}}`,
		deploymentID, service, deploymentID)
}
