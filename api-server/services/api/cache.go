package api

import (
	"log/slog"
	"nudgebee/services/common"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleCacheApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	groupV2 := r.Group("/v1/cache")
	groupV2.POST("/list_namespaces", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "cache_list_namespaces")
		resp := common.CacheListNamesapces()
		c.JSON(200, resp)
	})

	groupV2.POST("/list_namespace_keys", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "cache_list_namespace_keys")
		payload := map[string]string{}
		err := c.ShouldBindJSON(&payload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "cache_list_namespace_keys", "invalid_json")
			logger.Error("namespace: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := common.CacheListKeys(payload["namespace"])
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "cache_list_namespace_keys", "list_keys_failed")
			logger.Error("namespace: error creating access", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
	})

	groupV2.POST("/invalidate_namespace", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "cache_invalidate_namespace")
		payload := map[string]string{}
		err := c.ShouldBindJSON(&payload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "cache_invalidate_namespace", "invalid_json")
			logger.Error("namespace: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		err = common.CacheClear(payload["namespace"])
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "cache_invalidate_namespace", "clear_cache_failed")
			logger.Error("namespace: error creating access", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, map[string]string{"message": "success"})
	})

	groupV2.POST("/get_namespace_value", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "cache_get_namespace_value")
		payload := map[string]string{}
		err := c.ShouldBindJSON(&payload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "cache_get_namespace_value", "invalid_json")
			logger.Error("cache: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, exists := common.CacheGet(payload["namespace"], payload["key"])
		c.JSON(200, map[string]any{
			"value":  resp,
			"exists": exists,
		})
	})
}
