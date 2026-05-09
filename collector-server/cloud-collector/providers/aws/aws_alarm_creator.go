package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// CreateCloudWatchAlarm creates a CloudWatch alarm using the provided configuration
func CreateCloudWatchAlarm(ctx context.Context, account providers.Account, config providers.AlarmCreationConfig, region string) error {
	// Validate alarm configuration before attempting to create
	if err := ValidateAlarmConfig(config); err != nil {
		return fmt.Errorf("invalid alarm configuration: %w", err)
	}

	cfg, err := getAwsConfigFromAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to create AWS config: %w", err)
	}

	cfg.Region = region
	cwClient := cloudwatch.NewFromConfig(cfg)

	// Convert comparison operator
	comparisonOperator, err := parseComparisonOperator(config.ComparisonOperator)
	if err != nil {
		return err
	}

	// Convert treat missing data
	treatMissingData := config.TreatMissingData
	if treatMissingData == "" {
		treatMissingData = "notBreaching"
	}

	// Create base alarm input
	input := &cloudwatch.PutMetricAlarmInput{
		AlarmName:          aws.String(config.AlarmName),
		EvaluationPeriods:  aws.Int32(int32(config.EvaluationPeriods)),
		DatapointsToAlarm:  aws.Int32(int32(config.DatapointsToAlarm)),
		Threshold:          aws.Float64(config.Threshold),
		ComparisonOperator: comparisonOperator,
		TreatMissingData:   aws.String(treatMissingData),
		ActionsEnabled:     aws.Bool(true),
	}

	// Check if this is a metric math alarm or simple metric alarm
	if len(config.Metrics) > 0 {
		// Metric math alarm - use Metrics field
		metricDataQueries := make([]types.MetricDataQuery, len(config.Metrics))
		for i, mq := range config.Metrics {
			query := types.MetricDataQuery{
				Id:         aws.String(mq.Id),
				ReturnData: aws.Bool(mq.ReturnData),
			}

			if mq.Label != "" {
				query.Label = aws.String(mq.Label)
			}

			if mq.Expression != "" {
				// This is an expression query
				query.Expression = aws.String(mq.Expression)
			} else if mq.MetricStat != nil {
				// This is a metric stat query
				dimensions := make([]types.Dimension, len(mq.MetricStat.Metric.Dimensions))
				for j, dim := range mq.MetricStat.Metric.Dimensions {
					dimensions[j] = types.Dimension{
						Name:  aws.String(dim.Name),
						Value: aws.String(dim.Value),
					}
				}

				query.MetricStat = &types.MetricStat{
					Metric: &types.Metric{
						Namespace:  aws.String(mq.MetricStat.Metric.Namespace),
						MetricName: aws.String(mq.MetricStat.Metric.MetricName),
						Dimensions: dimensions,
					},
					Period: aws.Int32(int32(mq.MetricStat.Period)),
					Stat:   aws.String(mq.MetricStat.Stat),
				}
			}

			metricDataQueries[i] = query
		}

		input.Metrics = metricDataQueries
	} else {
		// Simple metric alarm - use individual metric fields
		// Convert dimensions
		dimensions := make([]types.Dimension, len(config.Dimensions))
		for i, dim := range config.Dimensions {
			dimensions[i] = types.Dimension{
				Name:  aws.String(dim.Name),
				Value: aws.String(dim.Value),
			}
		}

		// Convert statistic
		statistic, err := parseStatistic(config.Statistic)
		if err != nil {
			return err
		}

		input.MetricName = aws.String(config.MetricName)
		input.Namespace = aws.String(config.Namespace)
		input.Statistic = statistic
		input.Period = aws.Int32(int32(config.Period))
		input.Dimensions = dimensions
	}

	// Create the alarm
	_, err = cwClient.PutMetricAlarm(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to create CloudWatch alarm '%s' in region %s: %w", config.AlarmName, region, err)
	}

	// Successfully created - logging will be done at the caller level
	return nil
}

// CreateCloudWatchAlarmFromRecommendation creates a CloudWatch alarm from a recommendation's data
func CreateCloudWatchAlarmFromRecommendation(ctx context.Context, account providers.Account, recommendation providers.Recommendation) error {

	providerCtx := providers.NewCloudProviderContext(ctx)
	logger := providerCtx.GetLogger()
	logger.Info("Creating CloudWatch alarm from recommendation",
		"rule_name", recommendation.RuleName,
		"resource_id", recommendation.ResourceId,
		"resource_region", recommendation.ResourceRegion,
		"data_keys", func() []string {
			keys := make([]string, 0, len(recommendation.Data))
			for k := range recommendation.Data {
				keys = append(keys, k)
			}
			return keys
		}())

	// Extract alarm config from recommendation data
	alarmConfigData, ok := recommendation.Data["alarm_config"]
	if !ok {
		logger.Error("alarm_config not found in recommendation data",
			"rule_name", recommendation.RuleName,
			"resource_id", recommendation.ResourceId,
			"available_data_keys", func() []string {
				keys := make([]string, 0, len(recommendation.Data))
				for k := range recommendation.Data {
					keys = append(keys, k)
				}
				return keys
			}())
		return fmt.Errorf("alarm_config not found in recommendation data for rule %s (resource: %s)",
			recommendation.RuleName, recommendation.ResourceId)
	}

	// Convert to AlarmCreationConfig
	var alarmConfig providers.AlarmCreationConfig
	configBytes, err := json.Marshal(alarmConfigData)
	if err != nil {
		logger.Error("Failed to marshal alarm config", "error", err, "alarm_config_data", alarmConfigData)
		return fmt.Errorf("failed to marshal alarm config: %w", err)
	}

	if err := json.Unmarshal(configBytes, &alarmConfig); err != nil {
		logger.Error("Failed to unmarshal alarm config", "error", err, "config_bytes", string(configBytes))
		return fmt.Errorf("failed to unmarshal alarm config: %w", err)
	}

	logger.Info("Parsed alarm configuration successfully",
		"alarm_name", alarmConfig.AlarmName,
		"threshold", alarmConfig.Threshold,
		"metric_name", alarmConfig.MetricName,
		"namespace", alarmConfig.Namespace,
		"has_metrics", len(alarmConfig.Metrics) > 0)

	// Create the alarm
	return CreateCloudWatchAlarm(ctx, account, alarmConfig, recommendation.ResourceRegion)
}

// DeleteCloudWatchAlarm deletes a CloudWatch alarm
func DeleteCloudWatchAlarm(ctx context.Context, account providers.Account, alarmName, region string) error {
	cfg, err := getAwsConfigFromAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to create AWS config: %w", err)
	}

	cfg.Region = region
	cwClient := cloudwatch.NewFromConfig(cfg)

	_, err = cwClient.DeleteAlarms(ctx, &cloudwatch.DeleteAlarmsInput{
		AlarmNames: []string{alarmName},
	})
	if err != nil {
		return fmt.Errorf("failed to delete CloudWatch alarm: %w", err)
	}

	return nil
}

// DescribeCloudWatchAlarm retrieves details of a specific CloudWatch alarm
func DescribeCloudWatchAlarm(ctx context.Context, account providers.Account, alarmName, region string) (*types.MetricAlarm, error) {
	cfg, err := getAwsConfigFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS config: %w", err)
	}

	cfg.Region = region
	cwClient := cloudwatch.NewFromConfig(cfg)

	output, err := cwClient.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{
		AlarmNames: []string{alarmName},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe CloudWatch alarm: %w", err)
	}

	if len(output.MetricAlarms) == 0 {
		return nil, fmt.Errorf("alarm not found: %s", alarmName)
	}

	return &output.MetricAlarms[0], nil
}

// parseComparisonOperator converts string to CloudWatch ComparisonOperator enum
func parseComparisonOperator(operator string) (types.ComparisonOperator, error) {
	switch operator {
	case "GreaterThanThreshold":
		return types.ComparisonOperatorGreaterThanThreshold, nil
	case "GreaterThanOrEqualToThreshold":
		return types.ComparisonOperatorGreaterThanOrEqualToThreshold, nil
	case "LessThanThreshold":
		return types.ComparisonOperatorLessThanThreshold, nil
	case "LessThanOrEqualToThreshold":
		return types.ComparisonOperatorLessThanOrEqualToThreshold, nil
	default:
		return "", fmt.Errorf("invalid comparison operator: %s", operator)
	}
}

// parseStatistic converts string to CloudWatch Statistic enum
func parseStatistic(stat string) (types.Statistic, error) {
	switch stat {
	case "Average":
		return types.StatisticAverage, nil
	case "Sum":
		return types.StatisticSum, nil
	case "Minimum":
		return types.StatisticMinimum, nil
	case "Maximum":
		return types.StatisticMaximum, nil
	case "SampleCount":
		return types.StatisticSampleCount, nil
	default:
		return "", fmt.Errorf("invalid statistic: %s", stat)
	}
}

// UpdateCloudWatchAlarm updates an existing CloudWatch alarm
func UpdateCloudWatchAlarm(ctx context.Context, account providers.Account, config providers.AlarmCreationConfig, region string) error {
	// PutMetricAlarm creates or updates, so we can use the same function
	return CreateCloudWatchAlarm(ctx, account, config, region)
}

// ValidateAlarmConfig validates an alarm configuration before creating it
// Handles both simple metric alarms and metric math alarms
func ValidateAlarmConfig(config providers.AlarmCreationConfig) error {
	// Common validations for all alarm types
	if config.AlarmName == "" {
		return fmt.Errorf("alarm name is required")
	}
	if config.Period <= 0 {
		return fmt.Errorf("period must be greater than 0")
	}
	if config.EvaluationPeriods <= 0 {
		return fmt.Errorf("evaluation periods must be greater than 0")
	}
	if config.DatapointsToAlarm <= 0 {
		return fmt.Errorf("datapoints to alarm must be greater than 0")
	}
	if config.DatapointsToAlarm > config.EvaluationPeriods {
		return fmt.Errorf("datapoints to alarm cannot exceed evaluation periods")
	}
	if config.ComparisonOperator == "" {
		return fmt.Errorf("comparison operator is required")
	}

	// Determine alarm type and validate accordingly
	if len(config.Metrics) > 0 {
		// Metric Math Alarm validation (e.g., ALB HTTP error rate)
		return validateMetricMathAlarm(config)
	}

	// Simple Metric Alarm validation (e.g., EC2 CPU, RDS memory)
	return validateSimpleMetricAlarm(config)
}

// validateSimpleMetricAlarm validates a simple metric alarm configuration
func validateSimpleMetricAlarm(config providers.AlarmCreationConfig) error {
	if config.MetricName == "" {
		return fmt.Errorf("metric name is required for simple metric alarm")
	}
	if config.Namespace == "" {
		return fmt.Errorf("namespace is required for simple metric alarm")
	}
	if config.Statistic == "" {
		return fmt.Errorf("statistic is required for simple metric alarm")
	}
	if len(config.Dimensions) == 0 {
		return fmt.Errorf("at least one dimension is required for simple metric alarm")
	}

	return nil
}

// validateMetricMathAlarm validates a metric math alarm configuration
func validateMetricMathAlarm(config providers.AlarmCreationConfig) error {
	if len(config.Metrics) == 0 {
		return fmt.Errorf("metrics array is required for metric math alarm")
	}

	hasReturnData := false
	for i, metric := range config.Metrics {
		// Validate metric ID
		if metric.Id == "" {
			return fmt.Errorf("metric ID is required for metric %d", i)
		}

		// Check if this metric returns data (at least one must)
		if metric.ReturnData {
			hasReturnData = true
		}

		// Validate metric stat or expression (must have one)
		if metric.MetricStat == nil && metric.Expression == "" {
			return fmt.Errorf("metric %s must have either metric_stat or expression", metric.Id)
		}

		// If this is a metric stat (not an expression), validate it
		if metric.MetricStat != nil {
			if err := validateMetricStat(metric.Id, *metric.MetricStat); err != nil {
				return err
			}
		}
	}

	if !hasReturnData {
		return fmt.Errorf("at least one metric must have return_data: true")
	}

	return nil
}

// validateMetricStat validates a metric statistic configuration
func validateMetricStat(metricId string, stat providers.MetricStatConfig) error {
	if stat.Metric.Namespace == "" {
		return fmt.Errorf("namespace is required for metric %s", metricId)
	}
	if stat.Metric.MetricName == "" {
		return fmt.Errorf("metric name is required for metric %s", metricId)
	}
	if stat.Stat == "" {
		return fmt.Errorf("statistic is required for metric %s", metricId)
	}
	if stat.Period <= 0 {
		return fmt.Errorf("period must be greater than 0 for metric %s", metricId)
	}
	if len(stat.Metric.Dimensions) == 0 {
		return fmt.Errorf("at least one dimension is required for metric %s", metricId)
	}

	return nil
}
