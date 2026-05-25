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
	"request": true,
	"grafana": true,
}

// validActionPattern ensures the action contains only safe alphanumeric/underscore/hyphen characters.
var validActionPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type relayProxyRequest struct {
	Body         map[string]any `json:"body"`
	NoSinks      bool           `json:"no_sinks,omitempty"`
	Cache        bool           `json:"cache,omitempty"`
	TrackHistory bool           `json:"track_history,omitempty"`
}

// relayActionRequest wraps the Hasura action envelope sent to the relay handler.
type relayActionRequest struct {
	Action           ActionRequestAction `json:"action"`
	Input            relayProxyRequest         `json:"input"`
	SessionVariables map[string]any            `json:"session_variables"`
}

// writeActions are relay action_names that mutate cluster state and require write permission.
var writeActions = map[string]bool{
	"delete_pod":               true,
	"delete_workload":          true,
	"create_workload":          true,
	"replace_workload":         true,
	"rollout_restart":          true,
	"add_silence":              true,
	"delete_silence":           true,
	"pod_script_run_enricher":  true,
	"pod_bash_enricher":        true,
	"kubectl_command_executor": true,
	"nubi_enricher":            true,
}

// requiredPermission returns the minimum access type needed for a given relay action_name.
func requiredPermission(actionName string) security.SecurityAccessType {
	if writeActions[actionName] {
		return security.SecurityAccessTypeUpdate
	}
	return security.SecurityAccessTypeRead
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

		// Parse Hasura action envelope (session_variables + input).
		var actionReq relayActionRequest
		if err := c.ShouldBindJSON(&actionReq); err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), metricName, "invalid_json")
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		reqPayload := actionReq.Input

		// Resolve identity from headers first, then Hasura session variables.
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
