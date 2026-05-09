package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"sort"
	"strings"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// gcloudServiceMetricsMap defines default metrics for each GCP service
// This is used for auto-detection when MetricNames are not explicitly provided
// Resource type keys match the database format: strings.ToLower(strings.ReplaceAll(ServiceName, " ", "-"))
var gcloudServiceMetricsMap = map[string]map[string][]string{
	"compute engine": {
		"compute-engine": { // Database stores as "compute-engine" not "instance"
			"cpu/utilization",
			"disk/read_bytes_count",
			"disk/write_bytes_count",
			"network/received_bytes_count",
			"network/sent_bytes_count",
			"uptime",
		},
		"compute.googleapis.com/instance": { // Support GCP native resource type format (lowercase)
			"cpu/utilization",
			"disk/read_bytes_count",
			"disk/write_bytes_count",
			"network/received_bytes_count",
			"network/sent_bytes_count",
			"uptime",
		},
		"compute.googleapis.com/Instance": { // Support GCP native resource type format (capitalized)
			"cpu/utilization",
			"disk/read_bytes_count",
			"disk/write_bytes_count",
			"network/received_bytes_count",
			"network/sent_bytes_count",
			"uptime",
		},
	},
	"cloud sql": {
		"cloud-sql": { // Database stores as "cloud-sql"
			"cpu/utilization",
			"memory/utilization",
			"disk/bytes_used",
			"disk/utilization",
			"network/connections",
			"network/received_bytes_count",
			"network/sent_bytes_count",
		},
	},
	"kubernetes engine": {
		"kubernetes-engine": { // Database stores as "kubernetes-engine"
			"container/cpu/core_usage_time",
			"container/memory/page_fault_count",
			"container/memory/working_set_bytes",
			"pod/network/received_bytes_count",
			"pod/network/sent_bytes_count",
		},
	},
	"cloud storage": {
		"cloud-storage": { // Database stores as "cloud-storage"
			"api/request_count",
			"network/received_bytes_count",
			"network/sent_bytes_count",
			"storage/total_bytes",
		},
	},
	"cloud run": {
		"cloud-run": { // Database stores as "cloud-run"
			"request_count",
			"request_latencies",
			"container/cpu/utilizations",
			"container/memory/utilizations",
		},
	},
	"cloud functions": {
		"cloud-functions": { // Database stores as "cloud-functions"
			"execution_count",
			"execution_times",
			"user_memory_bytes",
			"active_instances",
		},
	},
	"bigquery": {
		"bigquery": { // Database stores as "bigquery"
			"query/count",
			"query/execution_times",
			"slots/allocated_for_project",
			"slots/total_available",
			"storage/stored_bytes",
			"storage/uploaded_bytes",
		},
	},
}

// gcloudMetricsStatsMap defines default statistics for each metric type
// Used when statistics are not explicitly provided in the query
var gcloudMetricsStatsMap = map[string]string{
	// Compute Engine metrics
	"cpu/utilization":              "Average",
	"disk/read_bytes_count":        "Sum",
	"disk/write_bytes_count":       "Sum",
	"network/received_bytes_count": "Sum",
	"network/sent_bytes_count":     "Sum",
	"uptime":                       "Average",

	// Cloud SQL metrics
	"memory/utilization":  "Average",
	"disk/bytes_used":     "Average",
	"disk/utilization":    "Average",
	"network/connections": "Average",

	// Kubernetes Engine metrics
	"container/cpu/core_usage_time":      "Average",
	"container/memory/page_fault_count":  "Sum",
	"container/memory/working_set_bytes": "Average",
	"pod/network/received_bytes_count":   "Sum",
	"pod/network/sent_bytes_count":       "Sum",

	// Cloud Storage metrics
	"api/request_count":   "Sum",
	"storage/total_bytes": "Average",

	// Cloud Run metrics
	"request_count":                 "Sum",
	"request_latencies":             "Average",
	"container/cpu/utilizations":    "Average",
	"container/memory/utilizations": "Average",

	// Cloud Functions metrics
	"execution_count":   "Sum",
	"execution_times":   "Average",
	"user_memory_bytes": "Average",
	"active_instances":  "Average",

	// BigQuery metrics
	"query/count":                 "Sum",
	"query/execution_times":       "Average",
	"slots/allocated_for_project": "Average",
	"slots/total_available":       "Average",
	"storage/stored_bytes":        "Average",
	"storage/uploaded_bytes":      "Sum",
}

// listGcloudMonitoringMetricsDynamic calls Google Cloud Monitoring ListMetricDescriptors API
// to discover available metrics dynamically for a given service.
func listGcloudMonitoringMetricsDynamic(ctx providers.CloudProviderContext, account providers.Account, serviceName string) (providers.ListMetricsResponse, error) {
	metricTypePrefix := getMetricTypePrefix(serviceName, "")
	if metricTypePrefix == "" {
		return providers.ListMetricsResponse{}, fmt.Errorf("unknown GCP service: %s", serviceName)
	}

	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return providers.ListMetricsResponse{}, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := monitoring.NewMetricClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return providers.ListMetricsResponse{}, fmt.Errorf("failed to create monitoring client: %w", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			ctx.GetLogger().Warn("failed to close monitoring client", "error", err)
		}
	}()

	filter := fmt.Sprintf(`metric.type = starts_with("%s/")`, metricTypePrefix)
	it := client.ListMetricDescriptors(ctx.GetContext(), &monitoringpb.ListMetricDescriptorsRequest{
		Name:   fmt.Sprintf("projects/%s", session.ProjectId),
		Filter: filter,
	})

	metricSet := make(map[string]providers.AvailableMetric)
	for {
		desc, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return providers.ListMetricsResponse{}, err
		}
		// Extract the metric name after the prefix (e.g., "compute.googleapis.com/instance/cpu/utilization" → "cpu/utilization")
		name := desc.Type
		if strings.HasPrefix(name, metricTypePrefix+"/") {
			name = strings.TrimPrefix(name, metricTypePrefix+"/")
		}
		metricSet[name] = providers.AvailableMetric{
			Name:      name,
			Namespace: metricTypePrefix,
		}
	}

	metrics := make([]providers.AvailableMetric, 0, len(metricSet))
	for _, m := range metricSet {
		metrics = append(metrics, m)
	}
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].Name < metrics[j].Name })
	return providers.ListMetricsResponse{Metrics: metrics}, nil
}

func listGcloudMonitoringMetrics(request providers.ListMetricsRequest) (providers.ListMetricsResponse, error) {
	serviceName := strings.ToLower(request.ServiceName)
	serviceMetrics, ok := gcloudServiceMetricsMap[serviceName]
	if !ok {
		return providers.ListMetricsResponse{Metrics: []providers.AvailableMetric{}}, nil
	}

	resourceType := strings.ToLower(request.ResourceType)
	if resourceType == "" && len(serviceMetrics) > 0 {
		for rt := range serviceMetrics {
			resourceType = rt
			break
		}
	}

	metricNames := serviceMetrics[resourceType]
	metrics := make([]providers.AvailableMetric, 0, len(metricNames))
	for _, name := range metricNames {
		info := providers.AvailableMetric{
			Name: name,
		}
		if stat, ok := gcloudMetricsStatsMap[name]; ok {
			info.Statistics = []string{stat}
		}
		metrics = append(metrics, info)
	}

	return providers.ListMetricsResponse{Metrics: metrics}, nil
}

// getGcloudMonitoringMetrics retrieves metrics from Google Cloud Monitoring (formerly Stackdriver)
func getGcloudMonitoringMetrics(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return providers.QueryMetricsResponse{}, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := monitoring.NewMetricClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return providers.QueryMetricsResponse{}, fmt.Errorf("failed to create monitoring client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close google cloud monitoring metrics client", "error", cerr)
		}
	}()
	// Define the time period for metrics
	startTime := time.Now().UTC().Add(-1 * time.Hour)
	if filter.StartDate != nil {
		startTime = *filter.StartDate
	}
	endTime := time.Now().UTC()
	if filter.EndDate != nil {
		endTime = *filter.EndDate
	}

	// Default interval/step
	step := 60 * time.Second
	if filter.Step > 0 {
		step = filter.Step
	}

	var metricItems []providers.MetricItem

	// Get metric type prefix based on service
	metricTypePrefix := getMetricTypePrefix(filter.ServiceName, filter.ResourceType)

	// Auto-detect metrics if not provided (similar to Azure implementation)
	metricNames := filter.MetricNames
	if len(metricNames) == 0 && filter.ServiceName != "" {
		serviceName := normalizeServiceName(filter.ServiceName)
		resourceType := filter.ResourceType // Don't lowercase - keep as-is from database

		ctx.GetLogger().Info("gcp:QueryMetrics attempting auto-detection",
			"service", filter.ServiceName,
			"serviceKey", serviceName,
			"resourceType", resourceType)

		if serviceMetrics, ok := gcloudServiceMetricsMap[serviceName]; ok {
			// Try exact match first
			if resourceType != "" && serviceMetrics[resourceType] != nil {
				metricNames = serviceMetrics[resourceType]
				ctx.GetLogger().Info("gcp:QueryMetrics auto-detected metrics for resource type",
					"service", filter.ServiceName,
					"resourceType", resourceType,
					"metrics", metricNames)
			} else {
				// Try lowercase match as fallback
				resourceTypeLower := strings.ToLower(resourceType)
				if resourceTypeLower != "" && serviceMetrics[resourceTypeLower] != nil {
					metricNames = serviceMetrics[resourceTypeLower]
					ctx.GetLogger().Info("gcp:QueryMetrics auto-detected metrics for resource type (lowercase match)",
						"service", filter.ServiceName,
						"resourceType", resourceTypeLower,
						"metrics", metricNames)
				} else {
					// Fallback: if specific resource type not found, use first available metrics set
					ctx.GetLogger().Warn("gcp:QueryMetrics no metrics found for specific resource type, using fallback",
						"resourceType", resourceType,
						"availableTypes", getKeys(serviceMetrics))
					for _, metrics := range serviceMetrics {
						metricNames = metrics
						ctx.GetLogger().Info("gcp:QueryMetrics using fallback metrics", "metrics", metricNames)
						break
					}
				}
			}
		} else {
			ctx.GetLogger().Warn("gcp:QueryMetrics no metrics map found for service",
				"service", filter.ServiceName,
				"availableServices", getKeys(gcloudServiceMetricsMap))
		}
	}

	// If specific metric names are provided (or auto-detected), query each one
	if len(metricNames) > 0 {
		for _, metricName := range metricNames {
			// If the metric name is already a full metric type (e.g. from a GCP alert policy filter),
			// use it directly instead of prepending the service prefix again.
			metricType := metricName
			if metricTypePrefix != "" && !strings.Contains(metricName, ".googleapis.com") {
				metricType = fmt.Sprintf("%s/%s", metricTypePrefix, metricName)
			}

			// Determine statistic to use for this metric
			var statistic string
			if len(filter.Statistics) > 0 {
				statistic = filter.Statistics[0]
			} else if defaultStat, ok := gcloudMetricsStatsMap[metricName]; ok {
				statistic = defaultStat
			} else {
				statistic = "Average"
			}

			items, err := queryMetric(ctx, client, session.ProjectId, metricType, filter, startTime, endTime, step, statistic)
			if err != nil {
				errStr := err.Error()
				_, _, _, isPermErr := IsGCPPermissionError(err)
				// "code = NotFound" — metric type doesn't exist in this project (e.g. workload
				// hasn't emitted it yet, or our default-metrics list is wider than what the
				// project supports). "code = InvalidArgument" — filter+resource pair invalid
				// for this metric. Both are project-config / data-shape conditions, not faults.
				isMissingMetric := strings.Contains(errStr, "code = NotFound") || strings.Contains(errStr, "code = InvalidArgument")
				if isPermErr || isMissingMetric || strings.Contains(errStr, "could not find default credentials") {
					ctx.GetLogger().Warn("failed to query metric", "error", err, "metric", metricName)
				} else {
					ctx.GetLogger().Error("failed to query metric", "error", err, "metric", metricName)
				}
				RecordGCPPermissionError(ctx, err)
				continue
			}
			metricItems = append(metricItems, items...)
		}
	} else {
		// No metrics could be auto-detected, log warning
		ctx.GetLogger().Warn("gcp:QueryMetrics no metrics available",
			"service", filter.ServiceName,
			"resourceType", filter.ResourceType,
			"hint", "Add service to gcloudServiceMetricsMap or provide explicit MetricNames")
	}

	return providers.QueryMetricsResponse{
		Items: metricItems,
	}, nil
}

func queryMetric(ctx providers.CloudProviderContext, client *monitoring.MetricClient, projectId, metricType string, filter providers.QueryMetricsRequest, startTime, endTime time.Time, step time.Duration, statistic string) ([]providers.MetricItem, error) {
	var metricItems []providers.MetricItem

	// Build the filter string
	filterStr := fmt.Sprintf("metric.type = \"%s\"", metricType)

	// Get resource label key
	resourceLabelKey := getResourceLabelKey(filter.ServiceName, filter.ResourceType)

	// Add region filter first (before resource IDs to avoid AND/OR mixing)
	// NOTE: For Compute Engine, when we have specific instance_ids, we don't need zone filter
	// because instance_ids are globally unique. The zone filter may cause issues if combined
	// with instance_id filter, so we skip it when filtering by specific resource IDs.
	if filter.Region != "" && len(filter.ResourceIds) == 0 {
		// Only apply region/zone filter if we're not filtering by specific resource IDs
		// Different services use different location label names
		locationLabelKey := getLocationLabelKey(filter.ServiceName)

		// For Compute Engine, zone label contains region prefix (us-central1-a, us-central1-b, etc.)
		// Use starts_with for simpler zone filtering (more reliable than regex)
		serviceName := normalizeServiceName(filter.ServiceName)
		if serviceName == "compute engine" && locationLabelKey == "zone" {
			// GCP Monitoring filter syntax: resource.labels.zone = starts_with("us-central1")
			filterStr += fmt.Sprintf(" AND resource.labels.%s = starts_with(\"%s\")", locationLabelKey, filter.Region)
		} else {
			// Use exact match for other services (GCP MQL syntax)
			filterStr += fmt.Sprintf(" AND resource.labels.%s = \"%s\"", locationLabelKey, filter.Region)
		}
	}

	// Add resource filters if resource IDs are specified
	if len(filter.ResourceIds) > 0 && resourceLabelKey != "" {
		// For other services: use instance_id filter
		// Build OR conditions for resource IDs
		// Format: resource.labels.instance_id = one_of("id1", "id2", "id3", ...)
		// This is the proper GCP syntax that avoids AND/OR mixing
		resourceIdList := make([]string, len(filter.ResourceIds))
		for i, id := range filter.ResourceIds {
			resourceIdList[i] = fmt.Sprintf("\"%s\"", strings.ReplaceAll(id, "\"", "\\\""))
		}
		filterStr += fmt.Sprintf(" AND resource.labels.%s = one_of(%s)", resourceLabelKey, strings.Join(resourceIdList, ", "))
	}

	// Determine aggregation (similar to CloudWatch statistics)
	aggregation := &monitoringpb.Aggregation{
		AlignmentPeriod: durationpb.New(step),
	}

	// Map statistic to GCP aggregation methods
	switch statistic {
	case "Average", "average", "avg":
		aggregation.PerSeriesAligner = monitoringpb.Aggregation_ALIGN_MEAN
	case "Sum", "sum":
		aggregation.PerSeriesAligner = monitoringpb.Aggregation_ALIGN_SUM
	case "Maximum", "max":
		aggregation.PerSeriesAligner = monitoringpb.Aggregation_ALIGN_MAX
	case "Minimum", "min":
		aggregation.PerSeriesAligner = monitoringpb.Aggregation_ALIGN_MIN
	case "Count", "count":
		aggregation.PerSeriesAligner = monitoringpb.Aggregation_ALIGN_COUNT
	case "Delta", "delta":
		// ALIGN_DELTA computes the change in value over the alignment period
		// Used for CUMULATIVE metrics (like Query Insights execution_time)
		aggregation.PerSeriesAligner = monitoringpb.Aggregation_ALIGN_DELTA
	default:
		aggregation.PerSeriesAligner = monitoringpb.Aggregation_ALIGN_MEAN
	}

	// Create the request
	req := &monitoringpb.ListTimeSeriesRequest{
		Name:   fmt.Sprintf("projects/%s", projectId),
		Filter: filterStr,
		Interval: &monitoringpb.TimeInterval{
			StartTime: timestamppb.New(startTime),
			EndTime:   timestamppb.New(endTime),
		},
		Aggregation: aggregation,
	}

	// Log the query for debugging
	ctx.GetLogger().Info("gcp:queryMetric executing query",
		"metricType", metricType,
		"filter", filterStr,
		"startTime", startTime,
		"endTime", endTime,
		"step", step,
		"aggregation", aggregation.PerSeriesAligner.String(),
		"region", filter.Region)

	// Execute the query
	it := client.ListTimeSeries(ctx.GetContext(), req)
	for {
		resp, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to iterate time series: %w", err)
		}

		// Extract resource ID from labels
		resourceId := ""
		if val, ok := resp.Resource.Labels["instance_id"]; ok {
			resourceId = val
		} else if val, ok := resp.Resource.Labels["database_id"]; ok {
			resourceId = val
		} else if val, ok := resp.Resource.Labels["bucket_name"]; ok {
			resourceId = val
		}

		// Extract region
		region := ""
		if val, ok := resp.Resource.Labels["region"]; ok {
			region = val
		} else if val, ok := resp.Resource.Labels["location"]; ok {
			region = val
		} else if val, ok := resp.Resource.Labels["zone"]; ok {
			region = val
		}

		// Extract data points
		var values []float64
		var timestamps []time.Time
		for _, point := range resp.Points {
			timestamp := point.Interval.EndTime.AsTime()
			var value float64

			switch v := point.Value.Value.(type) {
			case *monitoringpb.TypedValue_DoubleValue:
				value = v.DoubleValue
			case *monitoringpb.TypedValue_Int64Value:
				value = float64(v.Int64Value)
			case *monitoringpb.TypedValue_BoolValue:
				if v.BoolValue {
					value = 1.0
				} else {
					value = 0.0
				}
			}

			values = append(values, value)
			timestamps = append(timestamps, timestamp)
		}

		// Create metric item with the correct structure
		metricItem := providers.MetricItem{
			Name:        extractMetricName(resp.Metric.Type),
			Statistics:  statistic, // Use the statistic that was passed in
			ResourceId:  resourceId,
			Values:      values,
			Timestamps:  timestamps,
			Region:      region,
			ServiceName: filter.ServiceName,
		}

		metricItems = append(metricItems, metricItem)
	}

	ctx.GetLogger().Info("gcp:queryMetric completed",
		"metricType", metricType,
		"timeSeriesCount", len(metricItems))

	return metricItems, nil
}

// getMetricTypePrefix returns the GCP metric type prefix based on service and resource type
func getMetricTypePrefix(serviceName, resourceType string) string {
	serviceName = normalizeServiceName(serviceName)

	switch serviceName {
	case "compute engine":
		return "compute.googleapis.com/instance"
	case "cloud sql":
		return "cloudsql.googleapis.com/database"
	case "cloud storage":
		return "storage.googleapis.com"
	case "bigquery":
		return "bigquery.googleapis.com"
	case "kubernetes engine":
		return "container.googleapis.com"
	case "cloud functions":
		return "cloudfunctions.googleapis.com"
	case "cloud run":
		return "run.googleapis.com"
	case "cloud pub/sub":
		return "pubsub.googleapis.com"
	case "cloud monitoring":
		return "monitoring.googleapis.com"
	case "networking":
		return "networkmanagement.googleapis.com"
	case "vm manager":
		return "vmmigration.googleapis.com"
	case "vertex ai":
		return "aiplatform.googleapis.com"
	case "gemini api":
		return "gemini.googleapis.com"
	default:
		return serviceName
	}
}

// getResourceLabelKey returns the resource label key used to identify specific resources
func getResourceLabelKey(serviceName, resourceType string) string {
	serviceName = normalizeServiceName(serviceName)

	// Query Insights metrics use a different resource type with different labels
	// Resource type: cloudsql_instance_database (NOT cloudsql_database)
	// Label: resource_id (NOT database_id)
	if resourceType == "cloudsql_instance_database" {
		return "resource_id"
	}

	switch serviceName {
	case "compute engine":
		return "instance_id"
	case "cloud sql":
		return "database_id"
	case "cloud storage":
		return "bucket_name"
	case "bigquery":
		return "dataset_id"
	case "kubernetes engine":
		return "cluster_name"
	case "cloud functions":
		return "function_name"
	case "cloud run":
		return "service_name"
	case "cloud pub/sub":
		return "topic_id"
	case "cloud monitoring":
		return "metric_id"
	case "networking":
		return "network_id"
	case "vm manager":
		return "vm_id"
	case "vertex ai":
		return "model_id"
	case "gemini api":
		return "api_key"
	default:
		return "resource_id"
	}
}

// getLocationLabelKey returns the resource label key used for location/region filtering
// Different GCP services use different label names for location
func getLocationLabelKey(serviceName string) string {
	serviceName = normalizeServiceName(serviceName)

	switch serviceName {
	case "cloud sql":
		return "region" // Cloud SQL uses "region" label
	case "compute engine":
		return "zone" // Compute Engine uses "zone" label
	case "kubernetes engine":
		return "location" // GKE uses "location" label
	case "cloud run":
		return "location" // Cloud Run uses "location" label
	case "cloud functions":
		return "location" // Cloud Functions uses "location" label
	default:
		return "location" // Default to "location"
	}
}

// extractMetricName extracts the short metric name from the full metric type
// e.g., "compute.googleapis.com/instance/cpu/utilization" -> "cpu/utilization"
func extractMetricName(metricType string) string {
	// Find the last part after the service prefix
	// Format: "service.googleapis.com/resource_type/metric/submetric"
	// We want to extract everything after the second slash
	parts := []rune(metricType)
	slashCount := 0
	lastIndex := 0

	for i, char := range parts {
		if char == '/' {
			slashCount++
			if slashCount == 2 { // After "compute.googleapis.com/instance/"
				lastIndex = i + 1
				break
			}
		}
	}

	if lastIndex > 0 && lastIndex < len(parts) {
		return string(parts[lastIndex:])
	}

	return metricType
}

// normalizeServiceName normalizes service names to lowercase for case-insensitive matching
func normalizeServiceName(serviceName string) string {
	// Convert to lowercase to match the service map keys
	// Google Cloud service names are case-insensitive in our implementation
	return strings.ToLower(serviceName)
}

// getKeys returns the keys of a map as a slice (helper for logging)
func getKeys[K comparable, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
