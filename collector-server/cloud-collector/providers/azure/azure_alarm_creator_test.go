package azure

import (
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

// Test ValidateAzureAlarmConfig with various configurations
func TestValidateAzureAlarmConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  providers.AlarmCreationConfig
		wantErr bool
	}{
		{
			name: "valid configuration",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				MetricName:         "Percentage CPU",
				Namespace:          "Microsoft.Compute/virtualMachines",
				Period:             300,
				EvaluationPeriods:  3,
				Threshold:          80,
				ComparisonOperator: "GreaterThanThreshold",
				Statistic:          "Average",
			},
			wantErr: false,
		},
		{
			name: "missing alarm name",
			config: providers.AlarmCreationConfig{
				MetricName:         "Percentage CPU",
				Namespace:          "Microsoft.Compute/virtualMachines",
				Period:             300,
				EvaluationPeriods:  3,
				ComparisonOperator: "GreaterThanThreshold",
			},
			wantErr: true,
		},
		{
			name: "missing metric name",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				Namespace:          "Microsoft.Compute/virtualMachines",
				Period:             300,
				EvaluationPeriods:  3,
				ComparisonOperator: "GreaterThanThreshold",
			},
			wantErr: true,
		},
		{
			name: "missing namespace",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				MetricName:         "Percentage CPU",
				Period:             300,
				EvaluationPeriods:  3,
				ComparisonOperator: "GreaterThanThreshold",
			},
			wantErr: true,
		},
		{
			name: "invalid period",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				MetricName:         "Percentage CPU",
				Namespace:          "Microsoft.Compute/virtualMachines",
				Period:             0,
				EvaluationPeriods:  3,
				ComparisonOperator: "GreaterThanThreshold",
			},
			wantErr: true,
		},
		{
			name: "invalid evaluation periods",
			config: providers.AlarmCreationConfig{
				AlarmName:          "test-alarm",
				MetricName:         "Percentage CPU",
				Namespace:          "Microsoft.Compute/virtualMachines",
				Period:             300,
				EvaluationPeriods:  0,
				ComparisonOperator: "GreaterThanThreshold",
			},
			wantErr: true,
		},
		{
			name: "missing comparison operator",
			config: providers.AlarmCreationConfig{
				AlarmName:         "test-alarm",
				MetricName:        "Percentage CPU",
				Namespace:         "Microsoft.Compute/virtualMachines",
				Period:            300,
				EvaluationPeriods: 3,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAzureAlarmConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAzureAlarmConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test convertComparisonOperator
func TestConvertComparisonOperator(t *testing.T) {
	tests := []struct {
		name     string
		operator string
		want     armmonitor.Operator
	}{
		{"GreaterThan", "GreaterThanThreshold", armmonitor.OperatorGreaterThan},
		{"GreaterThanOrEqual", "GreaterThanOrEqualToThreshold", armmonitor.OperatorGreaterThanOrEqual},
		{"LessThan", "LessThanThreshold", armmonitor.OperatorLessThan},
		{"LessThanOrEqual", "LessThanOrEqualToThreshold", armmonitor.OperatorLessThanOrEqual},
		{"Default", "Unknown", armmonitor.OperatorGreaterThan},
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

// Test convertStatisticToAggregation
func TestConvertStatisticToAggregation(t *testing.T) {
	tests := []struct {
		name      string
		statistic string
		want      armmonitor.AggregationTypeEnum
	}{
		{"Average", "Average", armmonitor.AggregationTypeEnumAverage},
		{"Sum", "Sum", armmonitor.AggregationTypeEnumTotal},
		{"Maximum", "Maximum", armmonitor.AggregationTypeEnumMaximum},
		{"Minimum", "Minimum", armmonitor.AggregationTypeEnumMinimum},
		{"Count", "Count", armmonitor.AggregationTypeEnumCount},
		{"Default", "Unknown", armmonitor.AggregationTypeEnumAverage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertStatisticToAggregation(tt.statistic)
			if got != tt.want {
				t.Errorf("convertStatisticToAggregation() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test convertSeverityToAzure
func TestConvertSeverityToAzure(t *testing.T) {
	tests := []struct {
		name     string
		severity string
		want     int32
	}{
		{"Critical", "critical", 0},
		{"High", "high", 1},
		{"Medium", "medium", 2},
		{"Low", "low", 3},
		{"Info", "info", 4},
		{"Informational", "informational", 4},
		{"Default", "unknown", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertSeverityToAzure(tt.severity)
			if got != tt.want {
				t.Errorf("convertSeverityToAzure() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test secondsToISO8601Duration
func TestSecondsToISO8601Duration(t *testing.T) {
	tests := []struct {
		name    string
		seconds int
		want    string
	}{
		{"30 seconds", 30, "PT30S"},
		{"1 minute", 60, "PT1M"},
		{"5 minutes", 300, "PT5M"},
		{"1 hour", 3600, "PT1H"},
		{"1 hour 30 minutes", 5400, "PT1H30M"},
		{"2 hours", 7200, "PT2H"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := secondsToISO8601Duration(tt.seconds)
			if got != tt.want {
				t.Errorf("secondsToISO8601Duration() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test sanitizeAlarmName
func TestSanitizeAlarmName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"valid name", "test-alarm_123", "test-alarm_123"},
		{"with special chars", "test@alarm#123", "testalarm123"},
		{"with spaces", "test alarm 123", "testalarm123"},
		{"with dots", "test.alarm.123", "test.alarm.123"},
		{"long name", "this-is-a-very-long-alarm-name-that-exceeds-the-maximum-length-allowed-by-azure-metric-alerts-and-should-be-truncated-to-hundred-characters", "this-is-a-very-long-alarm-name-that-exceeds-the-maximum-length-allowed-by-azure-metric-alerts-and-sh"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeAlarmName(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeAlarmName() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test buildAzureAlarmConfig
func TestBuildAzureAlarmConfig(t *testing.T) {
	resource := providers.Resource{
		Name: "test-vm",
	}

	template := providers.AlarmTemplate{
		Name: "cpu-high",
		Configuration: providers.AlarmConfiguration{
			MetricName:         "Percentage CPU",
			Namespace:          "Microsoft.Compute/virtualMachines",
			Statistic:          "Average",
			Period:             300,
			EvaluationPeriods:  3,
			DatapointsToAlarm:  2,
			ComparisonOperator: "GreaterThanThreshold",
			TreatMissingData:   "notBreaching",
		},
	}

	threshold := 80.0

	config := buildAzureAlarmConfig(resource, template, threshold)

	if config.MetricName != template.Configuration.MetricName {
		t.Errorf("MetricName = %v, want %v", config.MetricName, template.Configuration.MetricName)
	}
	if config.Namespace != template.Configuration.Namespace {
		t.Errorf("Namespace = %v, want %v", config.Namespace, template.Configuration.Namespace)
	}
	if config.Threshold != threshold {
		t.Errorf("Threshold = %v, want %v", config.Threshold, threshold)
	}
	if config.Period != template.Configuration.Period {
		t.Errorf("Period = %v, want %v", config.Period, template.Configuration.Period)
	}
}

// Test getDataKeys
func TestGetDataKeys(t *testing.T) {
	data := map[string]interface{}{
		"alarm_config": map[string]interface{}{},
		"severity":     "high",
		"priority":     1,
	}

	keys := getDataKeys(data)

	if len(keys) != 3 {
		t.Errorf("getDataKeys() returned %d keys, want 3", len(keys))
	}

	keyMap := make(map[string]bool)
	for _, k := range keys {
		keyMap[k] = true
	}

	expectedKeys := []string{"alarm_config", "severity", "priority"}
	for _, expected := range expectedKeys {
		if !keyMap[expected] {
			t.Errorf("getDataKeys() missing key %s", expected)
		}
	}
}
