package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateAlarmConfig_SimpleMetricAlarm tests validation for simple metric alarms
func TestValidateAlarmConfig_SimpleMetricAlarm(t *testing.T) {
	tests := []struct {
		name        string
		config      providers.AlarmCreationConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid simple metric alarm",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				MetricName:         "CPUUtilization",
				Namespace:          "AWS/EC2",
				Statistic:          "Average",
				Period:             300,
				EvaluationPeriods:  2,
				DatapointsToAlarm:  2,
				Threshold:          80.0,
				ComparisonOperator: "GreaterThanThreshold",
				Dimensions: []providers.AlarmDimension{
					{Name: "InstanceId", Value: "i-1234567890abcdef0"},
				},
			},
			expectError: false,
		},
		{
			name: "missing alarm name",
			config: providers.AlarmCreationConfig{
				MetricName:         "CPUUtilization",
				Namespace:          "AWS/EC2",
				Statistic:          "Average",
				Period:             300,
				EvaluationPeriods:  2,
				DatapointsToAlarm:  2,
				Threshold:          80.0,
				ComparisonOperator: "GreaterThanThreshold",
				Dimensions: []providers.AlarmDimension{
					{Name: "InstanceId", Value: "i-1234567890abcdef0"},
				},
			},
			expectError: true,
			errorMsg:    "alarm name is required",
		},
		{
			name: "missing metric name",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				Namespace:          "AWS/EC2",
				Statistic:          "Average",
				Period:             300,
				EvaluationPeriods:  2,
				DatapointsToAlarm:  2,
				Threshold:          80.0,
				ComparisonOperator: "GreaterThanThreshold",
				Dimensions: []providers.AlarmDimension{
					{Name: "InstanceId", Value: "i-1234567890abcdef0"},
				},
			},
			expectError: true,
			errorMsg:    "metric name is required",
		},
		{
			name: "missing namespace",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				MetricName:         "CPUUtilization",
				Statistic:          "Average",
				Period:             300,
				EvaluationPeriods:  2,
				DatapointsToAlarm:  2,
				Threshold:          80.0,
				ComparisonOperator: "GreaterThanThreshold",
				Dimensions: []providers.AlarmDimension{
					{Name: "InstanceId", Value: "i-1234567890abcdef0"},
				},
			},
			expectError: true,
			errorMsg:    "namespace is required",
		},
		{
			name: "missing dimensions",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				MetricName:         "CPUUtilization",
				Namespace:          "AWS/EC2",
				Statistic:          "Average",
				Period:             300,
				EvaluationPeriods:  2,
				DatapointsToAlarm:  2,
				Threshold:          80.0,
				ComparisonOperator: "GreaterThanThreshold",
			},
			expectError: true,
			errorMsg:    "at least one dimension is required",
		},
		{
			name: "invalid evaluation periods",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				MetricName:         "CPUUtilization",
				Namespace:          "AWS/EC2",
				Statistic:          "Average",
				Period:             300,
				EvaluationPeriods:  0,
				DatapointsToAlarm:  2,
				Threshold:          80.0,
				ComparisonOperator: "GreaterThanThreshold",
				Dimensions: []providers.AlarmDimension{
					{Name: "InstanceId", Value: "i-1234567890abcdef0"},
				},
			},
			expectError: true,
			errorMsg:    "evaluation periods must be greater than 0",
		},
		{
			name: "datapoints exceeds evaluation periods",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				MetricName:         "CPUUtilization",
				Namespace:          "AWS/EC2",
				Statistic:          "Average",
				Period:             300,
				EvaluationPeriods:  2,
				DatapointsToAlarm:  3,
				Threshold:          80.0,
				ComparisonOperator: "GreaterThanThreshold",
				Dimensions: []providers.AlarmDimension{
					{Name: "InstanceId", Value: "i-1234567890abcdef0"},
				},
			},
			expectError: true,
			errorMsg:    "datapoints to alarm cannot exceed evaluation periods",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAlarmConfig(tt.config)
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateAlarmConfig_MetricMathAlarm tests validation for metric math alarms
func TestValidateAlarmConfig_MetricMathAlarm(t *testing.T) {
	tests := []struct {
		name        string
		config      providers.AlarmCreationConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid metric math alarm",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-http-error-rate-alarm",
				Period:             300,
				EvaluationPeriods:  2,
				DatapointsToAlarm:  2,
				Threshold:          5.0,
				ComparisonOperator: "GreaterThanThreshold",
				Metrics: []providers.MetricQueryConfig{
					{
						Id:         "m1",
						ReturnData: false,
						MetricStat: &providers.MetricStatConfig{
							Metric: providers.MetricInfoConfig{
								Namespace:  "AWS/ApplicationELB",
								MetricName: "HTTPCode_Target_5XX_Count",
								Dimensions: []providers.AlarmDimension{
									{Name: "LoadBalancer", Value: "app/my-alb/1234567890abcdef"},
								},
							},
							Stat:   "Sum",
							Period: 60,
						},
					},
					{
						Id:         "m2",
						ReturnData: false,
						MetricStat: &providers.MetricStatConfig{
							Metric: providers.MetricInfoConfig{
								Namespace:  "AWS/ApplicationELB",
								MetricName: "RequestCount",
								Dimensions: []providers.AlarmDimension{
									{Name: "LoadBalancer", Value: "app/my-alb/1234567890abcdef"},
								},
							},
							Stat:   "Sum",
							Period: 60,
						},
					},
					{
						Id:         "e1",
						Expression: "(m1/m2)*100",
						ReturnData: true,
						Label:      "Error Rate (%)",
					},
				},
			},
			expectError: false,
		},
		{
			name: "metric math alarm with no return data",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				Period:             300,
				EvaluationPeriods:  2,
				DatapointsToAlarm:  2,
				Threshold:          5.0,
				ComparisonOperator: "GreaterThanThreshold",
				Metrics: []providers.MetricQueryConfig{
					{
						Id:         "m1",
						ReturnData: false,
						MetricStat: &providers.MetricStatConfig{
							Metric: providers.MetricInfoConfig{
								Namespace:  "AWS/ApplicationELB",
								MetricName: "HTTPCode_Target_5XX_Count",
								Dimensions: []providers.AlarmDimension{
									{Name: "LoadBalancer", Value: "app/my-alb/1234567890abcdef"},
								},
							},
							Stat:   "Sum",
							Period: 60,
						},
					},
				},
			},
			expectError: true,
			errorMsg:    "at least one metric must have return_data: true",
		},
		{
			name: "metric without id",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				Period:             300,
				EvaluationPeriods:  2,
				DatapointsToAlarm:  2,
				Threshold:          5.0,
				ComparisonOperator: "GreaterThanThreshold",
				Metrics: []providers.MetricQueryConfig{
					{
						ReturnData: true,
						MetricStat: &providers.MetricStatConfig{
							Metric: providers.MetricInfoConfig{
								Namespace:  "AWS/ApplicationELB",
								MetricName: "RequestCount",
								Dimensions: []providers.AlarmDimension{
									{Name: "LoadBalancer", Value: "app/my-alb/1234567890abcdef"},
								},
							},
							Stat:   "Sum",
							Period: 60,
						},
					},
				},
			},
			expectError: true,
			errorMsg:    "metric ID is required",
		},
		{
			name: "metric with neither expression nor metric_stat",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				Period:             300,
				EvaluationPeriods:  2,
				DatapointsToAlarm:  2,
				Threshold:          5.0,
				ComparisonOperator: "GreaterThanThreshold",
				Metrics: []providers.MetricQueryConfig{
					{
						Id:         "m1",
						ReturnData: true,
					},
				},
			},
			expectError: true,
			errorMsg:    "must have either metric_stat or expression",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAlarmConfig(tt.config)
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestParseComparisonOperator tests the comparison operator parsing
func TestParseComparisonOperator(t *testing.T) {
	tests := []struct {
		name        string
		operator    string
		expectError bool
	}{
		{"GreaterThanThreshold", "GreaterThanThreshold", false},
		{"GreaterThanOrEqualToThreshold", "GreaterThanOrEqualToThreshold", false},
		{"LessThanThreshold", "LessThanThreshold", false},
		{"LessThanOrEqualToThreshold", "LessThanOrEqualToThreshold", false},
		{"InvalidOperator", "InvalidOperator", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseComparisonOperator(tt.operator)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestParseStatistic tests the statistic parsing
func TestParseStatistic(t *testing.T) {
	tests := []struct {
		name        string
		statistic   string
		expectError bool
	}{
		{"Average", "Average", false},
		{"Sum", "Sum", false},
		{"Minimum", "Minimum", false},
		{"Maximum", "Maximum", false},
		{"SampleCount", "SampleCount", false},
		{"InvalidStatistic", "InvalidStatistic", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseStatistic(tt.statistic)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestCreateCloudWatchAlarmFromRecommendation_MissingAlarmConfig tests error handling when alarm_config is missing
func TestCreateCloudWatchAlarmFromRecommendation_MissingAlarmConfig(t *testing.T) {
	ctx := context.Background()
	account := providers.Account{
		AccountNumber: "123456789012",
	}

	recommendation := providers.Recommendation{
		RuleName:       "aws_alb_http_error_rate_alarm_missing",
		ResourceId:     "app/my-alb/1234567890abcdef",
		ResourceRegion: "us-east-1",
		Data: map[string]interface{}{
			"reason": "test recommendation",
			// alarm_config is missing
		},
	}

	err := CreateCloudWatchAlarmFromRecommendation(ctx, account, recommendation)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alarm_config not found")
}

// TestCreateCloudWatchAlarmFromRecommendation_InvalidAlarmConfig tests error handling for invalid alarm_config
func TestCreateCloudWatchAlarmFromRecommendation_InvalidAlarmConfig(t *testing.T) {
	ctx := context.Background()
	account := providers.Account{
		AccountNumber: "123456789012",
	}

	tests := []struct {
		name        string
		alarmConfig interface{}
		errorMsg    string
	}{
		{
			name:        "alarm_config is not a map",
			alarmConfig: "invalid-string",
			errorMsg:    "failed to unmarshal alarm config",
		},
		{
			name: "missing alarm name",
			alarmConfig: map[string]interface{}{
				"namespace":           "AWS/EC2",
				"metric_name":         "CPUUtilization",
				"statistic":           "Average",
				"period":              300,
				"evaluation_periods":  2,
				"datapoints_to_alarm": 2,
				"threshold":           80.0,
				"comparison_operator": "GreaterThanThreshold",
			},
			errorMsg: "alarm name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recommendation := providers.Recommendation{
				RuleName:       "aws_ec2_cpu_utilization_alarm_missing",
				ResourceId:     "i-1234567890abcdef0",
				ResourceRegion: "us-east-1",
				Data: map[string]interface{}{
					"alarm_config": tt.alarmConfig,
				},
			}

			err := CreateCloudWatchAlarmFromRecommendation(ctx, account, recommendation)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errorMsg)
		})
	}
}

// TestCreateCloudWatchAlarmFromRecommendation_ValidConfig tests successful parsing with valid config
func TestCreateCloudWatchAlarmFromRecommendation_ValidConfig(t *testing.T) {
	// Skip this test if AWS credentials are not available
	// This is a unit test that only validates the parsing logic, not actual AWS API calls
	t.Skip("Skipping test that requires AWS credentials - use integration test instead")

	ctx := context.Background()
	account := providers.Account{
		AccountNumber: "123456789012",
	}

	// Test simple metric alarm config parsing
	recommendation := providers.Recommendation{
		RuleName:       "aws_ec2_cpu_utilization_alarm_missing",
		ResourceId:     "i-1234567890abcdef0",
		ResourceRegion: "us-east-1",
		Data: map[string]interface{}{
			"alarm_config": map[string]interface{}{
				"alarm_name":          "test-cpu-alarm",
				"namespace":           "AWS/EC2",
				"metric_name":         "CPUUtilization",
				"statistic":           "Average",
				"period":              300,
				"evaluation_periods":  2,
				"datapoints_to_alarm": 2,
				"threshold":           80.0,
				"comparison_operator": "GreaterThanThreshold",
				"treat_missing_data":  "notBreaching",
				"dimensions": []map[string]interface{}{
					{"name": "InstanceId", "value": "i-1234567890abcdef0"},
				},
			},
		},
	}

	// This will fail at AWS API call level, but should pass validation
	err := CreateCloudWatchAlarmFromRecommendation(ctx, account, recommendation)
	// We expect this to fail with AWS credentials error, not validation error
	if err != nil {
		assert.NotContains(t, err.Error(), "alarm_config not found")
		assert.NotContains(t, err.Error(), "invalid alarm configuration")
	}
}
