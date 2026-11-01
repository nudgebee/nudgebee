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
	queryMetricesCalls              []providers.QueryMetricsRequest
	lookupCloudTrailEventsCallCount int
	lookupCloudTrailEventsLastArgs  struct {
		input *cloudtrail.LookupEventsInput
	}
	getECSServiceDetailsCallCount int
	getECSServiceDetailsLastArgs  struct {
		region, clusterIdentifier, serviceIdentifier string
	}
	getECSServiceDetailsCalls []struct {
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
	d.queryMetricesCalls = append(d.queryMetricesCalls, filter)
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
	d.getECSServiceDetailsCalls = append(d.getECSServiceDetailsCalls, struct {
		region, clusterIdentifier, serviceIdentifier string
	}{region, clusterIdentifier, serviceIdentifier})
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
		ID:            testAccountId,
		AccountNumber: awsAccountNumber,
	}, nil
}

func (d *dummyProcessedEventHandler) GetAccountFromExternalId(pCtx providers.CloudProviderContext, externalId string, accountNumber string) (providers.Account, error) {
	pCtx.GetLogger().Info("dummyProcessedEventHandler: GetAccountFromExternalId called", "externalId", externalId, "accountNumber", accountNumber)
	return providers.Account{
		ID:            testAccountId,
		AccountNumber: accountNumber,
	}, nil
}

// testAccountId and testTenantId are the account UUID the dummy handlers resolve
// to and the tenant that owns it. The tenant cache is keyed by the account UUID
// (an account number is not tenant-unique), so the handlers must return one just
// as the real resolution path does.
const (
	testAccountId = "00000000-0000-0000-0000-000000000001"
	testTenantId  = "00000000-0000-0000-0000-000000000002"
)

// seedAccountTenantForTest populates the package-level account-tenant cache so
// that update_cloud_resource actions can resolve the tenant without a live DB
// lookup. In production this cache is filled by getAccountByExternalId; in tests
// we seed it directly. Registers a cleanup so the global cache doesn't leak
// across tests.
func seedAccountTenantForTest(t *testing.T) {
	t.Helper()
	setAccountTenant(testAccountId, testTenantId)
	t.Cleanup(func() {
		accountTenantCacheMu.Lock()
		delete(accountTenantCache, testAccountId)
		accountTenantCacheMu.Unlock()
	})
}

func TestAwsEvenBridge_Mock_ECS(t *testing.T) {
	seedAccountTenantForTest(t)
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
			"stopCode": "EssentialContainerExited",
			"stoppedReason": "Essential container in task exited due to an error.",
			"containers": [
				{
					"containerArn": "arn:aws:ecs:us-east-1:123456789012:container/my-cluster/abcdef1234567890/container-id-1",
					"taskArn": "arn:aws:ecs:us-east-1:123456789012:task/my-cluster/abcdef1234567890",
					"name": "my-app-container",
					"image": "nginx:latest",
					"exitCode": 137,
					"reason": "Essential container exited with non-zero status"
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
	// is the rule's service-level fingerprint (cluster + group + exit code, so
	// replacement tasks of the same crashing service dedup instead of minting a
	// fresh event per run); the source-native id is preserved on FindingId.
	assert.Equal(t, "ecs-task-stopped:my-cluster:service:my-ecs-service:137", processedEvent.EventId)
	assert.Equal(t, "evt-id-ecs-stopped-unexpectedly", processedEvent.FindingId)
	assert.Equal(t, "ECS Task State Change", processedEvent.EventName)
	assert.Equal(t, "ECS Task Stopped Unexpectedly: service:my-ecs-service (Exit Code: 137)", processedEvent.Title)
	assert.Equal(t, providers.EventSeverityHigh, processedEvent.EventSeverity)
	assert.Contains(t, processedEvent.Description, "ECS Task arn:aws:ecs:us-east-1:123456789012:task/my-cluster/abcdef1234567890")
	assert.Contains(t, processedEvent.Description, "stopped unexpectedly")
	assert.Equal(t, providers.EventStatusFiring, processedEvent.EventStatus)
	assert.Equal(t, "AmazonECS", processedEvent.ResourceServiceName)
	// The task belongs to a service (group "service:my-ecs-service"), so the
	// event attaches to the durable service resource, not the ephemeral task.
	assert.Equal(t, "arn:aws:ecs:us-east-1:123456789012:service/my-cluster/my-ecs-service", processedEvent.ResourceId)
	assert.Equal(t, "service", processedEvent.ResourceType)

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
	seedAccountTenantForTest(t)
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
	seedAccountTenantForTest(t)
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
	seedAccountTenantForTest(t)
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
	seedAccountTenantForTest(t)
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
	seedAccountTenantForTest(t)
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
	seedAccountTenantForTest(t)
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
	seedAccountTenantForTest(t)
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

func TestAwsEventBridge_ELB_CloudTrail_DeleteLoadBalancer(t *testing.T) {
	seedAccountTenantForTest(t)
	lbArn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-alb/50dc6c495c0c9188"
	sqsMessageBody := `{
		"version": "0",
		"id": "elb-delete-lb-001",
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.elasticloadbalancing",
		"account": "123456789012",
		"time": "2026-04-08T18:00:00Z",
		"region": "us-east-1",
		"resources": [],
		"detail": {
			"eventSource": "elasticloadbalancing.amazonaws.com",
			"eventName": "DeleteLoadBalancer",
			"awsRegion": "us-east-1",
			"requestParameters": {
				"loadBalancerArn": "` + lbArn + `"
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

	// A load balancer disappearing is the incident-worthy case: it must still
	// raise a visible event, unlike routine ELB activity (see the
	// ResourceSync_* tests below), even though both rules match this same
	// delivery independently.
	require.NotEmpty(t, processedEvent.EventId, "ELB_CloudTrail_Events should have matched and emitted an event")
	assert.Equal(t, lbArn+":us-east-1:123456789012", processedEvent.EventId)
	assert.Equal(t, "ELB DeleteLoadBalancer: "+lbArn, processedEvent.Title)
	assert.Equal(t, providers.EventSeverityInfo, processedEvent.EventSeverity)
	assert.Equal(t, providers.EventStatusClosed, processedEvent.EventStatus)
	assert.Equal(t, "AWSELB", processedEvent.ResourceServiceName)
	assert.Equal(t, lbArn, processedEvent.ResourceId)
	assert.Equal(t, "loadbalancer", processedEvent.ResourceType)
}

func TestAwsEventBridge_ELB_CloudTrail_ResourceSync_CreateLoadBalancer(t *testing.T) {
	seedAccountTenantForTest(t)
	lbArn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-alb/50dc6c495c0c9188"
	sqsMessageBody := `{
		"version": "0",
		"id": "elb-create-lb-001",
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.elasticloadbalancing",
		"account": "123456789012",
		"time": "2026-04-08T18:00:00Z",
		"region": "us-east-1",
		"resources": [],
		"detail": {
			"eventSource": "elasticloadbalancing.amazonaws.com",
			"eventName": "CreateLoadBalancer",
			"awsRegion": "us-east-1",
			"requestParameters": {
				"name": "my-alb",
				"type": "application"
			},
			"responseElements": {
				"loadBalancers": [
					{"loadBalancerArn": "` + lbArn + `", "dNSName": "my-alb-123.us-east-1.elb.amazonaws.com", "type": "application"}
				]
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

	// ELB_CloudTrail_Resource_Sync is an actions_only bookkeeping rule:
	// creating a load balancer is routine, not an incident, so it runs
	// update_cloud_resource for its side effect and emits NO downstream event.
	assert.Equal(t, 1, dummyHandler.getAccountFromCloudProviderIdCallCount, "account should have been resolved for the matched rule")
	assert.Empty(t, processedEvent.EventId, "actions_only rule must not emit a downstream event")
}

func TestAwsEventBridge_SQS_CloudTrail_ResourceSync_CreateQueue(t *testing.T) {
	seedAccountTenantForTest(t)
	queueUrl := "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"
	sqsMessageBody := `{
		"version": "0",
		"id": "sqs-create-queue-001",
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.sqs",
		"account": "123456789012",
		"time": "2026-04-08T18:15:00Z",
		"region": "us-east-1",
		"resources": [],
		"detail": {
			"eventSource": "sqs.amazonaws.com",
			"eventName": "CreateQueue",
			"awsRegion": "us-east-1",
			"requestParameters": {
				"queueName": "my-queue"
			},
			"responseElements": {
				"queueUrl": "` + queueUrl + `"
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

	// SQS_CloudTrail_Resource_Sync is an actions_only bookkeeping rule:
	// creating a queue is routine, not an incident, so it runs
	// update_cloud_resource for its side effect and emits NO downstream event.
	assert.Equal(t, 1, dummyHandler.getAccountFromCloudProviderIdCallCount, "account should have been resolved for the matched rule")
	assert.Empty(t, processedEvent.EventId, "actions_only rule must not emit a downstream event")
}

func TestAwsEventBridge_SQS_CloudTrail_DeleteQueue(t *testing.T) {
	seedAccountTenantForTest(t)
	queueUrl := "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"
	sqsMessageBody := `{
		"version": "0",
		"id": "sqs-delete-queue-001",
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.sqs",
		"account": "123456789012",
		"time": "2026-04-08T18:20:00Z",
		"region": "us-east-1",
		"resources": [],
		"detail": {
			"eventSource": "sqs.amazonaws.com",
			"eventName": "DeleteQueue",
			"awsRegion": "us-east-1",
			"requestParameters": {
				"queueUrl": "` + queueUrl + `"
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

	require.NotEmpty(t, processedEvent.EventId, "SQS_CloudTrail_Events should have matched and emitted an event")
	// resource_id/fingerprint/title derive the queue name (last path segment
	// of queueUrl) rather than the raw URL, so this matches the row the
	// bulk inventory sync stores it under (Id=queueName, not queueUrl).
	assert.Equal(t, "my-queue:us-east-1:123456789012", processedEvent.EventId)
	assert.Equal(t, "SQS DeleteQueue: my-queue", processedEvent.Title)
	assert.Equal(t, "my-queue", processedEvent.ResourceId)
	assert.Equal(t, "queue", processedEvent.ResourceType)
}

func TestAwsEventBridge_SNS_CloudTrail_DeleteTopic(t *testing.T) {
	seedAccountTenantForTest(t)
	topicArn := "arn:aws:sns:us-east-1:123456789012:my-topic"
	sqsMessageBody := `{
		"version": "0",
		"id": "sns-delete-topic-001",
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.sns",
		"account": "123456789012",
		"time": "2026-04-08T18:25:00Z",
		"region": "us-east-1",
		"resources": [],
		"detail": {
			"eventSource": "sns.amazonaws.com",
			"eventName": "DeleteTopic",
			"awsRegion": "us-east-1",
			"requestParameters": {
				"topicArn": "` + topicArn + `"
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

	// A topic disappearing is the incident-worthy case: it must still raise a
	// visible event, unlike routine SNS activity (see the ResourceSync_*
	// tests below), even though both rules match this same delivery
	// independently.
	require.NotEmpty(t, processedEvent.EventId, "SNS_CloudTrail_Events should have matched and emitted an event")
	assert.Equal(t, topicArn+":us-east-1:123456789012", processedEvent.EventId)
	assert.Equal(t, "SNS DeleteTopic: "+topicArn, processedEvent.Title)
	assert.Equal(t, providers.EventSeverityInfo, processedEvent.EventSeverity)
	assert.Equal(t, providers.EventStatusClosed, processedEvent.EventStatus)
	assert.Equal(t, "AmazonSNS", processedEvent.ResourceServiceName)
	// Inventory (aws_sns.go) stores topics by name, not ARN — ResourceId must
	// match that so "view resource" linking resolves to the real row.
	assert.Equal(t, "my-topic", processedEvent.ResourceId)
	assert.Equal(t, "topic", processedEvent.ResourceType)
}

func TestAwsEventBridge_SNS_CloudTrail_ResourceSync_CreateTopic(t *testing.T) {
	seedAccountTenantForTest(t)
	topicArn := "arn:aws:sns:us-east-1:123456789012:my-topic"
	sqsMessageBody := `{
		"version": "0",
		"id": "sns-create-topic-001",
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.sns",
		"account": "123456789012",
		"time": "2026-04-08T18:25:00Z",
		"region": "us-east-1",
		"resources": [],
		"detail": {
			"eventSource": "sns.amazonaws.com",
			"eventName": "CreateTopic",
			"awsRegion": "us-east-1",
			"requestParameters": {
				"name": "my-topic"
			},
			"responseElements": {
				"topicArn": "` + topicArn + `"
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

	// SNS_CloudTrail_Resource_Sync is an actions_only bookkeeping rule:
	// creating a topic is routine, not an incident, so it runs
	// update_cloud_resource for its side effect and emits NO downstream event.
	assert.Equal(t, 1, dummyHandler.getAccountFromCloudProviderIdCallCount, "account should have been resolved for the matched rule")
	assert.Empty(t, processedEvent.EventId, "actions_only rule must not emit a downstream event")
}

// renderActionParams renders a named rule's single action params directly,
// exercising the same param-rendering path executeAction uses, without
// going through update_cloud_resource's DB call. This lets tests assert on
// resource_id/resource_type/new_status directly — the values responsible
// for every ID-mapping bug fixed in this file's rules — instead of only
// checking whether a rule matched and stayed silent.
func renderActionParams(t *testing.T, ruleName string, sqsMessageBody string) map[string]any {
	t.Helper()

	rules, err := GetEventRules("")
	require.NoError(t, err)

	var rule *EventProcessingRule
	for i := range rules.Rules {
		if rules.Rules[i].Name == ruleName {
			rule = &rules.Rules[i]
			break
		}
	}
	require.NotNil(t, rule, "rule %q not found", ruleName)
	require.Len(t, rule.Actions, 1, "expected exactly one action on rule %q", ruleName)

	ebEvent, err := parseEventBridgeEvent([]byte(sqsMessageBody))
	require.NoError(t, err)

	p := &TemplatedEventBridgeProcessor{}
	pCtx := providers.NewCloudProviderContext(context.Background())
	templateData := p.prepareTemplateData(pCtx, ebEvent, providers.Account{})

	rendered := make(map[string]any)
	for k, v := range rule.Actions[0].Params {
		rendered[k] = p.renderParamValue(pCtx, k, v, templateData)
	}
	return rendered
}

func TestAwsEventBridge_ELB_CloudTrail_ResourceSync_CreateLoadBalancer_Params(t *testing.T) {
	lbArn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-alb/50dc6c495c0c9188"
	sqsMessageBody := `{
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.elasticloadbalancing",
		"account": "123456789012",
		"time": "2026-04-08T18:00:00Z",
		"region": "us-east-1",
		"detail": {
			"eventSource": "elasticloadbalancing.amazonaws.com",
			"eventName": "CreateLoadBalancer",
			"requestParameters": {"name": "my-alb", "type": "application"},
			"responseElements": {
				"loadBalancers": [{"loadBalancerArn": "` + lbArn + `", "type": "application"}]
			}
		}
	}`

	params := renderActionParams(t, "ELB_CloudTrail_Resource_Sync", sqsMessageBody)
	assert.Equal(t, lbArn, params["resource_id"])
	// aws_elb.go stores ALBs as "application_loadbalancer", not the Classic-only "loadbalancer".
	assert.Equal(t, "application_loadbalancer", params["resource_type"])
	assert.Equal(t, "Active", params["new_status"])
}

func TestAwsEventBridge_ELB_CloudTrail_ResourceSync_DeleteLoadBalancer_Modern_Params(t *testing.T) {
	lbArn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-alb/50dc6c495c0c9188"
	sqsMessageBody := `{
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.elasticloadbalancing",
		"account": "123456789012",
		"time": "2026-04-08T18:00:00Z",
		"region": "us-east-1",
		"detail": {
			"eventSource": "elasticloadbalancing.amazonaws.com",
			"eventName": "DeleteLoadBalancer",
			"requestParameters": {"loadBalancerArn": "` + lbArn + `"}
		}
	}`

	params := renderActionParams(t, "ELB_CloudTrail_Resource_Sync", sqsMessageBody)
	assert.Equal(t, lbArn, params["resource_id"])
	// aws_elb.go stores ALBs as "application_loadbalancer", not the Classic-only "loadbalancer" —
	// a hardcoded "loadbalancer" here would miss the row on the pre-reconcile's type match.
	assert.Equal(t, "application_loadbalancer", params["resource_type"])
	assert.Equal(t, "Deleted", params["new_status"])
}

func TestAwsEventBridge_ELB_CloudTrail_ResourceSync_DeleteLoadBalancer_Classic_Params(t *testing.T) {
	// Classic ELB's DeleteLoadBalancer has no loadBalancerArn, only
	// loadBalancerName. aws_elb.go stores Classic rows with Id = name, so the
	// key must be the bare name: a constructed ARN misses that row when the
	// live lookup finds nothing (the LB is gone) and inserts an orphan.
	sqsMessageBody := `{
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.elasticloadbalancing",
		"account": "123456789012",
		"time": "2026-04-08T18:00:00Z",
		"region": "us-east-1",
		"detail": {
			"eventSource": "elasticloadbalancing.amazonaws.com",
			"eventName": "DeleteLoadBalancer",
			"requestParameters": {"loadBalancerName": "my-classic-elb"}
		}
	}`

	params := renderActionParams(t, "ELB_CloudTrail_Resource_Sync", sqsMessageBody)
	assert.Equal(t, "my-classic-elb", params["resource_id"])
	assert.Equal(t, "loadbalancer", params["resource_type"])
	assert.Equal(t, "Deleted", params["new_status"])
}

func TestAwsEventBridge_ELB_CloudTrail_ResourceSync_CreateLoadBalancer_Classic_Params(t *testing.T) {
	// Classic CreateLoadBalancer's response is just dNSName; the name is only in
	// requestParameters. Reading it from responseElements used to render a bare
	// "...:loadbalancer/" prefix, which matched every LB in the region.
	sqsMessageBody := `{
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.elasticloadbalancing",
		"account": "123456789012",
		"time": "2026-04-08T18:00:00Z",
		"region": "us-east-1",
		"detail": {
			"eventSource": "elasticloadbalancing.amazonaws.com",
			"eventName": "CreateLoadBalancer",
			"requestParameters": {"loadBalancerName": "my-classic-elb", "availabilityZones": ["us-east-1a"]},
			"responseElements": {"dNSName": "my-classic-elb-1234.us-east-1.elb.amazonaws.com"}
		}
	}`

	params := renderActionParams(t, "ELB_CloudTrail_Resource_Sync", sqsMessageBody)
	assert.Equal(t, "my-classic-elb", params["resource_id"])
	assert.Equal(t, "loadbalancer", params["resource_type"])
	assert.Equal(t, "Active", params["new_status"])
}

func TestAwsEventBridge_ELB_CloudTrail_ResourceSync_NoIdentifier_RendersBlank(t *testing.T) {
	// A failed CreateLoadBalancer carries neither an ARN nor a Classic name. The
	// key must render blank (which updateCloudResource skips), never an ARN prefix.
	sqsMessageBody := `{
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.elasticloadbalancing",
		"account": "123456789012",
		"time": "2026-04-08T18:00:00Z",
		"region": "us-east-1",
		"detail": {
			"eventSource": "elasticloadbalancing.amazonaws.com",
			"eventName": "CreateLoadBalancer",
			"errorCode": "ValidationError",
			"requestParameters": {"name": "my-alb", "type": "application"}
		}
	}`

	params := renderActionParams(t, "ELB_CloudTrail_Resource_Sync", sqsMessageBody)
	assert.Equal(t, "", strings.TrimSpace(fmt.Sprint(params["resource_id"])))
}

func TestFindLookupMatch(t *testing.T) {
	web2 := providers.Resource{Id: "web-2", Name: "web-2", Arn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/web-2"}
	web := providers.Resource{Id: "web", Name: "web", Arn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/web"}
	orders := providers.Resource{Id: "orders", Name: "orders", Arn: "arn:aws:lambda:us-east-1:123456789012:function:orders"}

	// Deleted "web" is gone; the Contains fallback still returns "web-2". It must not be used.
	_, found := findLookupMatch([]providers.Resource{web2}, "web")
	assert.False(t, found)

	// When both come back, pick the exact one regardless of order.
	got, found := findLookupMatch([]providers.Resource{web2, web}, "web")
	assert.True(t, found)
	assert.Equal(t, "web", got.Id)

	// Exact ARN key.
	got, found = findLookupMatch([]providers.Resource{orders}, orders.Arn)
	assert.True(t, found)
	assert.Equal(t, "orders", got.Id)

	// Qualified Lambda ARN resolved by GetFunction to the base function.
	got, found = findLookupMatch([]providers.Resource{orders}, orders.Arn+":PROD")
	assert.True(t, found)
	assert.Equal(t, "orders", got.Id)

	// A longer function name sharing the prefix is not a qualifier.
	_, found = findLookupMatch([]providers.Resource{orders}, orders.Arn+"-v2")
	assert.False(t, found)

	// DeleteQueue "orders": the queue is gone, but the region scan's
	// strings.Contains filter still returns "orders-dlq". It must not be
	// adopted, or the DLQ is marked Deleted and "orders" stays Active.
	ordersDlq := providers.Resource{Id: "orders-dlq", Name: "orders-dlq", Arn: "arn:aws:sqs:us-east-1:123456789012:orders-dlq"}
	_, found = findLookupMatch([]providers.Resource{ordersDlq}, "orders")
	assert.False(t, found)

	// Same for SNS: DeleteTopic "alerts" with "alerts-critical" still present.
	alertsCritical := providers.Resource{Id: "alerts-critical", Name: "alerts-critical", Arn: "arn:aws:sns:us-east-1:123456789012:alerts-critical"}
	_, found = findLookupMatch([]providers.Resource{alertsCritical}, "alerts")
	assert.False(t, found)
}

func TestAwsEventBridge_ELB_CloudTrail_ResourceSync_SetSecurityGroups_Params(t *testing.T) {
	lbArn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-alb/50dc6c495c0c9188"
	sqsMessageBody := `{
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.elasticloadbalancing",
		"account": "123456789012",
		"time": "2026-04-08T18:00:00Z",
		"region": "us-east-1",
		"detail": {
			"eventSource": "elasticloadbalancing.amazonaws.com",
			"eventName": "SetSecurityGroups",
			"requestParameters": {"loadBalancerArn": "` + lbArn + `", "securityGroups": ["sg-0123456789"]}
		}
	}`

	params := renderActionParams(t, "ELB_CloudTrail_Resource_Sync", sqsMessageBody)
	assert.Equal(t, lbArn, params["resource_id"])
	// aws_elb.go stores ALBs as "application_loadbalancer", not the Classic-only "loadbalancer".
	assert.Equal(t, "application_loadbalancer", params["resource_type"])
	assert.Equal(t, "", params["new_status"], "SetSecurityGroups doesn't change the LB's lifecycle status")
}

func TestAwsEventBridge_SQS_CloudTrail_ResourceSync_CreateQueue_Params(t *testing.T) {
	queueUrl := "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"
	sqsMessageBody := `{
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.sqs",
		"account": "123456789012",
		"time": "2026-04-08T18:15:00Z",
		"region": "us-east-1",
		"detail": {
			"eventSource": "sqs.amazonaws.com",
			"eventName": "CreateQueue",
			"requestParameters": {"queueName": "my-queue"},
			"responseElements": {"queueUrl": "` + queueUrl + `"}
		}
	}`

	params := renderActionParams(t, "SQS_CloudTrail_Resource_Sync", sqsMessageBody)
	assert.Equal(t, "my-queue", params["resource_id"], "must match the bulk inventory sync's Id (queue name), not the raw queueUrl")
	assert.Equal(t, "Active", params["new_status"])
}

func TestAwsEventBridge_SQS_CloudTrail_ResourceSync_DeleteQueue_Params(t *testing.T) {
	queueUrl := "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"
	sqsMessageBody := `{
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.sqs",
		"account": "123456789012",
		"time": "2026-04-08T18:20:00Z",
		"region": "us-east-1",
		"detail": {
			"eventSource": "sqs.amazonaws.com",
			"eventName": "DeleteQueue",
			"requestParameters": {"queueUrl": "` + queueUrl + `"}
		}
	}`

	params := renderActionParams(t, "SQS_CloudTrail_Resource_Sync", sqsMessageBody)
	assert.Equal(t, "my-queue", params["resource_id"], "must match the bulk inventory sync's Id (queue name), not the raw queueUrl")
	assert.Equal(t, "Deleted", params["new_status"])
}

func TestAwsEventBridge_SNS_CloudTrail_ResourceSync_CreateTopic_Params(t *testing.T) {
	topicArn := "arn:aws:sns:us-east-1:123456789012:my-topic"
	sqsMessageBody := `{
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.sns",
		"account": "123456789012",
		"time": "2026-04-08T18:25:00Z",
		"region": "us-east-1",
		"detail": {
			"eventSource": "sns.amazonaws.com",
			"eventName": "CreateTopic",
			"requestParameters": {"name": "my-topic"},
			"responseElements": {"topicArn": "` + topicArn + `"}
		}
	}`

	params := renderActionParams(t, "SNS_CloudTrail_Resource_Sync", sqsMessageBody)
	assert.Equal(t, "my-topic", params["resource_id"], "must match the bulk inventory sync's Id (topic name), not the raw topicArn")
	assert.Equal(t, "topic", params["resource_type"])
	assert.Equal(t, "Active", params["new_status"])
}

func TestAwsEventBridge_SNS_CloudTrail_ResourceSync_DeleteTopic_Params(t *testing.T) {
	topicArn := "arn:aws:sns:us-east-1:123456789012:my-topic"
	sqsMessageBody := `{
		"detail-type": "AWS API Call via CloudTrail",
		"source": "aws.sns",
		"account": "123456789012",
		"time": "2026-04-08T18:26:00Z",
		"region": "us-east-1",
		"detail": {
			"eventSource": "sns.amazonaws.com",
			"eventName": "DeleteTopic",
			"requestParameters": {"topicArn": "` + topicArn + `"}
		}
	}`

	params := renderActionParams(t, "SNS_CloudTrail_Resource_Sync", sqsMessageBody)
	assert.Equal(t, "my-topic", params["resource_id"], "must match the bulk inventory sync's Id (topic name), not the raw topicArn")
	assert.Equal(t, "Deleted", params["new_status"])
}
