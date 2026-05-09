package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/license"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strings"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type HasuraActionRequestAction struct {
	Name string `json:"name"`
}

type HasuraActionRequestQuery struct {
	Query query.QueryRequest `json:"query"`
}

type HasuraActionRequest struct {
	Action           HasuraActionRequestAction `json:"action"`
	Input            map[string]any            `json:"input"`
	RequestQuery     string                    `json:"request_query"`
	SessionVariables map[string]any            `json:"session_variables"`
}

func buildContextFromHasuraPayload(c *gin.Context, h *HasuraActionRequest, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) (*security.RequestContext, error) {
	var ctx context.Context
	if c != nil && c.Request != nil && c.Request.Context() != nil {
		ctx = c.Request.Context()
	} else {
		ctx = context.Background()
	}

	// no need to handle role as its calculated by security context
	tenantId := ""
	userId := ""
	if c != nil && c.Request != nil {
		tenantId = c.Request.Header.Get("x-tenant-id")
		userId = c.Request.Header.Get("x-user-id")
	}

	if tenantId == "" && h.SessionVariables["x-hasura-user-tenant-id"] != nil {
		tenantId = h.SessionVariables["x-hasura-user-tenant-id"].(string)
	}

	if userId == "" && h.SessionVariables["x-hasura-user-id"] != nil {
		userId = h.SessionVariables["x-hasura-user-id"].(string)
	}

	var securityContext *security.SecurityContext
	var err error
	if h.SessionVariables["x-hasura-role"] == "admin" && tenantId == "" && userId == "" {
		securityContext = security.NewSecurityContextForSuperAdmin()
	} else if tenantId != "" && userId == "" {
		securityContext = security.NewSecurityContextForSuperAdminAndTenant(tenantId)
	} else {
		securityContext, err = security.NewSecurityContext(tenantId, userId)
		if err != nil {
			logger.Error("error creating security context", "error", err)
			return nil, err
		}
	}

	// If x-hasura-allowed-roles contains exact super_admin role, mark context as super admin
	allowedRoles, _ := h.SessionVariables["x-hasura-allowed-roles"].(string)
	for _, role := range strings.Split(strings.Trim(allowedRoles, "{}"), ",") {
		if strings.TrimSpace(role) == security.AUTH_SUPER_ADMIN_FULL_ROLE {
			securityContext.AddRole(security.AUTH_SUPER_ADMIN_FULL_ROLE)
			break
		}
	}

	span := trace.SpanFromContext(ctx)
	childLogger := logger.With("tenant_id", tenantId, "user_id", userId, "trace_id", span.SpanContext().TraceID().String())
	return security.NewRequestContext(ctx, securityContext, childLogger, tracer, meter), nil
}

func handleHasuraApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	groupV2 := r.Group("/hasura")

	groupV2.POST("/ml", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "ml")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "ml", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraMlAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/query", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "query")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "query", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest("invalid json - "+err.Error()))
			return
		}

		handleHasuraQueryAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/promql/parser", func(ctx *gin.Context) {
		common.MetricsApiRequestsTotal(ctx.Request.Context(), "promql_parser")
		var hasuraPayload HasuraActionRequest
		err := ctx.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(ctx.Request.Context(), "promql_parser", "invalid_json")
			ctx.JSON(400, common.ErrorHasuraActionBadRequest("invalid json - "+err.Error()))
			return
		}
		handleHasuraPromQL(&hasuraPayload, ctx, tracer, meter, logger)
	})

	groupV2.POST("/recommendation", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "recommendation")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "recommendation", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		handleHasuraRecommendationAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/account", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "account")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "account", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		handleHasuraAccountAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/tenant", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "tenant")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "tenant", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		handleHasuraTenantAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/user", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "user")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "user", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		handleHasuraUserAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/nb", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "nb")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "nb", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraNbAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/cloud", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "cloud")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "cloud", "invalid_json")
			c.JSON(400, []common.Error{
				{
					Message: err.Error(),
				},
			})
			return
		}
		handleHasuraCloudAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/eventrule", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "eventrule")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "eventrule", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraEventRuleAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/slo", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "slo")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "slo", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraSloAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/event", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "event")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "event", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraEventAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/pr-raise", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "pr_raise")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraPRRaiseAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/application", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "application")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleApplicationEvent(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/ai", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "ai")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraAiFeedbackAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/insights", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "insights")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraInsights(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/integration", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "integration")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraIntegrationAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/anomaly", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "anomaly")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraAnomaly(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/license", func(c *gin.Context) {
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		license.CheckLimits(c, tracer, meter, logger)
	})

	groupV2.POST("/logs", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "logs")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraLogsAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/metrics", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "metrics")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraMetricsAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/cluster-health", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "nb")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "nb", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraClusterHealthAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/k8s-upgrade", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "nb")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "nb", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraUpgradePlanAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/provider", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "provider")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "nb", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraAccountProviderAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/traces", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "traces")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "nb", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraTracesAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/user-history", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "user-history")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "nb", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraAccountUserHistoryAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/knowledge-graph", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "knowledge_graph")

		// Read raw body to allow parsing as either request type
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "read_body_error")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		var hasuraPayload HasuraActionRequest
		if err := json.Unmarshal(bodyBytes, &hasuraPayload); err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		// If action name is blank, try parsing as HasuraCronRequest
		if hasuraPayload.Action.Name == "" {
			var cronPayload HasuraCronRequest
			if err := json.Unmarshal(bodyBytes, &cronPayload); err == nil && cronPayload.Name != "" {
				hasuraPayload.Action.Name = cronPayload.Name
				hasuraPayload.Input = cronPayload.Payload
			}
		}

		handleHasuraKnowledgeGraphAction(&hasuraPayload, c, tracer, meter, logger)
	})

	groupV2.POST("/logs-group", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "logs-group")
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		handleHasuraLogsAction(&hasuraPayload, c, tracer, meter, logger)
	})
}
