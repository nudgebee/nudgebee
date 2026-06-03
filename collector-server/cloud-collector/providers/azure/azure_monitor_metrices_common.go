package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/azquery"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

// getAzureMonitorMetrics is a common function to query metrics from Azure Monitor
// Similar to getAwsCloudwatchMetrics for AWS services
// azureServiceMetricsMap defines the default metrics to collect for each Azure service
var azureServiceMetricsMap = map[string]map[string][]string{
	"microsoft.compute/virtualmachines": {
		"virtualmachine": {
			"Percentage CPU",
			"Network In Total",
			"Network Out Total",
			"Disk Read Bytes",
			"Disk Write Bytes",
			"Disk Read Operations/Sec",
			"Disk Write Operations/Sec",
			"Available Memory Bytes",
		},
		"virtualmachines": { // Support plural form used in database
			"Percentage CPU",
			"Network In Total",
			"Network Out Total",
			"Disk Read Bytes",
			"Disk Write Bytes",
			"Disk Read Operations/Sec",
			"Disk Write Operations/Sec",
			"Available Memory Bytes",
		},
	},
	// SQL Servers and Databases
	// Note: The service name in database is "microsoft.sql/servers" but database resources
	// have full type "microsoft.sql/servers/databases", so we support both
	"microsoft.sql/servers": {
		"databases": {
			"cpu_percent",
			"physical_data_read_percent",
			"log_write_percent",
			"storage_percent",
			"connection_successful",
			"connection_failed",
			"blocked_by_firewall",
		},
	},
	"microsoft.sql/servers/databases": {
		"database": {
			"cpu_percent",
			"physical_data_read_percent",
			"log_write_percent",
			"storage_percent",
			"connection_successful",
			"connection_failed",
			"blocked_by_firewall",
		},
		"databases": {
			"cpu_percent",
			"physical_data_read_percent",
			"log_write_percent",
			"storage_percent",
			"connection_successful",
			"connection_failed",
			"blocked_by_firewall",
		},
	},
	// SQL Managed Instances and their managed databases
	"microsoft.sql/managedinstances": {
		"managedinstances": {
			"avg_cpu_percent",
			"io_requests",
			"io_bytes_read",
			"io_bytes_written",
			"reserved_storage_mb",
			"storage_space_used_mb",
			"virtual_core_count",
		},
		"databases": {
			"storage_space_used_mb",
		},
	},
	"microsoft.sql/managedinstances/databases": {
		"databases": {
			"storage_space_used_mb",
		},
	},
	// Storage Accounts (Blob)
	"microsoft.storage/storageaccounts": {
		"storageaccounts": {
			"UsedCapacity",
			"Transactions",
			"Ingress",
			"Egress",
			"Availability",
		},
	},
	// Add more Azure services as needed
}

// azureMetricsStatsMap defines the default statistics for each metric
var azureMetricsStatsMap = map[string][]string{
	"Percentage CPU":             {"Average"},
	"Network In Total":           {"Total"},
	"Network Out Total":          {"Total"},
	"Disk Read Bytes":            {"Total"},
	"Disk Write Bytes":           {"Total"},
	"Disk Read Operations/Sec":   {"Average"},
	"Disk Write Operations/Sec":  {"Average"},
	"Available Memory Bytes":     {"Average"},
	"cpu_percent":                {"Maximum"},
	"physical_data_read_percent": {"Maximum"},
	"log_write_percent":          {"Maximum"},
	"storage_percent":            {"Maximum"},
	"connection_successful":      {"Total"},
	"connection_failed":          {"Total"},
	"blocked_by_firewall":        {"Total"},
	"avg_cpu_percent":            {"Average"},
	"io_requests":                {"Total"},
	"io_bytes_read":              {"Total"},
	"io_bytes_written":           {"Total"},
	"reserved_storage_mb":        {"Average"},
	"storage_space_used_mb":      {"Average"},
	"virtual_core_count":         {"Average"},
	"UsedCapacity":               {"Average"},
	"Transactions":               {"Total"},
	"Ingress":                    {"Total"},
	"Egress":                     {"Total"},
	"Availability":               {"Average"},
}

// listAzureMonitorMetricsDynamic calls Azure Monitor MetricDefinitions API for a specific resource
// to discover available metrics dynamically. Requires a resource ID.
func listAzureMonitorMetricsDynamic(ctx providers.CloudProviderContext, account providers.Account, resourceId string) (providers.ListMetricsResponse, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ListMetricsResponse{}, err
	}
	client, err := armmonitor.NewMetricDefinitionsClient(session.SubscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ListMetricsResponse{}, fmt.Errorf("failed to create MetricDefinitionsClient: %w", err)
	}

	metricSet := make(map[string]providers.AvailableMetric)
	pager := client.NewListPager(resourceId, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx.GetContext())
		if err != nil {
			return providers.ListMetricsResponse{}, err
		}
		for _, def := range page.Value {
			if def.Name != nil && def.Name.Value != nil {
				name := *def.Name.Value
				namespace := ""
				if def.Namespace != nil {
					namespace = *def.Namespace
				}
				var stats []string
				for _, agg := range def.SupportedAggregationTypes {
					if agg != nil {
						stats = append(stats, string(*agg))
					}
				}
				metricSet[name] = providers.AvailableMetric{
					Name:       name,
					Namespace:  namespace,
					Statistics: stats,
				}
			}
		}
	}

	metrics := make([]providers.AvailableMetric, 0, len(metricSet))
	for _, m := range metricSet {
		metrics = append(metrics, m)
	}
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].Name < metrics[j].Name })
	return providers.ListMetricsResponse{Metrics: metrics}, nil
}

func listAzureMonitorMetrics(request providers.ListMetricsRequest) (providers.ListMetricsResponse, error) {
	serviceName := strings.ToLower(request.ServiceName)
	serviceMetrics, ok := azureServiceMetricsMap[serviceName]
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
			Name:      name,
			Namespace: serviceName,
		}
		if stats, ok := azureMetricsStatsMap[name]; ok {
			info.Statistics = stats
		}
		metrics = append(metrics, info)
	}

	return providers.ListMetricsResponse{Metrics: metrics}, nil
}

func getAzureMonitorMetrics(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	logger := ctx.GetLogger()

	logger.Debug("azure:getAzureMonitorMetrics called", "service", filter.ServiceName, "resourceCount", len(filter.ResourceIds))

	// Validate required parameters
	if filter.StartDate == nil || filter.EndDate == nil {
		return providers.QueryMetricsResponse{}, fmt.Errorf("StartDate and EndDate must be provided")
	}

	if len(filter.ResourceIds) == 0 {
		return providers.QueryMetricsResponse{}, fmt.Errorf("ResourceIds must be provided for Azure Monitor metrics query")
	}

	// Get Azure credentials
	session, err := getAzureSessionFromAccount(ctx, account)
	if err != nil {
		return providers.QueryMetricsResponse{}, fmt.Errorf("failed to get azure session: %w", err)
	}

	cred, err := azidentity.NewClientSecretCredential(session.TenantID, session.ClientID, session.ClientSecret, nil)
	if err != nil {
		return providers.QueryMetricsResponse{}, fmt.Errorf("failed to create azure credential: %w", err)
	}

	// Create Azure Monitor Metrics client
	metricsClient, err := azquery.NewMetricsClient(cred, nil)
	if err != nil {
		logger.Error("azure:QueryMetrics failed to create MetricsClient", "error", err)
		return providers.QueryMetricsResponse{}, fmt.Errorf("failed to create MetricsClient: %w", err)
	}

	// Format timespan for Azure Monitor (convert to UTC for API compatibility)
	startUTC := filter.StartDate.UTC()
	endUTC := filter.EndDate.UTC()
	timespanStr := fmt.Sprintf("%s/%s", startUTC.Format(time.RFC3339), endUTC.Format(time.RFC3339))
	timeInterval := azquery.TimeInterval(timespanStr)

	// Azure Storage metrics only support PT1H time grain (not P1D).
	// Clamp the interval to 1 hour for storage services.
	step := filter.Step
	svcLower := strings.ToLower(filter.ServiceName)
	if strings.HasPrefix(svcLower, "microsoft.storage/") && step > time.Hour {
		step = time.Hour
		logger.Debug("azure:QueryMetrics clamped interval for storage service", "service", filter.ServiceName, "interval", "PT1H")
	}

	// Format interval (step) to ISO 8601 duration format
	interval, err := formatInterval(step)
	if err != nil {
		return providers.QueryMetricsResponse{}, fmt.Errorf("invalid step duration: %w", err)
	}

	// Auto-detect metrics if not provided (similar to AWS CloudWatch implementation)
	metricNamesSlice := filter.MetricNames
	metricNamespace := filter.MetricNamespace

	if len(metricNamesSlice) == 0 && filter.ServiceName != "" {
		serviceName := strings.ToLower(filter.ServiceName)
		resourceType := strings.ToLower(filter.ResourceType)

		// If ResourceType is empty, try to infer it from the first resource ID
		if resourceType == "" && len(filter.ResourceIds) > 0 {
			inferredFullType := getResourceType(filter.ResourceIds[0])
			if inferredFullType != "" {
				// Extract just the resource type part (e.g., "virtualmachines" from "microsoft.compute/virtualmachines")
				parts := strings.Split(inferredFullType, "/")
				if len(parts) == 2 {
					resourceType = parts[1]
					// Also update serviceName if it's empty or doesn't match
					if serviceName == "" || serviceName != inferredFullType {
						serviceName = inferredFullType
					}
				} else {
					resourceType = inferredFullType
				}
				logger.Info("azure:getAzureMonitorMetrics inferred resource type from resource ID",
					"resourceID", filter.ResourceIds[0],
					"inferredFullType", inferredFullType,
					"serviceName", serviceName,
					"resourceType", resourceType)
			}
		}

		// Try to get metrics from the service configuration
		if serviceMetrics, ok := azureServiceMetricsMap[serviceName]; ok {
			if resourceType != "" {
				if metrics, ok := serviceMetrics[resourceType]; ok {
					metricNamesSlice = metrics
					logger.Debug("azure:getAzureMonitorMetrics found metrics for resource type",
						"serviceName", serviceName,
						"resourceType", resourceType,
						"metrics", metricNamesSlice)
				} else {
					// Fallback: if specific resource type not found, use first available metrics set
					logger.Warn("azure:getAzureMonitorMetrics resource type not found, using fallback",
						"serviceName", serviceName,
						"resourceType", resourceType,
						"availableTypes", getKeys(serviceMetrics))
					for _, metrics := range serviceMetrics {
						metricNamesSlice = metrics
						logger.Info("azure:getAzureMonitorMetrics using fallback metrics", "metrics", metricNamesSlice)
						break
					}
				}
			} else {
				// If no resource type specified, use the first available metrics set
				for _, metrics := range serviceMetrics {
					metricNamesSlice = metrics
					logger.Debug("azure:getAzureMonitorMetrics no resource type specified, using first available", "metrics", metricNamesSlice)
					break
				}
			}
		}

		if len(metricNamesSlice) == 0 {
			logger.Info("No metrics configured for Azure service", "service", filter.ServiceName, "resourceType", resourceType)
			return providers.QueryMetricsResponse{
				StartDate: *filter.StartDate,
				EndDate:   *filter.EndDate,
				Step:      filter.Step,
				Items:     []providers.MetricItem{},
			}, nil
		}

		logger.Debug("Auto-detected metrics for Azure service", "service", filter.ServiceName, "resourceType", resourceType, "metrics", metricNamesSlice)
	}

	// Auto-detect metric namespace if not provided
	// Azure Monitor requires proper casing for metric namespaces
	if metricNamespace == "" && filter.ServiceName != "" {
		serviceName := strings.ToLower(filter.ServiceName)
		// Map service names to their metric namespaces (must match Azure's casing exactly)
		namespaceMap := map[string]string{
			// Compute
			"microsoft.compute/virtualmachines":         "Microsoft.Compute/virtualMachines",
			"microsoft.compute/virtualmachinescalesets": "Microsoft.Compute/virtualMachineScaleSets",
			"microsoft.compute/disks":                   "Microsoft.Compute/disks",

			// Container Services
			"microsoft.containerservice/managedclusters": "Microsoft.ContainerService/managedClusters",
			"microsoft.app/containerapps":                "Microsoft.App/containerApps",

			// Databases
			"microsoft.sql/servers":                     "Microsoft.Sql/servers",
			"microsoft.sql/servers/databases":           "Microsoft.Sql/servers/databases",
			"microsoft.sql/managedinstances":            "Microsoft.Sql/managedInstances",
			"microsoft.sql/managedinstances/databases":  "Microsoft.Sql/managedInstances/databases",
			"microsoft.dbformariadb/servers":            "Microsoft.DBforMariaDB/servers",
			"microsoft.dbformysql/flexibleservers":      "Microsoft.DBforMySQL/flexibleServers",
			"microsoft.dbforpostgresql/flexibleservers": "Microsoft.DBforPostgreSQL/flexibleServers",
			"microsoft.documentdb/databaseaccounts":     "Microsoft.DocumentDB/databaseAccounts",
			"microsoft.cache/redis":                     "Microsoft.Cache/redis",

			// Storage
			"microsoft.storage/storageaccounts":              "Microsoft.Storage/storageAccounts",
			"microsoft.storage/storageaccounts/fileservices": "Microsoft.Storage/storageAccounts/fileServices",

			// Networking
			"microsoft.network/applicationgateways":  "Microsoft.Network/applicationGateways",
			"microsoft.network/azurefirewalls":       "Microsoft.Network/azureFirewalls",
			"microsoft.network/dnszones":             "Microsoft.Network/dnsZones",
			"microsoft.network/expressroutecircuits": "Microsoft.Network/expressRouteCircuits",
			"microsoft.network/frontdoors":           "Microsoft.Network/frontDoors",
			"microsoft.network/loadbalancers":        "Microsoft.Network/loadBalancers",
			"microsoft.network/publicipaddresses":    "Microsoft.Network/publicIPAddresses",
			"microsoft.network/virtualnetworks":      "Microsoft.Network/virtualNetworks",

			// Web & Functions
			"microsoft.web/sites":           "Microsoft.Web/sites",
			"microsoft.web/sites/functions": "Microsoft.Web/sites/functions",

			// CDN
			"microsoft.cdn/profiles": "Microsoft.Cdn/profiles",

			// Key Vault
			"microsoft.keyvault/vaults": "Microsoft.KeyVault/vaults",
		}
		if ns, ok := namespaceMap[serviceName]; ok {
			metricNamespace = ns
			logger.Debug("Auto-detected metric namespace", "namespace", metricNamespace)
		}
	}

	// Prepare metric names string
	metricNames := strings.Join(metricNamesSlice, ",")
	if metricNames == "" {
		logger.Warn("no metric names provided and unable to auto-detect, Azure will return default metrics")
	}

	// Prepare aggregations (statistics)
	// Auto-detect statistics based on metrics if not provided
	statisticsSlice := filter.Statistics
	if len(statisticsSlice) == 0 && len(metricNamesSlice) > 0 {
		// Use configured statistics for each metric, or default to Maximum
		statsSet := make(map[string]bool)
		for _, metricName := range metricNamesSlice {
			if stats, ok := azureMetricsStatsMap[metricName]; ok {
				for _, stat := range stats {
					statsSet[stat] = true
				}
			} else {
				// Default to Maximum for metrics without specific configuration
				statsSet["Maximum"] = true
			}
		}

		// Convert set to slice
		for stat := range statsSet {
			statisticsSlice = append(statisticsSlice, stat)
		}

		logger.Debug("Auto-detected statistics for metrics", "statistics", statisticsSlice)
	}

	var aggregations []*azquery.AggregationType
	if len(statisticsSlice) > 0 {
		for _, stat := range statisticsSlice {
			aggType := azquery.AggregationType(stat)
			aggregations = append(aggregations, &aggType)
		}
	} else {
		// Default to Average if no statistics specified and unable to auto-detect
		avgType := azquery.AggregationTypeAverage
		aggregations = append(aggregations, &avgType)
	}

	// Build filter string from dimensions
	filterStr, err := buildFilterString(filter.Dimensions)
	if err != nil {
		return providers.QueryMetricsResponse{}, fmt.Errorf("failed to build filter string: %w", err)
	}

	// Prepare response
	finalResp := providers.QueryMetricsResponse{
		StartDate: *filter.StartDate,
		EndDate:   *filter.EndDate,
		Step:      filter.Step,
		Items:     make([]providers.MetricItem, 0),
	}

	// Query metrics for each resource
	var queryErrors []error
	for _, resourceID := range filter.ResourceIds {
		logger.Debug("azure:QueryMetrics building options",
			"resourceID", resourceID,
			"metricNames", metricNames,
			"hasNamespace", metricNamespace != "",
			"namespace", metricNamespace)

		// Build query options
		options := &azquery.MetricsClientQueryResourceOptions{
			Timespan:    &timeInterval,
			Interval:    &interval,
			MetricNames: &metricNames,
			Aggregation: aggregations,
		}

		// Add metric namespace (required by Azure SDK - it sends empty string if not set)
		if metricNamespace != "" {
			logger.Debug("azure:QueryMetrics setting namespace", "namespace", metricNamespace)
			options.MetricNamespace = &metricNamespace
		}

		// Add filter if provided
		if filterStr != "" {
			options.Filter = &filterStr
		}

		// Execute query
		resp, err := metricsClient.QueryResource(
			ctx.GetContext(),
			resourceID,
			options,
		)
		if err != nil {
			logger.Error("azure:QueryMetrics failed to query for resource", "resourceID", resourceID, "error", err)
			queryErrors = append(queryErrors, fmt.Errorf("resourceID %s: %w", resourceID, err))
			continue
		}

		// Process metrics from response
		for _, sdkMetric := range resp.Value {
			if sdkMetric.Name == nil || sdkMetric.Name.Value == nil {
				continue
			}

			metricName := *sdkMetric.Name.Value

			// Process each requested statistic
			for _, statName := range statisticsSlice {
				item := providers.MetricItem{
					Name:        metricName,
					Statistics:  statName,
					ResourceId:  resourceID,
					Region:      filter.Region,
					ServiceName: filter.ServiceName,
					Values:      []float64{},
					Timestamps:  []time.Time{},
				}

				// Extract values from time series data
				for _, ts := range sdkMetric.TimeSeries {
					for _, data := range ts.Data {
						var value *float64

						// Map statistic type to value
						switch azquery.AggregationType(statName) {
						case azquery.AggregationTypeAverage:
							value = data.Average
						case azquery.AggregationTypeMaximum:
							value = data.Maximum
						case azquery.AggregationTypeMinimum:
							value = data.Minimum
						case azquery.AggregationTypeTotal:
							value = data.Total
						case azquery.AggregationTypeCount:
							value = data.Count
						default:
							logger.Warn("azure:QueryMetrics: unknown or unhandled statistic type", "statistic", statName)
							continue
						}

						// Append value and timestamp if value exists
						if value != nil && data.TimeStamp != nil {
							item.Values = append(item.Values, *value)
							item.Timestamps = append(item.Timestamps, *data.TimeStamp)
						}
					}
				}

				// Only add metric item if it has values
				if len(item.Values) > 0 {
					finalResp.Items = append(finalResp.Items, item)
				}
			}
		}
	}

	// Return error if all queries failed
	if len(queryErrors) > 0 {
		if len(finalResp.Items) == 0 {
			// All queries failed
			var errorStrings []string
			for _, e := range queryErrors {
				errorStrings = append(errorStrings, e.Error())
			}
			return providers.QueryMetricsResponse{}, fmt.Errorf("failed to query metrics for all resources: [%s]", strings.Join(errorStrings, "; "))
		}
		// Some queries failed but we got some results
		logger.Warn("some metric queries failed", "errorCount", len(queryErrors), "successCount", len(finalResp.Items))
	}

	return finalResp, nil
}

// Note: formatInterval and buildFilterString functions are already defined in main.go
// and are reused by this common metrics function
