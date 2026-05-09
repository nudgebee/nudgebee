package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewChronosphereRows(t *testing.T) {
	// Mock Chronosphere response data in OpenTelemetry format
	mockResponse := map[string]any{
		"traces": []any{
			map[string]any{
				"resource_spans": []any{
					map[string]any{
						"scope_spans": []any{
							map[string]any{
								"spans": []any{
									map[string]any{
										"trace_id":             "trace-123",
										"span_id":              "span-456",
										"parent_span_id":       "parent-789",
										"name":                 "GET /api/users",
										"start_time_unix_nano": "1691234567890123456",
										"end_time_unix_nano":   "1691234567890223456",
										"status":               "OK",
										"attributes": []any{
											map[string]any{
												"key": "service.name",
												"value": map[string]any{
													"string_value": "user-service",
												},
											},
											map[string]any{
												"key": "service.namespace",
												"value": map[string]any{
													"string_value": "production",
												},
											},
											map[string]any{
												"key": "http.method",
												"value": map[string]any{
													"string_value": "GET",
												},
											},
										},
									},
									map[string]any{
										"trace_id":             "trace-123",
										"span_id":              "span-789",
										"parent_span_id":       "span-456",
										"name":                 "SELECT * FROM users",
										"start_time_unix_nano": "1691234567890130000",
										"end_time_unix_nano":   "1691234567890220000",
										"status":               "OK",
										"attributes": []any{
											map[string]any{
												"key": "service.name",
												"value": map[string]any{
													"string_value": "database-service",
												},
											},
											map[string]any{
												"key": "service.namespace",
												"value": map[string]any{
													"string_value": "production",
												},
											},
											map[string]any{
												"key": "db.statement",
												"value": map[string]any{
													"string_value": "SELECT * FROM users WHERE id = ?",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	accountId := "test-account-123"
	rows, err := newChronosphereRows(mockResponse, accountId)

	assert.Nil(t, err)
	assert.NotNil(t, rows)

	// Check columns match traces_v2 format
	expectedCols := []string{
		"tenant_id", "trace_id", "span_id", "parent_span_id", "workload_name", "workload_namespace",
		"timestamp", "status_code", "span_name", "resource", "duration_ns", "destination_workload_name",
		"destination_workload_namespace", "destination_name", "headers", "http_status_code",
		"request_payload", "http_response", "trace_source", "spanattributes", "account_id",
	}
	assert.Equal(t, expectedCols, rows.cols)

	// Check we have 2 rows (2 spans)
	assert.Equal(t, 2, len(rows.rows))

	// Check first row data (indices match traces_v2 format)
	firstRow := rows.rows[0].([]any)
	assert.Equal(t, "", firstRow[0])                    // tenant_id (empty)
	assert.Equal(t, "trace-123", firstRow[1])           // trace_id
	assert.Equal(t, "span-456", firstRow[2])            // span_id
	assert.Equal(t, "parent-789", firstRow[3])          // parent_span_id
	assert.Equal(t, "user-service", firstRow[4])        // workload_name (service_name)
	assert.Equal(t, "production", firstRow[5])          // workload_namespace (service_namespace)
	assert.Equal(t, "1691234567890123456", firstRow[6]) // timestamp (start_time)
	assert.Equal(t, "STATUS_CODE_OK", firstRow[7])      // status_code
	assert.Equal(t, "GET /api/users", firstRow[8])      // span_name (operation_name)
	// firstRow[9] is resource - could be empty if no db.statement/http.url
	// firstRow[10] is duration_ns - calculated from start/end times
	assert.Equal(t, accountId, firstRow[20]) // account_id

	// Verify trace source is set correctly
	assert.Equal(t, "otel", firstRow[18]) // trace_source

	// Verify spanattributes contains extracted attributes
	spanAttributes := firstRow[19].(string) // spanattributes
	assert.Contains(t, spanAttributes, "service.name")
	assert.Contains(t, spanAttributes, "user-service")
	assert.Contains(t, spanAttributes, "http.method")
	assert.Contains(t, spanAttributes, "GET")

	// Check second row data (indices match traces_v2 format)
	secondRow := rows.rows[1].([]any)
	assert.Equal(t, "", secondRow[0])                    // tenant_id (empty)
	assert.Equal(t, "trace-123", secondRow[1])           // trace_id
	assert.Equal(t, "span-789", secondRow[2])            // span_id
	assert.Equal(t, "span-456", secondRow[3])            // parent_span_id
	assert.Equal(t, "database-service", secondRow[4])    // workload_name (service_name)
	assert.Equal(t, "production", secondRow[5])          // workload_namespace (service_namespace)
	assert.Equal(t, "SELECT * FROM users", secondRow[8]) // span_name (operation_name)

	t.Logf("✅ Chronosphere response parsing successful - converted %d spans to tabular format", len(rows.rows))
}

func TestNewChronosphereRowsWithSpansDirectly(t *testing.T) {
	// Test fallback when response has spans directly (not in traces array)
	mockResponse := map[string]any{
		"spans": []any{
			map[string]any{
				"trace_id":             "trace-abc",
				"span_id":              "span-def",
				"name":                 "POST /api/orders",
				"start_time_unix_nano": "1691234567890000000",
				"end_time_unix_nano":   "1691234567890100000",
				"status":               "OK",
				"attributes": []any{
					map[string]any{
						"key": "service.name",
						"value": map[string]any{
							"string_value": "order-service",
						},
					},
				},
			},
		},
	}

	accountId := "test-account-456"
	rows, err := newChronosphereRows(mockResponse, accountId)

	assert.Nil(t, err)
	assert.NotNil(t, rows)
	assert.Equal(t, 1, len(rows.rows))

	firstRow := rows.rows[0].([]any)
	assert.Equal(t, "", firstRow[0])                 // tenant_id (empty)
	assert.Equal(t, "trace-abc", firstRow[1])        // trace_id
	assert.Equal(t, "span-def", firstRow[2])         // span_id
	assert.Equal(t, "POST /api/orders", firstRow[8]) // span_name (operation_name)
	assert.Equal(t, "order-service", firstRow[4])    // workload_name (service_name)

	t.Logf("✅ Direct spans parsing successful")
}

func TestNewChronosphereRowsInvalidResponse(t *testing.T) {
	// Test error handling for invalid response format
	invalidResponse := map[string]any{
		"data": []any{
			map[string]any{
				"columns": []string{"col1", "col2"},
			},
		},
	}

	accountId := "test-account-789"
	rows, err := newChronosphereRows(invalidResponse, accountId)

	assert.NotNil(t, err)
	assert.Nil(t, rows)
	assert.Contains(t, err.Error(), "unable to find traces or spans")

	t.Logf("✅ Invalid response properly rejected: %v", err)
}

func TestNewAgentWarehouseRowsProviderDetection(t *testing.T) {
	// Test that newAgentWarehouseRows correctly routes to Chronosphere handling
	mockResponse := map[string]any{
		"traces": []any{
			map[string]any{
				"resource_spans": []any{
					map[string]any{
						"scope_spans": []any{
							map[string]any{
								"spans": []any{
									map[string]any{
										"trace_id": "trace-routing-test",
										"span_id":  "span-routing-test",
										"name":     "test-operation",
										"attributes": []any{
											map[string]any{
												"key": "service.name",
												"value": map[string]any{
													"string_value": "routing-test-service",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	accountId := "routing-test-account"

	// Test Chronosphere provider
	rows, err := NewAgentWarehouseRows(mockResponse, accountId, "agent_warehouse_chronosphere")
	assert.Nil(t, err)
	assert.NotNil(t, rows)
	assert.Equal(t, 1, len(rows.rows))

	// Should have traces_v2 column structure
	expectedCols := []string{
		"tenant_id", "trace_id", "span_id", "parent_span_id", "workload_name", "workload_namespace",
		"timestamp", "status_code", "span_name", "resource", "duration_ns", "destination_workload_name",
		"destination_workload_namespace", "destination_name", "headers", "http_status_code",
		"request_payload", "http_response", "trace_source", "spanattributes", "account_id",
	}
	assert.Equal(t, expectedCols, rows.cols)

	t.Logf("✅ Provider routing works correctly for Chronosphere")
}
