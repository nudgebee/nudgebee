package observability

import (
	"os"
	"testing"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizeClickhouseAttrMap pins the three input shapes a ClickHouse Map
// column can arrive as via the HTTP JSON driver. The previous heatmap mapper
// only accepted the string form, which meant every real-world row silently
// returned nil for span_attributes / resource_attributes.
func TestNormalizeClickhouseAttrMap(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		assert.Nil(t, normalizeClickhouseAttrMap(nil))
	})

	t.Run("map[string]interface{} — native ClickHouse HTTP driver shape", func(t *testing.T) {
		raw := map[string]interface{}{
			"rpc.grpc.status_code": "9",
			"rpc.method":           "EmptyCart",
			"rpc.service":          "oteldemo.CartService",
			"server.port":          float64(8080), // numeric values stringified
		}
		got := normalizeClickhouseAttrMap(raw)
		require.NotNil(t, got)
		assert.Equal(t, "9", got["rpc.grpc.status_code"])
		assert.Equal(t, "EmptyCart", got["rpc.method"])
		assert.Equal(t, "oteldemo.CartService", got["rpc.service"])
		assert.Equal(t, "8080", got["server.port"])
	})

	t.Run("map[string]string — typed driver shape", func(t *testing.T) {
		raw := map[string]string{
			"k8s.pod.name":        "checkout-86db459f8-9hshw",
			"k8s.deployment.name": "checkout",
		}
		got := normalizeClickhouseAttrMap(raw)
		require.NotNil(t, got)
		assert.Equal(t, "checkout-86db459f8-9hshw", got["k8s.pod.name"])
		assert.Equal(t, "checkout", got["k8s.deployment.name"])
	})

	t.Run("string — legacy stringified JSON", func(t *testing.T) {
		raw := `{"http.method":"POST","http.status_code":"500"}`
		got := normalizeClickhouseAttrMap(raw)
		require.NotNil(t, got)
		assert.Equal(t, "POST", got["http.method"])
		assert.Equal(t, "500", got["http.status_code"])
	})

	t.Run("empty string returns nil", func(t *testing.T) {
		assert.Nil(t, normalizeClickhouseAttrMap(""))
	})

	t.Run("invalid JSON string returns nil", func(t *testing.T) {
		assert.Nil(t, normalizeClickhouseAttrMap("not json"))
	})

	t.Run("unrecognised type returns nil", func(t *testing.T) {
		assert.Nil(t, normalizeClickhouseAttrMap(42))
	})
}

// TestMapRowToOpenTelemetryHeatmapTrace_PreservesAttributes is the end-to-end
// regression guard for the trace-details UI bug: a ClickHouse row containing
// resource_attributes and span_attributes as native maps must produce an
// OpenTelemetryTraceHeatMap with both fields populated. Previously the mapper
// only accepted the string form and silently dropped every row's attributes,
// which surfaced as null values in the "Trace Details" modal.
func TestMapRowToOpenTelemetryHeatmapTrace_PreservesAttributes(t *testing.T) {
	// Row shape as produced by ClickHouse's HTTP JSON driver for a cartFailure
	// EmptyCart error span (from account a2a30b02 in dev).
	row := map[string]interface{}{
		"trace_id":     "1c7a206c26c8b294c8884f1d7af621a0",
		"span_id":      "57314713e89fe082",
		"span_name":    "POST /oteldemo.CartService/EmptyCart",
		"status_code":  "STATUS_CODE_ERROR",
		"timestamp":    "2026-04-24 07:05:26.143052+00:00",
		"duration_ns":  float64(430459500),
		"service_name": "cart",
		"spanattributes": map[string]interface{}{
			"rpc.grpc.status_code": "9",
			"rpc.method":           "EmptyCart",
			"rpc.service":          "oteldemo.CartService",
			"url.path":             "/oteldemo.CartService/EmptyCart",
			"grpc.method":          "/oteldemo.CartService/EmptyCart",
		},
		"resourceattributes": map[string]interface{}{
			"k8s.deployment.name": "cart",
			"k8s.namespace.name":  "demo",
			"k8s.pod.name":        "cart-6c88b9dc7b-hrfcn",
			"service.name":        "cart",
			"cluster":             "cluster-name",
			"host.arch":           "x64",
		},
	}

	got, err := MapRowToOpenTelemetryHeatmapTrace(row)
	require.NoError(t, err)

	assert.Equal(t, "1c7a206c26c8b294c8884f1d7af621a0", got.TraceID)
	assert.Equal(t, "57314713e89fe082", got.SpanID)
	assert.Equal(t, "STATUS_CODE_ERROR", got.StatusCode)
	assert.Equal(t, "cart", got.ServiceName)

	// Before the fix, both of these were nil because the mapper's type
	// assertion looked for a string.
	require.NotNil(t, got.SpanAttributes, "span_attributes must not be nil")
	assert.Equal(t, "9", got.SpanAttributes["rpc.grpc.status_code"])
	assert.Equal(t, "EmptyCart", got.SpanAttributes["rpc.method"])
	assert.Equal(t, "oteldemo.CartService", got.SpanAttributes["rpc.service"])

	require.NotNil(t, got.ResourceAttributes, "resource_attributes must not be nil")
	assert.Equal(t, "cart", got.ResourceAttributes["k8s.deployment.name"])
	assert.Equal(t, "demo", got.ResourceAttributes["k8s.namespace.name"])
	assert.Equal(t, "cart-6c88b9dc7b-hrfcn", got.ResourceAttributes["k8s.pod.name"])
}

// TestMapRowToOpenTelemetryHeatmapTrace_MissingAttributesIsOK ensures rows
// without span/resource attributes don't error and just return a heatmap with
// empty attribute maps.
func TestMapRowToOpenTelemetryHeatmapTrace_MissingAttributesIsOK(t *testing.T) {
	row := map[string]interface{}{
		"trace_id":     "t1",
		"span_id":      "s1",
		"span_name":    "POST",
		"status_code":  "STATUS_CODE_UNSET",
		"timestamp":    "2026-04-24 07:05:26.142678+00:00",
		"duration_ns":  float64(177392),
		"service_name": "checkout",
	}

	got, err := MapRowToOpenTelemetryHeatmapTrace(row)
	require.NoError(t, err)
	assert.Equal(t, "t1", got.TraceID)
	assert.Equal(t, "s1", got.SpanID)
	assert.Nil(t, got.SpanAttributes)
	assert.Nil(t, got.ResourceAttributes)
}

// TestGetTraceHeatMap_Live exercises the full heatmap path end-to-end against
// the local Metastore and the relay-server port-forward at localhost:8088.
// Uses the known checkout error trace from the dev cartFailure scenario —
// before the fix, every span in the response came back with span_attributes
// and resource_attributes as nil (surfaced in the UI as null fields in the
// Trace Details modal).
//
// Gated on TEST_LIVE_HEATMAP=1. Run with:
//
//	cd api-server/services
//	TEST_LIVE_HEATMAP=1 APP_DATABASE_URL=... go test -v \
//	  -run TestGetTraceHeatMap_Live ./observability/
func TestGetTraceHeatMap_Live(t *testing.T) {
	if os.Getenv("TEST_LIVE_HEATMAP") != "1" {
		t.Skip("set TEST_LIVE_HEATMAP=1 to run (requires Metastore + relay-server port-forward)")
	}

	const (
		accountID = "a2a30b02-0f67-42e5-a2ab-c658230fd798"
		traceID   = "1c7a206c26c8b294c8884f1d7af621a0"
	)

	ctx := security.NewRequestContextForTenantAdmin("890cad87-c452-4aa7-b84a-742cee0454a1", nil, nil, nil)

	traces, err := GetTraceHeatMap(ctx, TracesHeatMapRequest{
		AccountId: accountID,
		TraceId:   traceID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, traces, "expected spans for the known trace")

	t.Logf("loaded %d spans for trace %s", len(traces), traceID)

	// Count how many spans carry populated attribute maps. Before the fix
	// every span had nil maps — so this count would be 0.
	spansWithSpanAttrs := 0
	spansWithResourceAttrs := 0
	for _, tr := range traces {
		if len(tr.SpanAttributes) > 0 {
			spansWithSpanAttrs++
		}
		if len(tr.ResourceAttributes) > 0 {
			spansWithResourceAttrs++
		}
	}
	t.Logf("spans with span_attributes populated:    %d / %d", spansWithSpanAttrs, len(traces))
	t.Logf("spans with resource_attributes populated: %d / %d", spansWithResourceAttrs, len(traces))

	assert.Greater(t, spansWithSpanAttrs, 0,
		"at least one span should carry span_attributes (was 0 before the fix)")
	assert.Greater(t, spansWithResourceAttrs, 0,
		"at least one span should carry resource_attributes (was 0 before the fix)")

	// Find a known error span and spot-check that its attribute maps are
	// actually populated (the bug produced nil maps, which would fail even
	// a loose existence check).
	for _, tr := range traces {
		if tr.SpanID == "57314713e89fe082" { // cart EmptyCart SERVER span
			assert.Equal(t, "STATUS_CODE_ERROR", tr.StatusCode)
			assert.Greater(t, len(tr.SpanAttributes), 0,
				"cart error span must have non-empty span_attributes")
			assert.Greater(t, len(tr.ResourceAttributes), 0,
				"cart error span must have non-empty resource_attributes")
			assert.Equal(t, "cart", tr.ResourceAttributes["k8s.deployment.name"])
			t.Logf("cart error span: %d span_attrs, %d resource_attrs; k8s.deployment.name=%q",
				len(tr.SpanAttributes), len(tr.ResourceAttributes),
				tr.ResourceAttributes["k8s.deployment.name"])
			break
		}
	}
}

// TestClickhouseInt64 pins the input shapes ClickHouse's HTTP JSON driver
// can return for a 64-bit integer column. The previous mapper accepted only
// float64, which silently dropped UInt64 columns like otel_traces.Duration
// (ClickHouse FORMAT JSON quotes 64-bit integers as strings by default to
// avoid precision loss in JSON parsers), surfacing as "0ns" in the UI.
func TestClickhouseInt64(t *testing.T) {
	t.Run("string — default UInt64 encoding", func(t *testing.T) {
		assert.Equal(t, int64(257591), clickhouseInt64("257591"))
	})

	t.Run("float64 — Float-typed or aggregate columns", func(t *testing.T) {
		assert.Equal(t, int64(257591), clickhouseInt64(float64(257591)))
	})

	t.Run("large value beyond float64 safe-integer range survives via string", func(t *testing.T) {
		// 2^53 + 1 — cannot round-trip through float64 without precision loss.
		// This is exactly why ClickHouse quotes 64-bit ints in JSON.
		assert.Equal(t, int64(9007199254740993), clickhouseInt64("9007199254740993"))
	})

	t.Run("nil returns 0", func(t *testing.T) {
		assert.Equal(t, int64(0), clickhouseInt64(nil))
	})

	t.Run("malformed string returns 0", func(t *testing.T) {
		assert.Equal(t, int64(0), clickhouseInt64("not-a-number"))
	})

	t.Run("unrecognised type returns 0", func(t *testing.T) {
		assert.Equal(t, int64(0), clickhouseInt64(true))
	})
}

// TestMapRowToOpenTelemetryTrace_DurationParsesQuotedUInt64 is the regression
// guard for the duration_ns "0ns" bug: ClickHouse delivers UInt64 columns as
// quoted strings, and the old (float64)-only type assertion silently dropped
// the value on every row.
func TestMapRowToOpenTelemetryTrace_DurationParsesQuotedUInt64(t *testing.T) {
	row := map[string]interface{}{
		"trace_id":    "abc123",
		"span_id":     "def456",
		"duration_ns": "1234567", // ClickHouse JSON-quoted UInt64
	}
	trace, err := MapRowToOpenTelemetryTrace(row)
	require.NoError(t, err)
	assert.Equal(t, int64(1234567), trace.DurationNs,
		"quoted UInt64 from ClickHouse must parse into DurationNs")
}

// TestMapRowToOpenTelemetryHeatmapTrace_DurationParsesQuotedUInt64 is the
// same regression guard for the heatmap (Service & Operation drilldown) path.
func TestMapRowToOpenTelemetryHeatmapTrace_DurationParsesQuotedUInt64(t *testing.T) {
	row := map[string]interface{}{
		"trace_id":    "abc123",
		"span_id":     "def456",
		"duration_ns": "1234567",
	}
	trace, err := MapRowToOpenTelemetryHeatmapTrace(row)
	require.NoError(t, err)
	assert.Equal(t, int64(1234567), trace.DurationNs)
}
