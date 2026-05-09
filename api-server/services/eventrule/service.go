package eventrule

import (
	"bytes"
	stdctx "context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/database"
	"nudgebee/services/observability/alertrule"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/noirbizarre/gonja"
)

// agentToServerActionMap maps agent-side enricher action names to their
// server-side equivalents. Used to skip server auto-actions when the agent
// already provided equivalent evidence.
var agentToServerActionMap = map[string][]string{
	"logs_enricher":             {"k8s_pod_log_enricher", "logs", "signoz_logs_enricher", "cloud_logs", "datadog_logs"},
	"pod_logs_enricher":         {"k8s_pod_log_enricher", "logs", "signoz_logs_enricher", "cloud_logs", "datadog_logs"},
	"prometheus_enricher":       {"prometheus_enricher"},
	"alert_graph_enricher":      {"prometheus_enricher"},
	"pod_node_metrics_enricher": {"k8s_pod_cpu_metric_enricher", "k8s_pod_memory_metric_enricher"},
}

// logActions is the set of mutually exclusive log-collection actions.
// Once any one of these runs, the rest should be skipped to avoid
// duplicate log evidences.
var logActions = map[string]bool{
	"logs":                 true,
	"signoz_logs_enricher": true,
	"cloud_logs":           true,
	"datadog_logs":         true,
	"k8s_pod_log_enricher": true,
}

// upsertEventRule inserts or updates an event rule, returning the id and whether a row was affected.
func upsertEventRule(dbms *database.DatabaseManager, annotations any, labels any, alert, expr, duration, accountId, tenantId, source, category, severity string, enabled bool, alertType, metricProvider, metricProviderSource, externalRuleId string, providerConfig map[string]interface{}) (string, bool, error) {
	annotationsJSON, err := common.MarshalJson(annotations)
	if err != nil {
		return "", false, fmt.Errorf("failed to marshal annotations: %w", err)
	}
	labelsJSON, err := common.MarshalJson(labels)
	if err != nil {
		return "", false, fmt.Errorf("failed to marshal labels: %w", err)
	}

	if alertType == "" {
		alertType = "metric"
	}

	var providerConfigJSON *string
	if providerConfig != nil {
		pcJSON, err := common.MarshalJson(providerConfig)
		if err != nil {
			return "", false, fmt.Errorf("failed to marshal provider_config: %w", err)
		}
		s := string(pcJSON)
		providerConfigJSON = &s
	}

	var id string
	err = dbms.QueryRowAndScan(&id, `
		INSERT INTO event_rules (id, account_id, tenant_id, alert, annotations, expr, duration, labels, source, category, severity, enabled, alert_type, metric_provider, metric_provider_source, external_rule_id, provider_config, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, now(), now())
		ON CONFLICT (account_id, tenant_id, alert)
		DO UPDATE SET updated_at = now(), annotations = EXCLUDED.annotations, expr = EXCLUDED.expr, duration = EXCLUDED.duration,
			labels = EXCLUDED.labels, severity = EXCLUDED.severity, category = EXCLUDED.category, source = EXCLUDED.source, enabled = EXCLUDED.enabled,
			alert_type = EXCLUDED.alert_type, metric_provider = EXCLUDED.metric_provider, metric_provider_source = EXCLUDED.metric_provider_source,
			external_rule_id = EXCLUDED.external_rule_id, provider_config = EXCLUDED.provider_config
		RETURNING id`,
		accountId, tenantId, alert, annotationsJSON, expr, duration, labelsJSON, source, category, severity, enabled,
		alertType, nilIfEmpty(metricProvider), nilIfEmpty(metricProviderSource), nilIfEmpty(externalRuleId), providerConfigJSON,
	)
	if err != nil {
		return "", false, err
	}
	return id, id != "", nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// isWebhookSource returns true if the source is a webhook-ingested event rule
// (no external creation needed — rule was auto-created from incoming webhook).
func isWebhookSource(source string) bool {
	return strings.HasSuffix(source, "_webhook")
}

// isExternalProviderSource returns true if the source represents an external
// observability provider that supports alert rule creation.
func isExternalProviderSource(source string) bool {
	externalSources := map[string]bool{
		"datadog": true, "newrelic": true, "dynatrace": true,
		"cloudwatch": true, "azure_monitor": true, "gcp_monitoring": true,
		"splunk": true, "elasticsearch": true, "loki": true,
		"signoz": true, "grafana": true, "chronosphere_user": true,
	}
	return externalSources[source]
}

// resolveProviderFromSource maps event_rule_source values to (provider, providerSource) tuples.
func resolveProviderFromSource(source string) (string, string) {
	switch source {
	case "datadog":
		return "datadog", "user"
	case "newrelic":
		return "newrelic", "user"
	case "dynatrace":
		return "dynatrace", "user"
	case "splunk":
		return "splunk_observability_platform", "user"
	case "elasticsearch":
		return "ES", "user"
	case "signoz":
		return "signoz", "user"
	case "grafana":
		return "grafana", "user"
	case "chronosphere_user":
		return "chronosphere", "user"
	case "loki":
		return "loki", "agent"
	case "cloudwatch":
		return "aws_cloudwatch", "user"
	case "azure_monitor":
		return "azure_app_insights", "user"
	case "gcp_monitoring":
		return "gcp_monitoring", "user"
	default:
		return source, "user"
	}
}

// resolveSourceFromMetricProvider maps a metric_provider value to the corresponding event_rule_source.
// This is the reverse of resolveProviderFromSource, used as a safety net so the backend
// can auto-resolve the source even if the frontend sends a wrong/default source.
func resolveSourceFromMetricProvider(metricProvider string) string {
	switch metricProvider {
	case "datadog":
		return "datadog"
	case "newrelic":
		return "newrelic"
	case "dynatrace":
		return "dynatrace"
	case "splunk_observability_platform":
		return "splunk"
	case "ES":
		return "elasticsearch"
	case "signoz":
		return "signoz"
	case "grafana":
		return "grafana"
	case "chronosphere":
		return "chronosphere_user"
	case "loki":
		return "loki"
	case "aws_cloudwatch":
		return "cloudwatch"
	case "azure_app_insights":
		return "azure_monitor"
	case "gcp_monitoring":
		return "gcp_monitoring"
	default:
		return ""
	}
}

// upsertAgentPlaybook inserts or updates an agent playbook, returning the id.
func upsertAgentPlaybook(dbms *database.DatabaseManager, cloudAccountId, tenantId string, triggerParams, actionParams any, alertName string, source *string) (string, error) {
	triggerParamsJSON, err := common.MarshalJson(triggerParams)
	if err != nil {
		return "", fmt.Errorf("failed to marshal trigger_params: %w", err)
	}
	actionParamsJSON, err := common.MarshalJson(actionParams)
	if err != nil {
		return "", fmt.Errorf("failed to marshal action_params: %w", err)
	}

	query := `
		INSERT INTO agent_playbook (id, cloud_account_id, tenant_id, alert_name, trigger_params, action_params`
	args := []any{cloudAccountId, tenantId, alertName, triggerParamsJSON, actionParamsJSON}

	if source != nil {
		query += `, source)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, cloud_account_id, alert_name)
		DO UPDATE SET trigger_params = EXCLUDED.trigger_params, action_params = EXCLUDED.action_params
		RETURNING id`
		args = append(args, *source)
	} else {
		query += `)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, cloud_account_id, alert_name)
		DO UPDATE SET trigger_params = EXCLUDED.trigger_params, action_params = EXCLUDED.action_params
		RETURNING id`
	}

	var id string
	err = dbms.QueryRowAndScan(&id, query, args...)
	if err != nil {
		return "", err
	}
	return id, nil
}

const agentEventSourceName = "prometheus"

func CreateEventRule(context *security.RequestContext, eventRequest EventConfig) (map[string]bool, error) {
	data := make(map[string]bool)

	// Allow tenant admin without userId for internal system calls (webhook processing, event queue consumers)
	// that use NewSecurityContextForSuperAdminAndTenant with no userId.
	// Account-level access is still enforced by HasAccountAccess below.
	if !context.GetSecurityContext().IsSuperAdmin() &&
		(context.GetSecurityContext().GetTenantId() == "" ||
			!context.GetSecurityContext().IsTenantAdmin()) {
		return data, fmt.Errorf("unauthorized")
	}

	if !context.GetSecurityContext().HasAccountAccess(eventRequest.AccountID, security.SecurityAccessTypeCreate) {
		return data, fmt.Errorf("account unauthorized")
	}

	context.GetLogger().Info("CreateEventRule", "data", eventRequest)

	// Auto-resolve source from metric_provider if source is not already external
	if !isExternalProviderSource(eventRequest.Source) && eventRequest.MetricProvider != "" {
		if resolved := resolveSourceFromMetricProvider(eventRequest.MetricProvider); resolved != "" {
			eventRequest.Source = resolved
		}
	}

	externalRuleId := ""

	// 1. Prometheus: sync via relay (existing path)
	if eventRequest.Source == agentEventSourceName {
		relayRequest := relay.RelayExecuteRequest{
			NoSinks: true,
			Cache:   false,
			Body: relay.ActionExecuteBody{
				AccountID:  eventRequest.AccountID,
				ActionName: "create_or_replace_alert_rule",
				ActionParams: map[string]any{
					"alert":       eventRequest.Alert,
					"expr":        eventRequest.Expr,
					"duration":    eventRequest.Duration,
					"annotations": eventRequest.Annotations,
					"labels":      eventRequest.Labels,
				},
			},
		}
		_, err2 := relay.Execute(relayRequest)
		if err2 != nil {
			return data, err2
		}
	}

	// 2. External providers: create alert rule in external system first
	if isExternalProviderSource(eventRequest.Source) {
		provider, providerSource := resolveProviderFromSource(eventRequest.Source)
		result, err := alertrule.CreateAlertRule(context, provider, providerSource, alertrule.AlertRuleConfig{
			AccountId: eventRequest.AccountID,
			Name:      eventRequest.Alert,
			AlertType: eventRequest.AlertType,
			Query:     eventRequest.Expr,
			Severity:  eventRequest.Severity,
			Duration:  eventRequest.Duration,
			Annotations: map[string]string{
				"description": eventRequest.Annotations.Description,
				"summary":     eventRequest.Annotations.Summary,
			},
			Labels: map[string]string{
				"severity": eventRequest.Labels.Severity,
			},
			Enabled:        eventRequest.Enabled,
			ProviderConfig: eventRequest.ProviderConfig,
		})
		if err != nil {
			return data, fmt.Errorf("failed to create alert rule in %s: %w", provider, err)
		}
		externalRuleId = result.ExternalRuleId
		context.GetLogger().Info("CreateEventRule: external rule created", "provider", provider, "external_rule_id", externalRuleId)
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return data, fmt.Errorf("failed to get database manager: %w", err)
	}

	ruleId, ok, err := upsertEventRule(dbms, eventRequest.Annotations, eventRequest.Labels,
		eventRequest.Alert, eventRequest.Expr, eventRequest.Duration,
		eventRequest.AccountID, context.GetSecurityContext().GetTenantId(),
		eventRequest.Source, eventRequest.Category, eventRequest.Severity, eventRequest.Enabled,
		eventRequest.AlertType, eventRequest.MetricProvider, eventRequest.MetricProviderSource,
		externalRuleId, eventRequest.ProviderConfig)
	if err != nil {
		return data, err
	}
	context.GetLogger().Info("CreateEventRule", "rule_id", ruleId)
	data["response"] = ok

	// Skip playbook creation for webhook-ingested rules
	if isWebhookSource(eventRequest.Source) {
		return data, nil
	}

	source := "user"
	playbookId, err := upsertAgentPlaybook(dbms,
		eventRequest.AccountID, context.GetSecurityContext().GetTenantId(),
		eventRequest.TriggerParams, eventRequest.ActionParams, eventRequest.Alert, &source)
	if err != nil {
		return data, err
	}
	data["response"] = playbookId != ""
	return data, nil
}

func UpdateEventRule(context *security.RequestContext, eventRequest EventConfig) (map[string]bool, error) {
	data := make(map[string]bool)

	if context.GetSecurityContext().GetUserId() == "" || context.GetSecurityContext().GetTenantId() == "" {
		return data, common.ErrorUnauthorized("unauthorized")
	}

	if !context.GetSecurityContext().HasAccountAccess(eventRequest.AccountID, security.SecurityAccessTypeUpdate) {
		return data, common.ErrorUnauthorized("unauthorized")
	}

	context.GetLogger().Info("UpdateEventRule", "data", slog.AnyValue(eventRequest))

	// Auto-resolve source from metric_provider if source is not already external
	if !isExternalProviderSource(eventRequest.Source) && eventRequest.MetricProvider != "" {
		if resolved := resolveSourceFromMetricProvider(eventRequest.MetricProvider); resolved != "" {
			eventRequest.Source = resolved
		}
	}

	// 1. Prometheus: sync via relay (existing path)
	if eventRequest.Source == agentEventSourceName {
		relayRequest := relay.RelayExecuteRequest{
			NoSinks: true,
			Cache:   false,
			Body: relay.ActionExecuteBody{
				AccountID:  eventRequest.AccountID,
				ActionName: "create_or_replace_alert_rule",
				ActionParams: map[string]any{
					"alert":       eventRequest.Alert,
					"expr":        eventRequest.Expr,
					"duration":    eventRequest.Duration,
					"annotations": eventRequest.Annotations,
					"labels":      eventRequest.Labels,
				},
			},
		}
		_, err2 := relay.Execute(relayRequest)
		if err2 != nil {
			return data, err2
		}
	}

	// 2. External providers: update alert rule in external system
	externalRuleId := ""
	if isExternalProviderSource(eventRequest.Source) {
		// Fetch existing external_rule_id from DB
		dbmsLookup, err := database.GetDatabaseManager(database.Metastore)
		if err == nil {
			lookupErr := dbmsLookup.QueryRowAndScan(&externalRuleId,
				"SELECT COALESCE(external_rule_id, '') FROM event_rules WHERE account_id = $1 AND tenant_id = $2 AND alert = $3",
				eventRequest.AccountID, context.GetSecurityContext().GetTenantId(), eventRequest.Alert)
			if lookupErr != nil {
				context.GetLogger().Error("UpdateEventRule: failed to lookup external_rule_id", "error", lookupErr)
			}
		}

		if externalRuleId != "" {
			provider, providerSource := resolveProviderFromSource(eventRequest.Source)
			_, err := alertrule.UpdateAlertRule(context, provider, providerSource, externalRuleId, alertrule.AlertRuleConfig{
				AccountId: eventRequest.AccountID,
				Name:      eventRequest.Alert,
				AlertType: eventRequest.AlertType,
				Query:     eventRequest.Expr,
				Severity:  eventRequest.Severity,
				Duration:  eventRequest.Duration,
				Annotations: map[string]string{
					"description": eventRequest.Annotations.Description,
					"summary":     eventRequest.Annotations.Summary,
				},
				Labels: map[string]string{
					"severity": eventRequest.Labels.Severity,
				},
				Enabled:        eventRequest.Enabled,
				ProviderConfig: eventRequest.ProviderConfig,
			})
			if err != nil {
				context.GetLogger().Error("UpdateEventRule: failed to update external rule", "error", err)
				// Continue with local update even if external update fails
			}
		}
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return data, fmt.Errorf("failed to get database manager: %w", err)
	}

	ruleId, ok, err := upsertEventRule(dbms, eventRequest.Annotations, eventRequest.Labels,
		eventRequest.Alert, eventRequest.Expr, eventRequest.Duration,
		eventRequest.AccountID, context.GetSecurityContext().GetTenantId(),
		eventRequest.Source, eventRequest.Category, eventRequest.Severity, eventRequest.Enabled,
		eventRequest.AlertType, eventRequest.MetricProvider, eventRequest.MetricProviderSource,
		externalRuleId, eventRequest.ProviderConfig)
	if err != nil {
		return data, err
	}
	context.GetLogger().Info("UpdateEventRule", "rule_id", ruleId)
	data["response"] = ok

	playbookId, err := upsertAgentPlaybook(dbms,
		eventRequest.AccountID, context.GetSecurityContext().GetTenantId(),
		eventRequest.TriggerParams, eventRequest.ActionParams, eventRequest.Alert, nil)
	if err != nil {
		return data, err
	}
	data["response"] = playbookId != ""

	if eventRequest.Source == agentEventSourceName {
		// call async refresh of agent playbooks with a bounded context
		logger := context.GetLogger()
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("panic in refreshAgentPlaybooks", "recover", r)
				}
			}()
			ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 30*time.Second)
			defer cancel()
			err := refreshAgentPlaybooks(ctx, logger, eventRequest.AccountID)
			if err != nil {
				logger.Error("failed to refresh agent playbooks", "error", err)
			}
		}()
	}
	return data, nil
}

func DisableEventRule(context *security.RequestContext, eventRequest DisableEventConfig) (map[string]bool, error) {
	data := make(map[string]bool)

	if context.GetSecurityContext().GetUserId() == "" || context.GetSecurityContext().GetTenantId() == "" {
		return data, fmt.Errorf("unauthorized")
	}

	if !context.GetSecurityContext().HasAccountAccess(eventRequest.AccountID, security.SecurityAccessTypeUpdate) {
		return data, common.ErrorUnauthorized("unauthorized")
	}

	context.GetLogger().Info("DisableEventRule", "data", eventRequest)

	// Get source and external_rule_id of event rule
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return data, err
	}

	var eventSource string
	var externalRuleId *string
	err = dbms.Db.QueryRow("SELECT source, external_rule_id FROM event_rules WHERE id = $1", eventRequest.Id).Scan(&eventSource, &externalRuleId)
	if err != nil {
		context.GetLogger().Error("eventrule: unable to find source of event rule", "error", err, "id", eventRequest.Id)
		return data, errors.New("unable to find source of event rule")
	}

	if eventSource == agentEventSourceName {
		relayRequest := relay.RelayExecuteRequest{
			NoSinks: true,
			Cache:   false,
			Body: relay.ActionExecuteBody{
				AccountID:  eventRequest.AccountID,
				ActionName: "delete_alert_rule",
				ActionParams: map[string]any{
					"alert":     eventRequest.Alert,
					"group":     eventRequest.Group,
					"namespace": eventRequest.Namespace,
				},
			},
		}

		_, err2 := relay.Execute(relayRequest)
		if err2 != nil {
			return data, err2
		}
	}

	// For external providers: delete rule in external system when disabling
	if isExternalProviderSource(eventSource) && externalRuleId != nil && *externalRuleId != "" && !eventRequest.Enable {
		provider, providerSource := resolveProviderFromSource(eventSource)
		err := alertrule.DeleteAlertRule(context, provider, providerSource, eventRequest.AccountID, *externalRuleId)
		if err != nil {
			context.GetLogger().Error("DisableEventRule: failed to delete external rule", "provider", provider, "external_rule_id", *externalRuleId, "error", err)
			// Continue with local disable even if external delete fails
		}
	}

	var updatedId string
	err = dbms.QueryRowAndScan(&updatedId, `
		UPDATE event_rules SET enabled = $4, updated_at = now()
		WHERE tenant_id = $1 AND id = $2 AND account_id = $3
		RETURNING id`,
		context.GetSecurityContext().GetTenantId(), eventRequest.Id, eventRequest.AccountID, eventRequest.Enable,
	)
	if err != nil {
		return data, err
	}
	context.GetLogger().Info("DisableEventRule", "rule_id", updatedId)
	data["response"] = updatedId != ""
	return data, nil
}

func refreshAgentPlaybooks(ctx stdctx.Context, logger *slog.Logger, accountId string) error {
	relayRequest := relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:    accountId,
			ActionName:   "refresh_playbook",
			ActionParams: map[string]any{},
		},
	}

	// Check context before making the relay call
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	_, err := relay.Execute(relayRequest)
	if err != nil {
		return err
	}
	logger.Info("refreshed AgentPlaybooks", "accountId", accountId)
	return nil
}

func ListAlertAgentPlaybook(context *security.RequestContext, request ListAgentPlaybookRequest) ([]map[string]any, error) {
	if !context.GetSecurityContext().IsSuperAdmin() &&
		(context.GetSecurityContext().GetUserId() == "" ||
			context.GetSecurityContext().GetTenantId() == "" ||
			!context.GetSecurityContext().IsTenantAdmin()) {
		return nil, fmt.Errorf("unauthorized")
	}

	if !context.GetSecurityContext().HasAccountAccess(request.CloudAccountId, security.SecurityAccessTypeRead) {
		return nil, fmt.Errorf("account unauthorized")
	}

	context.GetLogger().Info("ListAgentPlaybook", "request", request)

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	type agentPlaybookRow struct {
		ActionParams   json.RawMessage `db:"action_params" json:"action_params"`
		AlertName      string          `db:"alert_name" json:"alert_name"`
		CloudAccountId string          `db:"cloud_account_id" json:"cloud_account_id"`
		Source         *string         `db:"source" json:"source"`
		TriggerParams  json.RawMessage `db:"trigger_params" json:"trigger_params"`
		Id             string          `db:"id" json:"id"`
	}

	var rows []agentPlaybookRow
	err = dbms.QueryAndScan(&rows, `
		SELECT action_params, alert_name, cloud_account_id, source, trigger_params, id
		FROM agent_playbook
		WHERE cloud_account_id = $1 AND alert_name = $2`,
		request.CloudAccountId, request.AlertName,
	)
	if err != nil {
		return nil, err
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("no agent playbook found")
	}

	converted := make([]map[string]any, len(rows))
	for i, row := range rows {
		item := map[string]any{
			"id":               row.Id,
			"alert_name":       row.AlertName,
			"cloud_account_id": row.CloudAccountId,
			"source":           row.Source,
		}
		// Parse JSON fields back to any for downstream compatibility
		var actionParams any
		if err := json.Unmarshal(row.ActionParams, &actionParams); err == nil {
			item["action_params"] = actionParams
		}
		var triggerParams any
		if err := json.Unmarshal(row.TriggerParams, &triggerParams); err == nil {
			item["trigger_params"] = triggerParams
		}
		converted[i] = item
	}
	return converted, nil
}

// isK8sAgentConnected reports whether the account has a non-proxy agent row whose
// connection_status is populated. Returns false on any DB error or no matching row —
// callers use this strictly as a hint to skip k8s-agent-dependent actions, so
// failing closed avoids surfacing transient DB errors as spurious relay calls.
func isK8sAgentConnected(accountId string) bool {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return false
	}
	var exists int
	if qErr := dbms.Db.Get(&exists,
		`SELECT 1 FROM agent WHERE cloud_account_id = $1 AND type != 'proxy' AND connection_status IS NOT NULL LIMIT 1`,
		accountId); qErr != nil {
		return false
	}
	return true
}

// Action defines the interface that all playbook actions must implement.
func ExecutePlaybook(context *security.RequestContext, accountId string, event playbooks.PlaybookEvent) ([]PlaybookActionExecutionResponse, error) {
	context.GetLogger().Info("eventrule: executing playbook", "event_id", event.EventId, "accountId", accountId, "alertName", event.Name)

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		context.GetLogger().Error("eventrule: failed to get database manager", "error", err)
		return nil, err
	}
	rows := []string{}
	err = dbms.QueryAndScan(&rows, "select action_params::text from agent_playbook where cloud_account_id = $1 and alert_name = $2 and (source IS NULL OR source != 'agent')", accountId, event.AggregationKey)
	if err != nil {
		context.GetLogger().Error("eventrule: failed to query database", "error", err)
	}

	playbooksData := []map[string]map[string]any{}
	for _, row := range rows {
		err = json.Unmarshal([]byte(row), &playbooksData)
		if err != nil {
			return nil, err
		}
	}

	playbookEventContext := playbooks.NewPlaybookActionContext(context.GetSecurityContext().GetTenantId(), accountId, context.GetLogger(), event)

	// Execute each action found in the playbook.
	var executionResults []PlaybookActionExecutionResponse
	outputs := map[string]any{}
	extractedLabels := map[string]map[string]any{}
	executedAction := []string{}
	for i, actionMap := range playbooksData {
		for actionName, rawParams := range actionMap {
			action, found := playbooks.GetAction(actionName)
			if !found {
				context.GetLogger().Warn("eventrule: action not found in registry", "actionName", actionName)
				continue
			}
			executedAction = append(executedAction, actionName)
			response := PlaybookActionExecutionResponse{
				Response:        nil,
				Error:           nil,
				ActionCondition: "",
				ActionTitle:     "",
				ActionName:      actionName,
				ActionArgs:      rawParams,
				DurationSeconds: 0,
			}
			context.GetLogger().Info("executing action", "event_id", event.EventId, "actionName", actionName, "title", rawParams["title"])

			// Save original unevaluated rawParams for per-service template re-evaluation
			originalRawParams := make(map[string]any, len(rawParams))
			for k, v := range rawParams {
				originalRawParams[k] = v
			}

			// Check if this step has for_each - if yes, we need to defer template evaluation
			// for parameters that reference {{ item }}, which won't exist until inside the loop
			hasForEachParam := rawParams["for_each"] != nil

			var evaluatedParams map[string]any
			var err error

			if hasForEachParam {
				// Only evaluate control parameters initially
				// Other parameters will be evaluated inside the for_each loop with item context
				controlParams := map[string]any{}
				otherParams := map[string]any{}

				controlKeys := map[string]bool{
					"for_each":                   true,
					"for_each_limit":             true,
					"for_each_on_limit_exceeded": true,
					"if":                         true,
				}

				for k, v := range rawParams {
					if controlKeys[k] {
						controlParams[k] = v
					} else {
						otherParams[k] = v
					}
				}

				// Evaluate only control parameters
				evaluatedControlParams, err := evaluateRawParamsTemplates(controlParams, event, outputs, extractedLabels)
				if err != nil {
					context.GetLogger().Error("eventrule: failed to evaluate control parameters", "actionName", actionName, "error", err)
					response.Error = err
					executionResults = append(executionResults, response)
					continue
				}

				// Merge back: evaluated control params + unevaluated other params
				evaluatedParams = make(map[string]any)
				for k, v := range evaluatedControlParams {
					evaluatedParams[k] = v
				}
				for k, v := range otherParams {
					evaluatedParams[k] = v
				}
			} else {
				// No for_each: evaluate all parameters normally
				evaluatedParams, err = evaluateRawParamsTemplates(rawParams, event, outputs, extractedLabels)
				if err != nil {
					context.GetLogger().Error("eventrule: failed to execute action", "actionName", actionName, "title", rawParams["title"], "error", err)
					response.Error = err
					executionResults = append(executionResults, response)
					continue
				}
			}

			//at this point these are already evaluated using previous step
			if evaluatedParams["if"] != nil {
				switch val := evaluatedParams["if"].(type) {
				case string:
					if !strings.EqualFold(val, "true") {
						continue
					}
				case bool:
					if !val {
						continue
					}
				}
			}

			rawParams = evaluatedParams

			// Check for for_each support
			forEachArray := []any{}
			hasForEach := false
			if forEachExpr, ok := rawParams["for_each"]; ok && forEachExpr != nil {
				hasForEach = true
				// The for_each value could be:
				// 1. Already an array (if passed directly from playbook)
				// 2. A string that is a direct reference path (e.g., "extracted_labels['action_0']['_series']")
				//    In this case, we need to manually extract the value from the context
				switch v := forEachExpr.(type) {
				case []any:
					forEachArray = v
				case string:
					// The string might be:
					// 1. A Python-style list representation from template rendering: "[{'key': 'val'}, ...]"
					// 2. A direct reference that needs resolution

					// Check if it looks like a Python-style list
					if strings.HasPrefix(strings.TrimSpace(v), "[") {
						// Try to convert Python-style dict notation to JSON
						// Replace single quotes with double quotes for JSON compliance
						jsonStr := strings.ReplaceAll(v, "'", "\"")
						var tempArray []any
						if err := json.Unmarshal([]byte(jsonStr), &tempArray); err == nil {
							forEachArray = tempArray
						}
					}

					// If that didn't work, try extracting from extracted_labels directly
					if len(forEachArray) == 0 && strings.Contains(v, "extracted_labels") {
						// Parse simple path like: extracted_labels['action_0']['_series']
						parts := strings.Split(v, "'")
						if len(parts) >= 4 {
							actionKey := parts[1]
							fieldKey := parts[3] // usually '_series'
							if labels, exists := extractedLabels[actionKey]; exists {
								if seriesVal, hasField := labels[fieldKey]; hasField {
									if seriesArray, isArray := seriesVal.([]any); isArray {
										forEachArray = seriesArray
									} else if seriesMapArray, isMapArray := seriesVal.([]map[string]any); isMapArray {
										for _, item := range seriesMapArray {
											forEachArray = append(forEachArray, item)
										}
									}
								}
							}
						}
					}

					// If we still don't have an array, it's an error
					if len(forEachArray) == 0 {
						context.GetLogger().Error("eventrule: for_each template did not resolve to array", "actionName", actionName, "expression", v)
						response.Error = fmt.Errorf("for_each expression '%s' did not resolve to an array", v)
						executionResults = append(executionResults, response)
						continue
					}
				default:
					context.GetLogger().Error("eventrule: for_each must be array or template string", "actionName", actionName, "type", fmt.Sprintf("%T", v))
					response.Error = fmt.Errorf("for_each must be array or template string, got %T", v)
					executionResults = append(executionResults, response)
					continue
				}

				// Get and validate for_each_limit
				forEachLimit := 10 // default limit
				if limitVal, ok := rawParams["for_each_limit"]; ok {
					switch v := limitVal.(type) {
					case float64:
						forEachLimit = int(v)
					case int:
						forEachLimit = v
					}
				}

				// Hard cap at 20
				const maxForEachLimit = 20
				if forEachLimit > maxForEachLimit {
					context.GetLogger().Warn("eventrule: for_each_limit exceeds maximum, capping", "actionName", actionName, "requested", forEachLimit, "max", maxForEachLimit)
					forEachLimit = maxForEachLimit
				}

				// Check if we're truncating
				if len(forEachArray) > forEachLimit {
					onLimitExceeded := "warn" // default behavior
					if val, ok := rawParams["for_each_on_limit_exceeded"].(string); ok {
						onLimitExceeded = val
					}

					switch onLimitExceeded {
					case "error":
						response.Error = fmt.Errorf("for_each array has %d items but limit is %d", len(forEachArray), forEachLimit)
						executionResults = append(executionResults, response)
						continue
					case "warn":
						context.GetLogger().Warn("eventrule: truncating for_each array", "actionName", actionName, "arraySize", len(forEachArray), "limit", forEachLimit)
					}

					forEachArray = forEachArray[:forEachLimit]
				}

				// Remove for_each metadata from params before execution
				delete(rawParams, "for_each")
				delete(rawParams, "for_each_limit")
				delete(rawParams, "for_each_on_limit_exceeded")
			}

			// If no for_each, treat as single execution
			if !hasForEach {
				forEachArray = []any{nil}
			}

			// Execute action for each item in forEachArray
			for forEachIndex, forEachItem := range forEachArray {
				iterationResponse := response // copy base response
				iterationRawParams := make(map[string]any)
				for k, v := range rawParams {
					iterationRawParams[k] = v
				}

				// Re-evaluate templates with "item" context if this is a for_each loop
				if hasForEach {
					var err error
					iterationRawParams, err = evaluateRawParamsTemplatesWithExtraContext(
						iterationRawParams,
						event,
						outputs,
						extractedLabels,
						map[string]any{"item": forEachItem},
					)
					if err != nil {
						context.GetLogger().Error("eventrule: failed to evaluate templates with item context", "actionName", actionName, "error", err)
						iterationResponse.Error = err
						executionResults = append(executionResults, iterationResponse)
						continue
					}
				}

				// remove unnessary params
				if title, ok := iterationRawParams["title"].(string); ok {
					iterationResponse.ActionTitle = title
					delete(iterationRawParams, "title")
				} else if title, ok := iterationRawParams["action_name"].(string); ok {
					iterationResponse.ActionTitle = title
					delete(iterationRawParams, "action_name")
				}
				if condition, ok := iterationRawParams["if"].(string); ok {
					iterationResponse.ActionCondition = condition
					delete(iterationRawParams, "if")
				}

				// Split comma-separated services label for per-service execution
				svcValues := []string{event.Labels["service"]}
				if strings.Contains(event.Labels["service"], ",") {
					svcValues = strings.Split(event.Labels["service"], ",")
					for j := range svcValues {
						svcValues[j] = strings.TrimSpace(svcValues[j])
					}
				}

				pendingLabels := make(map[string]string)
				for svcIdx, svc := range svcValues {
					svcResponse := iterationResponse // copy for this service iteration
					svcParams := iterationRawParams
					execContext := playbookEventContext

					if len(svcValues) > 1 {
						// Build per-service event with cloned labels
						svcEvent := event
						svcLabels := make(map[string]string, len(event.Labels))
						for k, v := range event.Labels {
							svcLabels[k] = v
						}
						svcLabels["service"] = svc
						svcEvent.Labels = svcLabels
						execContext = playbooks.NewPlaybookActionContext(context.GetSecurityContext().GetTenantId(), accountId, context.GetLogger(), svcEvent)

						// Re-evaluate templates from original params with per-service event
						var evalErr error
						if hasForEach {
							svcParams, evalErr = evaluateRawParamsTemplatesWithExtraContext(
								originalRawParams, svcEvent, outputs, extractedLabels,
								map[string]any{"item": forEachItem},
							)
						} else {
							svcParams, evalErr = evaluateRawParamsTemplates(originalRawParams, svcEvent, outputs, extractedLabels)
						}
						if evalErr != nil {
							context.GetLogger().Error("eventrule: failed to evaluate templates for service", "actionName", actionName, "service", svc, "error", evalErr)
							svcResponse.Error = evalErr
							executionResults = append(executionResults, svcResponse)
							continue
						}

						// Check if condition for this service
						if svcParams["if"] != nil {
							skip := false
							switch val := svcParams["if"].(type) {
							case string:
								if !strings.EqualFold(val, "true") {
									skip = true
								}
							case bool:
								if !val {
									skip = true
								}
							}
							if skip {
								continue
							}
						}

						// Remove control/metadata params
						delete(svcParams, "for_each")
						delete(svcParams, "for_each_limit")
						delete(svcParams, "for_each_on_limit_exceeded")

						// Extract title and condition
						if title, ok := svcParams["title"].(string); ok {
							svcResponse.ActionTitle = title
							delete(svcParams, "title")
						} else if title, ok := svcParams["action_name"].(string); ok {
							svcResponse.ActionTitle = title
							delete(svcParams, "action_name")
						}
						if condition, ok := svcParams["if"].(string); ok {
							svcResponse.ActionCondition = condition
							delete(svcParams, "if")
						}
					}

					actionStart := time.Now()
					result, err := action.Execute(execContext, svcParams)
					svcResponse.DurationSeconds = time.Since(actionStart).Seconds()
					if err != nil {
						context.GetLogger().Error("eventrule: failed to execute action", "event_id", event.EventId, "actionName", actionName, "forEachIndex", forEachIndex, "service", svc, "error", err)
						svcResponse.Error = err
					} else {
						svcResponse.Response = result
					}

					// Generate output key with for_each index if applicable
					outputKey := svcResponse.ActionName + fmt.Sprintf("_%d", i)
					if hasForEach {
						outputKey = outputKey + fmt.Sprintf("_%d", forEachIndex)
					}
					if len(svcValues) > 1 {
						outputKey = outputKey + fmt.Sprintf("_%d", svcIdx)
					}

					outputs[outputKey] = result
					extractedLabels[outputKey] = map[string]any{}
					if result != nil {
						// add in default labels as well extractedLabels
						if k, ok := result.(playbooks.PlaybookActionResponseLabelExtractor); ok {
							extractedActionLabels := k.ExtractLabels()
							if extractedActionLabels == nil {
								extractedActionLabels = map[string]any{}
							}
							extractedLabels[outputKey] = extractedActionLabels
							for k, v := range extractedActionLabels {
								if v == nil {
									continue
								}
								if _, ok := event.Labels[k]; ok {
									continue
								}
								if k == "instance" {
									// special handling for instance label to extract last part after /
									if vStr, ok := v.(string); ok && strings.Contains(vStr, "/") {
										pendingLabels[k] = path.Base(vStr)
										continue
									}
								}
								switch v := v.(type) {
								case string:
									pendingLabels[k] = v
								default:
									pendingLabels[k] = fmt.Sprintf("%v", v)
								}
							}
						}

					}

					if svcResponse.ActionTitle == "" && svcResponse.Response != nil && svcResponse.Response.GetAdditionalInfo() != nil {
						if title, ok := svcResponse.Response.GetAdditionalInfo()["title"]; ok && title != nil {
							if titleStr, ok := title.(string); ok && titleStr != "" {
								svcResponse.ActionTitle = titleStr
							}
						}
					}

					if svcResponse.ActionTitle != "" {
						outputs[svcResponse.ActionTitle] = outputs[outputKey]
						extractedLabels[svcResponse.ActionTitle] = extractedLabels[outputKey]
					}

					executionResults = append(executionResults, svcResponse)
				}
				// Merge pending labels into event.Labels after all services have executed
				for k, v := range pendingLabels {
					if _, ok := event.Labels[k]; !ok {
						event.Labels[k] = v
					}
				}
			}

			// After for_each loop completes, aggregate all extracted labels
			// This allows subsequent steps to reference all extracted values at once
			// Example: {{ extracted_labels['signoz_logs_enricher_6']['_all_extracted']['task_id'] }}
			if hasForEach && len(forEachArray) > 0 {
				baseOutputKey := response.ActionName + fmt.Sprintf("_%d", i)
				aggregatedLabels := make(map[string][]any)

				// Collect all extracted labels from all iterations
				for forEachIndex := range forEachArray {
					iterationKey := baseOutputKey + fmt.Sprintf("_%d", forEachIndex)
					if labels, exists := extractedLabels[iterationKey]; exists {
						for labelKey, labelValue := range labels {
							// Skip the _series key itself to avoid duplication
							if labelKey == "_series" {
								continue
							}
							if aggregatedLabels[labelKey] == nil {
								aggregatedLabels[labelKey] = make([]any, 0)
							}

							// Flatten arrays: if labelValue is an array, add each element individually
							// This handles the case where label extractors now return arrays of values
							if labelValueArray, ok := labelValue.([]any); ok {
								for _, v := range labelValueArray {
									if v != nil && v != "" {
										// Check for duplicates before adding using deep equality
										// This handles complex types like maps and slices
										isDuplicate := false
										for _, existing := range aggregatedLabels[labelKey] {
											if reflect.DeepEqual(existing, v) {
												isDuplicate = true
												break
											}
										}
										if !isDuplicate {
											aggregatedLabels[labelKey] = append(aggregatedLabels[labelKey], v)
										}
									}
								}
							} else if labelValue != nil && labelValue != "" {
								// For non-array values, add directly (backward compatibility)
								aggregatedLabels[labelKey] = append(aggregatedLabels[labelKey], labelValue)
							}
						}
					}
				}

				// Store aggregated labels in the base output key (without iteration index)
				// with a special _all_extracted field
				if extractedLabels[baseOutputKey] == nil {
					extractedLabels[baseOutputKey] = make(map[string]any)
				}
				extractedLabels[baseOutputKey]["_all_extracted"] = aggregatedLabels

				// Build detailed logging info showing value counts per key
				aggregatedDetails := make(map[string]int)
				for key, values := range aggregatedLabels {
					aggregatedDetails[key] = len(values)
				}

				context.GetLogger().Info("eventrule: aggregated for_each labels",
					"actionName", actionName,
					"iterations", len(forEachArray),
					"aggregatedKeys", len(aggregatedLabels),
					"valueCounts", aggregatedDetails)
			}
		}
	}

	// Check if any log-related action already ran in the playbook.
	hasLogAction := false
	for _, name := range executedAction {
		if logActions[name] {
			hasLogAction = true
			break
		}
	}

	// Build skip set from pre-existing evidence (e.g. agent-provided enrichment).
	// Maps agent action names to their server-side equivalents so we don't
	// re-run enrichment that the agent already performed.
	skipActions := map[string]bool{}
	for actionName := range event.ExistingEvidenceActions {
		skipActions[actionName] = true
		if serverActions, ok := agentToServerActionMap[actionName]; ok {
			for _, sa := range serverActions {
				skipActions[sa] = true
			}
		}
	}

	// Also mark log action as already executed if agent evidence covers it
	if !hasLogAction {
		for action := range skipActions {
			if logActions[action] {
				hasLogAction = true
				break
			}
		}
	}

	// Pre-flight: k8s_*-prefixed actions require a connected agent. For cloud-only
	// accounts (ECS, RDS, etc.) with no agent registered, these always fail with
	// "agent not connected" — skip them up-front to avoid per-action relay round-trips
	// and noisy error logs. (Cannot call account.GetAgentConnectionDetails here due to
	// an import cycle eventrule -> account -> adapter -> llm -> tenant -> eventrule.)
	k8sAgentAvailable := isK8sAgentConnected(accountId)

	//run auto discovery actions
	for _, actionName := range playbooks.ListActions() {
		if slices.Contains(executedAction, actionName) {
			continue
		}
		// Skip k8s-agent-dependent actions when the account has no connected agent.
		if !k8sAgentAvailable && strings.HasPrefix(actionName, "k8s_") {
			continue
		}
		// Skip any log action if another log action already ran — avoids duplicate log collection
		if logActions[actionName] && hasLogAction {
			continue
		}
		// Skip actions where equivalent agent evidence already exists
		if skipActions[actionName] {
			context.GetLogger().Info("eventrule: skipping auto action (agent evidence exists)", "actionName", actionName)
			continue
		}
		action, found := playbooks.GetAction(actionName)
		if !found {
			continue
		}
		autoAction, ok := action.(playbooks.PlaybookAutoAction)
		if !ok {
			continue
		}
		if autoAction.CanAutoExecute(playbookEventContext) {
			result, err := autoAction.AutoExecute(playbookEventContext)
			if err != nil {
				context.GetLogger().Warn("eventrule: failed to execute auto action", "actionName", actionName, "error", err)
				continue
			}
			executionResults = append(executionResults, PlaybookActionExecutionResponse{
				Response:        result,
				Error:           nil,
				ActionCondition: "",
				ActionTitle:     "",
				ActionName:      actionName,
				ActionArgs:      map[string]any{},
			})

			// Mark log action as executed so subsequent log actions are skipped
			if logActions[actionName] {
				hasLogAction = true
			}

			if result != nil {
				// add in default labels as well extractedLabels
				if k, ok := result.(playbooks.PlaybookActionResponseLabelExtractor); ok {
					extractedActionLabels := k.ExtractLabels()
					if extractedActionLabels == nil {
						extractedActionLabels = map[string]any{}
					}
					for k, v := range extractedActionLabels {
						if v == nil {
							continue
						}
						if _, ok := event.Labels[k]; ok {
							continue
						}
						switch v := v.(type) {
						case string:
							event.Labels[k] = v
						default:
							event.Labels[k] = fmt.Sprintf("%v", v)
						}
					}
				}

			}
		}
	}

	return executionResults, nil
}

func evaluateRawParamsTemplates(rawParams map[string]any, event playbooks.PlaybookEvent, outputs map[string]any, extractedLabels map[string]map[string]any) (map[string]any, error) {
	return evaluateRawParamsTemplatesWithExtraContext(rawParams, event, outputs, extractedLabels, nil)
}

func evaluateRawParamsTemplatesWithExtraContext(rawParams map[string]any, event playbooks.PlaybookEvent, outputs map[string]any, extractedLabels map[string]map[string]any, extraContext map[string]any) (map[string]any, error) {

	// Marshal JSON without HTML escaping to avoid breaking template syntax
	// HTML escaping converts <, >, & to \u003c, \u003e, \u0026 which breaks templates
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(rawParams); err != nil {
		return nil, err
	}
	// Remove trailing newline added by encoder.Encode
	rawData := bytes.TrimSpace(buf.Bytes())

	templateCtx := gonja.Context{
		"alert": map[string]any{
			"labels":      event.Labels,
			"annotations": event.Annotations,
			"name":        event.Name,
		},
		"labels":           event.Labels,
		"extracted_labels": extractedLabels,
		"annotations":      event.Annotations,
		"outputs":          outputs,
	}

	// Add extra context variables (e.g., "item" for for_each loops)
	for k, v := range extraContext {
		templateCtx[k] = v
	}

	tpl, err := gonja.FromString(string(rawData))
	if err != nil {
		return nil, err
	}
	// Execute the template
	res, err := tpl.Execute(templateCtx)
	if err != nil {
		return nil, err
	}

	updatedParams := make(map[string]any)
	err = json.Unmarshal([]byte(res), &updatedParams)
	if err != nil {
		return nil, err
	}

	// Post-process: automatically parse JSON strings back to their actual types
	// This handles values that were serialized with |tojson filter
	updatedParams = parseJSONStrings(updatedParams)

	return updatedParams, nil
}

// parseJSONStrings recursively parses string values that are valid JSON
// into their actual types (arrays, objects, etc.)
func parseJSONStrings(params map[string]any) map[string]any {
	result := make(map[string]any)
	for key, value := range params {
		switch v := value.(type) {
		case string:
			// Try to parse as JSON ONLY for arrays and objects (from |tojson filter)
			// Don't parse primitive values to avoid converting "404" -> 404 (number)
			if len(v) > 0 {
				firstChar := v[0]
				// Only parse JSON arrays and objects
				if firstChar == '[' || firstChar == '{' {
					var parsed any
					if err := json.Unmarshal([]byte(v), &parsed); err == nil {
						result[key] = parsed
					} else {
						result[key] = value
					}
				} else {
					result[key] = value
				}
			} else {
				result[key] = value
			}
		case map[string]any:
			// Recursively process nested maps
			result[key] = parseJSONStrings(v)
		case []any:
			// Recursively process arrays
			arr := make([]any, len(v))
			for i, item := range v {
				if itemMap, ok := item.(map[string]any); ok {
					arr[i] = parseJSONStrings(itemMap)
				} else if itemStr, ok := item.(string); ok {
					// Try to parse array items that are JSON strings (only arrays/objects)
					if len(itemStr) > 0 {
						firstChar := itemStr[0]
						if firstChar == '[' || firstChar == '{' {
							var parsed any
							if err := json.Unmarshal([]byte(itemStr), &parsed); err == nil {
								arr[i] = parsed
							} else {
								arr[i] = item
							}
						} else {
							arr[i] = item
						}
					} else {
						arr[i] = item
					}
				} else {
					arr[i] = item
				}
			}
			result[key] = arr
		default:
			result[key] = value
		}
	}
	return result
}
