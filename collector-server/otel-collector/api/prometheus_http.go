package api

import (
	"log/slog"
	"net/http"
	"nudgebee/collector/otel/config"
	"nudgebee/collector/otel/metrics"
	"nudgebee/collector/otel/metrics/prometheus"
	"nudgebee/collector/otel/security"
	"strings"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handlePrometheusApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {

	r.GET("/prometheus/api/v1/query", func(c *gin.Context) {
		handlePrometheusRequest(c, "query", logger)
	})

	r.POST("/prometheus/api/v1/query", func(c *gin.Context) {
		handlePrometheusRequest(c, "query", logger)
	})

	r.GET("/prometheus/api/v1/query_range", func(c *gin.Context) {
		handlePrometheusRequest(c, "query_range", logger)
	})

	r.POST("/prometheus/api/v1/query_range", func(c *gin.Context) {
		handlePrometheusRequest(c, "query_range", logger)
	})

	r.GET("/prometheus/api/v1/format_query", func(c *gin.Context) {
		handlePrometheusRequest(c, "format_query", logger)
	})

	r.POST("/prometheus/api/v1/format_query", func(c *gin.Context) {
		handlePrometheusRequest(c, "format_query", logger)
	})

	r.GET("/prometheus/api/v1/series", func(c *gin.Context) {
		handlePrometheusRequest(c, "series", logger)
	})

	r.POST("/prometheus/api/v1/series", func(c *gin.Context) {
		handlePrometheusRequest(c, "series", logger)
	})

	r.GET("/prometheus/api/v1/labels", func(c *gin.Context) {
		handlePrometheusRequest(c, "labels", logger)
	})

	r.POST("/prometheus/api/v1/labels", func(c *gin.Context) {
		handlePrometheusRequest(c, "labels", logger)
	})

	r.GET("/prometheus/api/v1/label/:label_name/values", func(c *gin.Context) {
		handlePrometheusRequest(c, "label_values", logger)
	})
}

func handlePrometheusRequest(c *gin.Context, requestType string, logger *slog.Logger) {
	metricsQueryEndpoint := config.Config.OtelMetricsQueryEndpoint
	if metricsQueryEndpoint == "" {
		logger.Error("prometheus_endpoint is not configured")
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Internal Error"})
		return
	}
	var reader metrics.MetricsReader = prometheus.NewReader(metricsQueryEndpoint)

	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	authHeaderSplits := strings.Split(authHeader, " ")
	if len(authHeaderSplits) != 2 || authHeaderSplits[0] != "Basic" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	agentDetail, err := security.GetAccountFromAgentToken(authHeaderSplits[1])
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	logger = logger.With("tenant_id", agentDetail.TenantId, "account_id", agentDetail.AccountId)

	// Combine URL query parameters and form parameters. Form parameters take precedence for duplicate keys.
	requestParams := c.Request.URL.Query()
	if err := c.Request.ParseForm(); err == nil {
		for k, v := range c.Request.Form {
			requestParams[k] = v // This will overwrite if key exists in query, or add if new
		}
	}

	var resp metrics.MetricsResponse

	switch requestType {
	case "query":
		params := metrics.QueryParams{
			Query:   requestParams.Get("query"),
			Time:    requestParams.Get("time"),
			Timeout: requestParams.Get("timeout"),
		}
		resp = reader.Query(agentDetail, logger, params)
	case "query_range":
		params := metrics.QueryRangeParams{
			Query:   requestParams.Get("query"),
			Start:   requestParams.Get("start"),
			End:     requestParams.Get("end"),
			Step:    requestParams.Get("step"),
			Timeout: requestParams.Get("timeout"),
		}
		resp = reader.QueryRange(agentDetail, logger, params)
	case "format_query":
		params := metrics.FormatQueryParams{
			Query: requestParams.Get("query"),
		}
		resp = reader.FormatQuery(agentDetail, logger, params)
	case "series":
		params := metrics.SeriesParams{
			Matchers: requestParams["match[]"], // Get all 'match[]'
			Start:    requestParams.Get("start"),
			End:      requestParams.Get("end"),
			Limit:    requestParams.Get("limit"),
		}
		resp = reader.Series(agentDetail, logger, params)
	case "labels":
		params := metrics.LabelsParams{
			Matchers: requestParams["match[]"], // Get all 'match[]'
			Start:    requestParams.Get("start"),
			End:      requestParams.Get("end"),
			Limit:    requestParams.Get("limit"),
		}
		resp = reader.Labels(agentDetail, logger, params)
	case "label_values":
		labelName := c.Param("label_name")
		if labelName == "" {
			logger.Error("label_name parameter missing from path for label_values request")
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "label_name path parameter is required"})
			return
		}
		params := metrics.LabelValuesParams{
			Matchers: requestParams["match[]"], // Get all 'match[]'
			Start:    requestParams.Get("start"),
			End:      requestParams.Get("end"),
			Limit:    requestParams.Get("limit"),
		}
		resp = reader.LabelValues(agentDetail, logger, labelName, params)
	default:
		logger.Error("unknown prometheus request type", "type", requestType)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Internal server error: unknown request type"})
		return
	}

	// Process the response
	if resp.Error != nil {
		// Use the status code from the response if available and non-zero, otherwise default
		statusCode := resp.StatusCode
		if statusCode == 0 {
			statusCode = http.StatusInternalServerError // Default error status
		}
		c.AbortWithStatusJSON(statusCode, gin.H{"error": resp.Error.Error()})
		return
	}
	c.Data(resp.StatusCode, resp.ContentType, resp.Body)
}
