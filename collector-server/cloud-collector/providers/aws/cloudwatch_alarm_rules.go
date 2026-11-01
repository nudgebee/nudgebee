package aws

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// alarmRegionTimeout bounds DescribeAlarms paging for one region.
const alarmRegionTimeout = 5 * time.Minute

// describeRegionAlarmRules lists every CloudWatch alarm in one region as event
// rules. Composite alarms are requested explicitly: DescribeAlarms returns only
// metric alarms when AlarmTypes is omitted, so a composite alarm's state-change
// event previously had no rule definition to resolve.
//
// It returns nothing on error rather than the pages read so far, so a region is
// either stored whole or skipped whole.
func describeRegionAlarmRules(ctx context.Context, logger *slog.Logger, client cloudwatch.DescribeAlarmsAPIClient, regionName, accountNumber string) ([]providers.EventRule, error) {
	var rules []providers.EventRule
	paginator := cloudwatch.NewDescribeAlarmsPaginator(client, &cloudwatch.DescribeAlarmsInput{
		AlarmTypes: []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm, cwtypes.AlarmTypeCompositeAlarm},
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, alarm := range page.MetricAlarms {
			rule, err := metricAlarmRule(logger, alarm, regionName, accountNumber)
			if err != nil {
				logger.Error("unable to serialize data", "error", err)
				continue
			}
			rules = append(rules, rule)
		}
		for _, alarm := range page.CompositeAlarms {
			rule, err := compositeAlarmRule(alarm, regionName, accountNumber)
			if err != nil {
				logger.Error("unable to serialize data", "error", err)
				continue
			}
			rules = append(rules, rule)
		}
	}
	return rules, nil
}

func metricAlarmRule(logger *slog.Logger, alarm cwtypes.MetricAlarm, regionName, accountNumber string) (providers.EventRule, error) {
	namespace, ok := cloudwatchNamespaceServiceMap[aws.ToString(alarm.Namespace)]
	serviceName := ""
	if ok {
		serviceName = namespace.ServiceName
	} else {
		// Fallback for custom namespaces
		serviceName = "CloudWatch"
		logger.Info("using fallback for custom namespace in event rules", "namespace", aws.ToString(alarm.Namespace), "alarmName", aws.ToString(alarm.AlarmName))
	}

	exprJson, err := common.MarshalJson(alarm)
	if err != nil {
		return providers.EventRule{}, fmt.Errorf("serialize alarm %q: %w", aws.ToString(alarm.AlarmName), err)
	}

	// Handle optional fields safely to avoid nil pointer dereference
	var duration time.Duration
	if alarm.Period != nil {
		duration = time.Second * time.Duration(*alarm.Period)
	}

	threshold := "0"
	if alarm.Threshold != nil {
		threshold = fmt.Sprintf("%f", *alarm.Threshold)
	}

	metricName := ""
	if alarm.MetricName != nil {
		metricName = *alarm.MetricName
	}

	statistic := ""
	if alarm.Statistic != "" {
		statistic = string(alarm.Statistic)
	}

	return providers.EventRule{
		Name:        aws.ToString(alarm.AlarmName),
		Description: aws.ToString(alarm.AlarmDescription),
		Summary:     fmt.Sprintf("CloudWatch Alarm '%s' in region %s", aws.ToString(alarm.AlarmName), regionName),
		Expr:        string(exprJson),
		ExternalID:  aws.ToString(alarm.AlarmArn),
		Source:      "AWS_CloudWatch_Alarm",
		Severity:    providers.EventDefinitionSeverityCritical,
		Category:    serviceName,
		Duration:    duration,
		Labels: map[string]string{
			"aws_region":                 regionName,
			"aws_alarm":                  aws.ToString(alarm.AlarmName),
			"aws_account":                accountNumber,
			"aws_service_name":           serviceName,
			"aws_event_threshold":        threshold,
			"aws_event_metric_namespace": aws.ToString(alarm.Namespace),
			"aws_event_metric_name":      metricName,
			"aws_event_metric_statistic": statistic,
		},
	}, nil
}

// compositeAlarmRule maps a composite alarm. It has no metric, threshold or
// period of its own — its definition is the AlarmRule expression over other
// alarms, carried in the serialized alarm like a metric alarm's fields are.
func compositeAlarmRule(alarm cwtypes.CompositeAlarm, regionName, accountNumber string) (providers.EventRule, error) {
	exprJson, err := common.MarshalJson(alarm)
	if err != nil {
		return providers.EventRule{}, fmt.Errorf("serialize alarm %q: %w", aws.ToString(alarm.AlarmName), err)
	}
	return providers.EventRule{
		Name:        aws.ToString(alarm.AlarmName),
		Description: aws.ToString(alarm.AlarmDescription),
		Summary:     fmt.Sprintf("CloudWatch Composite Alarm '%s' in region %s", aws.ToString(alarm.AlarmName), regionName),
		Expr:        string(exprJson),
		ExternalID:  aws.ToString(alarm.AlarmArn),
		Source:      "AWS_CloudWatch_Alarm",
		Severity:    providers.EventDefinitionSeverityCritical,
		Category:    "CloudWatch",
		Labels: map[string]string{
			"aws_region":       regionName,
			"aws_alarm":        aws.ToString(alarm.AlarmName),
			"aws_alarm_type":   "composite",
			"aws_account":      accountNumber,
			"aws_service_name": "CloudWatch",
		},
	}, nil
}

// skipAlarmRegion reports whether a DescribeAlarms failure means the region
// should be skipped this cycle rather than reported. It applies the same
// region-skip taxonomy resource discovery uses: before it, an opt-in region
// the collector cannot dial (me-south-1 for Janus) failed the whole rule sync
// every day, and no alarm from any region was stored.
func skipAlarmRegion(logger *slog.Logger, err error, regionName string) bool {
	return skipRegion(logger, err, regionName, "service", "cloudwatch alarm rules")
}
