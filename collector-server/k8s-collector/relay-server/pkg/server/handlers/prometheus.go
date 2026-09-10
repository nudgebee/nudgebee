package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unsafe"

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
		c.JSON(http.StatusServiceUnavailable, utils.BuildError(http.StatusServiceUnavailable, "agent not connected"))
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

// appendPrometheusSeries appends one series from the agent's
// {metric, timestamps, values} shape as Prometheus' {metric, values:[[ts,val]]}
// shape, copying every field it does not have to rewrite straight out of the
// source JSON. tsBuf is a scratch slice reused across series; the grown slice
// is returned so the caller can hand it back on the next call.
func appendPrometheusSeries(dst []byte, tsBuf []gjson.Result, series gjson.Result) ([]byte, []gjson.Result, error) {
	if !series.IsObject() {
		return dst, tsBuf, errors.New("invalid series item format")
	}

	timestamps := series.Get("timestamps")
	values := series.Get("values")
	if !timestamps.IsArray() || !values.IsArray() {
		return dst, tsBuf, errors.New("missing or invalid 'timestamp' or 'values' in series")
	}

	timestamps.ForEach(func(_, ts gjson.Result) bool {
		tsBuf = append(tsBuf, ts)
		return true
	})

	dst = append(dst, '{')
	// Everything other than the two arrays we zip is copied verbatim.
	series.ForEach(func(key, value gjson.Result) bool {
		if k := key.Str; k == "timestamps" || k == "values" {
			return true
		}
		dst = append(dst, key.Raw...)
		dst = append(dst, ':')
		dst = append(dst, value.Raw...)
		dst = append(dst, ',')
		return true
	})

	dst = append(dst, `"values":`...)
	valuesStart := len(dst)
	dst = append(dst, '[')

	var (
		i      int
		valErr error
	)
	values.ForEach(func(_, value gjson.Result) bool {
		if i >= len(tsBuf) {
			valErr = errors.New("mismatch between number of timestamps and values")
			return false
		}
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, '[')
		if dst, valErr = appendPrometheusTimestamp(dst, tsBuf[i]); valErr != nil {
			return false
		}
		dst = append(dst, ',')
		dst = append(dst, value.Raw...)
		dst = append(dst, ']')
		i++
		return true
	})
	if valErr != nil {
		return dst, tsBuf, valErr
	}
	if i != len(tsBuf) {
		return dst, tsBuf, errors.New("mismatch between number of timestamps and values")
	}

	if i == 0 {
		// The previous implementation built the sample list with append onto a
		// nil slice, so a series with no samples marshalled to null rather than
		// []. Keep that on the wire.
		dst = append(dst[:valuesStart], "null"...)
	} else {
		dst = append(dst, ']')
	}
	return append(dst, '}'), tsBuf, nil
}

// appendPrometheusTimestamp writes a sample timestamp as a JSON number. The
// agent sends them either as numbers or as decimal strings; both used to land
// in a float64 and be rendered by encoding/json, so render them the same way.
func appendPrometheusTimestamp(dst []byte, ts gjson.Result) ([]byte, error) {
	switch ts.Type {
	case gjson.Number:
		return appendJSONFloat(dst, ts.Num)
	case gjson.String:
		f, err := strconv.ParseFloat(ts.Str, 64)
		if err != nil {
			return dst, fmt.Errorf("could not parse timestamp string: %w", err)
		}
		return appendJSONFloat(dst, f)
	default:
		return dst, fmt.Errorf("timestamp is not a string or float64: %s", ts.Type)
	}
}

// appendJSONFloat renders f exactly as encoding/json's float encoder does.
func appendJSONFloat(dst []byte, f float64) ([]byte, error) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return dst, fmt.Errorf("unsupported timestamp value: %v", f)
	}
	format := byte('f')
	if abs := math.Abs(f); abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		format = 'e'
	}
	dst = strconv.AppendFloat(dst, f, format, -1, 64)
	if format == 'e' {
		// clean up e-09 to e-9, the same fixup encoding/json applies
		if n := len(dst); n >= 4 && dst[n-4] == 'e' && dst[n-3] == '-' && dst[n-2] == '0' {
			dst[n-2] = dst[n-1]
			dst = dst[:n-1]
		}
	}
	return dst, nil
}

// processRelayResponsePayload reshapes the agent's reply into the Prometheus
// HTTP API response.
//
// It copies raw JSON out of the reply wherever it does not have to rewrite the
// value, instead of decoding into map[string]any and re-marshalling. That round
// trip used to dominate this handler: on a 50 MB query_range reply it allocated
// ~630 MB, which held the process in continuous GC (56% of relay CPU) and was
// the bulk of the response latency.
//
// Copying raw leaves three JSON-equivalent differences from the old output:
// object keys keep their source order rather than being sorted, `<`, `>` and `&`
// inside strings are no longer escaped to their \u00XX forms, and numeric sample
// values are no longer round-tripped through float64.
func processRelayResponsePayload(resp []byte, requestType string) ([]byte, error) {
	// Use gjson to traverse the nested response without unmarshalling the full payload.
	// Structure: data.success, data.findings[0].evidence[0].data → JSON string → [0].data → JSON string
	errMsg := errors.New("relay: unable to execute relay query")

	// gjson.GetBytes copies the matched Raw and Str out of the caller's buffer
	// for safety, which on a 60 MB reply means copying the whole escaped
	// evidence blob we never look at. Read through a string view instead: resp
	// is the AMQP delivery body, nobody mutates it, and every Raw we keep is
	// appended (copied) into the response buffer before this returns.
	respStr := unsafe.String(unsafe.SliceData(resp), len(resp))

	success := gjson.Get(respStr, "data.success")
	if success.Exists() && !success.Bool() {
		slog.Error("relay: relay query success is false")
		return nil, errMsg
	}

	evidenceData := gjson.Get(respStr, "data.findings.0.evidence.0.data")
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

	switch requestType {
	case "query_range":
		seriesList := mapData.Get("query.series_list_result")
		if !seriesList.Exists() {
			return nil, errors.New("series_list_result not found")
		}
		if seriesList.Type != gjson.Null && !seriesList.IsArray() {
			return nil, errors.New("failed to parse series_list_result: not an array")
		}
		// gjson's ForEach yields a non-container result once, as itself, so a
		// null series list has to skip iteration rather than fall into it.
		isArray := seriesList.IsArray()

		// The emitted matrix carries the same samples as the source, so size the
		// buffer off the source rather than letting append double a
		// multi-megabyte slice. Measured output runs ~5% over the source, so
		// 12.5% headroom keeps this to a single allocation.
		out := make([]byte, 0, len(seriesList.Raw)+len(seriesList.Raw)/8+promEnvelopeSlack)
		out = append(out, `{"data":{"result":`...)
		resultStart := len(out)
		out = append(out, '[')

		var (
			tsBuf     []gjson.Result
			seriesErr error
			count     int
		)
		if isArray {
			seriesList.ForEach(func(_, series gjson.Result) bool {
				if count > 0 {
					out = append(out, ',')
				}
				out, tsBuf, seriesErr = appendPrometheusSeries(out, tsBuf[:0], series)
				if seriesErr != nil {
					return false
				}
				count++
				return true
			})
		}
		if seriesErr != nil {
			slog.Error("failed to transform query_range result", "err", seriesErr)
			return nil, seriesErr
		}

		if count == 0 {
			// Matches the old nil-slice marshalling: an empty or null series
			// list came out as null, not [].
			out = append(out[:resultStart], "null"...)
		} else {
			out = append(out, ']')
		}
		return append(out, `,"resultType":"matrix"},"stats":{},"status":"success"}`...), nil

	case "query":
		return appendPrometheusEnvelope(
			[]byte(`{"data":{"result":`), mapData.Get("query"),
			`,"resultType":"vector"},"stats":{},"status":"success"}`), nil

	default:
		return appendPrometheusEnvelope(
			[]byte(`{"data":`), mapData.Get("data"),
			`,"stats":{},"status":"success"}`), nil
	}
}

// promEnvelopeSlack covers the fixed JSON scaffolding around the result array.
const promEnvelopeSlack = 128

// appendPrometheusEnvelope splices result verbatim between prefix and suffix,
// rendering a missing value as null the way the old .Value() + Marshal path did.
func appendPrometheusEnvelope(prefix []byte, result gjson.Result, suffix string) []byte {
	if result.Exists() {
		prefix = append(prefix, result.Raw...)
	} else {
		prefix = append(prefix, "null"...)
	}
	return append(prefix, suffix...)
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
