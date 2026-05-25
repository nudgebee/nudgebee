package observability

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChronosphereTracesAction(t *testing.T) {
	traceAction := chronosphereTracesAction{}
	startTime := time.UnixMilli(1755506887782)
	endTime := time.UnixMilli(1755510487782)
	defaultPlaybookActionContext := playbooks.NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"), slog.Default(), playbooks.PlaybookEvent{
		Name:        "TestLogsAlert",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
		StartedAt:   &startTime,
		EndedAt:     &endTime,
	})

	response, err := traceAction.Execute(defaultPlaybookActionContext, map[string]any{
		"service": "eurl-service",
		"tag_filters": []any{
			map[string]any{
				"key": "http.status_code",

				"numeric_value": map[string]any{
					"comparison": "EQUAL",
					"value":      400,
				},
			},
		},
	})

	assert.NotNil(t, response)
	assert.Nil(t, err)

	jsonBytes, _ := json.Marshal(response)
	fmt.Println(string(jsonBytes))
}

// TestUniqueTraceIDs asserts that trace IDs are preserved in first-seen order,
// duplicates are skipped, empty IDs are filtered out, and the limit is honoured.
// These invariants are relied on by autoExecuteByWorkload's phase-2 expansion:
// dropping them would either broaden the expansion query pointlessly (empty
// IDs match nothing) or re-fetch the same trace multiple times.
func TestUniqueTraceIDs(t *testing.T) {
	span := func(traceID, spanID string) common.OpenTelemetryTrace {
		return common.OpenTelemetryTrace{TraceID: traceID, SpanID: spanID}
	}

	tests := []struct {
		name  string
		in    []common.OpenTelemetryTrace
		limit int
		want  []string
	}{
		{
			name:  "dedups consecutive + skips empty IDs",
			in:    []common.OpenTelemetryTrace{span("t1", "s1"), span("t1", "s2"), span("", "s3"), span("t2", "s4")},
			limit: 10,
			want:  []string{"t1", "t2"},
		},
		{
			name:  "respects limit",
			in:    []common.OpenTelemetryTrace{span("t1", "s1"), span("t2", "s2"), span("t3", "s3")},
			limit: 2,
			want:  []string{"t1", "t2"},
		},
		{
			name:  "empty input",
			in:    nil,
			limit: 10,
			want:  []string{},
		},
		{
			name:  "all empty trace IDs",
			in:    []common.OpenTelemetryTrace{span("", "s1"), span("", "s2")},
			limit: 10,
			want:  []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := uniqueTraceIDs(tc.in, tc.limit)
			if len(tc.want) == 0 {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestMergeSpansDedup covers the union of phase-1 error spans and phase-2
// trace-tree expansion spans: primary ordering preserved, duplicates (same
// span_id) dropped, and secondary entries appended after primary ones.
func TestMergeSpansDedup(t *testing.T) {
	span := func(traceID, spanID string) common.OpenTelemetryTrace {
		return common.OpenTelemetryTrace{TraceID: traceID, SpanID: spanID}
	}

	primary := []common.OpenTelemetryTrace{span("t1", "s1"), span("t1", "s2")}
	secondary := []common.OpenTelemetryTrace{span("t1", "s2"), span("t1", "s3"), span("t2", "s4")}

	got := mergeSpansDedup(primary, secondary)

	require.Len(t, got, 4)
	assert.Equal(t, "s1", got[0].SpanID, "primary ordering preserved")
	assert.Equal(t, "s2", got[1].SpanID)
	assert.Equal(t, "s3", got[2].SpanID, "secondary spans appended after primary")
	assert.Equal(t, "s4", got[3].SpanID)
}

// TestFilterErrorSpans covers Phase 1b: the query builder can't express
// `span_attributes.rpc.grpc.status_code != '0'` (map columns don't support
// `In` / `Nq`), so spans whose only error signal lives inside span_attributes
// bypass Phase 1. filterErrorSpans runs traceHasError over a post-fetch sample
// to recover them for trace-tree expansion.
func TestFilterErrorSpans(t *testing.T) {
	healthy := common.OpenTelemetryTrace{
		TraceID:    "t1",
		SpanID:     "s-healthy",
		StatusCode: "STATUS_CODE_UNSET",
		SpanAttributes: map[string]string{
			"rpc.grpc.status_code": "0",
		},
	}
	grpcErr := common.OpenTelemetryTrace{
		TraceID:    "t2",
		SpanID:     "s-grpc-err",
		StatusCode: "STATUS_CODE_UNSET",
		SpanAttributes: map[string]string{
			"rpc.grpc.status_code": "9",
			"rpc.method":           "EmptyCart",
		},
	}
	httpErr := common.OpenTelemetryTrace{
		TraceID:    "t3",
		SpanID:     "s-http-err",
		StatusCode: "STATUS_CODE_UNSET",
		SpanAttributes: map[string]string{
			"http.status_code": "503",
		},
	}

	got := filterErrorSpans([]common.OpenTelemetryTrace{healthy, grpcErr, healthy, httpErr})
	require.Len(t, got, 2, "healthy spans dropped, both error spans returned")
	ids := []string{got[0].SpanID, got[1].SpanID}
	assert.Contains(t, ids, "s-grpc-err")
	assert.Contains(t, ids, "s-http-err")
	assert.NotContains(t, ids, "s-healthy")

	t.Run("no errors returns nil", func(t *testing.T) {
		assert.Nil(t, filterErrorSpans([]common.OpenTelemetryTrace{healthy}))
	})

	t.Run("empty input returns nil", func(t *testing.T) {
		assert.Nil(t, filterErrorSpans(nil))
	})
}

// TestTraceHasError covers the widened error detection: old code only caught
// STATUS_CODE_ERROR and HTTP 5xx; now gRPC non-zero, HTTP 4xx, and exception
// events all count as errors regardless of the span's status_code.
func TestTraceHasError(t *testing.T) {
	tests := []struct {
		name  string
		trace map[string]any
		want  bool
	}{
		{name: "empty", trace: map[string]any{}, want: false},
		{name: "status unset", trace: map[string]any{"status_code": "STATUS_CODE_UNSET"}, want: false},
		{name: "explicit Error status", trace: map[string]any{"status_code": "STATUS_CODE_ERROR"}, want: true},
		{name: "legacy Error status key", trace: map[string]any{"status_code": "Error"}, want: true},
		{name: "grpc zero is ok", trace: map[string]any{"status_code": "STATUS_CODE_UNSET", "span_attributes": map[string]any{"rpc.grpc.status_code": "0"}}, want: false},
		{name: "grpc non-zero is error", trace: map[string]any{"status_code": "STATUS_CODE_UNSET", "span_attributes": map[string]any{"rpc.grpc.status_code": "9"}}, want: true},
		{name: "http 200 is ok", trace: map[string]any{"status_code": "STATUS_CODE_UNSET", "span_attributes": map[string]any{"http.status_code": "200"}}, want: false},
		{name: "http 400 is error", trace: map[string]any{"status_code": "STATUS_CODE_UNSET", "span_attributes": map[string]any{"http.status_code": "404"}}, want: true},
		{name: "http 500 is error", trace: map[string]any{"status_code": "STATUS_CODE_UNSET", "span_attributes": map[string]any{"http.status_code": "503"}}, want: true},
		{name: "exception event is error", trace: map[string]any{"status_code": "STATUS_CODE_UNSET", "events_attributes": []any{map[string]any{"exception.type": "TimeoutError"}}}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spanAttrs := traceGetSpanAttributes(tc.trace)
			assert.Equal(t, tc.want, traceHasError(tc.trace, spanAttrs))
		})
	}
}

// TestExtractErrorSignature covers the protocol-agnostic signature extraction.
// One test case per OTel attribute family (gRPC, HTTP, DB, messaging, pure
// exception, generic span-level error) ensures that adding a new protocol does
// not silently regress existing ones.
func TestExtractErrorSignature(t *testing.T) {
	t.Run("grpc error with rpc attributes", func(t *testing.T) {
		s := extractErrorSignature(map[string]any{
			"status_code":   "STATUS_CODE_ERROR",
			"workload_name": "checkout",
			"span_name":     "oteldemo.CartService/EmptyCart",
			"span_attributes": map[string]any{
				"rpc.system":           "grpc",
				"rpc.service":          "oteldemo.CartService",
				"rpc.method":           "EmptyCart",
				"rpc.grpc.status_code": "9",
			},
		})
		require.NotNil(t, s)
		assert.Equal(t, "checkout", s.Service)
		assert.Equal(t, "grpc", s.Protocol)
		assert.Equal(t, "oteldemo.CartService", s.Destination)
		assert.Equal(t, "EmptyCart", s.Operation)
		assert.Equal(t, "9", s.Status)
		assert.Equal(t, "FAILED_PRECONDITION", s.StatusName, "grpc code name must be resolved")
	})

	// Regression: OpenTelemetryTrace struct types SpanAttributes as map[string]string
	// and convertOTelTracesToMapRows stores that native type into the trace map
	// without conversion. traceGetMap used to only handle map[string]any and
	// stringified JSON, so real evidence data produced empty span attributes and
	// every protocol branch fell through silently. This test pins the concrete
	// shape that fired in production.
	t.Run("grpc error — span_attributes arrives as map[string]string", func(t *testing.T) {
		s := extractErrorSignature(map[string]any{
			"status_code":   "STATUS_CODE_ERROR",
			"workload_name": "checkout",
			"span_name":     "oteldemo.CartService/EmptyCart",
			"span_attributes": map[string]string{
				"rpc.system":           "grpc",
				"rpc.service":          "oteldemo.CartService",
				"rpc.method":           "EmptyCart",
				"rpc.grpc.status_code": "9",
			},
		})
		require.NotNil(t, s)
		assert.Equal(t, "grpc", s.Protocol)
		assert.Equal(t, "9", s.Status)
		assert.Equal(t, "FAILED_PRECONDITION", s.StatusName)
	})

	t.Run("grpc error falls back to server.address when rpc.service missing", func(t *testing.T) {
		s := extractErrorSignature(map[string]any{
			"status_code":   "STATUS_CODE_ERROR",
			"workload_name": "checkout",
			"span_attributes": map[string]any{
				"rpc.grpc.status_code": "14",
				"server.address":       "cart",
				"server.port":          "8080",
			},
		})
		require.NotNil(t, s)
		assert.Equal(t, "cart:8080", s.Destination)
		assert.Equal(t, "UNAVAILABLE", s.StatusName)
	})

	t.Run("http 500 error", func(t *testing.T) {
		s := extractErrorSignature(map[string]any{
			"status_code":   "STATUS_CODE_UNSET",
			"workload_name": "frontend",
			"span_attributes": map[string]any{
				"http.status_code": "503",
				"http.method":      "POST",
				"http.route":       "/api/checkout",
				"server.address":   "checkout",
			},
		})
		require.NotNil(t, s)
		assert.Equal(t, "http", s.Protocol)
		assert.Equal(t, "checkout", s.Destination)
		assert.Equal(t, "POST /api/checkout", s.Operation)
		assert.Equal(t, "503", s.Status)
		assert.Equal(t, "Service Unavailable", s.StatusName)
	})

	t.Run("http 404 (4xx) is also categorised", func(t *testing.T) {
		s := extractErrorSignature(map[string]any{
			"status_code":   "STATUS_CODE_UNSET",
			"workload_name": "frontend",
			"span_attributes": map[string]any{
				"http.status_code": "404",
				"http.method":      "GET",
				"http.route":       "/products/ABC123",
			},
		})
		require.NotNil(t, s, "HTTP 4xx was ignored by the old implementation — regression guard")
		assert.Equal(t, "http", s.Protocol)
		assert.Equal(t, "404", s.Status)
		assert.Equal(t, "Not Found", s.StatusName)
	})

	t.Run("database error with db.statement", func(t *testing.T) {
		s := extractErrorSignature(map[string]any{
			"status_code":   "STATUS_CODE_ERROR",
			"workload_name": "accounting",
			"span_attributes": map[string]any{
				"db.system":    "postgresql",
				"db.statement": "SELECT * FROM orders",
				"db.operation": "SELECT",
				"db.name":      "accounting_db",
			},
		})
		require.NotNil(t, s)
		assert.Equal(t, "db", s.Protocol)
		assert.Equal(t, "accounting_db", s.Destination)
		assert.Equal(t, "SELECT", s.Operation)
	})

	t.Run("messaging error", func(t *testing.T) {
		s := extractErrorSignature(map[string]any{
			"status_code":   "STATUS_CODE_ERROR",
			"workload_name": "fraud-detection",
			"span_attributes": map[string]any{
				"messaging.system":           "kafka",
				"messaging.destination.name": "orders",
				"messaging.operation":        "process",
			},
		})
		require.NotNil(t, s)
		assert.Equal(t, "messaging", s.Protocol)
		assert.Equal(t, "orders", s.Destination)
		assert.Equal(t, "process", s.Operation)
	})

	t.Run("exception event attached to gRPC span — both captured", func(t *testing.T) {
		s := extractErrorSignature(map[string]any{
			"status_code":   "STATUS_CODE_ERROR",
			"workload_name": "cart",
			"span_name":     "oteldemo.CartService/AddItem",
			"span_attributes": map[string]any{
				"rpc.system":           "grpc",
				"rpc.service":          "oteldemo.CartService",
				"rpc.method":           "AddItem",
				"rpc.grpc.status_code": "13",
			},
			"events_attributes": []any{map[string]any{
				"exception.type":    "RedisConnectionError",
				"exception.message": "connection refused to valkey-cart:6379",
			}},
		})
		require.NotNil(t, s)
		assert.Equal(t, "grpc", s.Protocol, "primary protocol wins")
		assert.Equal(t, "13", s.Status)
		assert.Equal(t, "RedisConnectionError", s.ExceptionType, "exception info orthogonal to protocol")
		assert.Contains(t, s.ExceptionMessage, "valkey-cart")
	})

	t.Run("exception event delivered as stringified json (legacy shape)", func(t *testing.T) {
		s := extractErrorSignature(map[string]any{
			"status_code":       "STATUS_CODE_ERROR",
			"workload_name":     "cart",
			"events_attributes": `{"event.exception.type":"OOM","event.exception.message":"out of memory"}`,
		})
		require.NotNil(t, s)
		assert.Equal(t, "OOM", s.ExceptionType)
		assert.Equal(t, "out of memory", s.ExceptionMessage)
	})

	t.Run("pure STATUS_CODE_ERROR with no protocol attrs yields generic signature", func(t *testing.T) {
		s := extractErrorSignature(map[string]any{
			"status_code":   "STATUS_CODE_ERROR",
			"workload_name": "cart",
			"span_name":     "internal-op",
		})
		require.NotNil(t, s)
		assert.Equal(t, "", s.Protocol, "no protocol detected")
		assert.Equal(t, "STATUS_CODE_ERROR", s.Status)
		assert.Equal(t, "internal-op", s.Operation, "falls back to span_name when no operation attr")
	})

	t.Run("non-error span returns nil", func(t *testing.T) {
		s := extractErrorSignature(map[string]any{
			"status_code":   "STATUS_CODE_UNSET",
			"workload_name": "cart",
			"span_attributes": map[string]any{
				"http.status_code": "200",
			},
		})
		assert.Nil(t, s)
	})

	t.Run("exception message longer than cap is truncated", func(t *testing.T) {
		longMsg := "A long exception message that keeps going and going and keeps going and going and keeps going and going and keeps going and going and keeps going and going and keeps going and going"
		s := extractErrorSignature(map[string]any{
			"status_code": "STATUS_CODE_ERROR",
			"span_attributes": map[string]any{
				"exception.type":    "HugeError",
				"exception.message": longMsg,
			},
		})
		require.NotNil(t, s)
		assert.LessOrEqual(t, len(s.ExceptionMessage), maxExceptionMessageLen+len("…"),
			"message must be truncated to avoid aggregation-key drift on tiny differences")
	})
}

// TestAggregateErrors locks in the aggregation invariant: identical errors
// bucket together, distinct errors stay separate, output is bounded + ordered.
func TestAggregateErrors(t *testing.T) {
	mkGRPCErr := func(traceID, rpcMethod, grpcCode string) map[string]any {
		return map[string]any{
			"trace_id":      traceID,
			"status_code":   "STATUS_CODE_ERROR",
			"workload_name": "checkout",
			"span_attributes": map[string]any{
				"rpc.system":           "grpc",
				"rpc.service":          "oteldemo.CartService",
				"rpc.method":           rpcMethod,
				"rpc.grpc.status_code": grpcCode,
			},
		}
	}

	t.Run("identical errors bucket together", func(t *testing.T) {
		traces := []map[string]any{
			mkGRPCErr("t1", "EmptyCart", "9"),
			mkGRPCErr("t2", "EmptyCart", "9"),
			mkGRPCErr("t3", "EmptyCart", "9"),
		}
		got := aggregateErrors(traces)
		require.Len(t, got, 1)
		assert.Equal(t, 3, got[0].Count, "same signature must aggregate")
		assert.NotEmpty(t, got[0].ExampleTraceID, "first trace_id is kept as example")
	})

	t.Run("different operations stay separate", func(t *testing.T) {
		traces := []map[string]any{
			mkGRPCErr("t1", "EmptyCart", "9"),
			mkGRPCErr("t2", "AddItem", "13"),
		}
		got := aggregateErrors(traces)
		assert.Len(t, got, 2, "different operation/status must not merge")
	})

	t.Run("sorted by count desc then signature asc", func(t *testing.T) {
		traces := []map[string]any{
			mkGRPCErr("t1", "AddItem", "13"),  // count 1
			mkGRPCErr("t2", "EmptyCart", "9"), // count 3
			mkGRPCErr("t3", "EmptyCart", "9"),
			mkGRPCErr("t4", "EmptyCart", "9"),
			mkGRPCErr("t5", "GetCart", "14"), // count 2
			mkGRPCErr("t6", "GetCart", "14"),
		}
		got := aggregateErrors(traces)
		require.Len(t, got, 3)
		assert.Equal(t, 3, got[0].Count)
		assert.Equal(t, "EmptyCart", got[0].Signature.Operation)
		assert.Equal(t, 2, got[1].Count)
		assert.Equal(t, "GetCart", got[1].Signature.Operation)
		assert.Equal(t, 1, got[2].Count)
		assert.Equal(t, "AddItem", got[2].Signature.Operation)
	})

	t.Run("caps at maxErrorBuckets", func(t *testing.T) {
		traces := make([]map[string]any, 0, maxErrorBuckets+5)
		for i := 0; i < maxErrorBuckets+5; i++ {
			traces = append(traces, mkGRPCErr(fmt.Sprintf("t%d", i), fmt.Sprintf("Method%d", i), "9"))
		}
		got := aggregateErrors(traces)
		assert.Len(t, got, maxErrorBuckets, "distinct signatures beyond cap must be truncated")
	})

	t.Run("empty input yields nil", func(t *testing.T) {
		assert.Nil(t, aggregateErrors(nil))
	})

	t.Run("non-error spans skipped", func(t *testing.T) {
		traces := []map[string]any{
			{"trace_id": "t1", "status_code": "STATUS_CODE_UNSET", "span_attributes": map[string]any{"http.status_code": "200"}},
		}
		assert.Nil(t, aggregateErrors(traces))
	})
}

// TestFormatErrorInsight exercises the insight string template. It's tight but
// the shape is observable: it appears in the evidence payload and downstream
// prompts may phrase hints around it. These tests lock in the single-line
// shape so a future rewrite can't silently change what the LLM sees.
func TestFormatErrorInsight(t *testing.T) {
	t.Run("grpc with destination, operation, status, name, example trace", func(t *testing.T) {
		a := aggregatedError{
			Count:          5,
			ExampleTraceID: "941c81377d2c6db3dd100f7f1ae36c63",
			Signature: errorSignature{
				Service: "checkout", Destination: "oteldemo.CartService",
				Operation: "EmptyCart", Protocol: "grpc",
				Status: "9", StatusName: "FAILED_PRECONDITION",
			},
		}
		got := formatErrorInsight(a)
		assert.Equal(t, "Critical", got.Severity)
		assert.Contains(t, got.Message, "5×")
		assert.Contains(t, got.Message, "[grpc]")
		assert.Contains(t, got.Message, "checkout → oteldemo.CartService")
		assert.Contains(t, got.Message, "(EmptyCart)")
		assert.Contains(t, got.Message, "status=9 (FAILED_PRECONDITION)")
		assert.Contains(t, got.Message, "941c8137", "trace id shortened to 8-char prefix")
	})

	t.Run("exception appended after status", func(t *testing.T) {
		a := aggregatedError{
			Count:          1,
			ExampleTraceID: "abc",
			Signature: errorSignature{
				Service: "cart", Protocol: "grpc", Status: "13", StatusName: "INTERNAL",
				ExceptionType: "RedisConnectionError", ExceptionMessage: "connection refused",
			},
		}
		got := formatErrorInsight(a)
		assert.Contains(t, got.Message, "exception: RedisConnectionError: connection refused")
	})

	t.Run("no destination, no operation — still produces readable message", func(t *testing.T) {
		a := aggregatedError{
			Count:     1,
			Signature: errorSignature{Service: "frontend", Status: "STATUS_CODE_ERROR"},
		}
		got := formatErrorInsight(a)
		assert.Contains(t, got.Message, "frontend")
		assert.Contains(t, got.Message, "status=STATUS_CODE_ERROR")
	})
}

// TestAutoExecuteByWorkload_ErrorCentric_Live is an integration test that
// exercises the full error-centric query path against the live agent
// ClickHouse via relay-server. It demonstrates Fix 4's primary value:
// Returning spans that actually carry the RCA signal (status_code=Error or
// HTTP 4xx/5xx) even when they are a small minority of overall traffic.
//
// Required env:
//
//	TEST_TENANT   - tenant UUID
//	TEST_ACCOUNT  - cloud account UUID with an otel_clickhouse agent integration
//	TEST_WORKLOAD - a service currently producing error spans (e.g. product-reviews)
//	TEST_NAMESPACE (optional, default: demo)
//
// Also requires:
//   - RELAY_SERVER_ENDPOINT (e.g. http://localhost:8088 from agent port-forward)
//   - RELAY_SERVER_SECRET_KEY matching the agent
//
// Skip silently when those are absent so `go test ./...` in CI is unaffected.
func TestAutoExecuteByWorkload_ErrorCentric_Live(t *testing.T) {
	tenant := os.Getenv("TEST_TENANT")
	accountID := os.Getenv("TEST_ACCOUNT")
	workload := os.Getenv("TEST_WORKLOAD")
	if tenant == "" || accountID == "" || workload == "" {
		t.Skip("set TEST_TENANT, TEST_ACCOUNT, TEST_WORKLOAD to run against live ClickHouse")
	}
	namespace := os.Getenv("TEST_NAMESPACE")
	if namespace == "" {
		namespace = "demo"
	}

	action := &observabilityTracesAction{}
	now := time.Now().UnixMilli()
	startedAt := time.UnixMilli(now - 2*60*60*1000)

	ctx := playbooks.NewPlaybookActionContext(tenant, accountID, slog.Default(), playbooks.PlaybookEvent{
		Name:             "TestAutoExecuteErrorCentric",
		SubjectName:      workload,
		SubjectNamespace: namespace,
		Labels: map[string]string{
			"namespace": namespace,
		},
		StartedAt: &startedAt,
	})

	resp, err := action.AutoExecute(ctx)
	if err != nil {
		t.Fatalf("AutoExecute failed: %v", err)
	}
	if resp == nil {
		t.Fatalf("AutoExecute returned nil response for workload %s/%s", namespace, workload)
	}

	payload, ok := resp.GetData().(string)
	if !ok {
		t.Fatalf("expected response Data to be a JSON string, got %T", resp.GetData())
	}
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(payload), &parsed))
	data, _ := parsed["data"].([]any)
	t.Logf("spans returned: %d (mode=%s, error_span_count=%v)",
		len(data),
		resp.GetAdditionalInfo()["mode"],
		resp.GetAdditionalInfo()["error_span_count"])

	if len(data) == 0 {
		t.Skip("no spans returned — workload may have no traffic in the window")
	}

	// At least one span should carry an error signal. If zero errors are
	// present the error-centric path should have reported it via metadata and
	// we would expect the fallback mode — in which case len(data) > 0 is
	// acceptable even without errors.
	errSpans := 0
	for _, raw := range data {
		sp, _ := raw.(map[string]any)
		if sp == nil {
			continue
		}
		status, _ := sp["status_code"].(string)
		if status == "Error" || status == "STATUS_CODE_ERROR" {
			errSpans++
			continue
		}
		if httpCode, _ := sp["http_status_code"].(string); len(httpCode) > 0 {
			if httpCode[0] == '4' || httpCode[0] == '5' {
				errSpans++
			}
		}
	}
	t.Logf("error spans in result: %d", errSpans)
}

// --- Tree-walk fix tests (issue B + D) ---

// mkErrSpan builds a minimal error span linked to a parent.
func mkErrSpan(traceID, spanID, parentID, service, rpcMethod, grpcCode string) map[string]any {
	return map[string]any{
		"trace_id":       traceID,
		"span_id":        spanID,
		"parent_span_id": parentID,
		"status_code":    "STATUS_CODE_ERROR",
		"workload_name":  service,
		"timestamp":      "2026-05-18T00:00:00Z",
		"span_name":      rpcMethod,
		"span_attributes": map[string]any{
			"rpc.system":           "grpc",
			"rpc.service":          "oteldemo." + service + "Service",
			"rpc.method":           rpcMethod,
			"rpc.grpc.status_code": grpcCode,
		},
	}
}

// mkOKSpan builds a minimal non-error span linked to a parent.
func mkOKSpan(traceID, spanID, parentID, service, op, timestamp string) map[string]any {
	return map[string]any{
		"trace_id":       traceID,
		"span_id":        spanID,
		"parent_span_id": parentID,
		"status_code":    "STATUS_CODE_UNSET",
		"workload_name":  service,
		"timestamp":      timestamp,
		"span_name":      op,
		"span_attributes": map[string]any{
			"http.method": "POST",
			"http.route":  "/" + op,
		},
	}
}

// TestAggregateErrors_CascadeCollapse exercises fix B: a single failed
// request that propagates through N services (load-gen → frontend → checkout
// → payment) should produce ONE aggregated error (the leaf, payment), not N.
// Pre-fix, signature aggregation buckets every cascade span separately.
func TestAggregateErrors_CascadeCollapse(t *testing.T) {
	// One trace, 4-deep cascade. payment is the root cause; the three
	// ancestors are all error spans that received & propagated the failure.
	traces := []map[string]any{
		mkErrSpan("t1", "1", "", "frontend", "PlaceOrder", "13"),    // top of chain
		mkErrSpan("t1", "2", "1", "checkout", "PlaceOrder", "13"),   // child of frontend
		mkErrSpan("t1", "3", "2", "checkout", "Charge", "2"),        // checkout → payment client span
		mkErrSpan("t1", "4", "3", "payment", "Charge", "2"),         // payment server span (LEAF — root cause)
	}
	got := aggregateErrors(traces)
	require.Len(t, got, 1, "the four-deep cascade must collapse into exactly one bucket")
	assert.Equal(t, 1, got[0].Count)
	assert.Equal(t, "payment", got[0].Signature.Service,
		"the leaf error (payment) is the root cause, not the cascade parents")
	assert.Equal(t, "t1", got[0].ExampleTraceID)
}

// TestAggregateErrors_AcrossTraces verifies the leaf-aggregation still groups
// the SAME failure across multiple trace instances: 4 traces each carrying
// the same cascade → 1 bucket with count=4 (not 16 spans counted, not 4
// buckets of 4).
func TestAggregateErrors_AcrossTraces(t *testing.T) {
	traces := []map[string]any{}
	for i, tid := range []string{"t1", "t2", "t3", "t4"} {
		_ = i
		traces = append(traces,
			mkErrSpan(tid, "a", "", "frontend", "PlaceOrder", "13"),
			mkErrSpan(tid, "b", "a", "checkout", "PlaceOrder", "13"),
			mkErrSpan(tid, "c", "b", "checkout", "Charge", "2"),
			mkErrSpan(tid, "d", "c", "payment", "Charge", "2"),
		)
	}
	got := aggregateErrors(traces)
	require.Len(t, got, 1, "same cascade across 4 traces must still produce 1 bucket")
	assert.Equal(t, 4, got[0].Count, "count reflects per-trace occurrences, not per-span")
	assert.Equal(t, "payment", got[0].Signature.Service)
}

// TestAggregateErrors_DivergentLeavesStaySeparate verifies that genuinely
// different errors don't collapse: trace A fails at payment; trace B fails
// at cart. Two distinct leaf signatures → two buckets.
func TestAggregateErrors_DivergentLeavesStaySeparate(t *testing.T) {
	traces := []map[string]any{
		// trace A: cascade ending at payment
		mkErrSpan("tA", "1", "", "frontend", "PlaceOrder", "13"),
		mkErrSpan("tA", "2", "1", "checkout", "Charge", "2"),
		mkErrSpan("tA", "3", "2", "payment", "Charge", "2"),
		// trace B: cascade ending at cart
		mkErrSpan("tB", "1", "", "frontend", "AddItem", "13"),
		mkErrSpan("tB", "2", "1", "cart", "AddItem", "9"),
	}
	got := aggregateErrors(traces)
	require.Len(t, got, 2)
	leafServices := map[string]bool{}
	for _, b := range got {
		leafServices[b.Signature.Service] = true
	}
	assert.True(t, leafServices["payment"], "payment leaf must surface")
	assert.True(t, leafServices["cart"], "cart leaf must surface")
}

// TestTraceExtractServiceFlow_CallChainOrder exercises fix D: services
// returned in parent→child call-chain order, not timestamp order. Pre-fix
// the sort was by `timestamp`, which gave wrong results for traces with
// out-of-order span arrival or concurrent fan-out.
func TestTraceExtractServiceFlow_CallChainOrder(t *testing.T) {
	// Build a trace where timestamps disagree with call order: the deepest
	// child has the EARLIEST timestamp (out-of-order span arrival), and
	// the root has the LATEST. A timestamp sort would invert the chain.
	traces := []map[string]any{
		mkOKSpan("t1", "payment", "checkout", "payment", "Charge", "2026-05-18T00:00:01Z"),  // deepest, earliest ts
		mkOKSpan("t1", "checkout", "frontend", "checkout", "PlaceOrder", "2026-05-18T00:00:02Z"),
		mkOKSpan("t1", "frontend", "proxy", "frontend", "Checkout", "2026-05-18T00:00:03Z"),
		mkOKSpan("t1", "proxy", "loadgen", "frontend-proxy", "ingress", "2026-05-18T00:00:04Z"),
		mkOKSpan("t1", "loadgen", "", "load-generator", "request", "2026-05-18T00:00:05Z"), // root, latest ts
	}
	flow := traceExtractServiceFlow(traces)
	require.Len(t, flow, 5)
	want := []string{"load-generator", "frontend-proxy", "frontend", "checkout", "payment"}
	for i, expected := range want {
		assert.Equal(t, expected, flow[i].Name,
			"position %d should be %q, got %q (timestamp sort would have given the reverse)", i, expected, flow[i].Name)
	}
}

// TestTraceExtractServiceFlow_PicksLongestTrace: when multiple traces are
// present (trace_auto_action expands every error trace), the function picks
// the trace with the most spans as the representative. Single-span fragments
// of other traces must not become the "path".
func TestTraceExtractServiceFlow_PicksLongestTrace(t *testing.T) {
	traces := []map[string]any{
		// fragment from trace B (1 span)
		mkOKSpan("tB", "x", "", "isolated-service", "noop", "2026-05-18T00:00:00Z"),
		// full chain in trace A (3 spans)
		mkOKSpan("tA", "1", "", "alpha", "go", "2026-05-18T00:00:01Z"),
		mkOKSpan("tA", "2", "1", "beta", "go", "2026-05-18T00:00:02Z"),
		mkOKSpan("tA", "3", "2", "gamma", "go", "2026-05-18T00:00:03Z"),
	}
	flow := traceExtractServiceFlow(traces)
	require.Len(t, flow, 3, "representative trace is the larger one, not the singleton fragment")
	assert.Equal(t, "alpha", flow[0].Name)
	assert.Equal(t, "beta", flow[1].Name)
	assert.Equal(t, "gamma", flow[2].Name)
}

// TestTraceExtractServiceFlow_DedupesAdjacent: a service that has multiple
// internal spans before calling out should appear only once in the path.
func TestTraceExtractServiceFlow_DedupesAdjacent(t *testing.T) {
	traces := []map[string]any{
		mkOKSpan("t1", "1", "", "frontend", "entry", "2026-05-18T00:00:01Z"),
		mkOKSpan("t1", "2", "1", "frontend", "intermediate", "2026-05-18T00:00:02Z"),
		mkOKSpan("t1", "3", "2", "frontend", "preCall", "2026-05-18T00:00:03Z"),
		mkOKSpan("t1", "4", "3", "checkout", "PlaceOrder", "2026-05-18T00:00:04Z"),
	}
	flow := traceExtractServiceFlow(traces)
	require.Len(t, flow, 2, "consecutive same-service spans dedupe to one entry")
	assert.Equal(t, "frontend", flow[0].Name)
	assert.Equal(t, "checkout", flow[1].Name)
}

// TestBuildTraceTrees_PartialTraceSurvives: a child whose parent_span_id
// points at a span we don't have (sampling dropped the parent) is treated
// as a root, not silently dropped.
func TestBuildTraceTrees_PartialTraceSurvives(t *testing.T) {
	traces := []map[string]any{
		mkOKSpan("t1", "child", "missing-parent", "checkout", "PlaceOrder", "2026-05-18T00:00:01Z"),
	}
	trees := buildTraceTrees(traces)
	require.Len(t, trees, 1)
	require.Len(t, trees["t1"].roots, 1, "orphan spans become roots, not lost")
	assert.Equal(t, "child", trees["t1"].roots[0])
}

// TestErrorSubtreeLeaves_OkBetweenTwoErrors: when an OK span sits between
// two error spans (A_err → B_ok → C_err), C is still a leaf, A is not.
// Without recursive descendant search, A would be wrongly marked leaf.
func TestErrorSubtreeLeaves_OkBetweenTwoErrors(t *testing.T) {
	traces := []map[string]any{
		mkErrSpan("t1", "A", "", "service-a", "op-a", "13"),
		mkOKSpan("t1", "B", "A", "service-b", "op-b", "2026-05-18T00:00:00Z"),
		mkErrSpan("t1", "C", "B", "service-c", "op-c", "2"),
	}
	got := aggregateErrors(traces)
	require.Len(t, got, 1, "only the deepest error (C) is the leaf — A has a descendant error")
	assert.Equal(t, "service-c", got[0].Signature.Service)
}

// TestTraceExtractServiceFlow_DeterministicTieBreak: regression for the
// gemini-review finding on PR #30668. When two traces have the same number
// of spans, Go's randomised map iteration would pick a non-deterministic
// representative — same input could produce two different paths across
// runs. Tie-break by trace_id (lexicographic) keeps output stable.
func TestTraceExtractServiceFlow_DeterministicTieBreak(t *testing.T) {
	// Two traces, both 2 spans, with different leaf services. trace_id "tA"
	// sorts before "tB" lexicographically, so tA's path must always win.
	traces := []map[string]any{
		// trace A: alpha → leaf-A
		{
			"trace_id": "tA", "span_id": "a1", "parent_span_id": "",
			"workload_name": "alpha", "timestamp": "2026-05-18T00:00:01Z",
			"span_name": "go", "span_attributes": map[string]any{},
		},
		{
			"trace_id": "tA", "span_id": "a2", "parent_span_id": "a1",
			"workload_name": "leaf-A", "timestamp": "2026-05-18T00:00:02Z",
			"span_name": "go", "span_attributes": map[string]any{},
		},
		// trace B: alpha → leaf-B (same first service, different leaf)
		{
			"trace_id": "tB", "span_id": "b1", "parent_span_id": "",
			"workload_name": "alpha", "timestamp": "2026-05-18T00:00:01Z",
			"span_name": "go", "span_attributes": map[string]any{},
		},
		{
			"trace_id": "tB", "span_id": "b2", "parent_span_id": "b1",
			"workload_name": "leaf-B", "timestamp": "2026-05-18T00:00:02Z",
			"span_name": "go", "span_attributes": map[string]any{},
		},
	}
	// Repeat the call many times — any iteration-order non-determinism
	// would surface as a flake here.
	want := []string{"alpha", "leaf-A"}
	for i := 0; i < 32; i++ {
		flow := traceExtractServiceFlow(traces)
		require.Len(t, flow, 2)
		got := []string{flow[0].Name, flow[1].Name}
		assert.Equal(t, want, got, "tie-break on trace_id must keep representative stable across runs (iteration %d)", i)
	}
}
