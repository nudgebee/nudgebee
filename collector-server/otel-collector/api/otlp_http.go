package api

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/collector/otel/common"
	"nudgebee/collector/otel/config"
	"nudgebee/collector/otel/security"
	"strings"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlplogs "go.opentelemetry.io/proto/otlp/logs/v1"
	otlpmetrics "go.opentelemetry.io/proto/otlp/metrics/v1"
	otlpresource "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const HttpContentTypeJson = "application/json"
const HttpContentTypeProtobuf = "application/x-protobuf"

const OTEL_NB_ACCOUNT_ID = "nb.account_id"
const OTEL_NB_TENANT_ID = "nb.tenant_id"

type otlpMessageDetail struct {
	message            proto.Message
	destinatonEndpoint string
	destinationFormat  string
}

type otlpMessageError struct {
	message string
	code    int
}

func handleOtlpApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	r.POST("/otlp/v1/:service", func(c *gin.Context) {
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

		var tenantId = agentDetail.TenantId
		var accountId = agentDetail.AccountId

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Failed to read request body"})
			return
		}

		contentType := c.Request.Header.Get("Content-Type")
		contentEncoding := c.Request.Header.Get("Content-Encoding")

		decompressedBody, err := decompressBody(body, contentEncoding)
		if err != nil {
			logger.Error("otel: failed to decompress body", "error", err)
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Unsupported Content-Encoding"})
			return
		}

		var processMessage otlpMessageDetail
		var processErr otlpMessageError
		var service string

		switch contentType {
		case HttpContentTypeJson, HttpContentTypeProtobuf:
			service = c.Param("service")
			service = strings.TrimSuffix(service, "/")
			switch service {
			case "traces":
				processMessage, processErr = processTraceMessage(tenantId, accountId, decompressedBody, contentType, logger)
			case "metrics":
				processMessage, processErr = processMetricMessage(tenantId, accountId, decompressedBody, contentType, logger)
			case "logs":
				processMessage, processErr = processLogMessage(tenantId, accountId, decompressedBody, contentType, logger)
			default:
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Unknown target service"})
				return
			}
		default:
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Unsupported Content-Type"})
			return
		}

		if processErr.code != 0 {
			c.AbortWithStatusJSON(processErr.code, gin.H{"error": processErr.message})
			return
		}

		otlpBody, err := marshalOtlpRequest(processMessage.message, processMessage.destinationFormat)
		if err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}

		exportRequest(c, tenantId, accountId, otlpBody, processMessage.destinatonEndpoint, logger, service, processMessage.destinationFormat)
	})
}

func processTraceMessage(tenantId string, accountId string, body []byte, contentType string, logger *slog.Logger) (otlpMessageDetail, otlpMessageError) {
	var tracedata otlptrace.TracesData
	switch contentType {
	case HttpContentTypeJson:
		unmarshaller := protojson.UnmarshalOptions{DiscardUnknown: true}
		if err := unmarshaller.Unmarshal(body, &tracedata); err != nil {
			return otlpMessageDetail{}, otlpMessageError{
				code: http.StatusBadRequest,
			}
		}
	case HttpContentTypeProtobuf:
		if err := proto.Unmarshal(body, &tracedata); err != nil {
			return otlpMessageDetail{}, otlpMessageError{
				code: http.StatusBadRequest,
			}
		}
	default:
		return otlpMessageDetail{}, otlpMessageError{
			code: http.StatusBadRequest,
		}
	}

	for _, r := range tracedata.ResourceSpans {
		if r.Resource == nil {
			r.Resource = &otlpresource.Resource{}
		}
		addNBAttributes(r.Resource, tenantId, accountId)
	}

	endpoint := config.Config.GetString(fmt.Sprintf("otel_server_trace_%s_endpoint", config.Config.OtelTraceProvider), "")
	endpointFormat := config.Config.GetString(fmt.Sprintf("otel_server_trace_%s_format", config.Config.OtelTraceProvider), HttpContentTypeJson)

	return otlpMessageDetail{
		message:            &tracedata,
		destinatonEndpoint: endpoint,
		destinationFormat:  endpointFormat,
	}, otlpMessageError{}
}

func processMetricMessage(tenantId string, accountId string, body []byte, contentType string, logger *slog.Logger) (otlpMessageDetail, otlpMessageError) {
	var metricsdata otlpmetrics.MetricsData
	switch contentType {
	case HttpContentTypeJson:
		unmarshaller := protojson.UnmarshalOptions{DiscardUnknown: true}
		if err := unmarshaller.Unmarshal(body, &metricsdata); err != nil {
			return otlpMessageDetail{}, otlpMessageError{
				code: http.StatusBadRequest,
			}
		}
	case HttpContentTypeProtobuf:
		if err := proto.Unmarshal(body, &metricsdata); err != nil {
			return otlpMessageDetail{}, otlpMessageError{
				code: http.StatusBadRequest,
			}
		}
	default:
		return otlpMessageDetail{}, otlpMessageError{
			code: http.StatusBadRequest,
		}
	}

	for _, r := range metricsdata.ResourceMetrics {
		if r.Resource == nil {
			r.Resource = &otlpresource.Resource{}
		}
		addNBAttributes(r.Resource, tenantId, accountId)
	}

	endpoint := config.Config.GetString(fmt.Sprintf("otel_server_metrics_%s_endpoint", config.Config.OtelMetricsProvider), "")
	endpointFormat := config.Config.GetString(fmt.Sprintf("otel_server_metrics_%s_format", config.Config.OtelMetricsProvider), HttpContentTypeJson)

	return otlpMessageDetail{
		message:            &metricsdata,
		destinatonEndpoint: endpoint,
		destinationFormat:  endpointFormat,
	}, otlpMessageError{}
}

func processLogMessage(tenantId string, accountId string, body []byte, contentType string, logger *slog.Logger) (otlpMessageDetail, otlpMessageError) {
	var logsdata otlplogs.LogsData
	switch contentType {
	case HttpContentTypeJson:
		unmarshaller := protojson.UnmarshalOptions{DiscardUnknown: true}
		if err := unmarshaller.Unmarshal(body, &logsdata); err != nil {
			return otlpMessageDetail{}, otlpMessageError{
				code: http.StatusBadRequest,
			}
		}
	case HttpContentTypeProtobuf:
		if err := proto.Unmarshal(body, &logsdata); err != nil {
			return otlpMessageDetail{}, otlpMessageError{
				code: http.StatusBadRequest,
			}
		}
	default:
		return otlpMessageDetail{}, otlpMessageError{
			code: http.StatusBadRequest,
		}
	}

	for _, r := range logsdata.ResourceLogs {
		if r.Resource == nil {
			r.Resource = &otlpresource.Resource{}
		}
		addNBAttributes(r.Resource, tenantId, accountId)
	}

	endpoint := config.Config.GetString(fmt.Sprintf("otel_server_log_%s_endpoint", config.Config.OtelLogProvider), "")
	endpointFormat := config.Config.GetString(fmt.Sprintf("otel_server_log_%s_format", config.Config.OtelLogProvider), HttpContentTypeJson)

	return otlpMessageDetail{
		message:            &logsdata,
		destinatonEndpoint: endpoint,
		destinationFormat:  endpointFormat,
	}, otlpMessageError{}
}

func decompressBody(body []byte, contentEncoding string) ([]byte, error) {
	switch contentEncoding {
	case "gzip":
		gzipReader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer func() { _ = gzipReader.Close() }()
		return io.ReadAll(gzipReader)

	case "deflate":
		zlibReader, err := zlib.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create zlib reader: %w", err)
		}
		defer func() { _ = zlibReader.Close() }()
		return io.ReadAll(zlibReader)

	default:
		return body, nil
	}
}

func exportRequest(c *gin.Context, tenantId string, accountId string, body []byte, endpoint string, logger *slog.Logger, service string, contentType string) {
	if endpoint == "" || endpoint == "console" {
		logger.Info("otel: forwarding request", "tenantId", tenantId, "accountId", accountId, "service", service, "data", string(body))
		c.JSON(http.StatusOK, gin.H{"status": map[string]any{
			"code":    0,
			"message": "OK",
		}})
		return
	} else {
		logger.Info("otel: forwarding request", "tenantId", tenantId, "accountId", accountId, "service", service, "endpoint", endpoint)
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request for forwarding"})
		return
	}

	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Forwarded-For", c.Request.Header.Get("X-Forwarded-For"))
	req.Header.Set("X-Forwarded-Host", c.Request.Header.Get("Host"))
	req.Header.Set("X-Forwarded-Proto", c.Request.Header.Get("X-Forwarded-Proto"))

	client := common.HttpClient()
	resp, err := client.Do(req)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "Failed to forward request"})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Error reading response body", "error", err, "service", service)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if resp.StatusCode != http.StatusOK {
		slog.Error("unable to push data to otel/metrices", "data", string(responseBody), "status", resp.StatusCode)
		c.AbortWithStatus(resp.StatusCode)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": map[string]any{
		"code":    0,
		"message": "OK",
	}})
}

func marshalOtlpRequest(message proto.Message, format string) ([]byte, error) {
	switch format {
	case HttpContentTypeJson:
		marshaller := protojson.MarshalOptions{}
		return marshaller.Marshal(message)
	case HttpContentTypeProtobuf:
		return proto.Marshal(message)
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}

func addNBAttributes(resource *otlpresource.Resource, tenantId, accountId string) {
	if resource == nil {
		resource = &otlpresource.Resource{}
	}
	resource.Attributes = append(resource.Attributes,
		newResourceAttribute(OTEL_NB_TENANT_ID, tenantId),
		newResourceAttribute(OTEL_NB_ACCOUNT_ID, accountId),
	)
}

func newResourceAttribute(key, value string) *otlpcommon.KeyValue {
	return &otlpcommon.KeyValue{
		Key:   key,
		Value: &otlpcommon.AnyValue{Value: &otlpcommon.AnyValue_StringValue{StringValue: value}},
	}
}
