package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"nudgebee/relay-server/pkg/config"
	"nudgebee/relay-server/pkg/db"
	"nudgebee/relay-server/pkg/models"
	"nudgebee/relay-server/pkg/mq"
	"nudgebee/relay-server/pkg/server/middleware"
	"nudgebee/relay-server/pkg/utils"
)

func HandlePrometheusApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger, store db.AgentStore, cfg *config.Config, rpcClient mq.RPCClient,
) {

	r.GET("/prometheus/api/v1/query", middleware.PrometheusAuthMiddleware(cfg.Security.SecretKey), func(c *gin.Context) {
		handlePrometheusRequest(c, "query", logger, store, cfg, rpcClient)
	})

	r.POST("/prometheus/api/v1/query", middleware.PrometheusAuthMiddleware(cfg.Security.SecretKey), func(c *gin.Context) {
		handlePrometheusRequest(c, "query", logger, store, cfg, rpcClient)
	})

	r.GET("/prometheus/api/v1/query_range", middleware.PrometheusAuthMiddleware(cfg.Security.SecretKey), func(c *gin.Context) {
		handlePrometheusRequest(c, "query_range", logger, store, cfg, rpcClient)
	})

	r.POST("/prometheus/api/v1/query_range", middleware.PrometheusAuthMiddleware(cfg.Security.SecretKey), func(c *gin.Context) {
		handlePrometheusRequest(c, "query_range", logger, store, cfg, rpcClient)
	})

	r.GET("/prometheus/api/v1/series", middleware.PrometheusAuthMiddleware(cfg.Security.SecretKey), func(c *gin.Context) {
		handlePrometheusRequest(c, "series", logger, store, cfg, rpcClient)
	})

	r.POST("/prometheus/api/v1/series", middleware.PrometheusAuthMiddleware(cfg.Security.SecretKey), func(c *gin.Context) {
		handlePrometheusRequest(c, "series", logger, store, cfg, rpcClient)
	})

	r.GET("/prometheus/api/v1/labels", middleware.PrometheusAuthMiddleware(cfg.Security.SecretKey), func(c *gin.Context) {
		handlePrometheusRequest(c, "labels", logger, store, cfg, rpcClient)
	})

	r.POST("/prometheus/api/v1/labels", middleware.PrometheusAuthMiddleware(cfg.Security.SecretKey), func(c *gin.Context) {
		handlePrometheusRequest(c, "labels", logger, store, cfg, rpcClient)
	})

	r.GET("/prometheus/api/v1/label/:label_name/values", middleware.PrometheusAuthMiddleware(cfg.Security.SecretKey), func(c *gin.Context) {
		handlePrometheusRequest(c, "label_values", logger, store, cfg, rpcClient)
	})
}

func handlePrometheusRequest(c *gin.Context, requestType string, logger *slog.Logger, store db.AgentStore, cfg *config.Config, rpcClient mq.RPCClient) {
	// accountID is now set by the PrometheusAuthMiddleware
	accountID, exists := c.Get(middleware.CtxAccountID)
	if !exists {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "accountID not found in context"})
		return
	}

	logger = logger.With("account_id", accountID)

	// Combine URL query parameters and form parameters. Form parameters take precedence for duplicate keys.
	requestParams := c.Request.URL.Query()
	if err := c.Request.ParseForm(); err == nil {
		for k, v := range c.Request.Form {
			requestParams[k] = v // This will overwrite if key exists in query, or add if new
		}
	}

	now := time.Now().UTC()
	const backendFormat = "2006-01-02 15:04:05 UTC"

	startTime := parsePromTime(requestParams.Get("start"), now.Add(-1*time.Hour), logger)
	endTime := parsePromTime(requestParams.Get("end"), now, logger)

	// For query (not query_range), if 'time' is present, it overrides start/end.
	if timeStr := requestParams.Get("time"); timeStr != "" {
		tm := parsePromTime(timeStr, now, logger)
		startTime = tm
		endTime = tm
	}

	formattedStartTime := startTime.Format(backendFormat)
	formattedEndTime := endTime.Format(backendFormat)

	actionName := ""
	var actionParams map[string]any

	switch requestType {
	case "query":
		actionName = "prometheus_queries_enricher"
		actionParams = map[string]any{
			"promql_query": "",
			"step":         "",
			"instant":      true,
			"promql_queries": []map[string]string{
				{
					"key":   "query",
					"query": requestParams.Get("query"),
				},
			},
			"duration": map[string]string{
				"starts_at": formattedStartTime,
				"ends_at":   formattedEndTime,
			},
		}
	case "query_range":
		// Calculate step if not provided
		queryDuration := endTime.Sub(startTime)
		resolution := utils.GetResolutionFromDuration(queryDuration)
		calculatedStep := utils.CalculateStep(startTime, endTime, requestParams.Get("step"), resolution)

		actionName = "prometheus_queries_enricher"
		actionParams = map[string]any{
			"promql_query": "",
			"step":         calculatedStep,
			"instant":      false,
			"promql_queries": []map[string]string{
				{
					"key":   "query",
					"query": requestParams.Get("query"),
				},
			},
			"duration": map[string]string{
				"starts_at": formattedStartTime,
				"ends_at":   formattedEndTime,
			},
		}
	case "series":
		actionName = "prometheus_labels"
		actionParams = map[string]any{
			"label_name": "__name__",
		}
	case "labels":
		actionName = "prometheus_labels"
		actionParams = map[string]any{
			"label_name": requestParams["match[]"],
		}
	default:
		logger.Error("unknown prometheus request type", "type", requestType)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Internal server error: unknown request type"})
		return
	}

	body := map[string]any{
		"no_sinks": true,
		"body": map[string]any{
			"account_id":    accountID,
			"action_name":   actionName,
			"action_params": actionParams,
			"origin":        "Relay Prometheus API",
		},
		"request_id": uuid.NewString(),
	}

	rawBody, _ := json.Marshal(body)

	accountIDStr, ok := accountID.(string)
	if !ok {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "accountID in context is not a string"})
		return
	}

	processRequest(c, accountIDStr, logger, store, rawBody, cfg, rpcClient, requestType)
}

func processRequest(c *gin.Context, accountID string, logger *slog.Logger, store db.AgentStore, rawBody []byte, cfg *config.Config, rpcClient mq.RPCClient, requestType string,
) {
	c.Set(middleware.CtxAccountID, accountID)
	ctx := c.Request.Context()

	// Prometheus always goes through k8s agent
	agentType := "k8s"

	// Single DB call to get all agent status info
	connected, wsEnabled, fallbackURL, prometheusAdditionalLabel, err := store.GetAgentStatus(ctx, accountID, agentType)
	if err != nil {
		logger.Error("failed to check agent status", "account_id", accountID, "err", err)
		c.JSON(500, utils.BuildError(500, "internal server error"))
		return
	}
	if !connected {
		logger.Info("agent not connected", "account", accountID)
		c.JSON(400, utils.BuildError(400, "agent not connected"))
		return
	}

	requestID := gjson.GetBytes(rawBody, "request_id").String()
	actionName := gjson.GetBytes(rawBody, "body.action_name").String()
	// Add timestamp and request_id if missing (modify raw JSON)
	modifiedBodyStr := string(rawBody)
	if requestID == "" {
		requestID = strconv.FormatInt(time.Now().UnixNano(), 10)
		modifiedBodyStr, _ = sjson.Set(modifiedBodyStr, "request_id", requestID)
	}

	timestamp := time.Now().Unix()
	modifiedBodyStr, _ = sjson.Set(modifiedBodyStr, "body.timestamp", timestamp)

	if actionName == "prometheus_queries_enricher" || actionName == "prometheus_enricher" || actionName == "application_stats" || actionName == "slo_generator" {
		if prometheusAdditionalLabel != "" {
			// double escaping because of JSON
			prometheusAdditionalLabel = strings.ReplaceAll(prometheusAdditionalLabel, "\"", "\\\"")
			prometheusAdditionalLabel = prometheusAdditionalLabel + " , "
		}
		modifiedBodyStr = strings.ReplaceAll(modifiedBodyStr, "__CLUSTER__", prometheusAdditionalLabel)
	}

	modifiedBody := []byte(modifiedBodyStr)
	if !wsEnabled {
		// For fallback, we need the full request object
		var req models.ExternalActionRequest
		if err := json.Unmarshal(modifiedBody, &req); err != nil {
			c.JSON(400, utils.BuildError(400, "invalid JSON for fallback"))
			return
		}
		utils.FallbackPost(c, *logger, fallbackURL, req)
		return
	}

	// Use modified raw body as payload (zero-copy!)
	payload := modifiedBody
	rk := mq.RelayQueueName(accountID, agentType)

	// Log action execution for tracking
	logger.Info("executing action",
		"action", actionName,
		"corr_id", requestID)

	ctx, cancel := context.WithTimeout(ctx, cfg.HTTP.WriteTimeout)
	defer cancel()

	// Extract once for error logging
	actionParams := gjson.GetBytes(rawBody, "body.action_params")

	logger.Info("publishing RPC", "corr", requestID, "rk", rk)
	resp, err := rpcClient.Call(ctx, cfg.RabbitMQ.ExchangeName, rk, payload, requestID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || ctx.Err() != nil {
			logger.Error("RPC timeout", "corr", requestID, "action_name", actionName, "action_params", actionParams.String(), "err", err)
			c.JSON(504, utils.BuildError(504, "timeout waiting for agent"))
		} else {
			logger.Error("RPC error", "corr", requestID, "action_name", actionName, "action_params", actionParams.String(), "err", err)
			c.JSON(500, utils.BuildError(500, "RPC error"))
		}
		return
	}

	resp, err = processRelayResponsePayload(resp, requestType)
	if err != nil {
		logger.Error("error processing response payload", "err", err)
		c.JSON(500, utils.BuildError(500, "error processing response payload"))
		return
	}

	if _, err := c.Writer.Write(resp); err != nil {
		logger.Error("error writing response", "corr", requestID, "action_name", actionName, "action_params", actionParams.String(), "err", err)
		c.JSON(500, utils.BuildError(500, "error writing response"))
		return
	}

}

func transformToPrometheusValues(seriesList []any) ([]any, error) {
	var newSeriesList []any
	for _, seriesItem := range seriesList {
		seriesMap, ok := seriesItem.(map[string]any)
		if !ok {
			return nil, errors.New("invalid series item format")
		}

		// Check if the format is already the prometheus one
		if _, ok := seriesMap["values"].([][]any); ok {
			newSeriesList = append(newSeriesList, seriesMap)
			continue
		}

		timestamps, okTs := seriesMap["timestamps"].([]any)
		values, okVal := seriesMap["values"].([]any)

		if !okTs || !okVal {
			if _, ok := seriesMap["values"].([][]any); ok {
				newSeriesList = append(newSeriesList, seriesMap)
				continue
			}
			return nil, errors.New("missing or invalid 'timestamp' or 'values' in series")
		}

		if len(timestamps) != len(values) {
			return nil, errors.New("mismatch between number of timestamps and values")
		}

		var promValues [][]any
		for i := range timestamps {
			tsStr, ok := timestamps[i].(string)
			if !ok {
				if tsFloat, ok := timestamps[i].(float64); ok {
					promValues = append(promValues, []any{tsFloat, values[i]})
					continue
				}
				return nil, fmt.Errorf("timestamp is not a string or float64: %T", timestamps[i])
			}

			ts, err := strconv.ParseFloat(tsStr, 64)
			if err != nil {
				return nil, fmt.Errorf("could not parse timestamp string: %w", err)
			}
			promValues = append(promValues, []any{ts, values[i]})
		}

		delete(seriesMap, "timestamps") // remove old timestamp field
		seriesMap["values"] = promValues
		newSeriesList = append(newSeriesList, seriesMap)
	}
	return newSeriesList, nil
}

func processRelayResponsePayload(resp []byte, requestType string) ([]byte, error) {
	// Use gjson to traverse the nested response without unmarshalling the full payload.
	// Structure: data.success, data.findings[0].evidence[0].data → JSON string → [0].data → JSON string
	errMsg := errors.New("relay: unable to execute relay query")

	success := gjson.GetBytes(resp, "data.success")
	if success.Exists() && !success.Bool() {
		slog.Error("relay: relay query success is false")
		return nil, errMsg
	}

	evidenceData := gjson.GetBytes(resp, "data.findings.0.evidence.0.data")
	if !evidenceData.Exists() {
		slog.Error("relay: evidence data not found in response path data.findings.0.evidence.0.data")
		return nil, errMsg
	}

	// evidence.data is a JSON string containing an array; parse and get first element's "data" field
	innerData := gjson.Parse(evidenceData.String()).Get("0.data")
	if !innerData.Exists() {
		slog.Error("relay: no data in evidence array")
		return nil, errMsg
	}

	// innerData is itself a JSON string; parse it to get the final payload
	mapData := gjson.Parse(innerData.String())
	if !mapData.Exists() {
		slog.Error("relay: failed to parse inner data")
		return nil, errMsg
	}

	if requestType == "query" || requestType == "query_range" {
		resultType := "vector"
		var result any

		if requestType == "query_range" {
			resultType = "matrix"
			seriesListRaw := mapData.Get("query.series_list_result")
			if !seriesListRaw.Exists() {
				return nil, errors.New("series_list_result not found")
			}
			// transformToPrometheusValues needs []any, so unmarshal just this slice
			var seriesList []any
			if err := json.Unmarshal([]byte(seriesListRaw.Raw), &seriesList); err != nil {
				return nil, fmt.Errorf("failed to parse series_list_result: %w", err)
			}
			transformedResult, err := transformToPrometheusValues(seriesList)
			if err != nil {
				slog.Error("failed to transform query_range result", "err", err)
				return nil, err
			}
			result = transformedResult
		} else {
			result = mapData.Get("query").Value()
		}

		response := map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": resultType,
				"result":     result,
			},
			"stats": map[string]any{},
		}
		return json.Marshal(response)
	}

	response := map[string]any{
		"status": "success",
		"data":   mapData.Get("data").Value(),
		"stats":  map[string]any{},
	}
	return json.Marshal(response)
}

func parsePromTime(timeStr string, defaultTime time.Time, logger *slog.Logger) time.Time {
	if timeStr == "" {
		return defaultTime
	}
	// Try parsing as float (unix timestamp) first
	if t, err := strconv.ParseFloat(timeStr, 64); err == nil {
		return time.Unix(int64(t), 0).UTC()
	}
	// Fallback to RFC3339
	if t, err := time.Parse(time.RFC3339, timeStr); err == nil {
		return t.UTC()
	}
	// if all fails, log and return default
	logger.Warn("could not parse time, defaulting", "time", timeStr)
	return defaultTime
}
