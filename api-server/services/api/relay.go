package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/security"
	"regexp"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// allowedRelayActions is the set of relay actions the proxy is permitted to forward.
var allowedRelayActions = map[string]bool{
	"request":             true,
	"grafana":             true,
	"jaeger_query_traces": true,
}

// validActionPattern ensures the action contains only safe alphanumeric/underscore/hyphen characters.
var validActionPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type relayProxyRequest struct {
	Body         map[string]any `json:"body"`
	NoSinks      bool           `json:"no_sinks,omitempty"`
	Cache        bool           `json:"cache,omitempty"`
	TrackHistory bool           `json:"track_history,omitempty"`
}

// relayActionRequest wraps the RPC action envelope sent to the relay handler.
type relayActionRequest struct {
	Action           ActionRequestAction `json:"action"`
	Input            relayProxyRequest   `json:"input"`
	SessionVariables map[string]any      `json:"session_variables"`
}

// readActions is the allow-list of relay action_names that only read/observe
// cluster or telemetry state and are therefore safe for read-only roles
// (tenant_admin_readonly, account_admin_readonly).
//
// This is intentionally a READ allow-list, not a write deny-list: any
// action_name NOT listed here requires write access (fail-closed). A
// write/exec/job-creating relay action (replica_rightsizing, rightsize_pvc,
// volume_delete, delete_pod, replace_workload, *_bash/_script/_kubectl exec,
// image_scanner, pod_profiler, ...) must never default to read just because
// someone forgot to enumerate it. tenant_admin and account_admin pass write
// checks anyway, so this only bounds the read-only roles.
//
// Only add an action here once you are sure it performs no mutation, no
// exec-in-pod, and creates no job on the cluster or external system.
var readActions = map[string]bool{
	// Live Kubernetes resource reads (PVC/PV/Services/etc. tabs).
	"get_resource":      true,
	"get_resource_yaml": true,
	"get_silences":      true,

	// Metrics / logs / traces / query reads.
	"metrics":                         true,
	"logs":                            true,
	"gke_logs":                        true,
	"cloud_logs":                      true,
	"traces":                          true,
	"traces_dependency_map":           true,
	"db_query":                        true,
	"query_data":                      true,
	"query_es":                        true,
	"query_es_indices":                true,
	"query_esindex_field":             true,
	"query_loki_labels":               true,
	"query_grafana_loki_label_values": true,
	"prometheus_enricher":             true,
	"prometheus_labels":               true,
	"prometheus_queries_enricher":     true,

	// Service maps / stats / cloud reads.
	"service_map":                 true,
	"cloud_service_map":           true,
	"knowledge_graph_service_map": true,
	"application_stats":           true,
	"cloud_resources":             true,
	"cloud_performance_insights":  true,

	// Read-only diagnostic enrichers (gather data, never mutate or exec).
	"event_resource_events_enricher":      true,
	"resource_events_enricher":            true,
	"impacted_services_enricher":          true,
	"job_events_enricher":                 true,
	"job_info_enricher":                   true,
	"job_pod_enricher":                    true,
	"logs_enricher":                       true,
	"node_allocatable_resources_enricher": true,
	"node_running_pods_enricher":          true,
	"node_status_enricher":                true,
	"noisy_neighbours_enricher":           true,
	"oom_killer_enricher":                 true,
	"pod_metric_enricher":                 true,
	"pod_node_metrics_enricher":           true,

	// External observability/incident fetches (read details only).
	"alert_rule_details":           true,
	"chronosphere_query_traces":    true,
	"datadog_error_tracking_issue": true,
	"datadog_event_details":        true,
	"datadog_incident":             true,
	"datadog_metrics":              true,
	"datadog_monitor":              true,
	"datadog_traces":               true,
	"jaeger_query_traces":          true,
	"newrelic_entity_details":      true,
	"newrelic_incident_details":    true,
	"newrelic_incidents":           true,
	"newrelic_issue_details":       true,
	"servicenow_incident":          true,
	"servicenow_incident_enriched": true,
	"solarwinds_alert_details":     true,
	"splunk_alert_details":         true,
}

// requiredPermission returns the minimum access type needed for a given relay
// action_name. Unknown action_names are treated as writes (fail-closed) so a
// read-only role can never invoke a mutating relay action that simply hasn't
// been enumerated.
func requiredPermission(actionName string) security.SecurityAccessType {
	if readActions[actionName] {
		return security.SecurityAccessTypeRead
	}
	return security.SecurityAccessTypeUpdate
}

func validateRelayAction(action string) bool {
	if !validActionPattern.MatchString(action) {
		return false
	}
	return allowedRelayActions[action]
}

func parseRelayIdentity(c *gin.Context, sessionVars map[string]any) (tenantId, userId string, ok bool) {
	tenantId = c.Request.Header.Get("x-tenant-id")
	userId = c.Request.Header.Get("x-user-id")

	// Fall back to session_variables when HTTP headers are absent.
	if tenantId == "" {
		tenantId = sessionString(sessionVars, "tenant_id")
	}
	if userId == "" {
		userId = sessionString(sessionVars, "user_id")
	}

	ok = tenantId != "" && userId != ""
	return
}

func validateRelayAccountAccess(reqPayload *relayProxyRequest, sc *security.SecurityContext) (accountId, actionName string, err error) {
	accountIdVal, ok := reqPayload.Body["account_id"].(string)
	if !ok || accountIdVal == "" {
		return "", "", errors.New("account_id is required and must be a string")
	}
	accountId = accountIdVal

	actionName, _ = reqPayload.Body["action_name"].(string)
	permission := requiredPermission(actionName)

	if !sc.HasAccountAccess(accountId, permission) {
		return "", "", errors.New("access denied")
	}
	return accountId, actionName, nil
}

func forwardRelayRequest(c *gin.Context, action string, reqPayload *relayProxyRequest, userId, tenantId string, reqCtx *security.RequestContext) {
	metricName := "relay_" + action
	relayURL := config.Config.RelayServerEndpoint + "/" + action

	resp, err := common.HttpPost(relayURL,
		common.HttpWithJsonBody(reqPayload),
		common.HttpWithHeaders(map[string]string{
			"Content-Type": "application/json",
			"X-SECRET-KEY": config.Config.RelayServerSecretKey,
			"X-USER-ID":    userId,
			"X-TENANT-ID":  tenantId,
		}),
		common.HttpWithContext(reqCtx.GetContext()),
	)
	if err != nil {
		common.MetricsApiRequestsFailedTotal(reqCtx.GetContext(), metricName, "relay_request_failed")
		reqCtx.GetLogger().Error("relay: failed to forward request", "error", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "relay server unavailable"})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		common.MetricsApiRequestsFailedTotal(reqCtx.GetContext(), metricName, "relay_read_failed")
		reqCtx.GetLogger().Error("relay: failed to read relay response", "error", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to read relay response"})
		return
	}

	c.Data(resp.StatusCode, "application/json", respBody)
}

func handleRelayApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	group := r.Group("/v1/relay")

	group.POST("/:action", func(c *gin.Context) {
		action := c.Param("action")
		metricName := "relay_" + action
		common.MetricsApiRequestsTotal(c.Request.Context(), metricName)

		// Validate action against allowlist
		if !validateRelayAction(action) {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), metricName, "invalid_action")
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid relay action"})
			return
		}

		// Parse RPC action envelope (session_variables + input).
		var actionReq relayActionRequest
		if err := c.ShouldBindJSON(&actionReq); err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), metricName, "invalid_json")
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		reqPayload := actionReq.Input

		// Resolve identity from headers first, then RPC session variables.
		tenantId, userId, ok := parseRelayIdentity(c, actionReq.SessionVariables)
		if !ok {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), metricName, "missing_identity")
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing x-tenant-id or x-user-id headers"})
			return
		}

		securityContext, err := security.NewSecurityContext(tenantId, userId)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), metricName, "invalid_security_context")
			logger.Error("relay: failed to build security context", "error", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid security context"})
			return
		}

		accountId, actionName, err := validateRelayAccountAccess(&reqPayload, securityContext)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), metricName, "access_denied")
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "description": "access denied"})
			return
		}

		span := trace.SpanFromContext(c.Request.Context())
		childLogger := logger.With("service", "relay", "action", action, "action_name", actionName,
			"account_id", accountId, "trace_id", span.SpanContext().TraceID().String())

		reqCtx := security.NewRequestContext(c.Request.Context(), securityContext, childLogger, tracer, meter)
		forwardRelayRequest(c, action, &reqPayload, userId, tenantId, reqCtx)
	})
}
