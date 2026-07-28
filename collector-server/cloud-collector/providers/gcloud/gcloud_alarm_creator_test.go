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
			MetricName:         "CPUUtilization",
			MetricTypeFilter:   "compute.googleapis.com/instance/cpu/utilization",
			ResourceType:       "gce_instance",
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
	if config.MetricName != "CPUUtilization" {
		t.Errorf("MetricName = %q, want %q", config.MetricName, "CPUUtilization")
	}
	// The authoritative GCP filter values must be carried from the template into the
	// runtime config; dropping them is what broke "Create Alarm" (issue #32382).
	if config.MetricTypeFilter != "compute.googleapis.com/instance/cpu/utilization" {
		t.Errorf("MetricTypeFilter = %q, want %q", config.MetricTypeFilter, "compute.googleapis.com/instance/cpu/utilization")
	}
	if config.ResourceType != "gce_instance" {
		t.Errorf("ResourceType = %q, want %q", config.ResourceType, "gce_instance")
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

// Regression for issue #32382: real GCP templates carry a short, human-friendly
// MetricName (e.g. "DiskUtilization") that is NOT a valid Cloud Monitoring metric
// type. buildSimpleCondition must use the template's MetricTypeFilter/ResourceType
// to build the filter instead of failing with "unknown resource type for metric".
func TestBuildSimpleCondition_ShortMetricNameWithTemplateFields(t *testing.T) {
	config := providers.AlarmCreationConfig{
		AlarmName:         "gcp_compute_disk_utilization_alarm_missing-1234567890",
		MetricName:        "DiskUtilization", // short label, as emitted by the YAML templates
		MetricTypeFilter:  "agent.googleapis.com/disk/percent_used",
		ResourceType:      "gce_instance",
		Period:            300,
		EvaluationPeriods: 2,
		Threshold:         0.85,
		Statistic:         "ALIGN_MEAN",
		Dimensions: []providers.AlarmDimension{
			{Name: "instance_id", Value: "1234567890"},
		},
	}

	condition, err := buildSimpleCondition(config)
	if err != nil {
		t.Fatalf("buildSimpleCondition() unexpected error = %v", err)
	}

	threshold := condition.GetConditionThreshold()
	if threshold == nil {
		t.Fatal("Expected ConditionThreshold, got nil")
		return
	}

	wantFilter := `resource.type="gce_instance" AND metric.type="agent.googleapis.com/disk/percent_used" AND resource.labels.instance_id="1234567890"`
	if threshold.GetFilter() != wantFilter {
		t.Errorf("Filter =\n  %q\nwant\n  %q", threshold.GetFilter(), wantFilter)
	}
}

// End-to-end regression for issue #32382 using the actual embedded YAML template:
// load the real "DiskUtilization" template, build the alarm config the way the
// collector does, then build the condition. This is the path that failed in QA.
func TestBuildSimpleCondition_FromEmbeddedTemplate(t *testing.T) {
	template, err := GetGCPTemplateByName("gcp_compute_disk_utilization_alarm_missing")
	if err != nil {
		t.Fatalf("GetGCPTemplateByName() error = %v", err)
	}

	resource := providers.Resource{Id: "1234567890", Name: "test-instance"}
	config := buildGCPAlarmConfig(resource, *template, 0.85, []providers.AlarmDimension{
		{Name: "instance_id", Value: "1234567890"},
	})

	if _, err := buildSimpleCondition(config); err != nil {
		t.Fatalf("buildSimpleCondition() from embedded template error = %v", err)
	}
}

// Regression for issue #33376: recommendations stored before #32489 carry only the
// short display label (MetricName) plus the namespace — no metric_type_filter /
// resource_type. buildSimpleCondition must recover those from the alarm templates so
// "Create Alarm" doesn't fail with "unknown resource type for metric: DiskUtilization".
func TestBuildSimpleCondition_StaleConfigRecoversFromTemplate(t *testing.T) {
	config := providers.AlarmCreationConfig{
		AlarmName:         "gcp_cloudsql_disk_utilization_alarm_missing-test-instance",
		MetricName:        "DiskUtilization", // short label only, as stored before #32489
		Namespace:         "cloudsql.googleapis.com",
		Period:            300,
		EvaluationPeriods: 2,
		Threshold:         0.8,
		Statistic:         "ALIGN_MEAN",
		Dimensions: []providers.AlarmDimension{
			{Name: "database_id", Value: "test-project:test-instance"},
		},
		// MetricTypeFilter and ResourceType intentionally empty.
	}

	condition, err := buildSimpleCondition(config)
	if err != nil {
		t.Fatalf("buildSimpleCondition() unexpected error = %v", err)
	}

	threshold := condition.GetConditionThreshold()
	if threshold == nil {
		t.Fatal("Expected ConditionThreshold, got nil")
		return
	}

	wantFilter := `resource.type="cloudsql_database" AND metric.type="cloudsql.googleapis.com/database/disk/utilization" AND resource.labels.database_id="test-project:test-instance"`
	if threshold.GetFilter() != wantFilter {
		t.Errorf("Filter =\n  %q\nwant\n  %q", threshold.GetFilter(), wantFilter)
	}
}

// resolveGCPMetricAndResource must recover the fully-qualified metric type and resource
// type from the templates for every short label the YAML templates emit — including the
// agent.googleapis.com/* Compute metrics that the hardcoded getResourceTypeFromMetric /
// getGCPMetricType maps do NOT know about.
func TestResolveGCPMetricAndResource_StaleLabels(t *testing.T) {
	tests := []struct {
		name           string
		namespace      string
		metricName     string
		wantResource   string
		wantMetricType string
		wantErr        bool
	}{
		{
			name:           "cloudsql disk utilization",
			namespace:      "cloudsql.googleapis.com",
			metricName:     "DiskUtilization",
			wantResource:   "cloudsql_database",
			wantMetricType: "cloudsql.googleapis.com/database/disk/utilization",
		},
		{
			name:           "cloudsql cpu utilization",
			namespace:      "cloudsql.googleapis.com",
			metricName:     "CPUUtilization",
			wantResource:   "cloudsql_database",
			wantMetricType: "cloudsql.googleapis.com/database/cpu/utilization",
		},
		{
			name:           "cloudsql memory utilization",
			namespace:      "cloudsql.googleapis.com",
			metricName:     "MemoryUtilization",
			wantResource:   "cloudsql_database",
			wantMetricType: "cloudsql.googleapis.com/database/memory/utilization",
		},
		{
			// agent.googleapis.com metric — not in the hardcoded maps, only in templates.
			name:           "compute disk utilization via agent metric",
			namespace:      "agent.googleapis.com",
			metricName:     "DiskUtilization",
			wantResource:   "gce_instance",
			wantMetricType: "agent.googleapis.com/disk/percent_used",
		},
		{
			name:           "compute memory utilization via agent metric",
			namespace:      "agent.googleapis.com",
			metricName:     "MemoryUtilization",
			wantResource:   "gce_instance",
			wantMetricType: "agent.googleapis.com/memory/percent_used",
		},
		{
			name:       "unknown label with unknown namespace still errors",
			namespace:  "unknown.googleapis.com",
			metricName: "TotallyMadeUpMetric",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotResource, gotMetricType, err := resolveGCPMetricAndResource(tt.namespace, tt.metricName, "", "")
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveGCPMetricAndResource() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if gotResource != tt.wantResource {
				t.Errorf("resourceType = %q, want %q", gotResource, tt.wantResource)
			}
			if gotMetricType != tt.wantMetricType {
				t.Errorf("metricType = %q, want %q", gotMetricType, tt.wantMetricType)
			}
		})
	}
}

// Explicit template fields must always win over template recovery, so freshly-generated
// recommendations are unaffected by the stale-config fallback.
func TestResolveGCPMetricAndResource_ExplicitFieldsWin(t *testing.T) {
	gotResource, gotMetricType, err := resolveGCPMetricAndResource(
		"cloudsql.googleapis.com", "DiskUtilization",
		"custom.googleapis.com/my/metric", "custom_resource")
	if err != nil {
		t.Fatalf("resolveGCPMetricAndResource() unexpected error = %v", err)
	}
	if gotResource != "custom_resource" {
		t.Errorf("resourceType = %q, want %q", gotResource, "custom_resource")
	}
	if gotMetricType != "custom.googleapis.com/my/metric" {
		t.Errorf("metricType = %q, want %q", gotMetricType, "custom.googleapis.com/my/metric")
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
