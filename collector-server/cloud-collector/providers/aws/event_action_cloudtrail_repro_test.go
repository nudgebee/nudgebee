package aws

import (
	"context"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturingCloudTrailProvider wraps dummyAwsProvider and records every
// LookupCloudTrailEvents call (dummyAwsProvider only keeps the last one),
// so this test can assert each individual call sent exactly one
// LookupAttribute — CloudTrail's LookupEvents hard limit.
type capturingCloudTrailProvider struct {
	dummyAwsProvider
	lookupCalls []*cloudtrail.LookupEventsInput
}

func (d *capturingCloudTrailProvider) LookupCloudTrailEvents(ctx providers.CloudProviderContext, account providers.Account, region string, input *cloudtrail.LookupEventsInput) (*cloudtrail.LookupEventsOutput, error) {
	d.lookupCalls = append(d.lookupCalls, input)
	return d.dummyAwsProvider.LookupCloudTrailEvents(ctx, account, region, input)
}

// TestECSTaskStoppedCannotPullContainerError_FetchesCloudTrailEvidence
// reproduces issue #38393's exact repro steps: deliver an "ECS Task State
// Change" STOPPED event whose stoppedReason contains "CannotPullContainerError"
// and confirm get_cloudtrail_for_ecr_pull_errors actually runs, instead of
// failing internally with "eventprocessor: unsupported action type:
// aws_get_cloudtrail_events" the way it did before the case was moved out of
// aws_create_alarm's nested switch.
func TestECSTaskStoppedCannotPullContainerError_FetchesCloudTrailEvidence(t *testing.T) {
	seedAccountTenantForTest(t)
	sqsMessageBody := `{
		"version": "0",
		"id": "evt-id-ecs-pull-error",
		"detail-type": "ECS Task State Change",
		"source": "aws.ecs",
		"account": "123456789012",
		"time": "2023-10-27T10:30:00Z",
		"region": "us-east-1",
		"resources": [
			"arn:aws:ecs:us-east-1:123456789012:task/my-cluster/abcdef1234567890"
		],
		"detail": {
			"clusterArn": "arn:aws:ecs:us-east-1:123456789012:cluster/my-cluster",
			"taskArn": "arn:aws:ecs:us-east-1:123456789012:task/my-cluster/abcdef1234567890",
			"taskDefinitionArn": "arn:aws:ecs:us-east-1:123456789012:task-definition/my-app-task-def:1",
			"lastStatus": "STOPPED",
			"desiredStatus": "STOPPED",
			"stopCode": "CannotPullContainerError",
			"stoppedReason": "CannotPullContainerError: pull access denied for my-repo, repository does not exist or may require 'docker login'",
			"containers": [
				{
					"containerArn": "arn:aws:ecs:us-east-1:123456789012:container/my-cluster/abcdef1234567890/container-id-1",
					"taskArn": "arn:aws:ecs:us-east-1:123456789012:task/my-cluster/abcdef1234567890",
					"name": "my-app-container",
					"image": "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-repo:latest",
					"lastStatus": "STOPPED",
					"reason": "CannotPullContainerError: pull access denied"
				}
			],
			"group": "service:my-ecs-service",
			"connectivity": "CONNECTED",
			"cpu": "256",
			"memory": "512"
		}
	}`

	rules, err := GetEventRules("")
	require.NoError(t, err)

	dummyAPI := &capturingCloudTrailProvider{}
	processor := NewTemplatedEventBridgeProcessor(rules, dummyAPI)
	dummyHandler := &dummyProcessedEventHandler{}
	pCtx := providers.NewCloudProviderContext(context.Background())

	processedEvent, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, sqsMessageBody, processor, dummyHandler)
	require.NoError(t, err, "processing itself must not error even though the buggy action used to fail internally")
	require.NotEmpty(t, processedEvent.EventId, "the CannotPullContainerError rule should have matched and emitted an event")

	// This is the direct repro assertion: before the fix, LookupCloudTrailEvents
	// was never called at all, because aws_get_cloudtrail_events had no
	// reachable case in the top-level switch on actionDef.Type.
	require.Len(t, dummyAPI.lookupCalls, 2,
		"get_cloudtrail_for_ecr_pull_errors should make exactly 2 LookupEvents calls (one per EventSource: ecr, iam) now that the case is reachable")

	for i, call := range dummyAPI.lookupCalls {
		assert.Len(t, call.LookupAttributes, 1,
			"call %d sent %d LookupAttributes; CloudTrail's LookupEvents accepts only one", i, len(call.LookupAttributes))
	}
	assert.Equal(t, "EventSource", string(dummyAPI.lookupCalls[0].LookupAttributes[0].AttributeKey))
	assert.Equal(t, "ecr.amazonaws.com", *dummyAPI.lookupCalls[0].LookupAttributes[0].AttributeValue)
	assert.Equal(t, "EventSource", string(dummyAPI.lookupCalls[1].LookupAttributes[0].AttributeKey))
	assert.Equal(t, "iam.amazonaws.com", *dummyAPI.lookupCalls[1].LookupAttributes[0].AttributeValue)
}
