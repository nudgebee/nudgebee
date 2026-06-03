package observability

import (
	"errors"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/service"
)

// LogGroupsTask defines a task for listing log groups (aggregated
// error/warning patterns). It mirrors LogsTask's provider routing — the
// downstream `log_group` action (handler: /rpc/logs-group)
// dispatches per (LogProvider, LogProviderSource) to each source's
// QueryLogGroup implementation.
type LogGroupsTask struct{}

func (t *LogGroupsTask) GetName() string {
	return "observability.log_groups"
}

func (t *LogGroupsTask) GetDescription() string {
	return "List Log Groups."
}

func (t *LogGroupsTask) GetDisplayName() string {
	return "List Log Groups"
}

func (t *LogGroupsTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing LogGroupsTask", "params", params)

	accountId := taskCtx.GetAccountID()

	endTime, startTime, err := parseTimeRange(params)
	if err != nil {
		return nil, err
	}

	accountProviderType := ""
	if val, ok := params["account_provider_type"].(string); ok {
		accountProviderType = val
	}

	logProvider := ""
	logProviderSource := ""
	request := map[string]any{}

	// Mirror LogsTask.Execute provider-routing. See logs_task.go for rationale.
	switch accountProviderType {
	case accountProviderAWS:
		logProvider = "aws_cloudwatch"
		logProviderSource = "user"
		if val, ok := params["region"].(string); ok && val != "" {
			request["region"] = val
		}

	case accountProviderAzure:
		logProvider = "aws_cloudwatch"
		logProviderSource = "user"
		if val, ok := params["log_analytics_workspace"].(string); ok && val != "" {
			resourceID, err := resolveAzureWorkspaceResourceID(taskCtx, accountId, val)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve Azure workspace resource ID: %w", err)
			}
			request["resource_id"] = resourceID
		}
		request["service_name"] = "azure_sql"

	case accountProviderGCP:
		return nil, errors.New("GCP cloud accounts are not yet supported by the List Log Groups task")

	case accountProviderK8sES:
		logProvider = "ES"
		if val, ok := params["index"].(string); ok && val != "" {
			request["index"] = val
		}
		if val, ok := params["query_type"].(string); ok && val != "" {
			request["query_type"] = val
		}

	case accountProviderK8s:
		logProvider = ""
		logProviderSource = ""

	default:
		logProvider = ""
		logProviderSource = ""
	}

	// Namespace / workload filters are consumed by Loki, Signoz SaaS, ES,
	// and ES SaaS sources via the Request map keys `selectedNamespace` /
	// `selectedWorkload`. Wire them from friendlier input field names.
	if val, ok := params["namespace"].(string); ok && val != "" {
		request["selectedNamespace"] = val
	}
	if val, ok := params["workload"].(string); ok && val != "" {
		request["selectedWorkload"] = val
	}

	// Optional user overrides (same semantics as LogsTask).
	if v, ok := params["log_provider"].(string); ok && v != "" {
		logProvider = v
	}
	if v, ok := params["log_provider_source"].(string); ok && v != "" {
		logProviderSource = v
	}

	requestContext := taskCtx.GetNewRequestContextForAccount(accountId)
	resp, err := service.FetchLogGroup(requestContext, service.ObservabilityLogGroupQueryRequest{
		AccountId:         accountId,
		LogProvider:       logProvider,
		LogProviderSource: logProviderSource,
		Request:           request,
		StartTime:         startTime.UnixMilli(),
		EndTime:           endTime.UnixMilli(),
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{"groups": resp.Groups}, nil
}

func (t *LogGroupsTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"account_id": {
				Type:        types.PropertyTypeAccount,
				Description: "NB Account Id",
				Required:    false,
				Order:       1,
			},
			// Synthetic: frontend derives from selected account's cloud_provider.
			"account_provider_type": {
				Type:        types.PropertyTypeString,
				Description: "Internal: derived cloud provider type for the selected account.",
				Required:    false,
				Hidden:      true,
				Order:       2,
			},
			"region": {
				Type:        types.PropertyTypeString,
				Description: "Cloud region (e.g. us-east-1).",
				Required:    false,
				Order:       3,
				DependsOn:   []string{"account_id", "account_provider_type"},
				VisibleWhen: &types.VisibleWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderAWS},
				},
				OptionsSource: &types.OptionsSource{
					Type:              "cloud_regions",
					DependencyMapping: map[string]string{"account_id": "account_id"},
				},
			},
			"log_analytics_workspace": {
				Type:        types.PropertyTypeString,
				Description: "Azure Log Analytics workspace.",
				Required:    false,
				Order:       4,
				DependsOn:   []string{"account_id", "account_provider_type"},
				VisibleWhen: &types.VisibleWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderAzure},
				},
				RequiredWhen: &types.RequiredWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderAzure},
				},
				OptionsSource: &types.OptionsSource{
					Type:              "azure_log_analytics_workspaces",
					DependencyMapping: map[string]string{"account_id": "account_id"},
				},
			},
			"index": {
				Type:        types.PropertyTypeString,
				Description: "Elasticsearch index pattern (e.g. app-logs-*).",
				Required:    false,
				Order:       5,
				DependsOn:   []string{"account_id", "account_provider_type"},
				VisibleWhen: &types.VisibleWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderK8sES},
				},
			},
			"query_type": {
				Type:        types.PropertyTypeString,
				Description: "Elasticsearch query type.",
				Required:    false,
				Options:     []string{"dsl"},
				Default:     "dsl",
				Order:       6,
				Hidden:      true,
				DependsOn:   []string{"account_id", "account_provider_type"},
				VisibleWhen: &types.VisibleWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderK8sES},
				},
			},
			"log_provider": {
				Type:        types.PropertyTypeString,
				Description: "Optional: log provider override (e.g. aws_cloudwatch, ES, loki, signoz). Auto-detected from account if empty.",
				Required:    false,
				Title:       "Log Provider",
				Order:       7,
			},
			"log_provider_source": {
				Type:        types.PropertyTypeString,
				Description: "Optional: log provider source override (e.g. agent, user). Auto-detected from account if empty.",
				Required:    false,
				Title:       "Log Provider Source",
				Order:       8,
			},
			// k8s-only filters — forwarded to providers (Loki, Signoz, ES)
			// as selectedNamespace / selectedWorkload in the request map.
			"namespace": {
				Type:        types.PropertyTypeString,
				Description: "Kubernetes namespace filter (e.g. nudgebee). Applied by Loki / Signoz / ES providers.",
				Required:    false,
				Title:       "Namespace",
				Order:       9,
				DependsOn:   []string{"account_provider_type"},
				VisibleWhen: &types.VisibleWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderK8s, accountProviderK8sES},
				},
			},
			"workload": {
				Type:        types.PropertyTypeString,
				Description: "Workload name filter (e.g. services-server). Matches pods beginning with this prefix.",
				Required:    false,
				Title:       "Workload",
				Order:       10,
				DependsOn:   []string{"account_provider_type"},
				VisibleWhen: &types.VisibleWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderK8s, accountProviderK8sES},
				},
			},
			"duration": {
				Type:        types.PropertyTypeString,
				Description: "Relative lookback window. Overrides start_time/end_time when set.",
				Required:    false,
				Title:       "Relative Range",
				Default:     "1h",
				Options:     []string{"5m", "15m", "30m", "1h", "3h", "6h", "12h", "24h"},
				Order:       11,
			},
			"start_time": {
				Type:        types.PropertyTypeTimestamp,
				Description: "Absolute start time. Ignored if duration is set.",
				Required:    false,
				Title:       "Start Time",
				Order:       12,
			},
			"end_time": {
				Type:        types.PropertyTypeTimestamp,
				Description: "Absolute end time. Ignored if duration is set.",
				Required:    false,
				Title:       "End Time",
				Order:       13,
			},
		},
	}
}

func (t *LogGroupsTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"groups": {
				Type:        types.PropertyTypeArray,
				Description: "Aggregated log groups (error/warning patterns) found in the time window.",
				Required:    true,
			},
		},
	}
}
