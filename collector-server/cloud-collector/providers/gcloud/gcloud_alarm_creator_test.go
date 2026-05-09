package gcloud

import (
	"nudgebee/collector/cloud/providers"
	"testing"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
)

// Test ValidateGCPAlarmConfig with various configurations
func TestValidateGCPAlarmConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  providers.AlarmCreationConfig
		wantErr bool
	}{
		{
			name: "valid simple metric alarm",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				MetricName:         "compute.googleapis.com/instance/cpu/utilization",
				Period:             60,
				EvaluationPeriods:  5,
				Threshold:          80,
				ComparisonOperator: "GreaterThanThreshold",
				Statistic:          "Average",
			},
			wantErr: false,
		},
		{
			name: "valid metric math alarm",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-math-alarm",
				Period:             60,
				EvaluationPeriods:  5,
				Threshold:          5,
				ComparisonOperator: "GreaterThanThreshold",
				Metrics: []providers.MetricQueryConfig{
					{
						Id:         "m1",
						ReturnData: false,
						MetricStat: &providers.MetricStatConfig{
							Metric: providers.MetricInfoConfig{
								MetricName: "compute.googleapis.com/instance/cpu/utilization",
							},
							Stat:   "Average",
							Period: 60,
						},
					},
					{
						Id:         "e1",
						Expression: "m1 * 100",
						ReturnData: true,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "missing alarm name",
			config: providers.AlarmCreationConfig{
				MetricName:         "test-metric",
				Period:             60,
				EvaluationPeriods:  5,
				ComparisonOperator: "GreaterThanThreshold",
			},
			wantErr: true,
		},
		{
			name: "invalid period",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				MetricName:         "test-metric",
				Period:             0,
				EvaluationPeriods:  5,
				ComparisonOperator: "GreaterThanThreshold",
			},
			wantErr: true,
		},
		{
			name: "invalid evaluation periods",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				MetricName:         "test-metric",
				Period:             60,
				EvaluationPeriods:  0,
				ComparisonOperator: "GreaterThanThreshold",
			},
			wantErr: true,
		},
		{
			name: "missing comparison operator",
			config: providers.AlarmCreationConfig{
				AlarmName:         "test-alarm",
				MetricName:        "test-metric",
				Period:            60,
				EvaluationPeriods: 5,
			},
			wantErr: true,
		},
		{
			name: "metric math without return_data",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				Period:             60,
				EvaluationPeriods:  5,
				ComparisonOperator: "GreaterThanThreshold",
				Metrics: []providers.MetricQueryConfig{
					{
						Id:         "m1",
						ReturnData: false,
						MetricStat: &providers.MetricStatConfig{
							Metric: providers.MetricInfoConfig{
								MetricName: "test-metric",
							},
							Stat:   "Average",
							Period: 60,
						},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGCPAlarmConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateGCPAlarmConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test buildGCPAlarmConfig constructs correct AlarmCreationConfig
func TestBuildGCPAlarmConfig(t *testing.T) {
	resource := providers.Resource{
		Id:   "1234567890",
		Name: "test-instance",
	}
	template := providers.AlarmTemplate{
		Name: "cpu-high",
		Configuration: providers.AlarmConfiguration{
			MetricName:         "compute.googleapis.com/instance/cpu/utilization",
			Namespace:          "compute.googleapis.com",
			Statistic:          "Average",
			Period:             300,
			EvaluationPeriods:  2,
			DatapointsToAlarm:  2,
			ComparisonOperator: "GreaterThanThreshold",
			TreatMissingData:   "missing",
		},
	}
	dimensions := []providers.AlarmDimension{
		{Name: "instance_id", Value: "1234567890"},
	}

	config := buildGCPAlarmConfig(resource, template, 80.0, dimensions)

	if config.AlarmName != "cpu-high-1234567890" {
		t.Errorf("AlarmName = %q, want %q", config.AlarmName, "cpu-high-1234567890")
	}
	if config.MetricName != "compute.googleapis.com/instance/cpu/utilization" {
		t.Errorf("MetricName = %q, want %q", config.MetricName, "compute.googleapis.com/instance/cpu/utilization")
	}
	if config.Threshold != 80.0 {
		t.Errorf("Threshold = %v, want %v", config.Threshold, 80.0)
	}
	if config.Period != 300 {
		t.Errorf("Period = %v, want %v", config.Period, 300)
	}
	if config.EvaluationPeriods != 2 {
		t.Errorf("EvaluationPeriods = %v, want %v", config.EvaluationPeriods, 2)
	}
	if config.ComparisonOperator != "GreaterThanThreshold" {
		t.Errorf("ComparisonOperator = %q, want %q", config.ComparisonOperator, "GreaterThanThreshold")
	}
	if len(config.Dimensions) != 1 {
		t.Fatalf("Dimensions length = %d, want 1", len(config.Dimensions))
	}
	if config.Dimensions[0].Name != "instance_id" || config.Dimensions[0].Value != "1234567890" {
		t.Errorf("Dimensions[0] = %+v, want {Name:instance_id Value:1234567890}", config.Dimensions[0])
	}
}

// Test convertComparisonOperator
func TestConvertComparisonOperator(t *testing.T) {
	tests := []struct {
		name     string
		operator string
		want     monitoringpb.ComparisonType
	}{
		{"GreaterThan", "GreaterThanThreshold", monitoringpb.ComparisonType_COMPARISON_GT},
		{"GreaterThanOrEqual", "GreaterThanOrEqualToThreshold", monitoringpb.ComparisonType_COMPARISON_GE},
		{"LessThan", "LessThanThreshold", monitoringpb.ComparisonType_COMPARISON_LT},
		{"LessThanOrEqual", "LessThanOrEqualToThreshold", monitoringpb.ComparisonType_COMPARISON_LE},
		{"Default", "Unknown", monitoringpb.ComparisonType_COMPARISON_GT},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertComparisonOperator(tt.operator)
			if got != tt.want {
				t.Errorf("convertComparisonOperator() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test convertStatisticToAligner
func TestConvertStatisticToAligner(t *testing.T) {
	tests := []struct {
		name      string
		statistic string
		want      monitoringpb.Aggregation_Aligner
	}{
		{"Average", "Average", monitoringpb.Aggregation_ALIGN_MEAN},
		{"Avg", "Avg", monitoringpb.Aggregation_ALIGN_MEAN},
		{"Sum", "Sum", monitoringpb.Aggregation_ALIGN_SUM},
		{"Minimum", "Minimum", monitoringpb.Aggregation_ALIGN_MIN},
		{"Min", "Min", monitoringpb.Aggregation_ALIGN_MIN},
		{"Maximum", "Maximum", monitoringpb.Aggregation_ALIGN_MAX},
		{"Max", "Max", monitoringpb.Aggregation_ALIGN_MAX},
		{"SampleCount", "SampleCount", monitoringpb.Aggregation_ALIGN_COUNT},
		{"Default", "Unknown", monitoringpb.Aggregation_ALIGN_MEAN},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertStatisticToAligner(tt.statistic)
			if got != tt.want {
				t.Errorf("convertStatisticToAligner() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test extractProjectID
func TestExtractProjectID(t *testing.T) {
	tests := []struct {
		name       string
		resourceID string
		want       string
	}{
		{
			name:       "standard resource ID",
			resourceID: "projects/my-project/zones/us-central1-a/instances/my-instance",
			want:       "my-project",
		},
		{
			name:       "resource ID without projects",
			resourceID: "zones/us-central1-a/instances/my-instance",
			want:       "",
		},
		{
			name:       "empty resource ID",
			resourceID: "",
			want:       "",
		},
		{
			name:       "just project",
			resourceID: "projects/test-project",
			want:       "test-project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractProjectID(tt.resourceID)
			if got != tt.want {
				t.Errorf("extractProjectID() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test buildSimpleCondition
func TestBuildSimpleCondition(t *testing.T) {
	config := providers.AlarmCreationConfig{
		AlarmName:         "test-alarm",
		MetricName:        "compute.googleapis.com/instance/cpu/utilization",
		Period:            60,
		EvaluationPeriods: 5,
		Threshold:         80,
		Statistic:         "Average",
		Dimensions: []providers.AlarmDimension{
			{Name: "instance_id", Value: "i-123456"},
		},
	}

	condition, err := buildSimpleCondition(config)
	if err != nil {
		t.Fatalf("buildSimpleCondition() unexpected error = %v", err)
	}

	if condition.DisplayName != config.AlarmName {
		t.Errorf("DisplayName = %v, want %v", condition.DisplayName, config.AlarmName)
	}

	threshold := condition.GetConditionThreshold()
	if threshold == nil {
		t.Fatal("Expected ConditionThreshold, got nil")
		return
	}

	if threshold.ThresholdValue != config.Threshold {
		t.Errorf("ThresholdValue = %v, want %v", threshold.ThresholdValue, config.Threshold)
	}
}

// Test buildMQLCondition
func TestBuildMQLCondition(t *testing.T) {
	config := providers.AlarmCreationConfig{
		AlarmName:          "test-math-alarm",
		Period:             60,
		EvaluationPeriods:  5,
		Threshold:          80,
		ComparisonOperator: "GreaterThanThreshold",
		Metrics: []providers.MetricQueryConfig{
			{
				Id:         "e1",
				Expression: "m1 * 100",
				ReturnData: true,
			},
		},
	}

	condition, err := buildMQLCondition(config)
	if err != nil {
		t.Fatalf("buildMQLCondition() unexpected error = %v", err)
	}

	if condition.DisplayName != config.AlarmName {
		t.Errorf("DisplayName = %v, want %v", condition.DisplayName, config.AlarmName)
	}

	mql := condition.GetConditionMonitoringQueryLanguage()
	if mql == nil {
		t.Fatal("Expected MonitoringQueryLanguageCondition, got nil")
		return
	}

	// Query should now include the threshold condition
	expectedQuery := "m1 * 100 | condition val() > 80.00"
	if mql.Query != expectedQuery {
		t.Errorf("Query = %v, want %v", mql.Query, expectedQuery)
	}
}

// Test getResourceTypeFromMetric
func TestGetResourceTypeFromMetric(t *testing.T) {
	tests := []struct {
		name       string
		metricName string
		want       string
		wantErr    bool
	}{
		{
			name:       "compute engine metric",
			metricName: "compute.googleapis.com/instance/cpu/utilization",
			want:       "gce_instance",
			wantErr:    false,
		},
		{
			name:       "cloud sql metric",
			metricName: "cloudsql.googleapis.com/database/cpu/utilization",
			want:       "cloudsql_database",
			wantErr:    false,
		},
		{
			name:       "storage metric",
			metricName: "storage.googleapis.com/storage/total_bytes",
			want:       "gcs_bucket",
			wantErr:    false,
		},
		{
			name:       "gke metric",
			metricName: "container.googleapis.com/container/cpu/usage_time",
			want:       "k8s_container",
			wantErr:    false,
		},
		{
			name:       "bigquery metric",
			metricName: "bigquery.googleapis.com/storage/stored_bytes",
			want:       "bigquery_project",
			wantErr:    false,
		},
		{
			name:       "unknown compute metric by prefix",
			metricName: "compute.googleapis.com/instance/unknown/metric",
			want:       "gce_instance",
			wantErr:    false,
		},
		{
			name:       "completely unknown metric",
			metricName: "unknown.googleapis.com/some/metric",
			want:       "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getResourceTypeFromMetric(tt.metricName)
			if (err != nil) != tt.wantErr {
				t.Errorf("getResourceTypeFromMetric() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getResourceTypeFromMetric() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test convertComparisonOperatorToMQL
func TestConvertComparisonOperatorToMQL(t *testing.T) {
	tests := []struct {
		name     string
		operator string
		want     string
	}{
		{"GreaterThan", "GreaterThanThreshold", ">"},
		{"GreaterThanOrEqual", "GreaterThanOrEqualToThreshold", ">="},
		{"LessThan", "LessThanThreshold", "<"},
		{"LessThanOrEqual", "LessThanOrEqualToThreshold", "<="},
		{"Default", "Unknown", ">"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertComparisonOperatorToMQL(tt.operator)
			if got != tt.want {
				t.Errorf("convertComparisonOperatorToMQL() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test convertStatisticToMQLFunction
func TestConvertStatisticToMQLFunction(t *testing.T) {
	tests := []struct {
		name      string
		statistic string
		want      string
	}{
		{"Average", "Average", "mean"},
		{"Avg", "Avg", "mean"},
		{"Sum", "Sum", "sum"},
		{"Minimum", "Minimum", "min"},
		{"Min", "Min", "min"},
		{"Maximum", "Maximum", "max"},
		{"Max", "Max", "max"},
		{"SampleCount", "SampleCount", "count"},
		{"Default", "Unknown", "mean"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertStatisticToMQLFunction(tt.statistic)
			if got != tt.want {
				t.Errorf("convertStatisticToMQLFunction() = %v, want %v", got, tt.want)
			}
		})
	}
}
