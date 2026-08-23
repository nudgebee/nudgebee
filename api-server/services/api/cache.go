package api

import (
	"log/slog"
	"nudgebee/services/common"
	"strings"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// collectInvalidateNamespaces extracts the namespace set from a flexible
// payload: accepts either {"namespace": "x"} (legacy single form) or
// {"namespaces": ["a", "b"]} (list form). Returns deduped, trimmed,
// non-empty entries; both shapes can coexist in one request.
func collectInvalidateNamespaces(payload map[string]any) []string {
	seen := make(map[string]bool)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
	}
	if v, ok := payload["namespace"].(string); ok {
		add(v)
	}
	if arr, ok := payload["namespaces"].([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok {
				add(s)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	return out
}

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
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := common.CacheListKeys(payload["namespace"])
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "cache_list_namespace_keys", "list_keys_failed")
			logger.Error("namespace: error creating access", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
	})

	// POST /v1/cache/invalidate_namespace — wipes shared Redis keys for one
	// or more cache namespaces. Accepts either {"namespace": "name"} (legacy
	// single-namespace shape) or {"namespaces": ["a", "b"]} (list form, new).
	//
	// Generic by design: api-server does direct Redis SCAN+DEL on the
	// "<namespace>:*" prefix, so this endpoint can clear caches owned by
	// any service (api-server, llm-server, future services) given they
	// share the platform Redis. The endpoint has zero hard-coded knowledge
	// of what's in those keys.
	//
	// Under cache_provider != "redis" the SCAN+DEL is a no-op (per-process
	// bigcache layers in other services can't be reached from here); for
	// namespaces registered in THIS process the legacy tag-based CacheClear
	// path runs too, so api-server's own caches still wipe.
	groupV2.POST("/invalidate_namespace", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "cache_invalidate_namespace")
		payload := map[string]any{}
		if err := c.ShouldBindJSON(&payload); err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "cache_invalidate_namespace", "invalid_json")
			logger.Error("invalidate_namespace: error binding request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		namespaces := collectInvalidateNamespaces(payload)
		if len(namespaces) == 0 {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "cache_invalidate_namespace", "missing_namespace")
			c.JSON(400, common.ErrorActionBadRequest("namespace or namespaces required"))
			return
		}

		counts := make(map[string]int, len(namespaces))
		var firstErr error
		total := 0
		for _, ns := range namespaces {
			// Legacy: clear in-process tag-based entries for namespaces
			// registered in api-server (best-effort; an unregistered namespace
			// just errors which we swallow — the Redis sweep below covers it).
			_ = common.CacheClear(ns)
			// Generic: direct Redis SCAN+DEL on the namespace prefix.
			n, err := common.CacheRedisDeleteNamespace(ns)
			counts[ns] = n
			total += n
			if err != nil && firstErr == nil {
				firstErr = err
				logger.Error("invalidate_namespace: redis sweep had errors", "namespace", ns, "error", err)
			}
		}

		if firstErr != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "cache_invalidate_namespace", "redis_sweep_failed")
			c.JSON(500, gin.H{"message": "partial", "per_namespace": counts, "total": total, "error": firstErr.Error()})
			return
		}
		logger.Info("invalidate_namespace: cleared", "per_namespace", counts, "total", total)
		c.JSON(200, gin.H{"message": "success", "per_namespace": counts, "total": total})
	})

	groupV2.POST("/get_namespace_value", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "cache_get_namespace_value")
		payload := map[string]string{}
		err := c.ShouldBindJSON(&payload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "cache_get_namespace_value", "invalid_json")
			logger.Error("cache: error binding request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, exists := common.CacheGet(payload["namespace"], payload["key"])
		c.JSON(200, map[string]any{
			"value":  resp,
			"exists": exists,
		})
	})
}
