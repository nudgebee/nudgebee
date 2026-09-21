package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/smithy-go"
)

// func getAwsCloudwatchAlarmsForMetrices(ctx providers.CloudProviderContext, account providers.Account, serviceName string, instanceIds []string, metrices []string) (providers.ListEventResponse, error) {
// 	sess, err := getAwsSessionFromAccount(account)
// 	if err != nil {
// 		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
// 		return providers.ListEventResponse{}, err
// 	}

// 	svc := cloudwatch.New(sess)

// 	namespaceDetail := serviceCloudwatchNamespaceMap[strings.ToLower(serviceName)]
// 	if namespaceDetail.Name == "" {
// 		ctx.GetLogger().Error("namespace not found", "serviceName", serviceName)
// 		return providers.ListEventResponse{}, nil
// 	}

// 	for _, metric := range metrices {
// 		for _, instanceId := range instanceIds {
// 			alarms, err := svc.DescribeAlarmsForMetric(&cloudwatch.DescribeAlarmsForMetricInput{
// 				Namespace:  aws.String(namespaceDetail.Name),
// 				MetricName: aws.String(metric),
// 				Dimensions: []*cloudwatch.Dimension{
// 					{
// 						Name:  aws.String(namespaceDetail.ResourceDimensionName),
// 						Value: aws.String(instanceId),
// 					},
// 				},
// 			})
// 			if err != nil {
// 				ctx.GetLogger().Error("failed to fetch cloudwatch alarms", "error", err, "accountNumber", account.AccountNumber)
// 				return providers.ListEventResponse{}, err
// 			}
// 			for _, alarm := range alarms.MetricAlarms {
// 				ctx.GetLogger().Info("alarm", "alarmName", *alarm.AlarmName, "alarmArn", *alarm.AlarmArn, "alarmState", *alarm.StateValue)
// 			}
// 		}
// 	}

// 	return providers.ListEventResponse{}, nil
// }

type CloudwatchNamespace struct {
	Name                  string
	ResourceDimensionName string
	ServiceName           string
	ResourceType          string
}

var cloudwatchNamespaceServiceMap = map[string]CloudwatchNamespace{
	"AWS/EC2": {
		Name:                  "AWS/EC2",
		ResourceDimensionName: "InstanceId",
		ServiceName:           ServiceNameEc2,
		ResourceType:          "compute-instance",
	},
	"AWS/RDS": {
		Name:                  "AWS/RDS",
		ResourceDimensionName: "DBInstanceIdentifier",
		ServiceName:           ServiceNameRDS,
		ResourceType:          "db",
	},
	"AWS/PI": {
		Name:                  "AWS/PI",
		ResourceDimensionName: "DBInstanceIdentifier",
		ServiceName:           ServiceNameRDS,
		ResourceType:          "db",
	},
	"AWS/S3": {
		Name:                  "AWS/S3",
		ResourceDimensionName: "BucketName",
		ServiceName:           ServiceNameS3,
		ResourceType:          "storage",
	},
	"AWS/Lambda": {
		Name:                  "AWS/Lambda",
		ResourceDimensionName: "FunctionName",
		ServiceName:           ServiceNameLambda,
		ResourceType:          "function",
	},
	"AWS/ElastiCache": {
		Name:                  "AWS/ElastiCache",
		ResourceDimensionName: "CacheClusterId",
		ServiceName:           ServiceNameElastiCache,
		ResourceType:          "cluster",
	},
	"AWS/Kafka": {
		Name:                  "AWS/Kafka",
		ResourceDimensionName: "Cluster Name",
		ServiceName:           ServiceNameMSK,
		ResourceType:          "cluster",
	},
	"AWS/ECS": {
		Name:                  "AWS/ECS",
		ResourceDimensionName: "ClusterName",
		ServiceName:           ServiceNameECS,
		ResourceType:          "cluster",
	},
	"AWS/ELB": {
		Name:                  "AWS/ELB",
		ResourceDimensionName: "LoadBalancer",
		ServiceName:           ServiceNameELB,
		ResourceType:          "loadbalancer",
	},
	"AWS/ApplicationELB": {
		Name:                  "AWS/ApplicationELB",
		ResourceDimensionName: "LoadBalancer",
		ServiceName:           ServiceNameELB,
		ResourceType:          "loadbalancer",
	},
	"AWS/NetworkELB": {
		Name:                  "AWS/NetworkELB",
		ResourceDimensionName: "LoadBalancer",
		ServiceName:           ServiceNameELB,
		ResourceType:          "loadbalancer",
	},
	"AWS/GatewayELB": {
		Name:                  "AWS/GatewayELB",
		ResourceDimensionName: "LoadBalancer",
		ServiceName:           ServiceNameELB,
		ResourceType:          "loadbalancer",
	},
	"AWS/DynamoDB": {
		Name:                  "AWS/DynamoDB",
		ResourceDimensionName: "TableName",
		ServiceName:           ServiceNameDynamoDB,
		ResourceType:          "database",
	},
	"AWS/EKS": {
		Name:                  "AWS/EKS",
		ResourceDimensionName: "ClusterName",
		ServiceName:           ServiceNameEKS,
		ResourceType:          "cluster",
	},
	"AWS/Redshift": {
		Name:                  "AWS/Redshift",
		ResourceDimensionName: "ClusterIdentifier",
		ServiceName:           ServiceNameRedshift,
		ResourceType:          "db",
	},
	"AWS/SNS": {
		Name:                  "AWS/SNS",
		ResourceDimensionName: "TopicName",
		ServiceName:           ServiceNameSNS,
		ResourceType:          "topic",
	},
	"AWS/SQS": {
		Name:                  "AWS/SQS",
		ResourceDimensionName: "QueueName",
		ServiceName:           ServiceNameSQS,
		ResourceType:          "queue",
	},
	"AWS/Events": {
		Name:                  "AWS/Events",
		ResourceDimensionName: "RuleName",
		ServiceName:           ServiceNameEventBridge,
		ResourceType:          "rule",
	},
	"AWS/AutoScaling": {
		Name:                  "AWS/AutoScaling",
		ResourceDimensionName: "AutoScalingGroupName",
		ServiceName:           ServiceNameAutoScaling,
		ResourceType:          "autoscaling-group",
	},
	"ECS/ContainerInsights": {
		Name:                  "ECS/ContainerInsights",
		ResourceDimensionName: "ClusterName",
		ServiceName:           ServiceNameECS,
		ResourceType:          "cluster",
	},
	"AWS/Fargate": {
		Name:                  "AWS/ECS",
		ResourceDimensionName: "ServiceName",
		ServiceName:           ServiceNameFargate,
		ResourceType:          "service",
	},
	"ContainerInsights": {
		Name:                  "ContainerInsights",
		ResourceDimensionName: "ClusterName",
		ServiceName:           ServiceNameEKS,
		ResourceType:          "cluster",
	},
	"CWAgent": {
		Name:                  "CWAgent",
		ResourceDimensionName: "InstanceId",
		ServiceName:           ServiceNameEc2,
		ResourceType:          "compute-instance",
	},
	"LambdaInsights": {
		Name:                  "LambdaInsights",
		ResourceDimensionName: "FunctionName",
		ServiceName:           ServiceNameLambda,
		ResourceType:          "function",
	},
	"AWS/Backup": {
		Name:                  "AWS/Backup",
		ResourceDimensionName: "BackupVaultName",
		ServiceName:           ServiceNameBackup,
		ResourceType:          "backup-vault",
	},
	"AWS/Bedrock": {
		Name:                  "AWS/Bedrock",
		ResourceDimensionName: "ProvisionedModelArn",
		ServiceName:           ServiceNameBedrock,
		ResourceType:          "provisioned-throughput",
	},
	"AWS/CloudFormation": {
		Name:                  "AWS/CloudFormation",
		ResourceDimensionName: "StackName",
		ServiceName:           ServiceNameCloudFormation,
		ResourceType:          "stack",
	},
	"AWS/CloudFront": {
		Name:                  "AWS/CloudFront",
		ResourceDimensionName: "DistributionId",
		ServiceName:           ServiceNameCloudFront,
		ResourceType:          "distribution",
	},
	"AWS/CloudTrail": {
		Name:                  "AWS/CloudTrail",
		ResourceDimensionName: "TrailName",
		ServiceName:           ServiceNameCloudTrail,
		ResourceType:          "trail",
	},
	"AWS/CodeArtifact": {
		Name:                  "AWS/CodeArtifact",
		ResourceDimensionName: "RepositoryName",
		ServiceName:           ServiceNameCodeArtifact,
		ResourceType:          "repository",
	},
	"AWS/EFS": {
		Name:                  "AWS/EFS",
		ResourceDimensionName: "FileSystemId",
		ServiceName:           ServiceNameEFS,
		ResourceType:          "file-system",
	},
	"AWS/ES": {
		Name:                  "AWS/ES",
		ResourceDimensionName: "DomainName",
		ServiceName:           ServiceNameES,
		ResourceType:          "domain",
	},
	"AWS/GuardDuty": {
		Name:                  "AWS/GuardDuty",
		ResourceDimensionName: "DetectorId",
		ServiceName:           ServiceNameGuardDuty,
		ResourceType:          "detector",
	},
	"AWS/KMS": {
		Name:                  "AWS/KMS",
		ResourceDimensionName: "KeyId",
		ServiceName:           ServiceNameKMS,
		ResourceType:          "key",
	},
	"AWS/SageMaker": {
		Name:                  "AWS/SageMaker",
		ResourceDimensionName: "EndpointName",
		ServiceName:           ServiceNameSageMaker,
		ResourceType:          "endpoint",
	},
	"AWS/SecretsManager": {
		Name:                  "AWS/SecretsManager",
		ResourceDimensionName: "SecretId",
		ServiceName:           ServiceNameSecretsManager,
		ResourceType:          "secret",
	},
	"AWS/SecurityHub": {
		Name:                  "AWS/SecurityHub",
		ResourceDimensionName: "HubArn",
		ServiceName:           ServiceNameSecurityHub,
		ResourceType:          "hub",
	},
	"AWS/SES": {
		Name:                  "AWS/SES",
		ResourceDimensionName: "Identity",
		ServiceName:           ServiceNameSES,
		ResourceType:          "identity",
	},
	"AWS/NATGateway": {
		Name:                  "AWS/NATGateway",
		ResourceDimensionName: "NatGatewayId",
		ServiceName:           ServiceNameVPC,
		ResourceType:          "natgateway",
	},
	"AWS/X-Ray": {
		Name:                  "AWS/X-Ray",
		ResourceDimensionName: "GroupName",
		ServiceName:           ServiceNameXray,
		ResourceType:          "group",
	},
}

type AlarmStatus string

const (
	AlarmStatusOk               AlarmStatus = "OK"
	AlarmStatusAlarm            AlarmStatus = "ALARM"
	AlarmStatusInsufficientData AlarmStatus = "INSUFFICIENT_DATA"
)

type AlarmsFilter struct {
	ResourceIds []string    `json:"resource_ids"`
	Metrics     []string    `json:"metrics"`
	ServiceName string      `json:"service_name" validate:"required"`
	Status      AlarmStatus `json:"status"`
	Region      string      `json:"region"`
}

// fetchAlarmsByResource fetches all CloudWatch alarms for a region once and returns
// them grouped by ResourceId. Each key is a resource identifier (e.g. EC2 InstanceId,
// RDS DBInstanceIdentifier, S3 BucketName) and the value is the slice of alarm Raw maps
// for that resource. This avoids repeated DescribeAlarms + STS AssumeRole calls per resource.
func fetchAlarmsByResource(ctx providers.CloudProviderContext, account providers.Account, region string) map[string][]any {
	alarms, err := getAwsCloudwatchAlarms(ctx, account, AlarmsFilter{
		Region: region,
	})
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch alarms for region, alarm recommendations may be incomplete",
			"error", err, "region", region)
		return make(map[string][]any)
	}
	byResource := make(map[string][]any)
	for _, a := range alarms.Items {
		byResource[a.ResourceId] = append(byResource[a.ResourceId], a.Raw)
	}
	return byResource
}

func getAwsCloudwatchAlarms(ctx providers.CloudProviderContext, account providers.Account, filter AlarmsFilter) (providers.ListEventResponse, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return providers.ListEventResponse{}, err
	}

	region := "us-east-1"
	if filter.Region != "" {
		region = filter.Region
	}
	cfg.Region = region
	svc := cloudwatch.NewFromConfig(cfg)
	events := []providers.Event{}
	paginator := cloudwatch.NewDescribeAlarmsPaginator(svc, &cloudwatch.DescribeAlarmsInput{
		StateValue: types.StateValue(filter.Status),
		MaxRecords: aws.Int32(100),
	})

	// Cache metric filter resolutions to avoid redundant DescribeMetricFilters calls
	mfCache := MetricFilterCache{}

	for paginator.HasMorePages() {
		alarms, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			// Downgrade permission errors to WARN — these are customer IAM misconfigs,
			// not collector bugs. The audit middleware already records them.
			if _, errCode, errMsg, isPermErr := IsAWSPermissionError(err); isPermErr {
				ctx.GetLogger().Warn("failed to fetch cloudwatch alarms", "errorCode", errCode, "errorMessage", errMsg, "accountNumber", account.AccountNumber)
			} else {
				var apiErr smithy.APIError
				if errors.As(err, &apiErr) {
					ctx.GetLogger().Error("failed to fetch cloudwatch alarms", "errorCode", apiErr.ErrorCode(), "errorMessage", apiErr.ErrorMessage(), "accountNumber", account.AccountNumber)
				} else {
					ctx.GetLogger().Error("failed to fetch cloudwatch alarms", "error", err, "accountNumber", account.AccountNumber)
				}
			}
			return providers.ListEventResponse{}, err
		}

		for _, alarm := range alarms.MetricAlarms {
			alarmName := aws.ToString(alarm.AlarmName)
			if alarm.Namespace == nil {
				ctx.GetLogger().Debug("alarm namespace is nil", "alarmName", alarmName)
				continue
			}

			threshold := "0"
			if alarm.Threshold != nil {
				threshold = fmt.Sprintf("%f", *alarm.Threshold)
			}

			// Convert SDK dimensions to the shared AlarmDimension type
			dims := make([]AlarmDimension, 0, len(alarm.Dimensions))
			for _, d := range alarm.Dimensions {
				dims = append(dims, AlarmDimension{
					Name:  aws.ToString(d.Name),
					Value: aws.ToString(d.Value),
				})
			}

			// Use the shared enrichment function for namespace mapping,
			// dimension matching, label building, and metric filter resolution.
			enrichment := EnrichCloudWatchAlarm(ctx.GetContext(), CloudWatchAlarmInfo{
				AlarmName:       alarmName,
				MetricName:      aws.ToString(alarm.MetricName),
				MetricNamespace: aws.ToString(alarm.Namespace),
				Statistic:       string(alarm.Statistic),
				Threshold:       threshold,
				Region:          region,
				AccountNumber:   account.AccountNumber,
				AlarmArn:        aws.ToString(alarm.AlarmArn),
				StateValue:      string(alarm.StateValue),
				Dimensions:      dims,
			}, true, &cfg, mfCache)

			stateReason := aws.ToString(alarm.StateReason)
			alarmArn := aws.ToString(alarm.AlarmArn)

			// Handle StateTransitionedTimestamp safely.
			//
			// FindingId is deliberately left unset so the per-firing identity comes
			// from providers.Event.FiringFindingID() — the same key the EventBridge
			// path produces. A CloudWatch state change reaches us twice, by push and
			// by this poller, and both already key on the alarm ARN plus the
			// transition timestamp; this path used to print that pair as
			// "<arn>/<RFC3339>" while the shared key prints "<arn>-<unix>", so the
			// unique index on (tenant, cloud_account_id, finding_id) could not match
			// them. Every alarm was stored twice and every firing advanced its dedup
			// chain by two.
			//
			// Nothing is lost by dropping the bespoke format: eventDate *is* the
			// transition timestamp, so the shared key is equally deterministic. When
			// the timestamp is absent it falls back to now() and the finding id
			// stops being stable across polls — pre-existing, and unchanged here.
			var eventDate time.Time
			if alarm.StateTransitionedTimestamp != nil {
				eventDate = *alarm.StateTransitionedTimestamp
			} else {
				eventDate = time.Now()
			}
			// Use alarmArn as stable fingerprint (without timestamp).
			// Per-firing uniqueness is handled by FindingId or fallback in etl_events.go.
			eventId := alarmArn

			events = append(events, providers.Event{
				Title:               fmt.Sprintf("%s::%s::%s", enrichment.ResourceServiceName, enrichment.ResourceId, stateReason),
				EventName:           alarmName,
				Description:         stateReason,
				Date:                eventDate,
				EventSource:         "AWS_CloudWatch_Alarm",
				EventId:             eventId,
				EventStatus:         providers.EventStatusFiring,
				EventSeverity:       providers.EventSeverityHigh,
				ResourceType:        enrichment.ResourceType,
				ResourceId:          enrichment.ResourceId,
				ResourceRegion:      region,
				ResourceServiceName: enrichment.ResourceServiceName,
				Raw:                 buildAlarmRawMap(alarm),
				Labels:              enrichment.Labels,
			})
		}
	}

	return providers.ListEventResponse{
		Items: events,
	}, nil
}

// buildAlarmRawMap extracts only the operationally relevant fields from a CloudWatch
// MetricAlarm for the Raw/evidences column. The full SDK struct serialized via
// fatih/structs.Map is very heavy (deep reflection); this produces a small, targeted map.
func buildAlarmRawMap(alarm types.MetricAlarm) map[string]any {
	raw := map[string]any{}

	if alarm.AlarmName != nil {
		raw["alarmName"] = *alarm.AlarmName
	}
	if alarm.AlarmArn != nil {
		raw["alarmArn"] = *alarm.AlarmArn
	}
	if alarm.AlarmDescription != nil {
		raw["alarmDescription"] = *alarm.AlarmDescription
	}
	raw["stateValue"] = string(alarm.StateValue)
	if alarm.StateReason != nil {
		raw["stateReason"] = *alarm.StateReason
	}
	if alarm.StateReasonData != nil {
		raw["stateReasonData"] = *alarm.StateReasonData
	}
	if alarm.MetricName != nil {
		raw["metricName"] = *alarm.MetricName
	}
	if alarm.Namespace != nil {
		raw["namespace"] = *alarm.Namespace
	}
	raw["statistic"] = string(alarm.Statistic)
	raw["comparisonOperator"] = string(alarm.ComparisonOperator)
	if alarm.ActionsEnabled != nil {
		raw["actionsEnabled"] = *alarm.ActionsEnabled
	}
	if alarm.Threshold != nil {
		raw["threshold"] = *alarm.Threshold
	}
	if alarm.EvaluationPeriods != nil {
		raw["evaluationPeriods"] = *alarm.EvaluationPeriods
	}
	if alarm.Period != nil {
		raw["period"] = *alarm.Period
	}
	if alarm.StateTransitionedTimestamp != nil {
		raw["stateTransitionedTimestamp"] = alarm.StateTransitionedTimestamp.Format(time.RFC3339)
	}
	if alarm.StateUpdatedTimestamp != nil {
		raw["stateUpdatedTimestamp"] = alarm.StateUpdatedTimestamp.Format(time.RFC3339)
	}
	if alarm.AlarmConfigurationUpdatedTimestamp != nil {
		raw["alarmConfigurationUpdatedTimestamp"] = alarm.AlarmConfigurationUpdatedTimestamp.Format(time.RFC3339)
	}

	if len(alarm.Dimensions) > 0 {
		dims := make([]map[string]string, 0, len(alarm.Dimensions))
		for _, d := range alarm.Dimensions {
			dim := map[string]string{}
			if d.Name != nil {
				dim["name"] = *d.Name
			}
			if d.Value != nil {
				dim["value"] = *d.Value
			}
			dims = append(dims, dim)
		}
		raw["dimensions"] = dims
	}

	return raw
}

// CloudWatchAlarmInfo holds the metric-level details of a CloudWatch alarm
// needed for resource identification and label enrichment.
type CloudWatchAlarmInfo struct {
	AlarmName       string
	MetricName      string
	MetricNamespace string
	Statistic       string
	Threshold       string
	Region          string
	AccountNumber   string
	AlarmArn        string
	StateValue      string
	// Dimensions as Name/Value pairs (works for both polling and EventBridge).
	Dimensions []AlarmDimension
}

// AlarmDimension is a key-value pair for a CloudWatch metric dimension.
type AlarmDimension struct {
	Name  string
	Value string
}

// CloudWatchAlarmEnrichment is the result of enriching a CloudWatch alarm event.
type CloudWatchAlarmEnrichment struct {
	// Resource identification
	ResourceId          string
	ResourceType        string
	ResourceServiceName string
	InstanceType        string // The dimension name used for ResourceId

	// Labels to set on the event
	Labels map[string]string
}

// EnrichCloudWatchAlarm uses the shared cloudwatchNamespaceServiceMap to determine
// the correct resource from alarm dimensions, sets individual dimension labels,
// and resolves metric filter log groups for custom namespaces.
// Both the polling path and EventBridge path call this function.
// MetricFilterCache caches resolved metric filter info by "metricName:metricNamespace"
// to avoid redundant DescribeMetricFilters API calls during polling loops.
// Pass nil to skip caching (e.g., for single EventBridge events).
type MetricFilterCache map[string]metricFilterInfo

func EnrichCloudWatchAlarm(ctx context.Context, info CloudWatchAlarmInfo, resolveMetricFilter bool, awsCfg *aws.Config, mfCache MetricFilterCache) CloudWatchAlarmEnrichment {
	result := CloudWatchAlarmEnrichment{
		Labels: map[string]string{},
	}

	// 1. Determine namespace info from shared map
	namespace, knownNamespace := cloudwatchNamespaceServiceMap[info.MetricNamespace]
	resolvedFromDimensions := knownNamespace
	if !knownNamespace {
		// An unrecognised namespace does not mean an unrecognisable resource.
		// A custom metric published against InstanceId is still about that EC2
		// instance, and the dimensions say so even when the namespace does not.
		//
		// Falling straight through to ResourceType "alarm" made the event
		// resolve to no cloud resource, which removed it from correlation and
		// from analysis with no signal either had happened. Measured on one
		// account: 22 such events produced 0 correlations and 0 analyses, while
		// 35 compute-instance events from the same alarms produced 24 and 4.
		// Application health is rarely a native AWS metric, so this is the
		// common case for anyone alarming on their own telemetry.
		if inferred, ok := namespaceFromDimensions(info.Dimensions); ok {
			resolvedFromDimensions = true
			namespace = CloudwatchNamespace{
				Name:                  info.MetricNamespace,
				ResourceDimensionName: inferred.ResourceDimensionName,
				ServiceName:           inferred.ServiceName,
				ResourceType:          inferred.ResourceType,
			}
		} else {
			namespace = CloudwatchNamespace{
				Name:                  info.MetricNamespace,
				ResourceDimensionName: "Resource",
				ServiceName:           "CloudWatch",
				ResourceType:          "alarm",
			}
		}
	}

	// 2. Smart dimension matching: use the namespace's expected dimension
	instance, instanceType := resolveResourceFromDimensions(info.Dimensions, namespace, info.AlarmName)

	// ECS special case: compose multi-part resource ID from cluster/task/container dimensions
	if namespace.ServiceName == ServiceNameECS {
		var cluster, task, container, service string
		for _, dim := range info.Dimensions {
			switch dim.Name {
			case "ClusterName":
				cluster = dim.Value
			case "TaskDefinition":
				task = dim.Value
			case "ContainerName":
				container = dim.Value
			case "ServiceName":
				service = dim.Value
			}
		}
		if cluster != "" {
			if container != "" && task != "" {
				instance = fmt.Sprintf("%s/%s/%s", cluster, task, container)
				instanceType = "ContainerName"
			} else if service != "" {
				instance = fmt.Sprintf("%s/%s", cluster, service)
				instanceType = "ServiceName"
			}
			// else: just use the cluster name (already set by resolveResourceFromDimensions)
		}
	}

	// 2b. Last resort for a custom namespace whose alarm names a logical service
	// rather than a resource ("Service=nginx"). The same metric is often ALSO
	// published with a resource dimension attached, and when it is, that sibling
	// series says which resource this alarm is about. Done here rather than at
	// evidence time on purpose: subject, subject_type and cloud_resource_id are
	// all decided in this function, and the fingerprint and dedup chain are built
	// from them — an event filed under the wrong subject cannot be repaired later.
	if !resolvedFromDimensions && awsCfg != nil {
		if found, ok := resolveResourceFromSiblingMetrics(ctx, *awsCfg, info); ok {
			namespace.ResourceDimensionName = found.dimensionName
			namespace.ServiceName = found.serviceName
			namespace.ResourceType = found.resourceType
			instance = found.value
			instanceType = found.dimensionName
		}
	}

	result.ResourceId = instance
	result.ResourceType = namespace.ResourceType
	result.ResourceServiceName = namespace.ServiceName
	result.InstanceType = instanceType

	// 3. Set standard labels
	result.Labels["aws_event_arn"] = info.AlarmArn
	result.Labels["aws_event_name"] = info.AlarmName
	result.Labels["aws_event_state"] = info.StateValue
	result.Labels["aws_region"] = info.Region
	result.Labels["aws_event_metric_name"] = info.MetricName
	result.Labels["aws_event_metric_namespace"] = info.MetricNamespace
	result.Labels["aws_event_metric_statistic"] = info.Statistic
	result.Labels["aws_event_threshold"] = info.Threshold
	result.Labels["aws_event_instance"] = instance
	result.Labels["aws_event_instance_type"] = instanceType
	result.Labels["aws_service_name"] = namespace.ServiceName
	result.Labels["aws_account"] = info.AccountNumber

	// 4. Store all dimensions as JSON array (backward compat)
	dimsJSON, err := json.Marshal(info.Dimensions)
	if err == nil {
		result.Labels["aws_event_alarm_dimensions"] = string(dimsJSON)
	}

	// 5. Store individual dimension labels for direct access
	for _, dim := range info.Dimensions {
		if dim.Name != "" && dim.Value != "" {
			safeKey := strings.ToLower(strings.NewReplacer(
				".", "_", "@", "", " ", "_",
			).Replace(dim.Name))
			result.Labels["aws_dim_"+safeKey] = dim.Value
		}
	}

	// 6. Resolve metric filter log group and filter pattern for custom namespaces
	if resolveMetricFilter && !knownNamespace && info.MetricName != "" && awsCfg != nil {
		cacheKey := info.MetricName + ":" + info.MetricNamespace
		if mfCache != nil {
			if cached, ok := mfCache[cacheKey]; ok {
				applyMetricFilterLabels(&result, cached)
				return result
			}
		}
		mfInfo, err := describeMetricFilter(ctx, *awsCfg, info.MetricName, info.MetricNamespace)
		if err == nil {
			if mfCache != nil {
				mfCache[cacheKey] = mfInfo
			}
			applyMetricFilterLabels(&result, mfInfo)
		}
	}

	return result
}

// siblingResource is the resource a sibling metric series names for an alarm
// whose own dimensions name none.
type siblingResource struct {
	dimensionName string
	value         string
	serviceName   string
	resourceType  string
}

const (
	siblingLookupTimeout    = 5 * time.Second
	siblingResourceCacheTTL = 10 * time.Minute
	// siblingCacheSweepAt is the size past which a write sweeps expired entries.
	// One entry per (account, region, namespace, metric, dimension set) that
	// could not be resolved from its own dimensions — a handful per account in
	// practice, so this is reached only by an estate far larger than any we run.
	siblingCacheSweepAt = 1024
)

type siblingCacheEntry struct {
	resource  *siblingResource // nil = looked up, nothing found
	expiresAt time.Time
}

// siblingCacheKey identifies one lookup. The cache is a package-level global
// shared by every account and region this collector polls, so account and region
// are part of the key: a custom namespace is a name the customer chose, and two
// tenants both publishing "Service=nginx" under the same namespace is an
// ordinary collision, not a far-fetched one. Keyed on a struct rather than a
// joined string so the only free-form field (dimensions) cannot run into the
// others through a separator that happens to appear in a dimension value.
type siblingCacheKey struct {
	accountNumber string
	region        string
	namespace     string
	metricName    string
	dimensions    string
}

var (
	siblingResourceCache   = map[siblingCacheKey]siblingCacheEntry{}
	siblingResourceCacheMu sync.RWMutex
)

func newSiblingCacheKey(info CloudWatchAlarmInfo) siblingCacheKey {
	parts := make([]string, 0, len(info.Dimensions))
	for _, d := range info.Dimensions {
		parts = append(parts, d.Name+"="+d.Value)
	}
	sort.Strings(parts)
	return siblingCacheKey{
		accountNumber: info.AccountNumber,
		region:        info.Region,
		namespace:     info.MetricNamespace,
		metricName:    info.MetricName,
		dimensions:    strings.Join(parts, ","),
	}
}

func getCachedSiblingResource(key siblingCacheKey) (*siblingResource, bool) {
	siblingResourceCacheMu.RLock()
	entry, ok := siblingResourceCache[key]
	siblingResourceCacheMu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		// Drop it on the way past. Re-check under the write lock: another
		// goroutine may have refreshed this key between the two locks, and
		// deleting a fresh entry would only cost an extra AWS call, not
		// correctness.
		siblingResourceCacheMu.Lock()
		if cur, still := siblingResourceCache[key]; still && time.Now().After(cur.expiresAt) {
			delete(siblingResourceCache, key)
		}
		siblingResourceCacheMu.Unlock()
		return nil, false
	}
	return entry.resource, true
}

func setCachedSiblingResource(key siblingCacheKey, r *siblingResource) {
	siblingResourceCacheMu.Lock()
	defer siblingResourceCacheMu.Unlock()
	// Expiry alone does not bound this map: an alarm that stops firing is never
	// read again, so its entry would sit here for the life of the process. Read
	// eviction handles the keys that come back; this handles the ones that do
	// not. Only walks the map when it has grown past the sweep threshold, so the
	// ordinary case stays a single insert.
	if len(siblingResourceCache) >= siblingCacheSweepAt {
		now := time.Now()
		for k, e := range siblingResourceCache {
			if now.After(e.expiresAt) {
				delete(siblingResourceCache, k)
			}
		}
	}
	siblingResourceCache[key] = siblingCacheEntry{resource: r, expiresAt: time.Now().Add(siblingResourceCacheTTL)}
}

// resolveResourceFromSiblingMetrics answers "which resource is this alarm about?"
// for a custom-namespace alarm whose own dimensions name a logical service
// ("Service=nginx") rather than a resource.
//
// Publishers commonly emit the same metric twice: once dimensioned the way the
// alarm is, and once with a resource dimension attached. When that second series
// exists, it — not a name guess — says which resource the alarm belongs to.
//
// Negative results are cached as well as positive ones: the common case is that
// no sibling exists, and without that every firing of every unresolvable alarm
// would pay another ListMetrics call.
func resolveResourceFromSiblingMetrics(ctx context.Context, cfg aws.Config, info CloudWatchAlarmInfo) (siblingResource, bool) {
	if info.MetricNamespace == "" || info.MetricName == "" || len(info.Dimensions) == 0 {
		return siblingResource{}, false
	}

	key := newSiblingCacheKey(info)
	if cached, hit := getCachedSiblingResource(key); hit {
		if cached == nil {
			return siblingResource{}, false
		}
		return *cached, true
	}

	filters := make([]types.DimensionFilter, 0, len(info.Dimensions))
	for _, d := range info.Dimensions {
		// @-prefixed dimensions are injected by CloudWatch Logs and are not real
		// metric dimensions; filtering on them matches nothing.
		if d.Name == "" || strings.HasPrefix(d.Name, "@") {
			continue
		}
		filters = append(filters, types.DimensionFilter{Name: aws.String(d.Name), Value: aws.String(d.Value)})
	}
	if len(filters) == 0 {
		setCachedSiblingResource(key, nil)
		return siblingResource{}, false
	}

	lookupCtx, cancel := context.WithTimeout(ctx, siblingLookupTimeout)
	defer cancel()

	out, err := cloudwatch.NewFromConfig(cfg).ListMetrics(lookupCtx, &cloudwatch.ListMetricsInput{
		Namespace:  aws.String(info.MetricNamespace),
		MetricName: aws.String(info.MetricName),
		Dimensions: filters,
	})
	if err != nil {
		// Not cached: a throttle or timeout is not evidence that no sibling
		// exists, and caching it would make one bad minute stick for ten.
		return siblingResource{}, false
	}

	resolved, ok := pickSiblingResource(out.Metrics, info.Dimensions)
	if !ok {
		setCachedSiblingResource(key, nil)
		return siblingResource{}, false
	}
	setCachedSiblingResource(key, &resolved)
	return resolved, true
}

// pickSiblingResource is the decision, kept pure so the rule is testable without
// an AWS client.
//
// A candidate series must carry every dimension the alarm carries, with the same
// values — a series that merely shares the metric name describes something else.
// Beyond those it must add exactly one dimension that resourceDimensionIndex
// recognises as naming a resource.
//
// Ambiguity refuses rather than guesses. Two different instances behind one
// service name is the normal shape of a load-balanced tier, and picking either
// would attribute the alarm to a machine that may be perfectly healthy — the
// same reason resourceDimensionIndex drops a dimension its namespaces disagree
// about.
func pickSiblingResource(metrics []types.Metric, alarmDims []AlarmDimension) (siblingResource, bool) {
	want := map[string]string{}
	for _, d := range alarmDims {
		if d.Name != "" && !strings.HasPrefix(d.Name, "@") {
			want[d.Name] = d.Value
		}
	}

	found := map[string]siblingResource{}
	for _, m := range metrics {
		have := map[string]string{}
		for _, d := range m.Dimensions {
			if d.Name != nil && d.Value != nil {
				have[*d.Name] = *d.Value
			}
		}
		matchesAlarm := true
		for name, value := range want {
			if have[name] != value {
				matchesAlarm = false
				break
			}
		}
		if !matchesAlarm {
			continue
		}
		for name, value := range have {
			if _, isAlarmDim := want[name]; isAlarmDim || value == "" {
				continue
			}
			ns, known := resourceDimensionIndex[name]
			if !known {
				continue
			}
			found[name+"\x00"+value] = siblingResource{
				dimensionName: name,
				value:         value,
				serviceName:   ns.ServiceName,
				resourceType:  ns.ResourceType,
			}
		}
	}

	if len(found) != 1 {
		return siblingResource{}, false
	}
	for _, r := range found {
		return r, true
	}
	return siblingResource{}, false
}

// resourceDimensionIndex maps a resource-identifying dimension name
// ("InstanceId", "DBInstanceIdentifier", ...) to the namespace that owns it.
// Built once from cloudwatchNamespaceServiceMap so it cannot drift from it.
// A dimension is indexed when every namespace claiming it agrees on the
// resource it identifies; genuine disagreement drops it, because guessing
// between two resource kinds is worse than the generic fallback.
var resourceDimensionIndex = func() map[string]CloudwatchNamespace {
	// Several namespaces legitimately share a dimension while agreeing on what
	// it identifies: AWS/EC2 and CWAgent both key on InstanceId and both mean a
	// compute-instance. What disqualifies a dimension is disagreement about the
	// resource, not the fact that more than one namespace uses it - counting
	// occurrences would drop InstanceId, which is the case that matters most.
	type kind struct{ service, resource string }
	kinds := map[string]map[kind]bool{}
	for _, ns := range cloudwatchNamespaceServiceMap {
		if ns.ResourceDimensionName == "" {
			continue
		}
		if kinds[ns.ResourceDimensionName] == nil {
			kinds[ns.ResourceDimensionName] = map[kind]bool{}
		}
		kinds[ns.ResourceDimensionName][kind{ns.ServiceName, ns.ResourceType}] = true
	}
	// Sorted so the entry stored for a shared dimension is the same on every
	// run. Callers read only ServiceName and ResourceType, which the agreement
	// check above already proved identical, so today this changes nothing -
	// but map iteration order is randomised, and leaving it unordered means the
	// first reader of any other field gets a value that varies per process.
	names := make([]string, 0, len(cloudwatchNamespaceServiceMap))
	for name := range cloudwatchNamespaceServiceMap {
		names = append(names, name)
	}
	sort.Strings(names)

	idx := map[string]CloudwatchNamespace{}
	for _, name := range names {
		ns := cloudwatchNamespaceServiceMap[name]
		if ns.ResourceDimensionName != "" && len(kinds[ns.ResourceDimensionName]) == 1 {
			idx[ns.ResourceDimensionName] = ns
		}
	}
	return idx
}()

// namespaceFromDimensions recovers the resource kind of a custom-namespace
// alarm from its dimensions. Returns false when no dimension identifies a
// known resource, leaving the caller's generic fallback in place.
func namespaceFromDimensions(dims []AlarmDimension) (CloudwatchNamespace, bool) {
	for _, dim := range dims {
		if ns, ok := resourceDimensionIndex[dim.Name]; ok && dim.Value != "" {
			return ns, true
		}
	}
	return CloudwatchNamespace{}, false
}

// resolveResourceFromDimensions finds the best resource identifier from alarm
// dimensions. Priority: the namespace's expected dimension, then any dimension
// that names a resource, then the first non-generic dimension, then the alarm
// name.
func resolveResourceFromDimensions(dims []AlarmDimension, namespace CloudwatchNamespace, alarmName string) (instance, instanceType string) {
	if len(dims) == 0 {
		return alarmName, "AlarmName"
	}

	// Try matching the namespace's expected resource dimension
	targetDim := namespace.ResourceDimensionName
	for _, dim := range dims {
		if dim.Name == targetDim {
			return dim.Value, targetDim
		}
	}

	// Prefer a dimension that actually identifies a resource. Falling straight
	// through to "first non-generic" made the result depend on the order
	// CloudWatch happened to list the dimensions in: three structurally
	// identical alarms carrying Service and InstanceId resolved two to the
	// instance and one to the service name, and the one that lost pointed at no
	// cloud resource at all - so it was dropped from correlation while its
	// siblings were kept. Same input, different answer, no signal.
	for _, dim := range dims {
		if _, known := resourceDimensionIndex[dim.Name]; known && dim.Value != "" {
			return dim.Value, dim.Name
		}
	}

	// Fallback: first non-generic dimension
	for _, dim := range dims {
		if dim.Name != "@aws.account" && dim.Name != "@aws.region" {
			return dim.Value, dim.Name
		}
	}

	// All dimensions are generic — use alarm name
	return alarmName, "AlarmName"
}

func applyMetricFilterLabels(result *CloudWatchAlarmEnrichment, mfInfo metricFilterInfo) {
	if mfInfo.LogGroupName != "" {
		result.Labels["metric_filter_log_group_name"] = mfInfo.LogGroupName
	}
	if mfInfo.FilterPattern != "" {
		result.Labels["metric_filter_pattern"] = mfInfo.FilterPattern
	}
}

// extractCloudWatchAlarmInfoFromEB builds a CloudWatchAlarmInfo from an EventBridge
// CloudWatch Alarm State Change event for use with EnrichCloudWatchAlarm.
func extractCloudWatchAlarmInfoFromEB(ebEvent EventBridgeEvent) (CloudWatchAlarmInfo, bool) {
	var detail map[string]any
	if err := json.Unmarshal(ebEvent.Detail, &detail); err != nil {
		return CloudWatchAlarmInfo{}, false
	}

	alarmName, _ := detail["alarmName"].(string)
	if alarmName == "" {
		return CloudWatchAlarmInfo{}, false
	}

	alarmArn, _ := detail["alarmArn"].(string)
	if alarmArn == "" && len(ebEvent.Resources) > 0 {
		alarmArn = ebEvent.Resources[0]
	}

	stateMap, _ := detail["state"].(map[string]any)
	stateValue, _ := stateMap["value"].(string)

	var metricName, metricNamespace, statistic, threshold string
	var dims []AlarmDimension

	config, _ := detail["configuration"].(map[string]any)
	if config != nil {
		if t, ok := config["threshold"]; ok {
			threshold = fmt.Sprintf("%v", t)
		}
		metrics, _ := config["metrics"].([]any)
		for _, metricRaw := range metrics {
			metric, _ := metricRaw.(map[string]any)
			if metric == nil {
				continue
			}
			metricStat, _ := metric["metricStat"].(map[string]any)
			if metricStat == nil {
				continue // skip math expressions
			}
			statistic, _ = metricStat["stat"].(string)
			innerMetric, _ := metricStat["metric"].(map[string]any)
			if innerMetric == nil {
				continue
			}
			metricName, _ = innerMetric["name"].(string)
			metricNamespace, _ = innerMetric["namespace"].(string)
			rawDims, _ := innerMetric["dimensions"].(map[string]any)
			for k, v := range rawDims {
				if vStr, ok := v.(string); ok {
					dims = append(dims, AlarmDimension{Name: k, Value: vStr})
				}
			}
			break // use first metric with metricStat
		}
	}

	return CloudWatchAlarmInfo{
		AlarmName:       alarmName,
		MetricName:      metricName,
		MetricNamespace: metricNamespace,
		Statistic:       statistic,
		Threshold:       threshold,
		Region:          ebEvent.Region,
		AccountNumber:   ebEvent.Account,
		AlarmArn:        alarmArn,
		StateValue:      stateValue,
		Dimensions:      dims,
	}, true
}

// metricFilterInfo holds the log group and filter pattern from a CloudWatch metric filter.
type metricFilterInfo struct {
	LogGroupName  string
	FilterPattern string
}

// resolveMetricFilter calls DescribeMetricFilters to find the log group
// and filter pattern that produces a given custom metric via a metric filter.
func describeMetricFilter(ctx context.Context, cfg aws.Config, metricName, metricNamespace string) (metricFilterInfo, error) {
	logsSvc := cloudwatchlogs.NewFromConfig(cfg)
	output, err := logsSvc.DescribeMetricFilters(ctx, &cloudwatchlogs.DescribeMetricFiltersInput{
		MetricName:      aws.String(metricName),
		MetricNamespace: aws.String(metricNamespace),
	})
	if err != nil {
		return metricFilterInfo{}, err
	}
	if len(output.MetricFilters) == 0 {
		return metricFilterInfo{}, nil
	}
	return metricFilterInfo{
		LogGroupName:  aws.ToString(output.MetricFilters[0].LogGroupName),
		FilterPattern: aws.ToString(output.MetricFilters[0].FilterPattern),
	}, nil
}
