package observability

import (
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/service"
)

// MetricsTask defines a task for querying logs provider.
type MetricsTask struct{}

func (t *MetricsTask) GetName() string {
	return "observability.metrics"
}

// GetDescription returns a brief description of the task.
func (t *MetricsTask) GetDescription() string {
	return "Query Metrics."
}

// GetDisplayName returns a human-readable name for the task.
func (t *MetricsTask) GetDisplayName() string {
	return "Query Metrics"
}

func (t *MetricsTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing MetricsTask", "params", params)
	accountId := taskCtx.GetAccountID()
	// params.account_id (form-selected) wins over the outer Hasura action arg
	// so users can target a different account than the workflow's RBAC scope
	// (e.g. AWS metrics from a k8s-scoped workflow).
	if v, ok := params["account_id"].(string); ok && v != "" {
		accountId = v
	}

	endTime, startTime, err := parseTimeRange(params)
	if err != nil {
		return nil, err
	}
	queries2 := map[string]string{}
	switch queries := params["queries"].(type) {
	case map[string]any:
		for k, v := range queries {
			// Values may arrive as either a plain string (PromQL / aws_cloudwatch
			// JSON already serialized client-side) or as a nested object (ES DSL,
			// or a QueryWhereClause array). Normalize everything downstream
			// consumes to a JSON string so ElasticSaasMetricSource.FetchMetricsQuery
			// can json.Unmarshal it without worrying about the on-wire shape.
			switch val := v.(type) {
			case string:
				queries2[k] = val
			default:
				buf, err := json.Marshal(val)
				if err != nil {
					return nil, fmt.Errorf("metrics: failed to marshal queries[%q]: %w", k, err)
				}
				queries2[k] = string(buf)
			}
		}
	case map[string]string:
		queries2 = queries
	default:
		if params["query"] != nil && params["query"] != "" {
			queries2["query"] = params["query"].(string)
		} else {
			return nil, errors.New("metrices: unsupported queries format or missing")
		}
	}

	accountProviderType := ""
	if val, ok := params["account_provider_type"].(string); ok {
		accountProviderType = val
	}

	metricProvider := ""
	metricProviderSource := ""
	request := map[string]any{}

	switch accountProviderType {
	case accountProviderAWS:
		metricProvider = "aws_cloudwatch"
		metricProviderSource = "user"
		if val, ok := params["service_name"].(string); ok && val != "" {
			request["service_name"] = val
		}
		if val, ok := params["region"].(string); ok && val != "" {
			request["region"] = val
		}
		if val, ok := params["resource_ids"].([]any); ok && len(val) > 0 {
			request["resource_ids"] = val
		}
		if val, ok := params["resource_type"].(string); ok && val != "" {
			request["resource_type"] = val
		}
		if val, ok := params["metric_names"].([]any); ok && len(val) > 0 {
			request["metric_names"] = val
		}
		if val, ok := params["statistics"].([]any); ok && len(val) > 0 {
			request["statistics"] = val
		}
		if val, ok := params["metric_namespace"].(string); ok && val != "" {
			request["metric_namespace"] = val
		}

	case accountProviderAzure:
		metricProvider = "aws_cloudwatch"
		metricProviderSource = "user"
		if val, ok := params["service_name"].(string); ok && val != "" {
			request["service_name"] = val
		}

	case accountProviderGCP:
		return nil, errors.New("GCP cloud accounts are not yet supported by the Query Metrics task")

	case accountProviderK8sES:
		// Kubernetes account with Elasticsearch as the resolved metric provider.
		// The frontend refines account_provider_type from "k8s" to "k8s_es"
		// once get_default_provider returns "es". Route through the ES handler
		// in services-server (ElasticSaasMetricSource.FetchMetricsQuery) — that
		// handler reads the target index from request["metric_name"], so we
		// forward the user-supplied `index` form field under that key. The ES
		// metrics handler does not consume query_type (unlike the logs handler),
		// so it is intentionally not forwarded.
		metricProvider = "ES"
		if val, ok := params["index"].(string); ok && val != "" {
			request["metric_name"] = val
		}

	case accountProviderK8s:
		// Kubernetes cluster account: let services-server resolve the default
		// metric provider (typically prometheus via agent).
		metricProvider = ""
		metricProviderSource = ""

	default:
		// Unknown / empty account_provider_type: let services-server resolve.
		metricProvider = ""
		metricProviderSource = ""
	}

	// Optional user overrides applied after the account-type switch. Non-empty
	// values from the form replace the switch-derived routing, so power users
	// can point a k8s account at a non-default provider (Prometheus / ES /
	// etc.) without editing task code. Empty override = no change.
	if v, _ := params["metric_provider"].(string); v != "" {
		metricProvider = v
	}
	if v, _ := params["metric_provider_source"].(string); v != "" {
		metricProviderSource = v
	}

	requestContext := taskCtx.GetNewRequestContextForAccount(accountId)
	resp, err := service.QueryMetrics(requestContext, service.ObservabilityMetricsQueryRequest{
		AccountId:            accountId,
		EndTime:              endTime.UnixMilli(),
		StartTime:            startTime.UnixMilli(),
		Queries:              queries2,
		MetricProvider:       metricProvider,
		MetricProviderSource: metricProviderSource,
		Request:              request,
	})

	if err != nil {
		return nil, err
	}

	return map[string]any{"metrics": resp.Results}, nil
}

func (t *MetricsTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
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
			"service_name": {
				Type:        types.PropertyTypeString,
				Description: "Cloud service name (e.g. AWS/EC2, AWS/RDS). Required for cloud providers like AWS CloudWatch.",
				Required:    false,
				Order:       3,
				DependsOn:   []string{"account_id", "account_provider_type"},
				VisibleWhen: &types.VisibleWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderAWS},
				},
				OptionsSource: &types.OptionsSource{
					Type:              "cloud_services",
					DependencyMapping: map[string]string{"account_id": "account_id"},
				},
			},
			"region": {
				Type:        types.PropertyTypeString,
				Description: "Cloud region (e.g. us-east-1). Required for AWS CloudWatch.",
				Required:    false,
				Order:       4,
				DependsOn:   []string{"account_id", "service_name", "account_provider_type"},
				VisibleWhen: &types.VisibleWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderAWS},
				},
				OptionsSource: &types.OptionsSource{
					Type:              "cloud_regions",
					DependencyMapping: map[string]string{"account_id": "account_id", "service_name": "service_name"},
				},
			},
			"resource_ids": {
				Type:        types.PropertyTypeArray,
				Description: "List of resource IDs to query.",
				Required:    false,
				SubType:     "string",
				Order:       5,
				DependsOn:   []string{"account_id", "service_name", "region", "account_provider_type"},
				VisibleWhen: &types.VisibleWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderAWS},
				},
				OptionsSource: &types.OptionsSource{
					Type:              "cloud_resources",
					DependencyMapping: map[string]string{"account_id": "account_id", "service_name": "service_name", "region": "region"},
				},
			},
			"resource_type": {
				Type:        types.PropertyTypeString,
				Description: "Resource type (e.g. instance, cluster).",
				Required:    false,
				Order:       6,
				DependsOn:   []string{"account_provider_type"},
				VisibleWhen: &types.VisibleWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderAWS},
				},
			},
			// Optional user overrides, also consumed by the cloud_metrics
			// options_source fetcher ([useOptionsSource.ts:94-104]) to
			// populate the AWS metric_names dropdown. Defaults match the AWS
			// CloudWatch case so the dropdown works out of the box; users can
			// retype either field to target a different provider (e.g.
			// "prometheus" / "agent" for a k8s account with a custom
			// Prometheus server).
			"metric_provider": {
				Type:        types.PropertyTypeString,
				Description: "Optional: metric provider override (e.g. aws_cloudwatch, ES, prometheus). Auto-detected from account if empty.",
				Required:    false,
				Title:       "Metric Provider",
				Order:       7,
			},
			"metric_provider_source": {
				Type:        types.PropertyTypeString,
				Description: "Optional: metric provider source override (e.g. agent, user). Auto-detected from account if empty.",
				Required:    false,
				Title:       "Metric Provider Source",
				Order:       8,
			},
			"metric_names": {
				Type:        types.PropertyTypeArray,
				Description: "List of metric names (e.g. CPUUtilization, NetworkIn).",
				Required:    false,
				SubType:     "string",
				Order:       9,
				DependsOn:   []string{"account_id", "service_name", "metric_provider", "metric_provider_source", "account_provider_type"},
				VisibleWhen: &types.VisibleWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderAWS},
				},
				OptionsSource: &types.OptionsSource{
					Type:              "cloud_metrics",
					DependencyMapping: map[string]string{"account_id": "account_id", "service_name": "service_name", "metric_provider": "metric_provider", "metric_provider_source": "metric_provider_source"},
				},
			},
			"statistics": {
				Type:        types.PropertyTypeArray,
				Description: "Statistics to compute (e.g. Average, Maximum, Sum, Minimum).",
				Required:    false,
				SubType:     "string",
				Order:       10,
				Options:     []string{"Average", "Sum", "Maximum", "Minimum"},
				DependsOn:   []string{"account_provider_type"},
				VisibleWhen: &types.VisibleWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderAWS},
				},
			},
			"metric_namespace": {
				Type:        types.PropertyTypeString,
				Description: "Metric namespace (e.g. AWS/EC2, AWS/RDS).",
				Required:    false,
				Order:       11,
				DependsOn:   []string{"account_provider_type"},
				VisibleWhen: &types.VisibleWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderAWS},
				},
			},
			// Elasticsearch field — visible when the k8s account's default
			// metric provider is ES (frontend refines account_provider_type to
			// k8s_es). The form value is forwarded to services-server under
			// request["metric_name"], which ElasticSaasMetricSource uses as the
			// target ES index. query_type is intentionally omitted: the ES
			// metrics handler does not consume it (unlike the logs path).
			"index": {
				Type:        types.PropertyTypeString,
				Description: "Elasticsearch index pattern (e.g. app-metrics-*).",
				Required:    false,
				Order:       11,
				DependsOn:   []string{"account_id", "account_provider_type"},
				VisibleWhen: &types.VisibleWhen{
					Field: "account_provider_type",
					Value: []string{accountProviderK8sES},
				},
			},
			"queries": {
				Type:        types.PropertyTypeObject,
				Description: `Metrics Queries {"query1": "query", "query2": "query"}`,
				Required:    true,
				Order:       12,
			},
			"duration": {
				Type:        types.PropertyTypeString,
				Description: "Relative lookback window. Overrides start_time/end_time when set.",
				Required:    false,
				Title:       "Relative Range",
				Default:     "1h",
				Options:     []string{"5m", "15m", "30m", "1h", "3h", "6h", "12h", "24h"},
				Order:       13,
			},
			"start_time": {
				Type:        types.PropertyTypeTimestamp,
				Description: "Absolute start time. Ignored if duration is set.",
				Required:    false,
				Title:       "Start Time",
				Order:       14,
			},
			"end_time": {
				Type:        types.PropertyTypeTimestamp,
				Description: "Absolute end time. Ignored if duration is set.",
				Required:    false,
				Title:       "End Time",
				Order:       15,
			},
		},
	}
}

func (t *MetricsTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"metrics": {
				Type:        types.PropertyTypeArray,
				Description: "The output of Metrics Query.",
				Required:    true,
			},
			"metadata": {
				Type:        types.PropertyTypeObject,
				Description: "Metadata for Metrics Query.",
			},
		},
	}
}
