package aws

import (
	"context"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/smithy-go/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dummyAwsProvider for testing actions without real AWS calls
type dummyAwsProvider struct {
	listResourcesCallCount int
	listResourcesLastArgs  struct {
		serviceName string
		regions     []string
		resourceIds []string
	}
	queryLogsCallCount              int
	queryLogsLastArgs               providers.QueryLogsRequest
	queryMetricesCallCount          int
	queryMetricesLastArgs           providers.QueryMetricsRequest
	lookupCloudTrailEventsCallCount int
	lookupCloudTrailEventsLastArgs  struct {
		input *cloudtrail.LookupEventsInput
	}
	getECSServiceDetailsCallCount int
	getECSServiceDetailsLastArgs  struct {
		region, clusterIdentifier, serviceIdentifier string
	}
	getECSTaskDefinitionDetailsCallCount int
	getECSTaskDefinitionDetailsLastArgs  struct {
		region, taskDefinitionIdentifier string
	}
}

func (d *dummyAwsProvider) QueryLogs(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryLogsRequest) (providers.QueryLogsResponse, error) {
	d.queryLogsCallCount++
	d.queryLogsLastArgs = query
	ctx.GetLogger().Info("dummyAwsProvider: QueryLogs called", "query", fmt.Sprintf("%+v", query))
	return providers.QueryLogsResponse{
		QueryId: "dummy-query-id",
		Status:  "Complete",
		Results: []providers.LogMessage{{Message: "dummy log entry for " + query.LogGroupName, Timestamp: time.Now().UnixMilli()}},
	}, nil
}

func (d *dummyAwsProvider) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	d.queryMetricesCallCount++
	d.queryMetricesLastArgs = filter
	ctx.GetLogger().Info("dummyAwsProvider: QueryMetrices called", "filter", fmt.Sprintf("%+v", filter))

	// Ensure MetricNames and ResourceIds are not empty before accessing them
	var metricName string
	if len(filter.MetricNames) > 0 {
		metricName = filter.MetricNames[0]
	}

	var resourceId string
	if len(filter.ResourceIds) > 0 {
		resourceId = filter.ResourceIds[0]
	}

	return providers.QueryMetricsResponse{
		Items: []providers.MetricItem{{Name: metricName, Values: []float64{10.5}, ResourceId: resourceId}},
	}, nil
}

func (d *dummyAwsProvider) ListResources(ctx providers.CloudProviderContext, account providers.Account, query providers.ListResourceRequest) (providers.ListResourcesResponse, error) {
	d.listResourcesCallCount++
	d.listResourcesLastArgs.serviceName = query.ServiceName
	d.listResourcesLastArgs.regions = query.Regions
	d.listResourcesLastArgs.resourceIds = query.ResourceIds
	ctx.GetLogger().Info("dummyAwsProvider: ListResources called", "serviceName", query.ServiceName, "regions", query.Regions)

	if query.ServiceName == "ecs" {
		return providers.ListResourcesResponse{Items: []providers.Resource{{
			Id:          "abcdef1234567890",
			Arn:         "arn:aws:ecs:us-east-1:123456789012:task/my-cluster/abcdef1234567890",
			Name:        "dummy-ecs-task-abcdef1234567890",
			Type:        "task",
			ServiceName: "ecs",
			Status:      providers.ResourceStatusActive,
			Region:      query.Regions[0],
			Meta: map[string]any{
				"taskDefinitionArn": "arn:aws:ecs:us-east-1:123456789012:task-definition/my-app-task-def:1",
				"lastStatus":        "STOPPED",
			},
		}}}, nil
	}
	if query.ServiceName == "ecr" {
		return providers.ListResourcesResponse{Items: []providers.Resource{{
			Id:          "nudgebee_runbook_sidecar_agent",
			Arn:         "arn:aws:ecr:us-east-1:123456789012:repository/nudgebee_runbook_sidecar_agent",
			Name:        "nudgebee_runbook_sidecar_agent",
			Type:        "repository",
			ServiceName: "ecr",
			Status:      providers.ResourceStatusActive,
			Region:      query.Regions[0],
		}}}, nil
	}
	return providers.ListResourcesResponse{Items: []providers.Resource{{Id: "dummy-resource", Name: "Dummy Resource", ServiceName: query.ServiceName}}}, nil
}

func (d *dummyAwsProvider) LookupCloudTrailEvents(ctx providers.CloudProviderContext, account providers.Account, region string, input *cloudtrail.LookupEventsInput) ([]types.Event, error) {
	d.lookupCloudTrailEventsCallCount++
	d.lookupCloudTrailEventsLastArgs.input = input
	ctx.GetLogger().Info("dummyAwsProvider: LookupCloudTrailEvents called", "region", region, "input", fmt.Sprintf("%+v", input))

	sampleEventTime := time.Now()
	return []types.Event{{
		EventId:         ptr.String("dummy-cloudtrail-event-id"),
		EventName:       ptr.String("DummyEventName"),
		EventTime:       &sampleEventTime,
		CloudTrailEvent: ptr.String(`{"userIdentity":{"type":"DummyUser"},"eventSource":"dummy.amazonaws.com"}`),
	}}, nil
}
func (d *dummyAwsProvider) GetECSServiceDetails(ctx providers.CloudProviderContext, account providers.Account, region, clusterIdentifier, serviceIdentifier string) (providers.Resource, error) {
	d.getECSServiceDetailsCallCount++
	d.getECSServiceDetailsLastArgs.region = region
	d.getECSServiceDetailsLastArgs.clusterIdentifier = clusterIdentifier
	d.getECSServiceDetailsLastArgs.serviceIdentifier = serviceIdentifier
	ctx.GetLogger().Info("dummyAwsProvider: GetECSServiceDetails called", "region", region, "cluster", clusterIdentifier, "service", serviceIdentifier)

	return providers.Resource{
		Id:          serviceIdentifier,
		Arn:         fmt.Sprintf("arn:aws:ecs:%s:%s:service/%s/%s", region, account.AccountNumber, clusterIdentifier, serviceIdentifier),
		Name:        serviceIdentifier,
		Type:        "service",
		ServiceName: "ecs",
		Status:      providers.ResourceStatusActive,
		Region:      region,
		Meta: map[string]any{
			"deployments":    []any{map[string]any{"id": "dpl-123", "status": "PRIMARY", "desiredCount": int64(1)}},
			"events":         []any{map[string]any{"id": "evt-abc", "message": "Service reached steady state."}},
			"loadBalancers":  []any{},
			"desiredCount":   int64(1),
			"runningCount":   int64(1),
			"taskDefinition": fmt.Sprintf("arn:aws:ecs:%s:%s:task-definition/dummy-task-def:1", region, account.AccountNumber),
		},
	}, nil
}

func (d *dummyAwsProvider) GetECSTaskDefinitionDetails(ctx providers.CloudProviderContext, account providers.Account, region, taskDefinitionIdentifier string) (providers.Resource, error) {
	d.getECSTaskDefinitionDetailsCallCount++
	d.getECSTaskDefinitionDetailsLastArgs.region = region
	d.getECSTaskDefinitionDetailsLastArgs.taskDefinitionIdentifier = taskDefinitionIdentifier
	ctx.GetLogger().Info("dummyAwsProvider: GetECSTaskDefinitionDetails called", "region", region, "taskDef", taskDefinitionIdentifier)

	return providers.Resource{
		Id:          taskDefinitionIdentifier,
		Arn:         taskDefinitionIdentifier,
		Name:        strings.Split(strings.Split(taskDefinitionIdentifier, "/")[1], ":")[0],
		Type:        "task-definition",
		ServiceName: "ecs",
		Status:      providers.ResourceStatusActive,
		Region:      region,
		Meta: map[string]any{
			"family":               "dummy-task-def",
			"revision":             int64(1),
			"containerDefinitions": []any{map[string]any{"name": "my-app-container", "image": "nginx:latest", "logConfiguration": map[string]any{"logDriver": "awslogs", "options": map[string]string{"awslogs-group": "/ecs/dummy-task-def"}}}},
		},
	}, nil
}

type dummyProcessedEventHandler struct {
	processEventCallCount                  int
	getAccountFromCloudProviderIdCallCount int
	lastProcessedEvent                     providers.Event
	lastOriginatingAccount                 providers.Account
}

func (d *dummyProcessedEventHandler) ProcessEvent(pCtx providers.CloudProviderContext, event providers.Event, originatingAccount providers.Account) error {
	d.processEventCallCount++
	d.lastProcessedEvent = event
	d.lastOriginatingAccount = originatingAccount
	pCtx.GetLogger().Info("dummyProcessedEventHandler: ProcessEvent called", "eventId", event.EventId, "originatingAccount", originatingAccount.AccountNumber)
	return nil
}

func (d *dummyProcessedEventHandler) GetAccountFromCloudProviderAccountId(pCtx providers.CloudProviderContext, awsAccountNumber string) (providers.Account, error) {
	d.getAccountFromCloudProviderIdCallCount++
	pCtx.GetLogger().Info("dummyProcessedEventHandler: GetAccountFromCloudProviderAccountId called", "awsAccountNumber", awsAccountNumber)
	return providers.Account{
		AccountNumber: awsAccountNumber,
	}, nil
}

func (d *dummyProcessedEventHandler) GetAccountFromExternalId(pCtx providers.CloudProviderContext, externalId string, accountNumber string) (providers.Account, error) {
	pCtx.GetLogger().Info("dummyProcessedEventHandler: GetAccountFromExternalId called", "externalId", externalId, "accountNumber", accountNumber)
	return providers.Account{
		AccountNumber: accountNumber,
	}, nil
}

// seedAccountMetadataForTest populates the package-level account-metadata cache
// so that update_cloud_resource actions can resolve the account UUID/tenant
// without a live DB lookup. In production this cache is filled by
// getAccountByExternalId; in tests we seed it directly. Registers a cleanup so
// the global cache doesn't leak across tests.
func seedAccountMetadataForTest(t *testing.T, accountNumber string) {
	t.Helper()
	accountMetadataCacheMutex.Lock()
	accountMetadataCache[accountNumber] = AccountMetadata{
		ID:       "00000000-0000-0000-0000-000000000001",
		TenantID: "00000000-0000-0000-0000-000000000002",
	}
	accountMetadataCacheMutex.Unlock()
	t.Cleanup(func() {
		accountMetadataCacheMutex.Lock()
		delete(accountMetadataCache, accountNumber)
		accountMetadataCacheMutex.Unlock()
	})
}

func TestAwsEvenBridge_Mock_ECS(t *testing.T) {
	seedAccountMetadataForTest(t, "123456789012")
	sqsMessageBody := `{
		"version": "0",
		"id": "evt-id-ecs-stopped-unexpectedly",
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
			"stoppedReason": "Essential container in task exited due to an error.",
			"containers": [
				{
					"containerArn": "arn:aws:ecs:us-east-1:123456789012:container/my-cluster/abcdef1234567890/container-id-1",
					"taskArn": "arn:aws:ecs:us-east-1:123456789012:task/my-cluster/abcdef1234567890",
					"name": "my-app-container",
					"image": "nginx:latest",
					"exitCode": 137,
					"reason": "OutOfMemoryError"
				}
			],
			"group": "service:my-ecs-service",
			"connectivity": "CONNECTED",
			"cpu": "256",
			"memory": "512"
		}
	}`

	rules, err := GetEventRules("")
	require.NoError(t, err, "Failed to load event rules")
	require.NotEmpty(t, rules.Rules, "No rules loaded from runbook")

	dummyAPI := &dummyAwsProvider{}
	processor := NewTemplatedEventBridgeProcessor(rules, dummyAPI)
	dummyHandler := &dummyProcessedEventHandler{}

	pCtx := providers.NewCloudProviderContext(context.Background())

	processedEvent, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, sqsMessageBody, processor, dummyHandler)

	require.NoError(t, err, "ProcessSQSMessageBodyForEventBridgeEvent returned an error")
	require.NotEmpty(t, processedEvent.EventId, "Processed event ID is empty, rule might not have matched or processing failed")

	// A STOPPED task event with a non-zero exit code matches three rules:
	// ECS_Task_All_Events (generic catch-all) and ECS_Task_Stopped_Unexpectedly
	// (both emitting), plus Resource_Sync_ECS_Task_State_Change (actions-only).
	// The processor emits the LAST matching emitting rule's event, so the
	// catch-all is ordered BEFORE the specific rules in the runbook: the
	// specific, higher-severity ECS_Task_Stopped_Unexpectedly (HIGH/FIRING) wins
	// and the crash is not masked by the generic Info/CLOSED event. Its EventId
	// is the rule's task-level fingerprint; the source-native id is preserved on
	// FindingId.
	assert.Equal(t, "ecs-task-stopped:arn:aws:ecs:us-east-1:123456789012:task/my-cluster/abcdef1234567890", processedEvent.EventId)
	assert.Equal(t, "evt-id-ecs-stopped-unexpectedly", processedEvent.FindingId)
	assert.Equal(t, "ECS Task State Change", processedEvent.EventName)
	assert.Equal(t, "ECS Task abcdef1234567890 Stopped Unexpectedly (Exit Code: 137)", processedEvent.Title)
	assert.Equal(t, providers.EventSeverityHigh, processedEvent.EventSeverity)
	assert.Contains(t, processedEvent.Description, "ECS Task arn:aws:ecs:us-east-1:123456789012:task/my-cluster/abcdef1234567890")
	assert.Contains(t, processedEvent.Description, "stopped unexpectedly")
	assert.Equal(t, providers.EventStatusFiring, processedEvent.EventStatus)
	assert.Equal(t, "AmazonECS", processedEvent.ResourceServiceName)
	assert.Equal(t, "abcdef1234567890", processedEvent.ResourceId)
	assert.Equal(t, "task", processedEvent.ResourceType)

	require.NotNil(t, processedEvent.Raw, "processedEvent.Raw should not be nil")
	rawMap := processedEvent.Raw

	assert.Equal(t, "STOPPED", rawMap["lastStatus"])
	assert.Equal(t, 1, dummyHandler.getAccountFromCloudProviderIdCallCount, "GetAccountFromCloudProviderAccountId should have been called once")

	// ECS_Task_Stopped_Unexpectedly_EventBridge runs three actions: fetch the
	// stopped task (aws_get_resource), its task definition (aws_get_resource),
	// and the primary container's logs (aws_get_log).
	require.Len(t, processedEvent.AdditionalContext, 3, "Expected 3 action evidence")
	assert.Equal(t, "aws_get_resource", processedEvent.AdditionalContext[0].AdditionalInfo["action_type"])
	assert.Equal(t, "aws_get_resource", processedEvent.AdditionalContext[1].AdditionalInfo["action_type"])
	assert.Equal(t, "aws_get_log", processedEvent.AdditionalContext[2].AdditionalInfo["action_type"])

	// Exactly one ListResources call: the stopped rule's task fetch. The generic
	// ECS_Task_All_Events rule deliberately has no aws_get_resource action (the
	// event Detail already carries the task object), and the fallback must pass
	// the task ARN as ResourceIds so ListResources uses the targeted
	// GetResourcesByIds path instead of a full region-wide ECS scan.
	assert.Equal(t, 1, dummyAPI.listResourcesCallCount, "Expected a single ListResources call")
	assert.Equal(t, "ecs", dummyAPI.listResourcesLastArgs.serviceName)
	assert.Equal(t, []string{"us-east-1"}, dummyAPI.listResourcesLastArgs.regions)
	assert.Equal(t, []string{"arn:aws:ecs:us-east-1:123456789012:task/my-cluster/abcdef1234567890"}, dummyAPI.listResourcesLastArgs.resourceIds)
}
func TestAwsEvenBridge_Mock_ECR(t *testing.T) {
	seedAccountMetadataForTest(t, "123456789012")
	sqsMessageBody := `{
    "version": "0",
    "id": "fcf0ea66-968f-3eda-ca24-40dcd217003b",
    "detail-type": "ECR Image Action",
    "source": "aws.ecr",
    "account": "123456789012",
    "time": "2025-06-11T06:43:44Z",
    "region": "us-east-1",
    "resources": [],
    "detail": {
        "result": "SUCCESS",
        "repository-name": "nudgebee_runbook_sidecar_agent",
        "image-digest": "sha256:778645862e8bba7ad64eabc8f6b51158e47124c12864ed42d7baf744b6329542",
        "action-type": "PUSH",
        "artifact-media-type": "application/vnd.oci.image.config.v1+json",
        "image-tag": "",
        "manifest-media-type": "application/vnd.oci.image.manifest.v1+json"
    }
}`

	rules, err := GetEventRules("")
	require.NoError(t, err, "Failed to load event rules")
	require.NotEmpty(t, rules.Rules, "No rules loaded from runbook")

	dummyAPI := &dummyAwsProvider{}
	processor := NewTemplatedEventBridgeProcessor(rules, dummyAPI)
	dummyHandler := &dummyProcessedEventHandler{}

	pCtx := providers.NewCloudProviderContext(context.Background())

	processedEvent, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, sqsMessageBody, processor, dummyHandler)

	require.NoError(t, err, "ProcessSQSMessageBodyForEventBridgeEvent returned an error")
	require.NotEmpty(t, processedEvent.EventId, "Processed event ID is empty, rule might not have matched or processing failed")

	// The ECR rule defines a fingerprint template, which the processor uses as
	// the dedup key AND the downstream EventId. The source-native per-delivery
	// id is preserved on FindingId.
	assert.Equal(t, "nudgebee_runbook_sidecar_agent:us-east-1:123456789012", processedEvent.EventId)
	assert.Equal(t, "fcf0ea66-968f-3eda-ca24-40dcd217003b", processedEvent.FindingId)
	assert.Equal(t, "ECR Image Action", processedEvent.EventName)
	assert.Equal(t, "ECR Event: Repo nudgebee_runbook_sidecar_agent Action: ECR Image Action", processedEvent.Title)
	assert.Equal(t, providers.EventSeverityInfo, processedEvent.EventSeverity)
	assert.Equal(t, providers.EventStatusClosed, processedEvent.EventStatus)
	assert.Equal(t, "AmazonECR", processedEvent.ResourceServiceName)
	assert.Equal(t, "nudgebee_runbook_sidecar_agent", processedEvent.ResourceId)
	assert.Equal(t, "ecr-repository", processedEvent.ResourceType)

	require.Len(t, processedEvent.AdditionalContext, 1, "Expected 1 action evidence")
}

func TestAwsEventBridge_RDS_TagChange(t *testing.T) {
	seedAccountMetadataForTest(t, "123456789012")
	sqsMessageBody := `{
		"version": "0",
		"id": "rds-tag-change-001",
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.rds",
		"account": "123456789012",
		"time": "2026-04-08T12:35:42Z",
		"region": "us-east-1",
		"resources": [],
		"detail": {
			"eventSource": "rds.amazonaws.com",
			"eventName": "AddTagsToResource",
			"awsRegion": "us-east-1",
			"requestParameters": {
				"resourceName": "arn:aws:rds:us-east-1:123456789012:db:database-1-instance-1",
				"tags": [
					{"key": "Environment", "value": "production"}
				]
			},
			"userIdentity": {
				"type": "IAMUser",
				"principalId": "AIDAEXAMPLE:test.user"
			}
		}
	}`

	rules, err := GetEventRules("")
	require.NoError(t, err)

	dummyAPI := &dummyAwsProvider{}
	processor := NewTemplatedEventBridgeProcessor(rules, dummyAPI)
	dummyHandler := &dummyProcessedEventHandler{}
	pCtx := providers.NewCloudProviderContext(context.Background())

	processedEvent, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, sqsMessageBody, processor, dummyHandler)
	require.NoError(t, err)

	// Resource_Sync_RDS_Tag_Change is an actions_only bookkeeping rule: it runs
	// update_cloud_resource for its side effect and deliberately emits NO
	// downstream event, so processedEvent is the zero value. We assert the rule
	// matched (account resolution happened) and that nothing was emitted.
	assert.Equal(t, 1, dummyHandler.getAccountFromCloudProviderIdCallCount, "account should have been resolved for the matched rule")
	assert.Empty(t, processedEvent.EventId, "actions_only rule must not emit a downstream event")
}

func TestAwsEventBridge_EC2_TagChange(t *testing.T) {
	seedAccountMetadataForTest(t, "123456789012")
	sqsMessageBody := `{
		"version": "0",
		"id": "ec2-tag-change-001",
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.ec2",
		"account": "123456789012",
		"time": "2026-04-08T13:00:00Z",
		"region": "us-east-1",
		"resources": [],
		"detail": {
			"eventSource": "ec2.amazonaws.com",
			"eventName": "CreateTags",
			"awsRegion": "us-east-1",
			"requestParameters": {
				"resourcesSet": {
					"items": [
						{"resourceId": "i-0abc123def456789"}
					]
				},
				"tagSet": {
					"items": [
						{"key": "Name", "value": "my-instance"}
					]
				}
			},
			"userIdentity": {
				"type": "IAMUser",
				"principalId": "AIDAEXAMPLE:admin"
			}
		}
	}`

	rules, err := GetEventRules("")
	require.NoError(t, err)

	dummyAPI := &dummyAwsProvider{}
	processor := NewTemplatedEventBridgeProcessor(rules, dummyAPI)
	dummyHandler := &dummyProcessedEventHandler{}
	pCtx := providers.NewCloudProviderContext(context.Background())

	processedEvent, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, sqsMessageBody, processor, dummyHandler)
	require.NoError(t, err)

	// Resource_Sync_EC2_Tag_Change is an actions_only bookkeeping rule that
	// matches instance (i-*) tag changes. It runs update_cloud_resource for its
	// side effect and emits NO downstream event (unlike the non-instance case,
	// which falls through to the generic DefaultEventBridgeProcessor).
	assert.Equal(t, 1, dummyHandler.getAccountFromCloudProviderIdCallCount, "account should have been resolved for the matched rule")
	assert.Empty(t, processedEvent.EventId, "actions_only rule must not emit a downstream event")
}

func TestAwsEventBridge_EC2_TagChange_NonInstance_Skipped(t *testing.T) {
	// Tag change on a volume (vol-*) should NOT match the EC2 tag rule
	sqsMessageBody := `{
		"version": "0",
		"id": "ec2-vol-tag-001",
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.ec2",
		"account": "123456789012",
		"time": "2026-04-08T13:00:00Z",
		"region": "us-east-1",
		"resources": [],
		"detail": {
			"eventSource": "ec2.amazonaws.com",
			"eventName": "CreateTags",
			"awsRegion": "us-east-1",
			"requestParameters": {
				"resourcesSet": {
					"items": [
						{"resourceId": "vol-0abc123def456789"}
					]
				},
				"tagSet": {
					"items": [
						{"key": "Name", "value": "my-volume"}
					]
				}
			},
			"userIdentity": {
				"type": "IAMUser",
				"principalId": "AIDAEXAMPLE:admin"
			}
		}
	}`

	rules, err := GetEventRules("")
	require.NoError(t, err)

	dummyAPI := &dummyAwsProvider{}
	processor := NewTemplatedEventBridgeProcessor(rules, dummyAPI)
	dummyHandler := &dummyProcessedEventHandler{}
	pCtx := providers.NewCloudProviderContext(context.Background())

	processedEvent, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, sqsMessageBody, processor, dummyHandler)
	require.NoError(t, err)

	// Should NOT match Resource_Sync_EC2_Tag_Change (filtered to i-* only).
	// The DefaultEventBridgeProcessor still processes it generically,
	// but the title should NOT contain "CreateTags" (our rule's format).
	assert.NotContains(t, processedEvent.Title, "EC2 CreateTags",
		"Volume tag change should not match EC2 instance tag change rule")
}

func TestAwsEventBridge_RDS_CloudTrail_Lifecycle(t *testing.T) {
	seedAccountMetadataForTest(t, "123456789012")
	sqsMessageBody := `{
		"version": "0",
		"id": "rds-lifecycle-001",
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.rds",
		"account": "123456789012",
		"time": "2026-04-08T14:00:00Z",
		"region": "us-east-1",
		"resources": [],
		"detail": {
			"eventSource": "rds.amazonaws.com",
			"eventName": "CreateDBInstance",
			"awsRegion": "us-east-1",
			"requestParameters": {
				"dBInstanceIdentifier": "my-new-database",
				"dBInstanceClass": "db.t3.micro",
				"engine": "mysql"
			},
			"userIdentity": {
				"type": "IAMUser",
				"principalId": "AIDAEXAMPLE:admin"
			}
		}
	}`

	rules, err := GetEventRules("")
	require.NoError(t, err)

	dummyAPI := &dummyAwsProvider{}
	processor := NewTemplatedEventBridgeProcessor(rules, dummyAPI)
	dummyHandler := &dummyProcessedEventHandler{}
	pCtx := providers.NewCloudProviderContext(context.Background())

	processedEvent, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, sqsMessageBody, processor, dummyHandler)
	require.NoError(t, err)

	// Resource_Sync_RDS_CloudTrail_Lifecycle is an actions_only bookkeeping rule:
	// it runs update_cloud_resource for its side effect and emits NO downstream
	// event, so processedEvent is the zero value. Assert the rule matched
	// (account resolution happened) and that nothing was emitted.
	assert.Equal(t, 1, dummyHandler.getAccountFromCloudProviderIdCallCount, "account should have been resolved for the matched rule")
	assert.Empty(t, processedEvent.EventId, "actions_only rule must not emit a downstream event")
}

func TestAwsEventBridge_RDS_CloudTrail_DeleteCluster(t *testing.T) {
	seedAccountMetadataForTest(t, "123456789012")
	sqsMessageBody := `{
		"version": "0",
		"id": "rds-delete-cluster-001",
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.rds",
		"account": "123456789012",
		"time": "2026-04-08T15:00:00Z",
		"region": "us-east-1",
		"resources": [],
		"detail": {
			"eventSource": "rds.amazonaws.com",
			"eventName": "DeleteDBCluster",
			"awsRegion": "us-east-1",
			"requestParameters": {
				"dBClusterIdentifier": "my-aurora-cluster"
			},
			"userIdentity": {
				"type": "IAMUser",
				"principalId": "AIDAEXAMPLE:admin"
			}
		}
	}`

	rules, err := GetEventRules("")
	require.NoError(t, err)

	dummyAPI := &dummyAwsProvider{}
	processor := NewTemplatedEventBridgeProcessor(rules, dummyAPI)
	dummyHandler := &dummyProcessedEventHandler{}
	pCtx := providers.NewCloudProviderContext(context.Background())

	processedEvent, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, sqsMessageBody, processor, dummyHandler)
	require.NoError(t, err)

	// Resource_Sync_RDS_CloudTrail_Lifecycle is an actions_only bookkeeping rule:
	// it runs update_cloud_resource for its side effect and emits NO downstream
	// event. Assert the rule matched and that nothing was emitted.
	assert.Equal(t, 1, dummyHandler.getAccountFromCloudProviderIdCallCount, "account should have been resolved for the matched rule")
	assert.Empty(t, processedEvent.EventId, "actions_only rule must not emit a downstream event")
}

func TestAwsEventBridge_ECS_CloudTrail_CreateCluster(t *testing.T) {
	seedAccountMetadataForTest(t, "123456789012")
	sqsMessageBody := `{
		"version": "0",
		"id": "ecs-cluster-001",
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.ecs",
		"account": "123456789012",
		"time": "2026-04-08T16:00:00Z",
		"region": "us-east-1",
		"resources": [],
		"detail": {
			"eventSource": "ecs.amazonaws.com",
			"eventName": "CreateCluster",
			"awsRegion": "us-east-1",
			"requestParameters": {
				"clusterName": "my-new-cluster"
			},
			"userIdentity": {
				"type": "IAMUser",
				"principalId": "AIDAEXAMPLE:admin"
			}
		}
	}`

	rules, err := GetEventRules("")
	require.NoError(t, err)

	dummyAPI := &dummyAwsProvider{}
	processor := NewTemplatedEventBridgeProcessor(rules, dummyAPI)
	dummyHandler := &dummyProcessedEventHandler{}
	pCtx := providers.NewCloudProviderContext(context.Background())

	processedEvent, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, sqsMessageBody, processor, dummyHandler)
	require.NoError(t, err)

	// Resource_Sync_ECS_Cluster_CloudTrail is an actions_only bookkeeping rule:
	// it runs update_cloud_resource for its side effect and emits NO downstream
	// event. Assert the rule matched and that nothing was emitted.
	assert.Equal(t, 1, dummyHandler.getAccountFromCloudProviderIdCallCount, "account should have been resolved for the matched rule")
	assert.Empty(t, processedEvent.EventId, "actions_only rule must not emit a downstream event")
}

func TestAwsEventBridge_ECS_CloudTrail_DeleteService(t *testing.T) {
	seedAccountMetadataForTest(t, "123456789012")
	sqsMessageBody := `{
		"version": "0",
		"id": "ecs-service-001",
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.ecs",
		"account": "123456789012",
		"time": "2026-04-08T17:00:00Z",
		"region": "us-east-1",
		"resources": [],
		"detail": {
			"eventSource": "ecs.amazonaws.com",
			"eventName": "DeleteService",
			"awsRegion": "us-east-1",
			"requestParameters": {
				"service": "arn:aws:ecs:us-east-1:123456789012:service/my-cluster/my-service",
				"cluster": "my-cluster"
			},
			"userIdentity": {
				"type": "IAMUser",
				"principalId": "AIDAEXAMPLE:admin"
			}
		}
	}`

	rules, err := GetEventRules("")
	require.NoError(t, err)

	dummyAPI := &dummyAwsProvider{}
	processor := NewTemplatedEventBridgeProcessor(rules, dummyAPI)
	dummyHandler := &dummyProcessedEventHandler{}
	pCtx := providers.NewCloudProviderContext(context.Background())

	processedEvent, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, sqsMessageBody, processor, dummyHandler)
	require.NoError(t, err)

	// Resource_Sync_ECS_Service_CloudTrail is an actions_only bookkeeping rule:
	// it runs update_cloud_resource for its side effect and emits NO downstream
	// event. Assert the rule matched and that nothing was emitted.
	assert.Equal(t, 1, dummyHandler.getAccountFromCloudProviderIdCallCount, "account should have been resolved for the matched rule")
	assert.Empty(t, processedEvent.EventId, "actions_only rule must not emit a downstream event")
}
