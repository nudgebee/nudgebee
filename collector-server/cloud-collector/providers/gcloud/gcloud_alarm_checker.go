package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"regexp"
	"strings"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/protobuf/encoding/protojson"
)

const ServiceNameLoadBalancing = "Cloud Load Balancing"

// validResourceID matches alphanumeric, hyphens, dots, underscores, and colons
var validResourceID = regexp.MustCompile(`^[a-zA-Z0-9._:/-]+$`)

// AlarmCheckResult contains alarm check result
type AlarmCheckResult struct {
	AlarmExists bool
	AlarmName   string
	AlarmState  string
}

// IsAlarmMissing checks if alarm matching template exists for resource
func IsAlarmMissing(resource providers.Resource, template providers.AlarmTemplate, resourceFilter string) (bool, error) {
	alertDetails, ok := resource.Meta["AlertPolicies"]
	if !ok {
		return true, nil
	}

	alertArray, ok := alertDetails.([]interface{})
	if !ok {
		return true, nil
	}

	if len(alertArray) == 0 {
		return true, nil
	}

	for _, alertInterface := range alertArray {
		exists, err := doesAlertMatchTemplate(alertInterface, template, resourceFilter)
		if err != nil {
			continue
		}
		if exists {
			return false, nil
		}
	}

	return true, nil
}

// doesAlertMatchTemplate checks if alert matches template
func doesAlertMatchTemplate(alertInterface interface{}, template providers.AlarmTemplate, resourceFilter string) (bool, error) {
	var alert monitoring.AlertPolicy

	alertBytes, err := common.MarshalJson(alertInterface)
	if err != nil {
		return false, fmt.Errorf("failed to marshal alert: %w", err)
	}

	if err := protojson.Unmarshal(alertBytes, &alert); err != nil {
		return false, fmt.Errorf("failed to unmarshal alert: %w", err)
	}

	// Check conditions
	for _, condition := range alert.Conditions {
		if matchCondition(condition, template, resourceFilter) {
			return true, nil
		}
	}

	return false, nil
}

// matchCondition checks if condition matches template
func matchCondition(condition *monitoring.AlertPolicy_Condition, template providers.AlarmTemplate, resourceFilter string) bool {
	metricThreshold := condition.GetConditionThreshold()
	if metricThreshold == nil {
		return false
	}

	// Check metric type
	expectedMetric := getGCPMetricType(template.Configuration.Namespace, template.Configuration.MetricName)
	if !strings.Contains(metricThreshold.Filter, expectedMetric) {
		return false
	}

	// Check resource filter
	if resourceFilter != "" && !strings.Contains(metricThreshold.Filter, resourceFilter) {
		return false
	}

	return true
}

// getGCPMetricType converts generic metric to GCP metric type
func getGCPMetricType(namespace, metricName string) string {
	switch namespace {
	case "compute.googleapis.com":
		switch metricName {
		case "CPUUtilization":
			return "compute.googleapis.com/instance/cpu/utilization"
		case "DiskReadBytes":
			return "compute.googleapis.com/instance/disk/read_bytes_count"
		case "DiskWriteBytes":
			return "compute.googleapis.com/instance/disk/write_bytes_count"
		}
	case "cloudsql.googleapis.com":
		switch metricName {
		case "CPUUtilization":
			return "cloudsql.googleapis.com/database/cpu/utilization"
		case "MemoryUtilization":
			return "cloudsql.googleapis.com/database/memory/utilization"
		case "DiskUtilization":
			return "cloudsql.googleapis.com/database/disk/utilization"
		}
	case "loadbalancing.googleapis.com":
		switch metricName {
		case "RequestCount":
			return "loadbalancing.googleapis.com/https/request_count"
		case "Latency":
			return "loadbalancing.googleapis.com/https/backend_latencies"
		}
	case "run.googleapis.com":
		switch metricName {
		case "CPUUtilization":
			return "run.googleapis.com/container/cpu/utilizations"
		case "MemoryUtilization":
			return "run.googleapis.com/container/memory/utilizations"
		}
	}

	return fmt.Sprintf("%s/%s", namespace, strings.ToLower(metricName))
}

// GetAlarmState returns alarm state from alert policy
func GetAlarmState(alertInterface interface{}) string {
	alertBytes, err := common.MarshalJson(alertInterface)
	if err != nil {
		return "UNKNOWN"
	}

	var alert monitoring.AlertPolicy
	if err := protojson.Unmarshal(alertBytes, &alert); err != nil {
		return "UNKNOWN"
	}

	// Check if enabled
	if !alert.GetEnabled().GetValue() {
		return "DISABLED"
	}

	// GCP doesn't expose real-time state in AlertPolicy
	// State is available through incidents API
	return "ENABLED"
}

// GetResourceFilterForService generates resource filter for service
func GetResourceFilterForService(serviceName, resourceID string) string {
	if !validResourceID.MatchString(resourceID) {
		return ""
	}

	switch serviceName {
	case ServiceNameCompute:
		return fmt.Sprintf(`resource.type="gce_instance" AND resource.labels.instance_id="%s"`, resourceID)
	case ServiceNameSQL:
		return fmt.Sprintf(`resource.type="cloudsql_database" AND resource.labels.database_id="%s"`, resourceID)
	case ServiceNameRun:
		return fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s"`, resourceID)
	case ServiceNameGKE:
		return fmt.Sprintf(`resource.type="k8s_cluster" AND resource.labels.cluster_name="%s"`, resourceID)
	case ServiceNameLoadBalancing:
		return fmt.Sprintf(`resource.type="https_lb_rule" AND resource.labels.forwarding_rule_name="%s"`, resourceID)
	default:
		return fmt.Sprintf(`resource.labels.resource_id="%s"`, resourceID)
	}
}
