package aws

import (
	"context"
	"fmt"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Emitting rules with no sample in precedenceCases. Each one must say why no
// other emitting rule can match its events, so precedence can't affect it.
var precedenceExemptRules = map[string]string{
	"ECS_Service_Created_EventBridge": "only emitting rule for CloudTrail CreateService calls",
	"ECR_All_Events_EventBridge":      "only emitting rule for aws.ecr events",
}

// precedenceCases feeds one sample event per emitting rule through the real
// embedded aws_runbook.yaml. Most samples also match a catch-all further down
// the file; the named rule must still be the one emitted (#38391).
var precedenceCases = []struct {
	name     string
	wantRule string
	body     string
}{
	{"ELB health-check stop", "ECS_Service_Health_Check_Failure_EventBridge",
		ecsTaskStoppedSample("ServiceSchedulerInitiated", "Task failed ELB health checks in (target-group arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/tg/abc)", `"exitCode": 137`)},
	{"deployment failed", "ECS_Service_Deployment_Failed_EventBridge",
		`{"id": "evt-deploy", "detail-type": "ECS Deployment State Change", "source": "aws.ecs", "account": "123456789012", "time": "2026-09-28T10:00:00Z", "region": "us-east-1",
		  "resources": ["arn:aws:ecs:us-east-1:123456789012:service/prod-cluster/checkout"],
		  "detail": {"eventType": "ERROR", "eventName": "SERVICE_DEPLOYMENT_FAILED", "deploymentId": "ecs-svc/123", "reason": "ECS deployment circuit breaker: tasks failed to start."}}`},
	{"CPU alarm", "ECS_Service_CPU_Or_Memory_Alarm",
		alarmSample("prod-cluster-checkout-HighCPUUtilization-Auto", "ALARM", "AWS/ECS", "CPUUtilization", `"ClusterName": "prod-cluster", "ServiceName": "checkout"`)},
	{"log error alarm", "ECS_Service_Log_Error_Alarm",
		alarmSample("prod-cluster-checkout-TaskLogErrors-Auto", "ALARM", "ECS/ContainerLogs", "ErrorCount", `"ClusterName": "prod-cluster", "ServiceName": "checkout"`)},
	{"RunningCountLow alarm", "ECS_Service_Running_Count_Low_Alarm",
		alarmSample("prod-cluster-checkout-RunningCountLow-Auto", "ALARM", "ECS/ContainerInsights", "RunningTaskCount", `"ClusterName": "prod-cluster", "ServiceName": "checkout"`)},
	{"PendingTasksTooLong alarm", "ECS_Task_Pending_Too_Long_Alarm",
		alarmSample("prod-cluster-checkout-PendingTasksTooLong-Auto", "ALARM", "ECS/ContainerInsights", "PendingTaskCount", `"ClusterName": "prod-cluster", "ServiceName": "checkout"`)},
	{"OOM stop", "ECS_Task_Stopped_OutOfMemory_EventBridge",
		ecsTaskStoppedSample("EssentialContainerExited", "OutOfMemoryError: Container killed due to memory usage", `"exitCode": 137`)},
	{"CannotPull stop", "ECS_Task_Stopped_CannotPullContainerError_EventBridge",
		ecsTaskStoppedSample("TaskFailedToStart", "CannotPullContainerError: pull image manifest has been retried 5 time(s): not found", ``)},
	{"crash stop", "ECS_Task_Stopped_Unexpectedly_EventBridge",
		ecsTaskStoppedSample("EssentialContainerExited", "Essential container in task exited", `"exitCode": 1`)},
	{"ALB 5XX alarm", "ECS_ALB_Target_5XX_Errors_Alarm",
		alarmSample("checkout-alb-5xx", "ALARM", "AWS/ApplicationELB", "HTTPCode_Target_5XX_Count", `"LoadBalancer": "app/prod-alb/1234", "TargetGroup": "targetgroup/checkout-tg/abcd"`)},
	{"scale-in stop", "ECS_Task_All_Events_EventBridge",
		ecsTaskStoppedSample("ServiceSchedulerInitiated", "Scaling activity initiated by (deployment ecs-svc/123)", `"exitCode": 0`)},
	{"service action", "ECS_Service_All_Events_EventBridge",
		`{"id": "evt-svc-action", "detail-type": "ECS Service Action", "source": "aws.ecs", "account": "123456789012", "time": "2026-09-28T10:00:00Z", "region": "us-east-1",
		  "resources": ["arn:aws:ecs:us-east-1:123456789012:service/prod-cluster/checkout"],
		  "detail": {"eventType": "WARN", "eventName": "SERVICE_TASK_START_IMPAIRED", "clusterArn": "arn:aws:ecs:us-east-1:123456789012:cluster/prod-cluster"}}`},
	{"EC2 stopped", "EC2_Instance_Unexpected_Stop_Terminate_EventBridge",
		ec2StateSample("stopped")},
	{"EC2 running", "EC2_All_Events_EventBridge",
		ec2StateSample("running")},
	{"alarm back to OK", "CloudWatch_Alarm_State_Change_Generic",
		alarmSample("prod-cluster-checkout-HighCPUUtilization-Auto", "OK", "AWS/ECS", "CPUUtilization", `"ClusterName": "prod-cluster", "ServiceName": "checkout"`)},
	{"ELB deleted", "ELB_CloudTrail_Events",
		`{"id": "evt-elb-delete", "detail-type": "AWS API Call via CloudTrail", "source": "aws.elasticloadbalancing", "account": "123456789012", "time": "2026-09-28T10:00:00Z", "region": "us-east-1",
		  "detail": {"eventSource": "elasticloadbalancing.amazonaws.com", "eventName": "DeleteLoadBalancer", "requestParameters": {"loadBalancerArn": "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/prod-alb/50dc6c495c0c9188"}}}`},
	{"SQS queue deleted", "SQS_CloudTrail_Events",
		`{"id": "evt-sqs-delete", "detail-type": "AWS API Call via CloudTrail", "source": "aws.sqs", "account": "123456789012", "time": "2026-09-28T10:00:00Z", "region": "us-east-1",
		  "detail": {"eventSource": "sqs.amazonaws.com", "eventName": "DeleteQueue", "requestParameters": {"queueUrl": "https://sqs.us-east-1.amazonaws.com/123456789012/orders"}}}`},
	{"SNS topic deleted", "SNS_CloudTrail_Events",
		`{"id": "evt-sns-delete", "detail-type": "AWS API Call via CloudTrail", "source": "aws.sns", "account": "123456789012", "time": "2026-09-28T10:00:00Z", "region": "us-east-1",
		  "detail": {"eventSource": "sns.amazonaws.com", "eventName": "DeleteTopic", "requestParameters": {"topicArn": "arn:aws:sns:us-east-1:123456789012:alerts"}}}`},
}

func TestRulePrecedence_MostSpecificEmittingRuleWins(t *testing.T) {
	seedAccountTenantForTest(t)
	rules, err := GetEventRules("")
	require.NoError(t, err)

	for _, tc := range precedenceCases {
		t.Run(tc.name, func(t *testing.T) {
			processor := NewTemplatedEventBridgeProcessor(rules, &dummyAwsProvider{})
			assert.Equal(t, tc.wantRule, emittedRule(t, processor, rules, tc.body))
		})
	}
}

// Every emitting rule needs a sample above (or an exemption), so a new rule
// placed below a catch-all that shadows it fails here instead of never firing.
func TestRulePrecedence_EveryEmittingRuleHasASample(t *testing.T) {
	rules, err := GetEventRules("")
	require.NoError(t, err)

	covered := map[string]bool{}
	for _, tc := range precedenceCases {
		covered[tc.wantRule] = true
	}
	for _, rule := range rules.Rules {
		if rule.ActionsOnly {
			continue
		}
		_, exempt := precedenceExemptRules[rule.Name]
		assert.True(t, covered[rule.Name] || exempt,
			"emitting rule %q has no precedenceCases sample: add one that it must win, placing the rule above any catch-all that also matches it", rule.Name)
	}
}

// Rules skipped after the winner must not reach AWS: for a CannotPull stop,
// ECS_Task_Stopped_Unexpectedly's Logs Insights query and
// ECS_Task_All_Events' metric fetch used to run and be thrown away.
func TestRulePrecedence_SkippedRulesDoNotCallAWS(t *testing.T) {
	seedAccountTenantForTest(t)
	rules, err := GetEventRules("")
	require.NoError(t, err)

	api := &dummyAwsProvider{}
	processor := NewTemplatedEventBridgeProcessor(rules, api)
	body := ecsTaskStoppedSample("TaskFailedToStart", "CannotPullContainerError: pull image manifest has been retried 5 time(s): not found", ``)
	require.Equal(t, "ECS_Task_Stopped_CannotPullContainerError_EventBridge", emittedRule(t, processor, rules, body))

	assert.Zero(t, api.queryLogsCallCount, "ECS_Task_Stopped_Unexpectedly's log query ran")
	assert.Zero(t, api.queryMetricesCallCount, "ECS_Task_All_Events' metric fetch ran")
}

// A catch-all's dedup must not change the winner: with dedup active, the same
// ELB health-check stop is the health-check Error on every delivery. Before
// #38391 the first delivery emitted ECS_Task_All_Events (Info/CLOSED) and only
// repeats inside its dedup window emitted the Error.
func TestRulePrecedence_WinnerStableAcrossDedup(t *testing.T) {
	seedAccountTenantForTest(t)
	rules, err := GetEventRules("")
	require.NoError(t, err)

	processor := NewTemplatedEventBridgeProcessor(rules, &dummyAwsProvider{})
	pCtx := providers.NewCloudProviderContext(context.Background())
	processor.BindContext(pCtx)

	body := ecsTaskStoppedSample("ServiceSchedulerInitiated", "Task failed ELB health checks in (target-group arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/tg/abc)", `"exitCode": 137`)
	for delivery := 1; delivery <= 2; delivery++ {
		ev, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, body, processor, &dummyProcessedEventHandler{})
		require.NoError(t, err)
		assert.Equal(t, providers.EventSeverityHigh, ev.EventSeverity, "delivery %d", delivery) // rule severity "Error"
		assert.Equal(t, providers.EventStatusFiring, ev.EventStatus, "delivery %d", delivery)
		assert.Contains(t, ev.Title, "Stopped due to Health Check Failure", "delivery %d", delivery)
	}
}

// emittedRule processes body and returns the name of the rule whose title,
// rendered against the same event, equals the emitted event's title.
func emittedRule(t *testing.T, processor *TemplatedEventBridgeProcessor, rules EventRuleSet, body string) string {
	t.Helper()
	pCtx := providers.NewCloudProviderContext(context.Background())
	ev, account, err := processSQSMessageBodyForEventBridgeEvent(pCtx, body, processor, &dummyProcessedEventHandler{})
	require.NoError(t, err)
	require.NotEmpty(t, ev.EventId, "no emitting rule matched")

	ebEvent, err := parseEventBridgeEvent([]byte(body))
	require.NoError(t, err)
	templateData := processor.prepareTemplateData(pCtx, ebEvent, account)
	for _, rule := range rules.Rules {
		if rule.ActionsOnly {
			continue
		}
		title, err := processor.renderField(pCtx, "Title", rule.EventOutput.Title, templateData)
		if err == nil && title == ev.Title {
			return rule.Name
		}
	}
	t.Fatalf("emitted title %q matches no rule's title", ev.Title)
	return ""
}

func ecsTaskStoppedSample(stopCode, stoppedReason, exitCode string) string {
	container := `"name": "app", "image": "123456789012.dkr.ecr.us-east-1.amazonaws.com/checkout:v9", "lastStatus": "STOPPED"`
	if exitCode != "" {
		container += ", " + exitCode
	}
	return fmt.Sprintf(`{"id": "evt-task-stopped", "detail-type": "ECS Task State Change", "source": "aws.ecs", "account": "123456789012", "time": "2026-09-28T10:00:00Z", "region": "us-east-1",
		"resources": ["arn:aws:ecs:us-east-1:123456789012:task/prod-cluster/abc123"],
		"detail": {
			"taskArn": "arn:aws:ecs:us-east-1:123456789012:task/prod-cluster/abc123",
			"clusterArn": "arn:aws:ecs:us-east-1:123456789012:cluster/prod-cluster",
			"taskDefinitionArn": "arn:aws:ecs:us-east-1:123456789012:task-definition/checkout:42",
			"group": "service:checkout",
			"lastStatus": "STOPPED",
			"desiredStatus": "STOPPED",
			"stopCode": %q,
			"stoppedReason": %q,
			"containers": [{%s}]
		}}`, stopCode, stoppedReason, container)
}

func alarmSample(alarmName, state, namespace, metricName, dimensions string) string {
	return fmt.Sprintf(`{"id": "evt-alarm", "detail-type": "CloudWatch Alarm State Change", "source": "aws.cloudwatch", "account": "123456789012", "time": "2026-09-28T10:00:00Z", "region": "us-east-1",
		"resources": ["arn:aws:cloudwatch:us-east-1:123456789012:alarm:%s"],
		"detail": {
			"alarmName": %q,
			"state": {"value": %q, "reason": "Threshold Crossed"},
			"previousState": {"value": "OK"},
			"configuration": {"metrics": [{
				"id": "m1",
				"metricStat": {"metric": {"namespace": %q, "name": %q, "dimensions": {%s}}, "period": 60, "stat": "Average"},
				"returnData": true
			}]}
		}}`, alarmName, alarmName, state, namespace, metricName, dimensions)
}

func ec2StateSample(state string) string {
	return fmt.Sprintf(`{"id": "evt-ec2", "detail-type": "EC2 Instance State-change Notification", "source": "aws.ec2", "account": "123456789012", "time": "2026-09-28T10:00:00Z", "region": "us-east-1",
		"resources": ["arn:aws:ec2:us-east-1:123456789012:instance/i-0abc"],
		"detail": {"instance-id": "i-0abc", "instanceId": "i-0abc", "state": %q}}`, state)
}
