package azure

import (
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/azquery"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/google/shlex"
	"github.com/samber/lo"
	"go.uber.org/multierr"
	"golang.org/x/sync/semaphore"
)

var azureServiceMap map[string]azureService

// defaultAzureProvider is the fully-initialized provider built in init().
// Event Grid constructors must use this instead of `&azureProvider{}` — a bare
// literal has nil services/servicesMap, which makes ListResources return
// ErrUnsupported for every realtime resource lookup.
var defaultAzureProvider *azureProvider

type azureProvider struct {
	services    []azureService
	servicesMap map[string]azureService
}

// azureServiceBase provides default implementations for azureService interface
// Services can embed this to get default behavior and override specific methods
// Deprecated: Services should implement Scope() directly. Kept for backward compatibility.
//
//nolint:unused // Kept for backward compatibility
type azureServiceBase struct{}

// Scope returns the default service scope (regional)
// Global services should override this method
// Deprecated: Services should implement Scope() directly. Kept for backward compatibility.
//
//nolint:unused // Kept for backward compatibility
func (b *azureServiceBase) Scope() ServiceScope {
	return ServiceScopeRegional
}

func getResourceType(resourceID string) string {
	// Split the resource ID by "/"
	parts := strings.Split(strings.Trim(resourceID, "/"), "/")

	// Find the index of "providers"
	for i, part := range parts {
		if strings.EqualFold(part, "providers") && i+2 < len(parts) {
			// Azure resource IDs can have nested resource types like:
			// /providers/microsoft.sql/servers/server-name/databases/db-name
			// We need to extract the full type path: "microsoft.sql/servers/databases"

			// Start with namespace/resourceType (e.g., "microsoft.sql/servers")
			resourceType := parts[i+1] + "/" + parts[i+2]

			// Check if there are more resource type levels (like /databases for SQL)
			// Pattern: after the resource name, check for additional type/name pairs
			// Format: /providers/namespace/type/name/subtype/subname
			// Index:      i      i+1     i+2  i+3   i+4     i+5
			j := i + 4             // Start checking after first resource name (i+3)
			for j < len(parts)-1 { // Ensure we have both type and name
				// Check if this looks like a resource type (not a resource name)
				// Resource names are typically UUIDs, names, or identifiers
				// Resource types are known Azure types like "databases", "providers", etc.
				possibleType := parts[j]

				// Add this to resource type path if it's followed by a name
				// This handles nested resources like servers/databases
				resourceType += "/" + possibleType
				j += 2 // Skip type and name pair
			}

			return strings.ToLower(resourceType)
		}
	}
	return ""
}

func GetAzureService(resourceType string) (azureService, bool) {
	resourceType = strings.ToLower(resourceType)
	if resourceType == "" {
		return nil, false
	}
	service, ok := azureServiceMap[resourceType]
	if ok {
		return service, true
	}
	// For nested resource types like "microsoft.sql/servers/databases",
	// try progressively shorter paths to find a parent service registered
	// as "microsoft.sql/servers". Also handles 2-segment types like
	// "microsoft.insights/components" falling back to "microsoft.insights".
	parts := strings.Split(resourceType, "/")
	for len(parts) >= 2 {
		parts = parts[:len(parts)-1]
		candidate := strings.Join(parts, "/")
		if service, ok := azureServiceMap[candidate]; ok {
			return service, true
		}
	}
	return nil, false
}

func GetQuery(workspacePath string, resourcID string, queryRequest providers.QueryLogsRequest) string {
	parts := strings.Split(workspacePath, "/")
	if len(parts) < 2 {
		return ""
	}
	query := fmt.Sprintf("%s | where _ResourceId == '%s'", parts[1], resourcID)
	if queryRequest.Limit != nil && *queryRequest.Limit > 0 {
		query = fmt.Sprintf("%s | limit %v", query, *queryRequest.Limit)
	}
	return query
}

// buildAzureTimespan returns a pointer to an azquery.TimeInterval covering the
// given window. If either bound is nil, it defaults to the last hour ending at
// the other bound (or now) so the caller doesn't fall back to the workspace
// default retention window.
func buildAzureTimespan(startTime, endTime *time.Time) *azquery.TimeInterval {
	if startTime == nil && endTime == nil {
		return nil
	}
	end := time.Now()
	if endTime != nil {
		end = *endTime
	}
	start := end.Add(-1 * time.Hour)
	if startTime != nil {
		start = *startTime
	}
	interval := azquery.NewTimeInterval(start.UTC(), end.UTC())
	return &interval
}

func QueryAzureLogs(ctx providers.CloudProviderContext, workspaceID, query string, startTime, endTime *time.Time, cred azcore.TokenCredential, account providers.Account) (providers.QueryLogsResponse, error) {
	client, err := azquery.NewLogsClient(cred, nil)
	if err != nil {
		return providers.QueryLogsResponse{}, fmt.Errorf("failed to create LogsClient: %w", err)
	}
	body := azquery.Body{Query: &query}
	if timespan := buildAzureTimespan(startTime, endTime); timespan != nil {
		body.Timespan = timespan
	}
	res := azquery.LogsClientQueryWorkspaceResponse{}
	for i := 0; i < 3; i++ {
		res, err = client.QueryWorkspace(ctx.GetContext(), workspaceID, body, nil)
		if err == nil {
			break
		}
		ctx.GetLogger().Info("azure:QueryLogs retrying query", "attempt", i+1, "error", err)
		time.Sleep(time.Duration(i+1) * time.Second)
	}
	if err != nil {
		return providers.QueryLogsResponse{
			QueryId: fmt.Sprintf("%d", time.Now().UnixNano()),
			Status:  "Failed",
		}, fmt.Errorf("query failed: %w", err)
	}

	response := providers.QueryLogsResponse{
		QueryId:    fmt.Sprintf("%d", time.Now().UnixNano()),
		Status:     "Complete",
		Statistics: providers.LogQueryStatistics{},
	}

	if len(res.Tables) > 0 {
		for _, table := range res.Tables {
			for _, row := range table.Rows {
				msg := providers.LogMessage{}
				var labels []providers.LogLabel

				for i, col := range row {
					if table.Columns[i].Name == nil {
						continue
					}
					colName := *table.Columns[i].Name
					if colName == "" {
						continue
					}
					val := fmt.Sprintf("%v", col)

					switch colName {
					case "Message", "message", "SyslogMessage":
						msg.Message = val
					case "TimeGenerated", "timestamp":
						if t, err := time.Parse(time.RFC3339Nano, val); err == nil {
							msg.Timestamp = t.UnixMilli()
						}
					default:
						labels = append(labels, providers.LogLabel{
							Label: colName,
							Value: val,
						})
					}
				}

				msg.Labels = labels
				response.Results = append(response.Results, msg)
			}
		}
		response.Statistics.RecordsMatched = float64(len(response.Results))
	}

	return response, nil
}

// QueryAzureLogsByResource queries logs in the context of an Azure resource (VM, AKS, etc.)
// using the resource-scoped query API instead of requiring a Log Analytics workspace ID.
func QueryAzureLogsByResource(ctx providers.CloudProviderContext, resourceID, query string, startTime, endTime *time.Time, cred azcore.TokenCredential, account providers.Account) (providers.QueryLogsResponse, error) {
	client, err := azquery.NewLogsClient(cred, nil)
	if err != nil {
		return providers.QueryLogsResponse{}, fmt.Errorf("failed to create LogsClient: %w", err)
	}
	body := azquery.Body{Query: &query}
	if timespan := buildAzureTimespan(startTime, endTime); timespan != nil {
		body.Timespan = timespan
	}
	res := azquery.LogsClientQueryResourceResponse{}
	for i := 0; i < 3; i++ {
		res, err = client.QueryResource(ctx.GetContext(), resourceID, body, nil)
		if err == nil {
			break
		}
		ctx.GetLogger().Info("azure:QueryLogsByResource retrying query", "attempt", i+1, "error", err)
		time.Sleep(time.Duration(i+1) * time.Second)
	}
	if err != nil {
		return providers.QueryLogsResponse{
			QueryId: fmt.Sprintf("%d", time.Now().UnixNano()),
			Status:  "Failed",
		}, fmt.Errorf("resource query failed: %w", err)
	}

	response := providers.QueryLogsResponse{
		QueryId:    fmt.Sprintf("%d", time.Now().UnixNano()),
		Status:     "Complete",
		Statistics: providers.LogQueryStatistics{},
	}

	if len(res.Tables) > 0 {
		for _, table := range res.Tables {
			for _, row := range table.Rows {
				msg := providers.LogMessage{}
				var labels []providers.LogLabel

				for i, col := range row {
					if table.Columns[i].Name == nil {
						continue
					}
					colName := *table.Columns[i].Name
					if colName == "" {
						continue
					}
					val := fmt.Sprintf("%v", col)

					switch colName {
					case "Message", "message", "SyslogMessage":
						msg.Message = val
					case "TimeGenerated", "timestamp":
						if t, err := time.Parse(time.RFC3339Nano, val); err == nil {
							msg.Timestamp = t.UnixMilli()
						}
					default:
						labels = append(labels, providers.LogLabel{
							Label: colName,
							Value: val,
						})
					}
				}

				msg.Labels = labels
				response.Results = append(response.Results, msg)
			}
		}
		response.Statistics.RecordsMatched = float64(len(response.Results))
	}

	return response, nil
}

// isWorkspaceResourceId checks if the resource ID points to a Log Analytics workspace
func isWorkspaceResourceId(resourceId string) bool {
	return strings.Contains(strings.ToLower(resourceId), "microsoft.operationalinsights/workspaces")
}

// extractWorkspaceId extracts the workspace GUID from a Log Analytics workspace resource ID
// by looking it up via the Azure API
func extractWorkspaceId(ctx providers.CloudProviderContext, resourceId string, cred *azidentity.ClientSecretCredential) (string, error) {
	subID, err := extractSubscriptionID(resourceId)
	if err != nil {
		return "", fmt.Errorf("failed to extract subscription ID from workspace resource ID: %w", err)
	}

	client, err := armoperationalinsights.NewWorkspacesClient(subID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return "", fmt.Errorf("failed to create operational insights client: %w", err)
	}

	// Extract resource group and workspace name from resource ID
	parts := strings.Split(resourceId, "/")
	var rgName, wsName string
	for i, p := range parts {
		if strings.EqualFold(p, "resourceGroups") && i+1 < len(parts) {
			rgName = parts[i+1]
		}
		if strings.EqualFold(p, "workspaces") && i+1 < len(parts) {
			wsName = parts[i+1]
		}
	}
	if rgName == "" || wsName == "" {
		return "", fmt.Errorf("could not extract resource group or workspace name from resource ID: %s", resourceId)
	}

	workspace, err := client.Get(ctx.GetContext(), rgName, wsName, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get workspace details: %w", err)
	}

	if workspace.Properties == nil || workspace.Properties.CustomerID == nil {
		return "", fmt.Errorf("workspace %s has no customer ID (workspace GUID)", wsName)
	}

	return *workspace.Properties.CustomerID, nil
}

func (a *azureProvider) QueryLogs(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryLogsRequest) (providers.QueryLogsResponse, error) {
	session, err := getAzureSessionFromAccount(ctx, account)
	if err != nil {
		return providers.QueryLogsResponse{}, fmt.Errorf("failed to get azure session: %w", err)
	}

	cred, err := azidentity.NewClientSecretCredential(session.TenantID, session.ClientID, session.ClientSecret, nil)
	if err != nil {
		return providers.QueryLogsResponse{}, fmt.Errorf("failed to create azure credential: %w", err)
	}

	// If the resource_id is a Log Analytics workspace, query it directly
	if query.LogGroupName == "" && isWorkspaceResourceId(query.ResourceId) {
		workspaceId, err := extractWorkspaceId(ctx, query.ResourceId, cred)
		if err != nil {
			return providers.QueryLogsResponse{}, fmt.Errorf("failed to resolve workspace ID: %w", err)
		}

		completeQuery := query.QueryString
		if completeQuery == "" {
			return providers.QueryLogsResponse{}, fmt.Errorf("query string is required when querying a workspace directly")
		}
		if query.Limit != nil && *query.Limit > 0 {
			completeQuery = fmt.Sprintf("%s | limit %v", completeQuery, *query.Limit)
		}

		ctx.GetLogger().Info("azure:QueryLogs querying workspace directly",
			"workspaceId", workspaceId, "query", completeQuery)

		return QueryAzureLogs(ctx, workspaceId, completeQuery, query.StartTime, query.EndTime, cred, account)
	}

	// If QueryString is provided with a non-workspace resource ID, try resource-scoped query.
	// Azure supports querying logs directly from resources (VMs, AKS, etc.) without
	// needing to resolve a workspace via diagnostic settings.
	if query.LogGroupName == "" && query.ResourceId != "" && query.QueryString != "" {
		completeQuery := query.QueryString
		if query.Limit != nil && *query.Limit > 0 {
			completeQuery = fmt.Sprintf("%s | limit %v", completeQuery, *query.Limit)
		}

		ctx.GetLogger().Info("azure:QueryLogs querying resource directly",
			"resourceId", query.ResourceId, "query", completeQuery)

		resp, err := QueryAzureLogsByResource(ctx, query.ResourceId, completeQuery, query.StartTime, query.EndTime, cred, account)
		if err == nil {
			return resp, nil
		}
		// Return the error rather than falling through to the legacy workspace builder,
		// which treats QueryString as a pipeline suffix and would produce invalid KQL.
		return providers.QueryLogsResponse{}, fmt.Errorf("resource-scoped query failed for %s: %w", query.ResourceId, err)
	}

	LogGroupPath := ""
	resourceType := ""
	if query.LogGroupName == "" {
		if query.ServiceName == "" {
			resourceType = getResourceType(query.ResourceId)
		} else {
			resourceType = strings.ToLower(query.ServiceName)
		}
		service, ok := GetAzureService(resourceType)
		if !ok {
			// Fallback: try resolving resource type from resource ID
			if query.ResourceId != "" {
				inferredType := getResourceType(query.ResourceId)
				if inferredType != "" && inferredType != resourceType {
					ctx.GetLogger().Info("azure:QueryLogs service name not found, falling back to inferred resource type",
						"serviceName", resourceType, "inferredType", inferredType)
					service, ok = GetAzureService(inferredType)
					resourceType = inferredType
				}
			}
			if !ok {
				return providers.QueryLogsResponse{}, fmt.Errorf("unsupported or unknown resource type for logs: %s", resourceType)
			}
		}
		LogGroupPath, err = service.GetLogGroupName(ctx, account, "", query.ResourceId)
		if err != nil || LogGroupPath == "" {
			return providers.QueryLogsResponse{}, fmt.Errorf("failed to get log group name for resource type %s: %w", resourceType, err)
		}
		// Most services return the ARM workspace resource ID from diagnostic settings.
		// Resolve it to the workspace customer GUID and default to AzureDiagnostics table.
		if isWorkspaceResourceId(LogGroupPath) {
			workspaceGUID, wErr := extractWorkspaceId(ctx, LogGroupPath, cred)
			if wErr != nil {
				return providers.QueryLogsResponse{}, fmt.Errorf("failed to resolve workspace GUID for resource type %s: %w", resourceType, wErr)
			}
			LogGroupPath = workspaceGUID + "/AzureDiagnostics"
		}
		query.LogGroupName = strings.Split(LogGroupPath, "/")[0]
		if query.LogGroupName == "" {
			return providers.QueryLogsResponse{}, fmt.Errorf("log group name is empty for resource type %s", resourceType)
		}
	}
	completeQuery := GetQuery(LogGroupPath, query.ResourceId, query)
	if query.QueryString != "" {
		if completeQuery == "" {
			completeQuery = query.QueryString
		} else {
			completeQuery = completeQuery + " | " + query.QueryString
		}
	}
	if completeQuery == "" {
		return providers.QueryLogsResponse{}, fmt.Errorf("query string is empty")
	}
	logResp, err := QueryAzureLogs(ctx, query.LogGroupName, completeQuery, query.StartTime, query.EndTime, cred, account)
	if err != nil {
		return providers.QueryLogsResponse{}, fmt.Errorf("failed to query logs: %w", err)
	}
	return logResp, nil
}
func getKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func getKeysFromServiceMap() []string {
	keys := make([]string, 0, len(azureServiceMetricsMap))
	for k := range azureServiceMetricsMap {
		keys = append(keys, k)
	}
	return keys
}

func formatInterval(step time.Duration) (string, error) {
	totalSeconds := int64(step.Seconds())

	if totalSeconds <= 0 {
		return "", fmt.Errorf("step duration must be positive")
	}

	if totalSeconds%86400 == 0 {
		return fmt.Sprintf("P%dD", totalSeconds/86400), nil
	}
	if totalSeconds%3600 == 0 {
		return fmt.Sprintf("PT%dH", totalSeconds/3600), nil
	}
	if totalSeconds%60 == 0 {
		return fmt.Sprintf("PT%dM", totalSeconds/60), nil
	}

	return "", fmt.Errorf("unsupported step: %v. Must be in full minutes, hours, or days", step)
}

func buildFilterString(dimensions []map[string]string) (string, error) {
	if len(dimensions) == 0 {
		return "", nil
	}

	var orGroups []string
	for _, dimMap := range dimensions {
		var andGroups []string
		for key, val := range dimMap {
			cleanVal := strings.ReplaceAll(val, "'", "''")
			andGroups = append(andGroups, fmt.Sprintf("%s eq '%s'", key, cleanVal))
		}

		if len(andGroups) == 0 {
			continue
		}

		if len(andGroups) > 1 {
			orGroups = append(orGroups, "("+strings.Join(andGroups, " and ")+")")
		} else {
			orGroups = append(orGroups, andGroups[0])
		}
	}

	return strings.Join(orGroups, " or "), nil
}

func (a *azureProvider) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Check if this is a global service to handle region filtering appropriately
	isGlobalService := false
	if query.ServiceName != "" {
		if service, ok := a.servicesMap[strings.ToLower(query.ServiceName)]; ok {
			if service.Scope() == ServiceScopeGlobal {
				isGlobalService = true
				ctx.GetLogger().Info("azure:QueryMetrics detected global service",
					"serviceName", query.ServiceName,
					"region", query.Region)
			}
		}
	}

	session, err := getAzureSessionFromAccount(ctx, account)
	if err != nil {
		return providers.QueryMetricsResponse{}, fmt.Errorf("failed to get azure session: %w", err)
	}

	cred, err := azidentity.NewClientSecretCredential(session.TenantID, session.ClientID, session.ClientSecret, nil)
	if err != nil {
		return providers.QueryMetricsResponse{}, fmt.Errorf("failed to create azure credential: %w", err)
	}

	metricsClient, err := azquery.NewMetricsClient(cred, nil)
	if err != nil {
		ctx.GetLogger().Error("azure:QueryMetrics failed to create MetricsClient", "error", err)
		return providers.QueryMetricsResponse{}, fmt.Errorf("failed to create MetricsClient: %w", err)
	}
	if query.StartDate == nil || query.EndDate == nil {
		return providers.QueryMetricsResponse{}, fmt.Errorf("StartDate and EndDate must be provided")
	}
	// Convert to UTC for Azure Monitor API compatibility
	startUTC := query.StartDate.UTC()
	endUTC := query.EndDate.UTC()
	timespanStr := fmt.Sprintf("%s/%s", startUTC.Format(time.RFC3339), endUTC.Format(time.RFC3339))
	timeInterval := azquery.TimeInterval(timespanStr)

	interval, err := formatInterval(query.Step)
	if err != nil {
		return providers.QueryMetricsResponse{}, fmt.Errorf("invalid step duration: %w", err)
	}

	// Auto-detect metric names if not provided
	metricNamesSlice := query.MetricNames
	if len(metricNamesSlice) == 0 {
		// Auto-detect metrics based on service name and resource type
		serviceKey := strings.ToLower(query.ServiceName)
		resourceTypeKey := strings.ToLower(query.ResourceType)

		// If ResourceType is empty, try to infer it from the first resource ID
		if resourceTypeKey == "" && len(query.ResourceIds) > 0 {
			inferredFullType := getResourceType(query.ResourceIds[0])
			if inferredFullType != "" {
				// Extract just the resource type part (e.g., "virtualmachines" from "microsoft.compute/virtualmachines")
				parts := strings.Split(inferredFullType, "/")
				if len(parts) == 2 {
					resourceTypeKey = parts[1]
					// Also update serviceKey if it's empty or doesn't match
					if serviceKey == "" || serviceKey != inferredFullType {
						serviceKey = inferredFullType
					}
				} else {
					resourceTypeKey = inferredFullType
				}
				ctx.GetLogger().Info("azure:QueryMetrics inferred resource type from resource ID",
					"resourceID", query.ResourceIds[0],
					"inferredFullType", inferredFullType,
					"serviceKey", serviceKey,
					"resourceTypeKey", resourceTypeKey)
			}
		}

		ctx.GetLogger().Info("azure:QueryMetrics attempting auto-detection",
			"service", query.ServiceName,
			"serviceKey", serviceKey,
			"resourceType", query.ResourceType,
			"resourceTypeKey", resourceTypeKey)

		if serviceMetrics, ok := azureServiceMetricsMap[serviceKey]; ok {
			if metrics, ok := serviceMetrics[resourceTypeKey]; ok {
				metricNamesSlice = metrics
				ctx.GetLogger().Info("azure:QueryMetrics auto-detected metrics",
					"service", query.ServiceName,
					"resourceType", resourceTypeKey,
					"metrics", metricNamesSlice)
			} else {
				// Fallback: if specific resource type not found, use first available metrics set
				ctx.GetLogger().Warn("azure:QueryMetrics no metrics found for specific resource type, using fallback",
					"resourceType", resourceTypeKey,
					"availableTypes", getKeys(serviceMetrics))
				for _, metrics := range serviceMetrics {
					metricNamesSlice = metrics
					ctx.GetLogger().Info("azure:QueryMetrics using fallback metrics", "metrics", metricNamesSlice)
					break
				}
			}
		} else {
			ctx.GetLogger().Warn("azure:QueryMetrics no metrics map found for service",
				"serviceKey", serviceKey,
				"availableServices", getKeysFromServiceMap())
		}
	}

	// If no metrics are configured for this service, skip querying entirely.
	// Services like microsoft.security/pricings don't support Azure Monitor metrics.
	if len(metricNamesSlice) == 0 {
		ctx.GetLogger().Debug("azure:QueryMetrics skipping service with no configured metrics",
			"serviceName", query.ServiceName,
			"resourceType", query.ResourceType)
		return providers.QueryMetricsResponse{
			StartDate: *query.StartDate,
			EndDate:   *query.EndDate,
			Step:      query.Step,
			Items:     make([]providers.MetricItem, 0),
		}, nil
	}

	metricNames := strings.Join(metricNamesSlice, ",")

	// Auto-detect statistics if not provided
	statisticsSlice := query.Statistics
	if len(statisticsSlice) == 0 && len(metricNamesSlice) > 0 {
		// Use statistics from azureMetricsStatsMap for each metric
		statsSet := make(map[string]bool)
		for _, metricName := range metricNamesSlice {
			if stats, ok := azureMetricsStatsMap[metricName]; ok {
				for _, stat := range stats {
					statsSet[stat] = true
				}
			} else {
				// Default to Average if not in map (matches AWS behavior)
				statsSet["Average"] = true
			}
		}

		// Convert set to slice
		for stat := range statsSet {
			statisticsSlice = append(statisticsSlice, stat)
		}

		ctx.GetLogger().Info("azure:QueryMetrics auto-detected statistics",
			"metrics", metricNamesSlice,
			"statistics", statisticsSlice)
	}

	var aggregations []*azquery.AggregationType
	for _, stat := range statisticsSlice {
		aggType := azquery.AggregationType(stat)
		aggregations = append(aggregations, &aggType)
	}

	filter, err := buildFilterString(query.Dimensions)
	if err != nil {
		return providers.QueryMetricsResponse{}, fmt.Errorf("failed to build filter string: %w", err)
	}

	finalResp := providers.QueryMetricsResponse{
		StartDate: *query.StartDate,
		EndDate:   *query.EndDate,
		Step:      query.Step,
		Items:     make([]providers.MetricItem, 0),
	}
	var queryErrors []error
	for _, resourceID := range query.ResourceIds {
		options := &azquery.MetricsClientQueryResourceOptions{
			Timespan:    &timeInterval,
			Interval:    &interval,
			MetricNames: &metricNames,
			Aggregation: aggregations,
		}

		ctx.GetLogger().Debug("azure:QueryMetrics sending query",
			"resourceID", resourceID,
			"metricNames", metricNames,
			"aggregations", statisticsSlice,
			"timespan", timespanStr,
			"interval", interval)

		// Only set MetricNamespace if it's not empty (Azure API rejects empty namespace)
		if query.MetricNamespace != "" {
			options.MetricNamespace = &query.MetricNamespace
		}

		if filter != "" {
			options.Filter = &filter
		}
		resp, err := metricsClient.QueryResource(
			ctx.GetContext(),
			resourceID,
			options,
		)
		if err != nil {
			// Log additional context for global services to help with debugging
			if isGlobalService {
				ctx.GetLogger().Error("azure:QueryMetrics failed to query for global service resource",
					"resourceID", resourceID,
					"serviceName", query.ServiceName,
					"scope", "global",
					"error", err)
			} else {
				ctx.GetLogger().Error("azure:QueryMetrics failed to query for resource",
					"resourceID", resourceID,
					"error", err)
			}
			queryErrors = append(queryErrors, fmt.Errorf("resourceID %s: %w", resourceID, err))
			continue
		}
		ctx.GetLogger().Debug("azure:QueryMetrics received response",
			"resourceID", resourceID,
			"metricsCount", len(resp.Value))

		for _, sdkMetric := range resp.Value {
			metricName := *sdkMetric.Name.Value

			ctx.GetLogger().Debug("azure:QueryMetrics processing metric",
				"metricName", metricName,
				"timeSeriesCount", len(sdkMetric.TimeSeries))

			for _, statName := range statisticsSlice {

				item := providers.MetricItem{
					Name:        metricName,
					Statistics:  statName,
					ResourceId:  resourceID,
					Region:      query.Region,
					ServiceName: query.ServiceName,
					Values:      []float64{},
					Timestamps:  []time.Time{},
				}

				dataPointCount := 0
				for _, ts := range sdkMetric.TimeSeries {
					for _, data := range ts.Data {
						dataPointCount++
						var value *float64
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
							ctx.GetLogger().Warn("azure:QueryMetrics: unknown or unhandled statistic type", "statistic", statName)
						}
						if value != nil {
							item.Values = append(item.Values, *value)
							item.Timestamps = append(item.Timestamps, *data.TimeStamp)
						}
					}
				}

				ctx.GetLogger().Debug("azure:QueryMetrics processed metric item",
					"metricName", metricName,
					"statName", statName,
					"dataPointCount", dataPointCount,
					"valuesCount", len(item.Values))

				if len(item.Values) > 0 {
					finalResp.Items = append(finalResp.Items, item)
				}

			}
		}
	}
	if len(queryErrors) > 0 {
		var errorStrings []string
		for _, e := range queryErrors {
			errorStrings = append(errorStrings, e.Error())
		}
		combinedError := fmt.Errorf("failed to query metrics for one or more resources: [%s]", strings.Join(errorStrings, "; "))
		return finalResp, combinedError
	}

	return finalResp, nil
}

func (a *azureProvider) ListMetrics(ctx providers.CloudProviderContext, account providers.Account, request providers.ListMetricsRequest) (providers.ListMetricsResponse, error) {
	cacheKey := "azure:" + account.ID + ":" + request.ServiceName + ":" + request.ResourceId
	if cached := providers.GetCachedMetrics(cacheKey); cached != nil {
		return *cached, nil
	}

	if request.ResourceId != "" {
		resp, err := listAzureMonitorMetricsDynamic(ctx, account, request.ResourceId)
		if err == nil && len(resp.Metrics) > 0 {
			providers.SetCachedMetrics(cacheKey, resp)
			return resp, nil
		}
		ctx.GetLogger().Warn("dynamic Azure ListMetrics failed, falling back to static", "resourceId", request.ResourceId, "error", err)
	}
	resp, err := listAzureMonitorMetrics(request)
	if err == nil {
		providers.SetCachedMetrics(cacheKey, resp)
	}
	return resp, err
}

// getRegions fetches all available Azure regions for the subscription
func (a *azureProvider) getRegions(ctx providers.CloudProviderContext, account providers.Account) ([]string, error) {
	session, err := getAzureSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get azure session: %w", err)
	}

	cred, err := azidentity.NewClientSecretCredential(session.TenantID, session.ClientID, session.ClientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	// Handle multiple subscription IDs
	subscriptionIDs := strings.Split(session.SubscriptionID, ",")
	var allRegions []string
	regionSet := make(map[string]bool) // To deduplicate regions

	client, err := armsubscriptions.NewClient(cred, getAzureAuditOpts(ctx))
	if err != nil {
		ctx.GetLogger().Error("azure:getRegions failed to create subscriptions client", "error", err)
		return nil, fmt.Errorf("failed to create subscriptions client: %w", err)
	}

	for _, subID := range subscriptionIDs {
		subID = strings.TrimSpace(subID)
		if subID == "" {
			continue
		}

		pager := client.NewListLocationsPager(subID, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				ctx.GetLogger().Error("azure:getRegions failed to list locations for one subscription, continuing with others", "error", err, "subscriptionID", subID)
				continue
			}

			for _, location := range page.Value {
				if location != nil && location.Name != nil {
					// Azure location names are lowercase without spaces (e.g., "eastus2")
					regionName := strings.ToLower(strings.ReplaceAll(*location.Name, " ", ""))
					if !regionSet[regionName] {
						regionSet[regionName] = true
						allRegions = append(allRegions, regionName)
					}
				}
			}
		}
	}

	if len(allRegions) == 0 {
		ctx.GetLogger().Warn("azure:getRegions no regions found, using default regions")
		// Fallback to common Azure regions if API fails
		return []string{"eastus", "eastus2", "westus", "westus2", "centralus", "northeurope", "westeurope"}, nil
	}

	ctx.GetLogger().Info("azure:getRegions fetched regions", "count", len(allRegions))
	return allRegions, nil
}

func (a *azureProvider) ListResources(ctx providers.CloudProviderContext, account providers.Account, query providers.ListResourceRequest) (providers.ListResourcesResponse, error) {
	var allResources []providers.Resource
	servicesToCheck := []azureService{}
	if query.ServiceName != "" {
		if s, ok := a.servicesMap[strings.ToLower(query.ServiceName)]; ok {
			servicesToCheck = append(servicesToCheck, s)
		}
	} else {
		servicesToCheck = a.services
	}

	if len(servicesToCheck) == 0 {
		return providers.ListResourcesResponse{
			Items: allResources,
		}, errors.ErrUnsupported
	}

	// Determine regions to query
	regions := query.Regions
	if len(regions) == 0 {
		// No regions specified - check if we have any regional services
		hasRegionalServices := false
		for _, service := range servicesToCheck {
			if service.Scope() != ServiceScopeGlobal {
				hasRegionalServices = true
				break
			}
		}

		// If we have regional services, fetch all regions
		if hasRegionalServices {
			var err error
			regions, err = a.getRegions(ctx, account)
			if err != nil {
				ctx.GetLogger().Warn("azure:ListResources failed to get regions, will only query global services", "error", err)
				regions = []string{} // Empty means we'll only query global services
			}
		}
	}

	// Process each service with bounded concurrency to prevent OOM from unbounded goroutine fan-out
	var wg sync.WaitGroup
	var mu sync.Mutex
	numGoroutines := 0
	for _, service := range servicesToCheck {
		if service.Scope() == ServiceScopeGlobal {
			numGoroutines++
		} else {
			numGoroutines += len(regions) + 1 // +1 for global service
		}
	}
	errChan := make(chan error, numGoroutines)
	sem := semaphore.NewWeighted(10) // limit concurrent service+region fetches

	for _, service := range servicesToCheck {
		if service.Scope() == ServiceScopeGlobal {
			// Global services - query once without region
			if err := sem.Acquire(ctx.GetContext(), 1); err != nil {
				errChan <- fmt.Errorf("failed to acquire semaphore for %s: %w", service.Name(), err)
				continue
			}
			wg.Add(1)
			go func(svc azureService) {
				defer wg.Done()
				defer sem.Release(1)
				defer func() {
					if r := recover(); r != nil {
						ctx.GetLogger().Error("azure:ListResources panic in global service",
							"service", svc.Name(), "panic", r, "stack", string(debug.Stack()))
						errChan <- fmt.Errorf("panic in %s: %v", svc.Name(), r)
					}
				}()
				ctx.GetLogger().Info("azure:ListResources fetching global resources", "service", svc.Name())

				resources, err := svc.GetResources(ctx, account, "")
				if err != nil {
					ctx.GetLogger().Error("azure:ListResources failed to get global resources",
						"error", err, "service", svc.Name())
					errChan <- fmt.Errorf("failed to get %s resources: %w", svc.Name(), err)
					return
				}

				mu.Lock()
				allResources = append(allResources, resources...)
				mu.Unlock()
			}(service)
		} else {
			// Regional services - query each region
			for _, region := range regions {
				if err := sem.Acquire(ctx.GetContext(), 1); err != nil {
					errChan <- fmt.Errorf("failed to acquire semaphore for %s (%s): %w", service.Name(), region, err)
					continue
				}
				wg.Add(1)
				go func(svc azureService, reg string) {
					defer wg.Done()
					defer sem.Release(1)
					defer func() {
						if r := recover(); r != nil {
							ctx.GetLogger().Error("azure:ListResources panic in regional service",
								"service", svc.Name(), "region", reg, "panic", r, "stack", string(debug.Stack()))
							errChan <- fmt.Errorf("panic in %s (%s): %v", svc.Name(), reg, r)
						}
					}()
					ctx.GetLogger().Info("azure:ListResources fetching regional resources",
						"service", svc.Name(), "region", reg)

					resources, err := svc.GetResources(ctx, account, reg)
					if err != nil {
						ctx.GetLogger().Error("azure:ListResources failed to get regional resources",
							"error", err, "service", svc.Name(), "region", reg)
						errChan <- fmt.Errorf("failed to get %s resources in %s: %w", svc.Name(), reg, err)
						return
					}

					mu.Lock()
					allResources = append(allResources, resources...)
					mu.Unlock()
				}(service, region)
			}
		}
	}

	wg.Wait()
	close(errChan)

	// Collect any errors
	var allErrors error
	for err := range errChan {
		allErrors = multierr.Append(allErrors, err)
	}

	// Filter by ResourceIds when specified, before normalization to avoid
	// processing resources that will be immediately discarded.
	if len(query.ResourceIds) > 0 {
		idSet := make(map[string]struct{}, len(query.ResourceIds))
		for _, id := range query.ResourceIds {
			idSet[strings.ToLower(id)] = struct{}{}
		}
		allResources = lo.Filter(allResources, func(r providers.Resource, _ int) bool {
			if _, ok := idSet[strings.ToLower(r.Id)]; ok {
				return true
			}
			if _, ok := idSet[strings.ToLower(r.Arn)]; ok {
				return true
			}
			return false
		})
	}

	allResources = lo.Map(allResources, func(r providers.Resource, _ int) providers.Resource {
		r.ServiceName = strings.ToLower(r.ServiceName)
		r.Type = strings.ToLower(r.Type)
		typeSplits := strings.Split(r.Type, "/")
		if len(typeSplits) > 1 {
			r.Type = typeSplits[len(typeSplits)-1]
		}
		r.Id = strings.ToLower(r.Id)
		r.Arn = strings.ToLower(r.Arn)
		return r
	})

	return providers.ListResourcesResponse{
		Items: allResources,
	}, allErrors
}

func (a *azureProvider) GetUsageReport(ctx providers.CloudProviderContext, account providers.Account, month time.Month, year int) (providers.GetUsageReportResponse, error) {
	return getAzureUsageReport(ctx, account, month, year)
}

func (a *azureProvider) ListRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) (providers.ListRecommendationsResponse, error) {
	// Enrich resources with existing metric alert details for alarm recommendation detection
	enrichedResources := enrichResourcesWithAlertDetails(ctx, account, existingResources)

	resourcesByService := make(map[string][]providers.Resource)
	for _, resource := range enrichedResources {
		resourcesByService[resource.ServiceName] = append(resourcesByService[resource.ServiceName], resource)
	}

	var allRecommendations []providers.Recommendation

	// If a specific service is requested and it's registered, call it directly.
	// This handles native/account-level services (e.g., Advisor) that have no entries in cloud_resourses.
	if filter.ServiceName != "" {
		if service, ok := a.servicesMap[strings.ToLower(filter.ServiceName)]; ok {
			resources := resourcesByService[filter.ServiceName]
			recommendations, err := service.GetRecommendations(ctx, account, filter, resources)
			if err != nil {
				return providers.ListRecommendationsResponse{}, fmt.Errorf("failed to get recommendations for service %s: %w", filter.ServiceName, err)
			}
			return providers.ListRecommendationsResponse{Items: recommendations}, nil
		}
	}

	for serviceName, resources := range resourcesByService {
		if service, ok := a.servicesMap[strings.ToLower(serviceName)]; ok {
			recommendations, err := service.GetRecommendations(ctx, account, filter, resources)
			if err != nil {
				return providers.ListRecommendationsResponse{}, fmt.Errorf("failed to get recommendations for service %s: %w", serviceName, err)
			}
			allRecommendations = append(allRecommendations, recommendations...)
		}
	}

	return providers.ListRecommendationsResponse{
		Items: allRecommendations,
	}, nil
}

func (a *azureProvider) ListSupportedRecommendations(ctx providers.CloudProviderContext) []providers.ListSupportedRecommendationsResponse {
	return []providers.ListSupportedRecommendationsResponse{}
}

func (a *azureProvider) ListEvents(ctx providers.CloudProviderContext, account providers.Account, query providers.ListEventRequest) (providers.ListEventResponse, error) {
	var allEvents []providers.Event

	// If no service names specified, fetch all alerts
	if len(query.ServiceNames) == 0 {
		filter := AlertsFilter{
			StartDate:       query.StartDate, // Pass StartDate for time-based filtering
			OnlyFiredAlerts: false,           // Fetch both fired AND resolved to track lifecycle
			// IMPORTANT: We need resolved alerts to update DB when alerts close.
			// With LastModifiedDateTime filtering, we only get alerts that CHANGED,
			// so we need both states to maintain accurate alert status in DB.
		}
		if len(query.ResourceIds) > 0 {
			filter.ResourceIds = query.ResourceIds
		}

		alertEvents, err := getAzureAlerts(ctx, account, filter)
		if err != nil {
			ctx.GetLogger().Error("azure:ListEvents failed to get alerts", "error", err)
			return providers.ListEventResponse{}, fmt.Errorf("failed to get alerts: %w", err)
		}
		allEvents = alertEvents.Items
	} else {
		// Handle multiple service names - fetch alerts for each service
		for _, serviceName := range query.ServiceNames {
			filter := AlertsFilter{
				ServiceName:     serviceName,
				StartDate:       query.StartDate, // Pass StartDate for time-based filtering
				OnlyFiredAlerts: false,           // Fetch both fired AND resolved
			}
			if len(query.ResourceIds) > 0 {
				filter.ResourceIds = query.ResourceIds
			}

			alertEvents, err := getAzureAlerts(ctx, account, filter)
			if err != nil {
				ctx.GetLogger().Error("azure:ListEvents failed to get alerts for service", "error", err, "serviceName", serviceName)
				// Record permission errors that would otherwise be swallowed
				recordProviderPermissionError(ctx, account, serviceName, "ListEvents", err)
				// Continue with other services instead of failing completely
				continue
			}
			allEvents = append(allEvents, alertEvents.Items...)
		}
	}

	// Build summary
	summary := buildEventSummary(allEvents)

	return providers.ListEventResponse{
		Items:   allEvents,
		Summary: summary,
	}, nil
}

// buildEventSummary creates a summary of events grouped by service and region
func buildEventSummary(events []providers.Event) []providers.EventSummary {
	summaryMap := make(map[string]*providers.EventSummary)

	for _, event := range events {
		// Create unique key combining service name and region
		key := event.ResourceServiceName + ":" + event.ResourceRegion

		if summary, exists := summaryMap[key]; exists {
			// Increment counters based on event content
			// Since these are metric alerts, we don't have create/delete/update semantics
			// We'll treat all events as "updates" to the monitoring state
			summary.ResourceUpdated++
		} else {
			// Create new summary entry
			summary := &providers.EventSummary{
				ServiceName:     event.ResourceServiceName,
				Region:          event.ResourceRegion,
				ResourceUpdated: 1,
			}
			summaryMap[key] = summary
		}
	}

	var summaries []providers.EventSummary
	for _, summary := range summaryMap {
		summaries = append(summaries, *summary)
	}

	return summaries
}

func (a *azureProvider) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm/metric alert recommendation
	if _, ok := recommendation.Data["alarm_config"]; ok {
		ctx.GetLogger().Info("azure: applying alarm recommendation",
			"rule_name", recommendation.RuleName,
			"resource_id", recommendation.ResourceId)
		return CreateAzureMetricAlertFromRecommendation(ctx, account, recommendation)
	}

	// Check if there's a service-specific implementation
	service, ok := GetAzureService(recommendation.ResourceServiceName)
	if ok {
		return service.ApplyRecommendation(ctx, account, recommendation)
	}

	ctx.GetLogger().Warn("azure: no ApplyRecommendation implementation",
		"service", recommendation.ResourceServiceName,
		"rule_name", recommendation.RuleName)
	return errors.ErrUnsupported
}

func (a *azureProvider) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Get the Azure service implementation
	service, ok := GetAzureService(strings.ToLower(command.ServiceName))
	if !ok {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("Azure service '%s' not found", command.ServiceName),
		}, fmt.Errorf("service not found: %s", command.ServiceName)
	}

	// Call the service's ApplyCommand method
	return service.ApplyCommand(ctx, account, command)
}

var azureBlockedCommands = []string{
	"login",
	"logout",
	"account clear",
	"configure",
}

func (a *azureProvider) ExecuteCliCommand(ctx providers.CloudProviderContext, account providers.Account, command string) (string, error) {
	args := strings.Fields(command)
	if len(args) < 1 || args[0] != "az" {
		return "", errors.New("command must start with 'az'")
	}

	if err := common.ValidateCliCommand(command, azureBlockedCommands); err != nil {
		return "", err
	}

	session, err := getAzureSessionFromAccount(ctx, account)
	if err != nil {
		return "", fmt.Errorf("failed to get access secret: %w", err)
	}

	// Create a temporary directory to isolate az config
	tmpDir, err := os.MkdirTemp("", "azure-cli-exec-")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			ctx.GetLogger().Error("azure: failed to remove temporary directory", "error", err, "dir", tmpDir)
		}
	}()

	cliEnv := []string{"AZURE_CONFIG_DIR=" + tmpDir}

	// Pass through AZURE_EXTENSION_DIR if configured, so extensions pre-installed
	// in the container image are found when AZURE_CONFIG_DIR is overridden.
	if extDir := os.Getenv("AZURE_EXTENSION_DIR"); extDir != "" {
		cliEnv = append(cliEnv, "AZURE_EXTENSION_DIR="+extDir)
	}

	// Login command
	loginCmd := fmt.Sprintf("az login --service-principal -u %s -p %s --tenant %s --output none", session.ClientID, session.ClientSecret, session.TenantID)
	_, stderr, err := common.SecureExecute(ctx.GetContext(), common.SecureCommandOptions{
		Command: loginCmd,
		Env:     cliEnv,
	})
	if err != nil {
		ctx.GetLogger().Error("az CLI login command execution failed", "error", err, "stderr", stderr)
		return "", fmt.Errorf("az CLI login failed: %w", err)
	}

	// Set subscription if provided
	if session.SubscriptionID != "" {
		setSubCmd := fmt.Sprintf("az account set --subscription %s", session.SubscriptionID)
		_, stderr, err := common.SecureExecute(ctx.GetContext(), common.SecureCommandOptions{
			Command: setSubCmd,
			Env:     cliEnv,
		})
		if err != nil {
			ctx.GetLogger().Error("az CLI account set command failed", "error", err, "stderr", stderr)
			return "", fmt.Errorf("az CLI account set failed: %w", err)
		}
	}

	// Install cost management extension if needed
	if strings.Contains(command, "costmanagement") {
		_, stderr, err := common.SecureExecute(ctx.GetContext(), common.SecureCommandOptions{
			Command: "az extension add --name costmanagement",
			Env:     cliEnv,
		})
		if err != nil && !strings.Contains(stderr, "is already installed") {
			ctx.GetLogger().Error("az CLI extension add command failed", "error", err, "stderr", stderr)
			return "", fmt.Errorf("az extension add failed: %w", err)
		}
	}

	// Handle backslash line continuations before parsing
	cleanCommand := strings.ReplaceAll(command, "\\\r\n", " ")
	cleanCommand = strings.ReplaceAll(cleanCommand, "\\\n", " ")

	// Execute the actual user command
	var stdout string
	opts := common.SecureCommandOptions{
		Command: cleanCommand,
		Env:     cliEnv,
	}

	// Determine if the command uses a pipe (pipeline)
	// We use shlex to properly handle quoted strings so that a pipe character inside a quote
	// doesn't trigger pipeline execution.
	usePipeline := false
	execArgs, err := shlex.Split(cleanCommand)
	if err == nil {
		for _, arg := range execArgs {
			if arg == "|" {
				usePipeline = true
				break
			}
		}
	} else {
		// If parsing fails, we fall back to a naive check
		if strings.Contains(cleanCommand, "|") {
			usePipeline = true
		}
	}

	if usePipeline {
		stdout, stderr, err = common.SecureExecutePipeline(ctx.GetContext(), opts)
	} else {
		stdout, stderr, err = common.SecureExecute(ctx.GetContext(), opts)
	}

	if err != nil {
		ctx.GetLogger().Error("az CLI command execution failed", "error", err, "stderr", stderr, "command", command)
		return stdout, fmt.Errorf("az CLI command failed: %w, Stderr: %s", err, stderr)
	}

	return stdout, nil
}

func (a *azureProvider) QueryServiceMap(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryServiceMapRequest) (providers.QueryServiceMapResponse, error) {
	var allServiceMaps []providers.ServiceMapApplication
	for _, resource := range query.Resources {
		if service, ok := a.servicesMap[strings.ToLower(resource.ServiceName)]; ok {
			resources, err := service.GetResources(ctx, account, query.Region)
			if err != nil {
				return providers.QueryServiceMapResponse{}, err
			}
			var fullResource providers.Resource
			for _, r := range resources {
				if strings.EqualFold(r.Id, resource.Resource) {
					fullResource = r
					break
				}
			}
			// If exact match failed, try prefix match for child resources
			// (e.g., VMSS instance .../virtualmachines/1 → parent VMSS)
			if fullResource.Id == "" {
				resLower := strings.ToLower(resource.Resource)
				for _, r := range resources {
					if strings.HasPrefix(resLower, strings.ToLower(r.Id)+"/") {
						fullResource = r
						break
					}
				}
			}
			if fullResource.Id == "" {
				return providers.QueryServiceMapResponse{}, fmt.Errorf("resource %s not found", resource.Resource)
			}
			serviceMap, err := service.GetServiceMap(ctx, account, fullResource)
			if err != nil {
				return providers.QueryServiceMapResponse{}, fmt.Errorf("failed to get service map for resource %s: %w", resource.Resource, err)
			}
			allServiceMaps = append(allServiceMaps, serviceMap)
		}
	}
	return providers.QueryServiceMapResponse{
		Applications: allServiceMaps,
	}, nil
}

func (a *azureProvider) ListEventRules(ctx providers.CloudProviderContext, account providers.Account) (providers.ListEventRules, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		ctx.GetLogger().Error("azure:ListEventRules failed to create azure credential", "error", err)
		return providers.ListEventRules{}, fmt.Errorf("failed to create azure credential: %w", err)
	}

	var allRules []providers.EventRule
	subscriptionIDs := strings.Split(session.SubscriptionID, ",")

	for _, subID := range subscriptionIDs {
		subID = strings.TrimSpace(subID)
		if subID == "" {
			continue
		}

		client, err := armmonitor.NewMetricAlertsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			ctx.GetLogger().Error("azure:ListEventRules failed to create metric alerts client", "error", err, "subscriptionID", subID)
			continue
		}

		pager := client.NewListBySubscriptionPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				ctx.GetLogger().Error("azure:ListEventRules failed to get next page", "error", err, "subscriptionID", subID)
				continue
			}

			for _, alert := range page.Value {
				if alert.Properties == nil || alert.Name == nil {
					continue
				}

				severity := "Medium"
				if alert.Properties.Severity != nil {
					switch *alert.Properties.Severity {
					case 0:
						severity = "Critical"
					case 1:
						severity = "High"
					case 2:
						severity = "Medium"
					case 3:
						severity = "Low"
					case 4:
						severity = "Verbose"
					}
				}

				// Build labels
				labels := map[string]string{
					"subscription_id": subID,
				}
				if alert.Location != nil {
					labels["region"] = *alert.Location
				}
				if alert.Properties.EvaluationFrequency != nil {
					labels["evaluation_frequency"] = *alert.Properties.EvaluationFrequency
				}
				if alert.Properties.WindowSize != nil {
					labels["window_size"] = *alert.Properties.WindowSize
				}

				// Build annotations
				annotations := map[string]string{}
				if alert.Properties.Description != nil {
					annotations["description"] = *alert.Properties.Description
				}
				if alert.ID != nil {
					annotations["alert_id"] = *alert.ID
				}

				// Serialize criteria as expression
				var expr string
				if criteriaBytes, err := json.Marshal(alert.Properties.Criteria); err != nil {
					ctx.GetLogger().Warn("azure:ListEventRules failed to marshal criteria", "error", err, "alertName", *alert.Name)
				} else {
					expr = string(criteriaBytes)
				}

				// Map Azure severity to EventDefinitionSeverity
				eventSeverity := providers.EventDefinitionSeverityWarning
				if severity == "Critical" || severity == "High" {
					eventSeverity = providers.EventDefinitionSeverityCritical
				}

				allRules = append(allRules, providers.EventRule{
					Name:        *alert.Name,
					Summary:     *alert.Name,
					Description: annotations["description"],
					Expr:        expr,
					Source:      "Azure_Monitor_Alert",
					Category:    "Azure Metric Alert",
					Severity:    eventSeverity,
					Labels:      labels,
				})
			}
		}
	}

	// Also fetch scheduled query rules (log-based alerts)
	for _, subID := range subscriptionIDs {
		subID = strings.TrimSpace(subID)
		if subID == "" {
			continue
		}

		sqrClient, err := armmonitor.NewScheduledQueryRulesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			ctx.GetLogger().Error("azure:ListEventRules failed to create scheduled query rules client", "error", err, "subscriptionID", subID)
			continue
		}

		sqrPager := sqrClient.NewListBySubscriptionPager(nil)
		for sqrPager.More() {
			page, err := sqrPager.NextPage(ctx.GetContext())
			if err != nil {
				ctx.GetLogger().Error("azure:ListEventRules failed to get scheduled query rules page", "error", err, "subscriptionID", subID)
				continue
			}

			for _, rule := range page.Value {
				if rule.Properties == nil || rule.Name == nil {
					continue
				}

				severity := "Medium"
				if rule.Properties.Severity != nil {
					switch int32(*rule.Properties.Severity) {
					case 0:
						severity = "Critical"
					case 1:
						severity = "High"
					case 2:
						severity = "Medium"
					case 3:
						severity = "Low"
					case 4:
						severity = "Verbose"
					}
				}

				labels := map[string]string{
					"subscription_id": subID,
					"alert_type":      "scheduled_query_rule",
				}
				if rule.Location != nil {
					labels["region"] = *rule.Location
				}
				if rule.Properties.EvaluationFrequency != nil {
					labels["evaluation_frequency"] = *rule.Properties.EvaluationFrequency
				}
				if rule.Properties.WindowSize != nil {
					labels["window_size"] = *rule.Properties.WindowSize
				}

				annotations := map[string]string{}
				if rule.Properties.Description != nil {
					annotations["description"] = *rule.Properties.Description
				}
				if rule.ID != nil {
					annotations["alert_id"] = *rule.ID
				}

				var expr string
				if rule.Properties.Criteria != nil {
					if criteriaBytes, err := json.Marshal(rule.Properties.Criteria); err != nil {
						ctx.GetLogger().Warn("azure:ListEventRules failed to marshal scheduled query criteria", "error", err, "ruleName", *rule.Name)
					} else {
						expr = string(criteriaBytes)
					}
				}

				eventSeverity := providers.EventDefinitionSeverityWarning
				if severity == "Critical" || severity == "High" {
					eventSeverity = providers.EventDefinitionSeverityCritical
				}

				allRules = append(allRules, providers.EventRule{
					Name:        *rule.Name,
					Summary:     *rule.Name,
					Description: annotations["description"],
					Expr:        expr,
					Source:      "Azure_Monitor_Alert",
					Category:    "Azure Scheduled Query Alert",
					Severity:    eventSeverity,
					Labels:      labels,
				})
			}
		}
	}

	return providers.ListEventRules{
		Items: allRules,
	}, nil
}

func (a *azureProvider) Name() string {
	return "Azure"
}

func init() {
	allServices := []azureService{
		&virtualMachineService{},
		&sqlDatabaseService{},
		&sqlManagedInstanceService{},
		&diskService{},
		&blobStorageService{},
		&virtualMachineScaleSetService{},
		&appAgentsService{},
		&mlWorkspacesService{},
		&publicIpService{},
		&botServicesService{},
		&containerRegistryService{},
		&loadBalancersService{},
		&operationalInsightsService{},
		&metricAlertsService{},
		&scheduledQueryRulesService{},
		&activityLogAlertsService{},
		&appServiceService{},
		&keyVaultService{},
		&storageAccountService{},
		&cosmosDBService{},
		&virtualNetworkService{},
		&functionsService{},
		&aksService{},
		&filesService{},
		&redisService{},
		&mysqlService{},
		&postgresService{},
		&mariadbService{},
		&appGatewayService{},
		&dnsService{},
		&frontDoorService{},
		&expressRouteService{},
		&firewallService{},
		&entraIDService{},
		&defenderService{},
		&sentinelService{},
		&ddosProtectionService{},
		&arcService{},
		&monitorService{},
		&policyService{},
		&devopsService{},
		&logicAppsService{},
		&pipelinesService{},
		&eventGridService{},
		&frontDoorCdnService{},
		&networkSecurityGroupService{},
		&networkInterfaceService{},
		&recoveryServicesVaultService{},
		&appServicePlanService{},
		&networkWatcherService{},
		&sshPublicKeyService{},
		&userAssignedIdentityService{},
		&advisorService{},
	}
	servicesMap := make(map[string]azureService)
	for _, service := range allServices {
		servicesMap[strings.ToLower(service.Name())] = service
	}

	// Wrap all services with permission audit decorator
	for key, svc := range servicesMap {
		servicesMap[key] = &auditedAzureService{inner: svc, serviceName: key}
	}

	defaultAzureProvider = &azureProvider{
		services:    allServices,
		servicesMap: servicesMap,
	}
	providers.RegisterProvider(defaultAzureProvider)
	azureServiceMap = servicesMap
}
