package aws

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Cluster and service names often contain "-". Splitting the alarm name on "-"
// looked up service "cluster" in cluster "prod". They now come from the
// alarm's dimensions, or, for the dimensionless alarms aws_create_alarm makes,
// from the namespace.
func TestECSLogErrorAlarm_LooksUpHyphenatedClusterAndService(t *testing.T) {
	for _, tc := range []struct{ name, namespace, dimensions string }{
		{"from dimensions", "ECS/ContainerLogs", `"ClusterName": "prod-cluster", "ServiceName": "checkout-api"`},
		{"auto-created alarm, no dimensions", "ECS/checkout-api/Logs-Auto", ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seedAccountTenantForTest(t)
			rules, err := GetEventRules("")
			require.NoError(t, err)

			api := &dummyAwsProvider{}
			processor := NewTemplatedEventBridgeProcessor(rules, api)
			body := alarmSample("prod-cluster-checkout-api-TaskLogErrors-Auto", "ALARM", tc.namespace, "TaskLogErrors", tc.dimensions)
			require.Equal(t, "ECS_Service_Log_Error_Alarm", emittedRule(t, processor, rules, body))

			require.NotEmpty(t, api.getECSServiceDetailsCalls)
			for _, call := range api.getECSServiceDetailsCalls {
				assert.Equal(t, "prod-cluster", call.clusterIdentifier)
				assert.Equal(t, "checkout-api", call.serviceIdentifier)
			}
		})
	}
}

// Nothing on an ALB alarm names the ECS service behind the target group, so no
// log lookup is guessed from the alarm name.
func TestECSALB5XXAlarm_NoGuessedLogLookup(t *testing.T) {
	seedAccountTenantForTest(t)
	rules, err := GetEventRules("")
	require.NoError(t, err)

	api := &dummyAwsProvider{}
	processor := NewTemplatedEventBridgeProcessor(rules, api)
	body := alarmSample("checkout-alb-5xx", "ALARM", "AWS/ApplicationELB", "HTTPCode_Target_5XX_Count", `"LoadBalancer": "app/prod-alb/1234", "TargetGroup": "targetgroup/checkout-tg/abcd"`)
	require.Equal(t, "ECS_ALB_Target_5XX_Errors_Alarm", emittedRule(t, processor, rules, body))

	assert.Zero(t, api.queryLogsCallCount)
	assert.Zero(t, api.getECSServiceDetailsCallCount)
}

// `x | replace "service:" ""` runs strings.ReplaceAll("service:", "", x), which
// garbled the service name in the text and in the service lookup.
func TestECSHealthCheckFailure_NamesTheService(t *testing.T) {
	seedAccountTenantForTest(t)
	rules, err := GetEventRules("")
	require.NoError(t, err)

	api := &dummyAwsProvider{}
	processor := NewTemplatedEventBridgeProcessor(rules, api)
	pCtx := providers.NewCloudProviderContext(context.Background())
	body := ecsTaskStoppedSample("ServiceSchedulerInitiated", "Task failed ELB health checks in (target-group arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/tg/abc)", `"exitCode": 137`)
	ev, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, body, processor, &dummyProcessedEventHandler{})
	require.NoError(t, err)

	assert.Contains(t, ev.Description, "(part of service checkout)")
	require.Len(t, api.getECSServiceDetailsCalls, 1)
	assert.Equal(t, "prod-cluster", api.getECSServiceDetailsCalls[0].clusterIdentifier)
	assert.Equal(t, "checkout", api.getECSServiceDetailsCalls[0].serviceIdentifier)
}

// A "# ..." note inside a block-scalar template is text, not a YAML comment:
// it ends up in the alert ("# serviceName is expected string") or in a lookup
// parameter ("repo # Basic parsing"). Fail on any such note in any rule.
func TestRunbook_NoDeveloperNotesInRenderedText(t *testing.T) {
	rules, err := GetEventRules("")
	require.NoError(t, err)

	actions := regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	note := func(s string) bool { return strings.Contains(actions.ReplaceAllString(s, ""), " #") }

	for _, rule := range rules.Rules {
		out := rule.EventOutput
		for field, tpl := range map[string]EventFieldTemplate{
			"title": out.Title, "description": out.Description, "fingerprint": out.Fingerprint, "resource_id": out.ResourceId,
		} {
			assert.False(t, note(tpl.Template), "%s: %s has a developer note in its text", rule.Name, field)
		}
		for _, action := range rule.Actions {
			for key, v := range action.Params {
				if s, ok := v.(string); ok {
					assert.False(t, note(s), "%s/%s: param %s has a developer note in its value", rule.Name, action.Name, key)
				}
			}
		}
	}
}
