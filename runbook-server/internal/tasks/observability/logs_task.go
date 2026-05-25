package observability

import (
	"errors"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/service"
	"strconv"
	"strings"
)

// LogsTask defines a task for querying logs provider.
type LogsTask struct{}

func (t *LogsTask) GetName() string {
	return "observability.logs"
}

// GetDescription returns a brief description of the task.
func (t *LogsTask) GetDescription() string {
	return "Query Logs."
}

// GetDisplayName returns a human-readable name for the task.
func (t *LogsTask) GetDisplayName() string {
	return "Query Logs"
}

// Cloud-account routing constants. The frontend writes the cloud provider type
// of the selected account into the synthetic `account_provider_type` form field
// (see ActionDetailsSidebar handleDataChange). When that value is one of the
// cloud-account types below, we route through the cloud-collector log path
// (provider = aws_cloudwatch + source = user) — matching what
// CloudLogsViewer.tsx does for the in-app cloud logs viewer.
const (
	accountProviderAWS   = "aws"
	accountProviderAzure = "azure"
	accountProviderGCP   = "gcp"
	accountProviderK8s   = "k8s"
	accountProviderK8sES = "k8s_es"
)

func (t *LogsTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing LogsTask", "params", params)
	if params["query"] == nil || params["query"] == "" {
		return nil, errors.New("query is requried")
	}

	accountId := taskCtx.GetAccountID()

	endTime, startTime, err := parseTimeRange(params)
	if err != nil {
		return nil, err
	}

	limit := 1000
	if params["limit"] != nil {
		switch t := params["limit"].(type) {
		case int:
			limit = t
		case int64:
			limit = int(t)
		case float32:
			limit = int(t)
		case float64:
			limit = int(t)
		case string:
			limit1, err := strconv.Atoi(t)
			if err != nil {
				return nil, errors.New("unable to parse limmit - " + t)
			}
			limit = limit1
		}
	}

	accountProviderType := ""
	if val, ok := params["account_provider_type"].(string); ok {
		accountProviderType = val
	}

	logProvider := ""
	logProviderSource := ""
	request := map[string]any{}

	switch accountProviderType {
	case accountProviderAWS:
		// Cloud-collector path. Region + log_group come from the form fields.
		logProvider = "aws_cloudwatch"
		logProviderSource = "user"
		if val, ok := params["region"].(string); ok && val != "" {
			request["region"] = val
		}
		if val, ok := params["log_group"].(string); ok && val != "" {
			request["log_group"] = val
		}

	case accountProviderAzure:
		// Cloud-collector path. The Azure Log Analytics workspace ARM resource ID
		// rides on request.resource_id — the cloud collector dispatches by
		// substring-matching `microsoft.operationalinsights/workspaces` on it
		// (see api-server/services/cloud/actions.go). service_name=azure_sql is
		// what CloudLogsViewer.tsx sends for Azure cloud accounts.
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
		// TODO: GCP support is not yet implemented for the workflow logs task.
		// The cloud-collector path requires resource_id + service_name (cloud sql)
		// and there is no UI selector for GCP resources here yet. Fail fast with
		// a clear error rather than letting services-server return a vague one
		// further down the stack. Tracked in #28180.
		return nil, errors.New("GCP cloud accounts are not yet supported by the Query Logs task")

	case accountProviderK8sES:
		// Kubernetes account with Elasticsearch as the resolved log provider.
		// The frontend refines account_provider_type from "k8s" to "k8s_es"
		// once get_default_provider returns "es". We set logProvider = "ES" so
		// services-server routes through the ES handler, and pass through
		// the user-specified index and query_type.
		logProvider = "ES"
		if val, ok := params["index"].(string); ok && val != "" {
			request["index"] = val
		}
		if val, ok := params["query_type"].(string); ok && val != "" {
			request["query_type"] = val
		}

	case accountProviderK8s:
		// Kubernetes cluster account: let services-server resolve the default
		// log provider via the agent's LogsConnectionProvider feature
		// (Loki / Signoz / ES / etc. depending on what the in-cluster agent reports).
		logProvider = ""
		logProviderSource = ""

	default:
		// Unknown / empty account_provider_type: let services-server resolve.
		logProvider = ""
		logProviderSource = ""
	}

	// Optional user overrides. When the caller sets log_provider /
	// log_provider_source explicitly, honour them after the account-type
	// switch so power users can override the default routing. An empty
	// override means "don't change" — the switch-derived value wins, which
	// for k8s accounts ends up at "" / "" so services-server auto-resolves
	// from agent features.
	if v, ok := params["log_provider"].(string); ok && v != "" {
		logProvider = v
	}
	if v, ok := params["log_provider_source"].(string); ok && v != "" {
		logProviderSource = v
	}

	requestContext := taskCtx.GetNewRequestContextForAccount(accountId)
	resp, err := service.QueryLogs(requestContext, service.ObservabilityLogQueryRequest{
		AccountId:         accountId,
		Query:             params["query"].(string),
		EndTime:           endTime.UnixMilli(),
		StartTime:         startTime.UnixMilli(),
		Limit:             limit,
		LogProvider:       logProvider,
		LogProviderSource: logProviderSource,
		Request:           request,
	})

	if err != nil {
		return nil, err
	}

	return map[string]any{"logs": resp.Logs, "metadata": resp.Metadata}, nil
}

func (t *LogsTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			// Account and provider-specific fields render first so the user
			// picks the data source before composing the query.
			"account_id": {
				Type:        types.PropertyTypeAccount,
				Description: "NB Account Id",
				Required:    false,
				Order:       1,
			},
			// Synthetic field. The frontend (ActionDetailsSidebar handleDataChange)
			// derives this from the selected account's cloud_provider / account_type
			// when the user picks an account, and uses it to drive VisibleWhen on
			// the provider-specific fields below. Hidden from the rendered form.
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
			"log_group": {
				Type:        types.PropertyTypeString,
				Description: "Log group name.",
				Required:    false,
				Order:       4,
				DependsOn:   []string{"account_id", "region", "account_provider_type"},
				VisibleWhen: &types.VisibleWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderAWS},
				},
				RequiredWhen: &types.RequiredWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderAWS},
				},
				OptionsSource: &types.OptionsSource{
					Type:              "cloud_log_groups",
					DependencyMapping: map[string]string{"account_id": "account_id", "region": "region"},
				},
			},
			"log_analytics_workspace": {
				Type:        types.PropertyTypeString,
				Description: "Azure Log Analytics workspace.",
				Required:    false,
				Order:       5,
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
			// Elasticsearch fields — visible when the k8s account's default
			// log provider is ES (frontend refines account_provider_type to k8s_es).
			"index": {
				Type:        types.PropertyTypeString,
				Description: "Elasticsearch index pattern (e.g. app-logs-*).",
				Required:    false,
				Order:       6,
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
				Order:       7,
				Hidden:      true,
				DependsOn:   []string{"account_id", "account_provider_type"},
				VisibleWhen: &types.VisibleWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderK8sES},
				},
			},
			// Optional overrides. Free-text so callers can target any
			// provider value services-server understands — the fixed list
			// of known providers drifts over time. Leave blank to let the
			// account-type switch in Execute (and services-server's agent
			// resolution for k8s accounts) pick the provider.
			"log_provider": {
				Type:        types.PropertyTypeString,
				Description: "Optional: log provider override (e.g. aws_cloudwatch, ES, loki, signoz). Auto-detected from account if empty.",
				Required:    false,
				Title:       "Log Provider",
				Order:       8,
			},
			"log_provider_source": {
				Type:        types.PropertyTypeString,
				Description: "Optional: log provider source override (e.g. agent, user). Auto-detected from account if empty.",
				Required:    false,
				Title:       "Log Provider Source",
				Order:       9,
			},
			"query": {
				Type:        types.PropertyTypeString,
				Description: "Logs Query",
				Required:    true,
				Order:       10,
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
			"limit": {
				Type:        types.PropertyTypeNumber,
				Description: "Number of Records to Pull",
				Required:    false,
				Default:     1000,
				Order:       14,
			},
		},
	}
}

func (t *LogsTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"logs": {
				Type:        types.PropertyTypeArray,
				Description: "The output of Logs Query.",
				Required:    true,
			},
			"metadata": {
				Type:        types.PropertyTypeObject,
				Description: "Metadata for Logs Query.",
				Required:    true,
			},
		},
	}
}

// resolveAzureWorkspaceResourceID resolves the workspace value from the form
// into an ARM resource ID. The frontend options source sends the display label
// (e.g. "kankshittestingloganalytics (eastus)") rather than the ARM resource
// ID. If the value already looks like an ARM resource ID we return it as-is;
// otherwise we strip the optional " (region)" suffix, look up the workspace
// by name in cloud_resourses, and return its resourse_id.
func resolveAzureWorkspaceResourceID(taskCtx types.TaskContext, accountID, value string) (string, error) {
	if strings.Contains(strings.ToLower(value), "microsoft.operationalinsights/workspaces") {
		return value, nil
	}

	// Strip optional " (region)" suffix added by the frontend label format.
	name := value
	if idx := strings.LastIndex(name, " ("); idx != -1 && strings.HasSuffix(name, ")") {
		name = name[:idx]
	}

	requestContext := taskCtx.GetNewRequestContextForAccount(accountID)
	resourceID, err := service.GetCloudResourceField(requestContext, accountID, name, "workspaces", "Active", "resource_id")
	if err != nil {
		return "", fmt.Errorf("lookup workspace %q: %w", name, err)
	}
	if resourceID == "" {
		return "", fmt.Errorf("no active Azure Log Analytics workspace found with name %q for account %s", name, accountID)
	}
	return resourceID, nil
}
