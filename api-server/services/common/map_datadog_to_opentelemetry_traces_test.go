package common

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Datadog sends custom.service either as a bare string or, since its
// service-catalog change, as a nested object. Both shapes must decode.
func TestDatadogSpansDecodeServiceShapes(t *testing.T) {
	payload := []byte(`[
      {
        "id": "span-object-service",
        "type": "spans",
        "attributes": {
          "service": "cartservice",
          "span_id": "1",
          "trace_id": "t1",
          "custom": {
            "language": "go",
            "version": "go1.26.5",
            "service": {"criticality": "critical", "namespace": "opentelemetry-demo"},
            "duration": 1000000
          }
        }
      },
      {
        "id": "span-version-object-service",
        "type": "spans",
        "attributes": {
          "service": "checkoutservice",
          "span_id": "2",
          "trace_id": "t1",
          "custom": {"service": {"version": "0.1.0"}}
        }
      },
      {
        "id": "span-string-service",
        "type": "spans",
        "attributes": {
          "service": "frontend",
          "span_id": "3",
          "trace_id": "t1",
          "custom": {"service": "frontend", "duration": 42}
        }
      }
    ]`)

	var ddTrace DatadogTrace
	require.NoError(t, json.Unmarshal(payload, &ddTrace.Data))
	require.Len(t, ddTrace.Data, 3)

	assert.Equal(t, DatadogString(""), ddTrace.Data[0].Attributes.Custom.Service)
	assert.Equal(t, int64(1000000), ddTrace.Data[0].Attributes.Custom.Duration)
	assert.Equal(t, DatadogString(""), ddTrace.Data[1].Attributes.Custom.Service)
	assert.Equal(t, DatadogString("frontend"), ddTrace.Data[2].Attributes.Custom.Service)

	drifted, _ := DatadogShapeDrift(ddTrace.Data)
	assert.Zero(t, drifted, "an object-shaped service is handled, not drift")

	otel := MapDatadogToOpenTelemetry(ddTrace)
	require.Len(t, otel, 3)
	assert.Equal(t, "cartservice", otel[0].ServiceName)
	assert.Equal(t, "checkoutservice", otel[1].ServiceName)
	assert.Equal(t, "frontend", otel[2].ServiceName)
}

// Datadog moved parts of the custom tag bag to the OpenTelemetry nested form:
// db.statement is {"text": "..."} and db.system is {"name": "..."}. The value
// must survive the change, not just the decode.
func TestDatadogSpansDecodeNestedShapesFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/datadog_spans_nested_shapes.json")
	require.NoError(t, err)

	var ddTrace DatadogTrace
	require.NoError(t, json.Unmarshal(raw, &ddTrace))
	require.Len(t, ddTrace.Data, 3)

	dbSpan := ddTrace.Data[0].Attributes
	require.NotNil(t, dbSpan.Custom.DB)
	assert.Equal(t, DatadogString("postgresql"), dbSpan.Custom.DB.System, "db.system carried as {name}")
	assert.Contains(t, string(dbSpan.Custom.DB.Statement), "SELECT", "db.statement carried as {text}")

	drifted, sample := DatadogShapeDrift(ddTrace.Data)
	assert.Zero(t, drifted, "no span should report shape drift, got sample: %s", sample)

	otel := MapDatadogToOpenTelemetry(ddTrace)
	require.Len(t, otel, 3)
	assert.Equal(t, "product-catalog", otel[0].ServiceName)
	assert.True(t, strings.Contains(otel[0].SpanAttributes["db.statement"], "SELECT"),
		"the SQL statement must reach span attributes, got %q", otel[0].SpanAttributes["db.statement"])
	assert.Equal(t, "postgresql", otel[0].SpanAttributes["db.system"])
}

// A tag shape this struct does not model must cost that tag, not the response:
// every span still decodes, and the drift is reported for logging.
func TestDatadogSpansTolerateUnknownShapeDrift(t *testing.T) {
	payload := []byte(`{"data": [
      {
        "id": "span-drifted",
        "type": "spans",
        "attributes": {
          "service": "frontend",
          "span_id": "1",
          "trace_id": "t1",
          "custom": {"component": {"nested": "unexpected"}, "duration": 7, "language": "go"}
        }
      },
      {
        "id": "span-healthy",
        "type": "spans",
        "attributes": {"service": "cartservice", "span_id": "2", "trace_id": "t1", "custom": {"duration": 9}}
      }
    ]}`)

	var ddTrace DatadogTrace
	require.NoError(t, json.Unmarshal(payload, &ddTrace), "one drifted tag must not fail the response")
	require.Len(t, ddTrace.Data, 2)

	drifted, sample := DatadogShapeDrift(ddTrace.Data)
	assert.Equal(t, 1, drifted)
	assert.NotEmpty(t, sample, "the drift is reported so it can be logged")

	otel := MapDatadogToOpenTelemetry(ddTrace)
	require.Len(t, otel, 2, "both spans survive")
	assert.Equal(t, "frontend", otel[0].ServiceName)
	assert.Equal(t, "cartservice", otel[1].ServiceName)
}
