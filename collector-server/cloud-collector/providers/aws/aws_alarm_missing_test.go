package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storedAlarm is an alarm as IsAlarmMissing reads it from Meta["AlarmDetails"]:
// the event Raw map after the JSON round trip through the stored resource row.
func storedAlarm(t *testing.T, alarm types.MetricAlarm) any {
	t.Helper()
	b, err := json.Marshal(buildAlarmRawMap(alarm))
	require.NoError(t, err)
	var stored map[string]any
	require.NoError(t, json.Unmarshal(b, &stored))
	return stored
}

func alarmDim(name, value string) types.Dimension {
	return types.Dimension{Name: aws.String(name), Value: aws.String(value)}
}

func simpleAlarm(namespace, metricName string, dims ...types.Dimension) types.MetricAlarm {
	return types.MetricAlarm{
		AlarmName:  aws.String(metricName + "-alarm"),
		Namespace:  aws.String(namespace),
		MetricName: aws.String(metricName),
		Dimensions: dims,
	}
}

// mathAlarm alarms on an expression over metricNames, each scoped by dims.
func mathAlarm(namespace string, metricNames []string, dims ...types.Dimension) types.MetricAlarm {
	queries := []types.MetricDataQuery{}
	for i, name := range metricNames {
		queries = append(queries, types.MetricDataQuery{
			Id: aws.String(fmt.Sprintf("m%d", i+1)),
			MetricStat: &types.MetricStat{
				Metric: &types.Metric{Namespace: aws.String(namespace), MetricName: aws.String(name), Dimensions: dims},
				Period: aws.Int32(60),
				Stat:   aws.String("Sum"),
			},
		})
	}
	queries = append(queries, types.MetricDataQuery{Id: aws.String("e1"), Expression: aws.String("m1 / m2"), ReturnData: aws.Bool(true)})
	return types.MetricAlarm{AlarmName: aws.String("math-alarm"), Metrics: queries}
}

func alarmTemplate(t *testing.T, service, name string) providers.AlarmTemplate {
	t.Helper()
	templates, err := LoadAlarmTemplates(service)
	require.NoError(t, err)
	for _, template := range templates {
		if template.Name == name {
			return template
		}
	}
	require.FailNow(t, "template not found", "%s in %s", name, service)
	return providers.AlarmTemplate{}
}

// A covering metric whose namespace has no known resource dimension makes every
// alarm fail to match, so the template would be recommended for every resource
// even where its alarm exists.
func TestAlarmTemplatesResolveToAResourceDimension(t *testing.T) {
	all := GetAllTemplates()
	services := make([]string, 0, len(all))
	for service := range all {
		services = append(services, service)
	}
	sort.Strings(services)
	assert.Equal(t, []string{"alb", "clb", "dynamodb", "ec2", "elasticache", "lambda", "nlb", "rds", "sns", "sqs"}, services)

	for service, templates := range all {
		for _, template := range templates {
			for _, metric := range coveringMetrics(template) {
				assert.NotEmpty(t, getDimensionNameForNamespace(metric.Namespace),
					"%s/%s: covering metric %q has no resource dimension", service, template.Name, metric.MetricName)
			}
		}
	}
}

// The rules each newly scanned service produces. Every one fires on most resources
// of a high-cardinality service, so none is rated above Medium.
func TestAlarmMissingRuleSetPerService(t *testing.T) {
	want := map[string][]string{
		"dynamodb": {
			"aws_dynamodb_read_throttle_events_alarm_missing",
			"aws_dynamodb_write_throttle_events_alarm_missing",
		},
		"elasticache": {
			"aws_elasticache_memory_usage_alarm_missing",
			"aws_elasticache_freeable_memory_alarm_missing",
			"aws_elasticache_connections_alarm_missing",
		},
		"lambda": {
			"aws_lambda_error_rate_alarm_missing",
			"aws_lambda_throttles_alarm_missing",
			"aws_lambda_dead_letter_errors_alarm_missing",
		},
		"sns": {"aws_sns_number_of_notifications_failed_alarm_missing"},
		"sqs": {
			"aws_sqs_approximate_number_of_messages_visible_alarm_missing",
			"aws_sqs_approximate_age_of_oldest_message_alarm_missing",
		},
	}
	for service, rules := range want {
		templates, err := LoadAlarmTemplates(service)
		require.NoError(t, err)
		got := []string{}
		for _, template := range templates {
			got = append(got, template.Name)
			assert.NotEqual(t, providers.RecommendationSeverityHigh, providers.RecommendationSeverityFromString(template.Severity), template.Name)
		}
		assert.Equal(t, rules, got, service)
	}
}

func TestIsAlarmMissingWithoutAlarmListingIsUnknown(t *testing.T) {
	template := alarmTemplate(t, "sqs", "aws_sqs_approximate_number_of_messages_visible_alarm_missing")

	_, err := IsAlarmMissing(providers.Resource{Id: "orders", Meta: map[string]any{}}, template, "orders")
	assert.ErrorIs(t, err, ErrAlarmDetailsUnknown)

	missing, err := IsAlarmMissing(providers.Resource{Id: "orders", Meta: map[string]any{"AlarmDetails": nil}}, template, "orders")
	require.NoError(t, err)
	assert.True(t, missing, "a listing with no alarms for the resource means the alarm is missing")
}

func TestAlarmCoverage(t *testing.T) {
	fn := alarmDim("FunctionName", "orders-worker")
	alb := alarmDim("LoadBalancer", "app/web/abc123")

	tests := []struct {
		name      string
		service   string
		template  string
		dimension string
		alarm     types.MetricAlarm
		covered   bool
	}{
		{"same metric on the function", "lambda", "aws_lambda_error_rate_alarm_missing", "orders-worker",
			simpleAlarm("AWS/Lambda", "Errors", fn), true},
		{"same metric on another function", "lambda", "aws_lambda_error_rate_alarm_missing", "orders-worker",
			simpleAlarm("AWS/Lambda", "Errors", alarmDim("FunctionName", "billing")), false},
		{"error-rate expression over the function's errors", "lambda", "aws_lambda_error_rate_alarm_missing", "orders-worker",
			mathAlarm("AWS/Lambda", []string{"Errors", "Invocations"}, fn), true},
		{"another metric on the function", "lambda", "aws_lambda_error_rate_alarm_missing", "orders-worker",
			simpleAlarm("AWS/Lambda", "Throttles", fn), false},
		{"the error-rate alarm Create Alarm makes", "alb", "aws_alb_http_error_rate_alarm_missing", "app/web/abc123",
			mathAlarm("AWS/ApplicationELB", []string{"HTTPCode_Target_4XX_Count", "HTTPCode_Target_5XX_Count", "RequestCount"}, alb), true},
		{"a plain 5XX count alarm", "alb", "aws_alb_http_error_rate_alarm_missing", "app/web/abc123",
			simpleAlarm("AWS/ApplicationELB", "HTTPCode_ELB_5XX_Count", alb), true},
		{"a request count alarm", "alb", "aws_alb_http_error_rate_alarm_missing", "app/web/abc123",
			simpleAlarm("AWS/ApplicationELB", "RequestCount", alb), false},
		{"a per-zone classic load balancer alarm", "clb", "aws_clb_unhealthy_host_count_alarm_missing", "legacy-web",
			simpleAlarm("AWS/ELB", "UnHealthyHostCount", alarmDim("AvailabilityZone", "us-east-1a"), alarmDim("LoadBalancerName", "legacy-web")), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template := alarmTemplate(t, tt.service, tt.template)
			resource := providers.Resource{Id: tt.dimension, Meta: map[string]any{"AlarmDetails": []any{storedAlarm(t, tt.alarm)}}}
			missing, err := IsAlarmMissing(resource, template, tt.dimension)
			require.NoError(t, err)
			assert.Equal(t, !tt.covered, missing)
		})
	}
}

// A classic load balancer alarm is filed under the load balancer whatever order
// CloudWatch lists its dimensions in, so discovery attaches it to the right resource.
func TestClassicLoadBalancerAlarmIsAboutTheLoadBalancer(t *testing.T) {
	got := EnrichCloudWatchAlarm(context.Background(), CloudWatchAlarmInfo{
		AlarmName:       "an-alarm",
		MetricNamespace: "AWS/ELB",
		Region:          "us-east-1",
		AccountNumber:   "111122223333",
		Dimensions: []AlarmDimension{
			{Name: "AvailabilityZone", Value: "us-east-1a"},
			{Name: "LoadBalancerName", Value: "legacy-web"},
		},
	}, false, nil, nil)
	assert.Equal(t, "legacy-web", got.ResourceId)
}

func TestAlarmMissingRecommendations(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	queue := func(meta map[string]any) providers.Resource {
		return providers.Resource{
			Id:          "orders",
			Arn:         "arn:aws:sqs:us-east-1:111122223333:orders",
			ServiceName: ServiceNameSQS,
			Type:        getAwsServiceResourceType(ServiceNameSQS, "queue"),
			Region:      "us-east-1",
			Meta:        meta,
		}
	}
	dimension := providers.AlarmDimension{Name: "QueueName", Value: "orders"}

	t.Run("recommends only the alarms the resource lacks", func(t *testing.T) {
		resource := queue(map[string]any{"AlarmDetails": []any{
			storedAlarm(t, simpleAlarm("AWS/SQS", "ApproximateNumberOfMessagesVisible", alarmDim("QueueName", "orders"))),
		}})
		recs := alarmMissingRecommendations(ctx, "sqs", resource, dimension, map[string]any{"queue_name": "orders"})
		require.Len(t, recs, 1)
		rec := recs[0]
		assert.False(t, rec.Unevaluated)
		assert.Equal(t, "aws_sqs_approximate_age_of_oldest_message_alarm_missing", rec.RuleName)
		assert.Equal(t, providers.RecommendationSeverityMedium, rec.Severity)
		assert.Equal(t, "orders", rec.Data["queue_name"])
		alarmConfig, ok := rec.Data["alarm_config"].(providers.AlarmCreationConfig)
		require.True(t, ok)
		assert.Equal(t, "aws_sqs_approximate_age_of_oldest_message_alarm_missing-orders", alarmConfig.AlarmName)
		assert.Equal(t, []providers.AlarmDimension{dimension}, alarmConfig.Dimensions)
		assert.NoError(t, ValidateAlarmConfig(alarmConfig))
	})

	t.Run("a resource without an alarm listing is not evaluated", func(t *testing.T) {
		recs := alarmMissingRecommendations(ctx, "sqs", queue(map[string]any{"QueueArn": "arn"}), dimension, nil)
		require.Len(t, recs, 2)
		for _, rec := range recs {
			assert.True(t, rec.Unevaluated, rec.RuleName)
		}
	})

	t.Run("memory usage is not recommended for memcached", func(t *testing.T) {
		cluster := func(engine string) providers.Resource {
			return providers.Resource{Id: "cache-1", Region: "us-east-1", Meta: map[string]any{"Engine": engine, "AlarmDetails": []any{}}}
		}
		rules := func(recs []providers.Recommendation) []string {
			names := []string{}
			for _, rec := range recs {
				names = append(names, rec.RuleName)
			}
			return names
		}
		cacheDimension := providers.AlarmDimension{Name: "CacheClusterId", Value: "cache-1"}
		assert.NotContains(t, rules(alarmMissingRecommendations(ctx, "elasticache", cluster("memcached"), cacheDimension, nil)), "aws_elasticache_memory_usage_alarm_missing")
		assert.Contains(t, rules(alarmMissingRecommendations(ctx, "elasticache", cluster("redis"), cacheDimension, nil)), "aws_elasticache_memory_usage_alarm_missing")
	})
}

func TestQueueServiceRecommendsMissingAlarms(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	resources := []providers.Resource{{
		Id:          "orders",
		ServiceName: ServiceNameSQS,
		Type:        getAwsServiceResourceType(ServiceNameSQS, "queue"),
		Region:      "us-east-1",
		Meta:        map[string]any{"QueueArn": "arn:aws:sqs:us-east-1:111122223333:orders", "AlarmDetails": []any{}},
	}}
	recs, err := (&awsQueueService{}).GetRecommendations(ctx, providers.Account{}, providers.ListRecommendationsRequest{}, resources)
	require.NoError(t, err)

	alarmRules := []string{}
	for _, rec := range recs {
		if _, ok := rec.Data["alarm_config"]; ok {
			alarmRules = append(alarmRules, rec.RuleName)
		}
	}
	assert.ElementsMatch(t, []string{
		"aws_sqs_approximate_number_of_messages_visible_alarm_missing",
		"aws_sqs_approximate_age_of_oldest_message_alarm_missing",
	}, alarmRules)
}

// Apply creates an alarm only for the service's own alarm-missing rules; anything
// else stays unsupported, as before.
func TestAlarmMissingApplyRoutesOnlyOwnRules(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	services := map[string]awsService{
		"aws_dynamodb_pitr_enabled":      &amazonDynamoDB{},
		"aws_elasticache_engine_version": &amazonElasticCache{},
		"aws_lambda_tracing":             &awsLambda{},
		"aws_sns_sse_enabled_cmk":        &amazonSns{},
		"aws_sqs_dlq_configured":         &awsQueueService{},
	}
	for ownRule, service := range services {
		for _, rule := range []string{ownRule, "aws_ec2_cpu_utilization_alarm_missing"} {
			err := service.ApplyRecommendation(ctx, providers.Account{}, providers.Recommendation{RuleName: rule})
			assert.True(t, errors.Is(err, errors.ErrUnsupported), "%T should not apply %s", service, rule)
		}
	}
}
