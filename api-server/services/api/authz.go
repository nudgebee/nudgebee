package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/tenant"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleAuthzApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	groupV2 := r.Group("/v1/authz")
	groupV2.POST("/validate_access", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "authz_validate_access")
		var hasuraPayload tenant.ValidateAccessRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "authz_validate_access", "invalid_json")
			logger.Error("authz: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := tenant.ValidateAccess(hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "authz_validate_access", "validate_access_failed")
			logger.Error("authz: error creating access", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
	})

	groupV2.POST("/get_security_context", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "authz_get_security_context")
		var hasuraPayload tenant.GetSecurityContextRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "authz_get_security_context", "invalid_json")
			logger.Error("authz: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := tenant.GetSecurityContext(hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "authz_get_security_context", "get_security_context_failed")
			logger.Error("authz: error creating access", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
	})

	groupV2.POST("/get_k8s_roles", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "authz_get_k8s_roles")
		var hasuraPayload tenant.GetK8sRolesRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "authz_get_k8s_roles", "invalid_json")
			logger.Error("authz: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := tenant.GetK8sRoles(hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "authz_get_k8s_roles", "get_k8s_roles_failed")
			logger.Error("authz: error creating access", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
	})

	groupV2.POST("/get_k8s_object_names", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "authz_get_k8s_object_names")
		var hasuraPayload tenant.GetK8sObjectNamesRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "authz_get_k8s_object_names", "invalid_json")
			logger.Error("authz: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := tenant.GetK8sObjectNames(hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "authz_get_k8s_object_names", "get_k8s_object_names_failed")
			logger.Error("authz: error creating access", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
	})
}
