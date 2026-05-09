package api

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"log/slog"
	"net/http"
	"nudgebee/collector/otel/config"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlplogs "go.opentelemetry.io/proto/otlp/logs/v1"
	otlpmetrics "go.opentelemetry.io/proto/otlp/metrics/v1"
	otlpresource "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestProcessTraceMessage(t *testing.T) {
	logger := slog.Default()

	// Configure necessary config values
	config.Config.OtelTraceProvider = "test-provider"
	config.Config.SetString(fmt.Sprintf("otel_server_trace_%s_endpoint", config.Config.OtelTraceProvider), "http://test-endpoint")
	config.Config.SetString(fmt.Sprintf("otel_server_trace_%s_format", config.Config.OtelTraceProvider), HttpContentTypeJson)

	testCases := []struct {
		name             string
		tenantId         string
		accountId        string
		body             []byte
		contentType      string
		contentEncoding  string
		expectedMessage  *otlptrace.TracesData
		expectedEndpoint string
		expectedFormat   string
		expectedError    otlpMessageError
	}{
		{
			name:      "ValidJSON",
			tenantId:  "test-tenant",
			accountId: "test-account",
			body: testTracesJsonBytes(t, &otlptrace.TracesData{
				ResourceSpans: []*otlptrace.ResourceSpans{{
					Resource: &otlpresource.Resource{
						Attributes: []*otlpcommon.KeyValue{
							newResourceAttribute("service.name", "test-service"),
						},
					},
				}},
			}),
			contentType:      HttpContentTypeJson,
			contentEncoding:  "",
			expectedMessage:  &otlptrace.TracesData{ResourceSpans: []*otlptrace.ResourceSpans{{Resource: &otlpresource.Resource{Attributes: []*otlpcommon.KeyValue{newResourceAttribute("service.name", "test-service"), newResourceAttribute("nb.tenant_id", "test-tenant"), newResourceAttribute("nb.account_id", "test-account")}}}}},
			expectedEndpoint: "http://test-endpoint",
			expectedFormat:   HttpContentTypeJson,
			expectedError:    otlpMessageError{},
		},
		{
			name:      "ValidProtobuf",
			tenantId:  "test-tenant",
			accountId: "test-account",
			body: testTracesProtobufBytes(t, &otlptrace.TracesData{
				ResourceSpans: []*otlptrace.ResourceSpans{{
					Resource: &otlpresource.Resource{
						Attributes: []*otlpcommon.KeyValue{
							newResourceAttribute("service.name", "test-service"),
						},
					},
				}},
			}),
			contentType:      HttpContentTypeProtobuf,
			contentEncoding:  "",
			expectedMessage:  &otlptrace.TracesData{ResourceSpans: []*otlptrace.ResourceSpans{{Resource: &otlpresource.Resource{Attributes: []*otlpcommon.KeyValue{newResourceAttribute("service.name", "test-service"), newResourceAttribute("nb.tenant_id", "test-tenant"), newResourceAttribute("nb.account_id", "test-account")}}}}},
			expectedEndpoint: "http://test-endpoint",
			expectedFormat:   HttpContentTypeJson,
			expectedError:    otlpMessageError{},
		},
		{
			name:             "InvalidJSON",
			tenantId:         "test-tenant",
			accountId:        "test-account",
			body:             []byte(`{invalidjson}`),
			contentType:      HttpContentTypeJson,
			contentEncoding:  "",
			expectedMessage:  nil,
			expectedEndpoint: "",
			expectedFormat:   "",
			expectedError: otlpMessageError{
				code: http.StatusBadRequest,
			},
		},
		{
			name:             "GzippedProtobuf",
			tenantId:         "test-tenant",
			accountId:        "test-account",
			body:             gzipBytes(t, testTracesProtobufBytes(t, &otlptrace.TracesData{})),
			contentType:      HttpContentTypeProtobuf,
			contentEncoding:  "gzip",
			expectedMessage:  &otlptrace.TracesData{ResourceSpans: []*otlptrace.ResourceSpans{{Resource: &otlpresource.Resource{Attributes: []*otlpcommon.KeyValue{newResourceAttribute("nb.tenant_id", "test-tenant"), newResourceAttribute("nb.account_id", "test-account")}}}}},
			expectedEndpoint: "http://test-endpoint",
			expectedFormat:   HttpContentTypeJson,
			expectedError: otlpMessageError{
				code: http.StatusBadRequest,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.expectedFormat == HttpContentTypeJson {
				config.Config.SetString(fmt.Sprintf("otel_server_trace_%s_format", config.Config.OtelTraceProvider), HttpContentTypeJson)
			}

			message, err := processTraceMessage(tc.tenantId, tc.accountId, tc.body, tc.contentType, logger)

			if tc.expectedError.code != 0 {
				assert.Equal(t, tc.expectedError, err)
				if tc.expectedError.code == http.StatusBadRequest {
					assert.Equal(t, http.StatusBadRequest, err.code)
				}
				return
			}
			require.Equal(t, tc.expectedError.code, err.code)

			assert.Equal(t, tc.expectedEndpoint, message.destinatonEndpoint)
			assert.Equal(t, tc.expectedFormat, message.destinationFormat)

			if tc.expectedMessage != nil {
				assert.True(t, proto.Equal(tc.expectedMessage, message.message))
			} else {
				assert.Nil(t, message.message)
			}

		})
	}
}

func TestProcessLogsMessage(t *testing.T) {
	logger := slog.Default()
	config.Config.OtelLogProvider = "test-provider"
	config.Config.SetString(fmt.Sprintf("otel_server_log_%s_endpoint", config.Config.OtelLogProvider), "http://test-log-endpoint")
	config.Config.SetString(fmt.Sprintf("otel_server_log_%s_format", config.Config.OtelLogProvider), HttpContentTypeJson)

	testCases := []struct {
		name             string
		tenantId         string
		accountId        string
		body             []byte
		contentType      string
		contentEncoding  string
		expectedMessage  *otlplogs.LogsData
		expectedEndpoint string
		expectedFormat   string
		expectedError    otlpMessageError
	}{
		{
			name:      "ValidJSON",
			tenantId:  "test-tenant",
			accountId: "test-account",
			body: testLogsJsonBytes(t, &otlplogs.LogsData{ResourceLogs: []*otlplogs.ResourceLogs{{
				Resource: &otlpresource.Resource{
					Attributes: []*otlpcommon.KeyValue{
						newResourceAttribute("service.name", "test-service"),
					},
				},
			}}}),
			contentType:      HttpContentTypeJson,
			contentEncoding:  "",
			expectedMessage:  &otlplogs.LogsData{ResourceLogs: []*otlplogs.ResourceLogs{{Resource: &otlpresource.Resource{Attributes: []*otlpcommon.KeyValue{newResourceAttribute("service.name", "test-service"), newResourceAttribute("nb.tenant_id", "test-tenant"), newResourceAttribute("nb.account_id", "test-account")}}}}},
			expectedEndpoint: "http://test-log-endpoint",
			expectedFormat:   HttpContentTypeJson,
			expectedError:    otlpMessageError{},
		},
		{
			name:      "ValidProtobuf",
			tenantId:  "test-tenant",
			accountId: "test-account",
			body: testLogsProtobufBytes(t, &otlplogs.LogsData{ResourceLogs: []*otlplogs.ResourceLogs{{
				Resource: &otlpresource.Resource{
					Attributes: []*otlpcommon.KeyValue{
						newResourceAttribute("service.name", "test-service"),
					},
				},
			}}}),
			contentType:      HttpContentTypeProtobuf,
			contentEncoding:  "",
			expectedMessage:  &otlplogs.LogsData{ResourceLogs: []*otlplogs.ResourceLogs{{Resource: &otlpresource.Resource{Attributes: []*otlpcommon.KeyValue{newResourceAttribute("service.name", "test-service"), newResourceAttribute("nb.tenant_id", "test-tenant"), newResourceAttribute("nb.account_id", "test-account")}}}}},
			expectedEndpoint: "http://test-log-endpoint",
			expectedFormat:   HttpContentTypeJson,
			expectedError:    otlpMessageError{},
		},
		{
			name:             "InvalidJSON",
			tenantId:         "test-tenant",
			accountId:        "test-account",
			body:             []byte(`{invalidjson}`),
			contentType:      HttpContentTypeJson,
			contentEncoding:  "",
			expectedMessage:  nil,
			expectedEndpoint: "",
			expectedFormat:   "",
			expectedError: otlpMessageError{
				code: http.StatusBadRequest,
			},
		},
		{
			name:             "GzippedProtobuf",
			tenantId:         "test-tenant",
			accountId:        "test-account",
			body:             gzipBytes(t, testLogsProtobufBytes(t, &otlplogs.LogsData{})),
			contentType:      HttpContentTypeProtobuf,
			contentEncoding:  "gzip",
			expectedMessage:  &otlplogs.LogsData{ResourceLogs: []*otlplogs.ResourceLogs{{Resource: &otlpresource.Resource{Attributes: []*otlpcommon.KeyValue{newResourceAttribute("nb.tenant_id", "test-tenant"), newResourceAttribute("nb.account_id", "test-account")}}}}},
			expectedEndpoint: "http://test-endpoint",
			expectedFormat:   HttpContentTypeJson,
			expectedError: otlpMessageError{
				code: http.StatusBadRequest,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.expectedFormat == HttpContentTypeJson {
				config.Config.SetString(fmt.Sprintf("otel_server_log_%s_format", config.Config.OtelTraceProvider), HttpContentTypeJson)
			}

			message, err := processLogMessage(tc.tenantId, tc.accountId, tc.body, tc.contentType, logger)

			if tc.expectedError.code != 0 {
				assert.Equal(t, tc.expectedError, err)
				if tc.expectedError.code == http.StatusBadRequest {
					assert.Equal(t, http.StatusBadRequest, err.code)
				}
				return
			}
			require.Equal(t, tc.expectedError.code, err.code)

			assert.Equal(t, tc.expectedEndpoint, message.destinatonEndpoint)
			assert.Equal(t, tc.expectedFormat, message.destinationFormat)

			if tc.expectedMessage != nil {
				assert.True(t, proto.Equal(tc.expectedMessage, message.message))
			} else {
				assert.Nil(t, message.message)
			}

		})
	}

}

func TestProcessMetricsMessage(t *testing.T) {

	logger := slog.Default()
	config.Config.OtelMetricsProvider = "test-provider"
	config.Config.SetString(fmt.Sprintf("otel_server_metrics_%s_endpoint", config.Config.OtelMetricsProvider), "http://test-metrics-endpoint")
	config.Config.SetString(fmt.Sprintf("otel_server_metrics_%s_format", config.Config.OtelMetricsProvider), HttpContentTypeJson) // Or Protobuf, as needed

	testCases := []struct {
		name             string
		tenantId         string
		accountId        string
		body             []byte
		contentType      string
		contentEncoding  string
		expectedMessage  *otlpmetrics.MetricsData
		expectedEndpoint string
		expectedFormat   string
		expectedError    otlpMessageError
	}{
		{
			name:      "ValidJSON",
			tenantId:  "test-tenant",
			accountId: "test-account",
			body: testMetricsJsonBytes(t, &otlpmetrics.MetricsData{ResourceMetrics: []*otlpmetrics.ResourceMetrics{{
				Resource: &otlpresource.Resource{
					Attributes: []*otlpcommon.KeyValue{
						newResourceAttribute("service.name", "test-service"),
					},
				},
			}}}),
			contentType:      HttpContentTypeJson,
			contentEncoding:  "",
			expectedMessage:  &otlpmetrics.MetricsData{ResourceMetrics: []*otlpmetrics.ResourceMetrics{{Resource: &otlpresource.Resource{Attributes: []*otlpcommon.KeyValue{newResourceAttribute("service.name", "test-service"), newResourceAttribute("nb.tenant_id", "test-tenant"), newResourceAttribute("nb.account_id", "test-account")}}}}},
			expectedEndpoint: "http://test-metrics-endpoint",
			expectedFormat:   HttpContentTypeJson,
			expectedError:    otlpMessageError{},
		},
		{
			name:      "ValidProtobuf",
			tenantId:  "test-tenant",
			accountId: "test-account",
			body: testMetricsProtobufBytes(t, &otlpmetrics.MetricsData{ResourceMetrics: []*otlpmetrics.ResourceMetrics{{
				Resource: &otlpresource.Resource{
					Attributes: []*otlpcommon.KeyValue{
						newResourceAttribute("service.name", "test-service"),
					},
				},
			}}}),
			contentType:      HttpContentTypeProtobuf,
			contentEncoding:  "",
			expectedMessage:  &otlpmetrics.MetricsData{ResourceMetrics: []*otlpmetrics.ResourceMetrics{{Resource: &otlpresource.Resource{Attributes: []*otlpcommon.KeyValue{newResourceAttribute("service.name", "test-service"), newResourceAttribute("nb.tenant_id", "test-tenant"), newResourceAttribute("nb.account_id", "test-account")}}}}},
			expectedEndpoint: "http://test-metrics-endpoint",
			expectedFormat:   HttpContentTypeJson,
			expectedError:    otlpMessageError{},
		},
		{
			name:             "InvalidJSON",
			tenantId:         "test-tenant",
			accountId:        "test-account",
			body:             []byte(`{invalidjson}`),
			contentType:      HttpContentTypeJson,
			contentEncoding:  "",
			expectedMessage:  nil,
			expectedEndpoint: "",
			expectedFormat:   "",
			expectedError: otlpMessageError{
				code: http.StatusBadRequest,
			},
		},
		{
			name:             "GzippedProtobuf",
			tenantId:         "test-tenant",
			accountId:        "test-account",
			body:             gzipBytes(t, testMetricsProtobufBytes(t, &otlpmetrics.MetricsData{})),
			contentType:      HttpContentTypeProtobuf,
			contentEncoding:  "gzip",
			expectedMessage:  &otlpmetrics.MetricsData{ResourceMetrics: []*otlpmetrics.ResourceMetrics{{Resource: &otlpresource.Resource{Attributes: []*otlpcommon.KeyValue{newResourceAttribute("service.name", "test-service"), newResourceAttribute("nb.tenant_id", "test-tenant"), newResourceAttribute("nb.account_id", "test-account")}}}}},
			expectedEndpoint: "http://test-endpoint",
			expectedFormat:   HttpContentTypeJson,
			expectedError: otlpMessageError{
				code: http.StatusBadRequest,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.expectedFormat == HttpContentTypeJson {
				config.Config.SetString(fmt.Sprintf("otel_server_metrics_%s_format", config.Config.OtelTraceProvider), HttpContentTypeJson)
			}

			message, err := processMetricMessage(tc.tenantId, tc.accountId, tc.body, tc.contentType, logger)

			if tc.expectedError.code != 0 {
				assert.Equal(t, tc.expectedError, err)
				if tc.expectedError.code == http.StatusBadRequest {
					assert.Equal(t, http.StatusBadRequest, err.code)
				}
				return
			}
			require.Equal(t, tc.expectedError.code, err.code)

			assert.Equal(t, tc.expectedEndpoint, message.destinatonEndpoint)
			assert.Equal(t, tc.expectedFormat, message.destinationFormat)

			if tc.expectedMessage != nil {
				assert.True(t, proto.Equal(tc.expectedMessage, message.message))
			} else {
				assert.Nil(t, message.message)
			}

		})
	}
}

func testTracesProtobufBytes(t *testing.T, traces *otlptrace.TracesData) []byte {
	bytes, err := proto.Marshal(traces)
	require.NoError(t, err)
	return bytes
}

func testMetricsProtobufBytes(t *testing.T, metrics *otlpmetrics.MetricsData) []byte {
	bytes, err := proto.Marshal(metrics)
	require.NoError(t, err)
	return bytes
}

func testLogsProtobufBytes(t *testing.T, logs *otlplogs.LogsData) []byte {
	bytes, err := proto.Marshal(logs)
	require.NoError(t, err)
	return bytes
}

func testTracesJsonBytes(t *testing.T, traces *otlptrace.TracesData) []byte {
	m := protojson.MarshalOptions{}
	bytes, err := m.Marshal(traces)
	require.NoError(t, err)
	return bytes
}

func testMetricsJsonBytes(t *testing.T, metrics *otlpmetrics.MetricsData) []byte {
	m := protojson.MarshalOptions{}
	bytes, err := m.Marshal(metrics)
	require.NoError(t, err)
	return bytes
}

func testLogsJsonBytes(t *testing.T, logs *otlplogs.LogsData) []byte {
	m := protojson.MarshalOptions{}
	bytes, err := m.Marshal(logs)
	require.NoError(t, err)
	return bytes
}

func gzipBytes(t *testing.T, data []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)

	_, err := zw.Write(data)
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	return buf.Bytes()
}
