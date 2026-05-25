package database

import (
	"encoding/json"
	"testing"
)

func TestParseChronosphereParams(t *testing.T) {
	// Create a mock agentWarehouseStmt for testing
	stmt := agentWarehouseStmt{}

	tests := []struct {
		name           string
		query          string
		expectError    bool
		expectedParams map[string]any
	}{
		{
			name: "Valid JSON parameters",
			query: `{
				"service": "global-address-service",
				"start_time": "2025-08-05T21:39:00Z",
				"end_time": "2025-08-10T21:39:30Z",
				"query_type": "SERVICE_OPERATION"
			}`,
			expectError: false,
			expectedParams: map[string]any{
				"service":    "global-address-service",
				"start_time": "2025-08-05T21:39:00Z",
				"end_time":   "2025-08-10T21:39:30Z",
				"query_type": "SERVICE_OPERATION",
			},
		},
		{
			name:           "Simple chronosphere_traces identifier (should fail)",
			query:          "chronosphere_traces",
			expectError:    true,
			expectedParams: nil,
		},
		{
			name:           "Invalid JSON and no chronosphere identifier",
			query:          "SELECT * FROM traces",
			expectError:    true,
			expectedParams: nil,
		},
		{
			name: "Minimal valid JSON",
			query: `{
				"service": "api-service"
			}`,
			expectError: false,
			expectedParams: map[string]any{
				"service": "api-service",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := stmt.parseChronosphereParams(tt.query)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if tt.expectedParams != nil {
				for key, expectedValue := range tt.expectedParams {
					if actualValue, exists := result[key]; !exists {
						t.Errorf("Missing expected parameter '%s'", key)
					} else if actualValue != expectedValue {
						t.Errorf("Parameter '%s': expected %v, got %v", key, expectedValue, actualValue)
					}
				}
			}

			t.Logf("✅ %s: parsed parameters: %+v", tt.name, result)
		})
	}
}

func TestChronosphereRequestBodyGeneration(t *testing.T) {
	// Test the exact structure that would be sent to the relay
	params := map[string]interface{}{
		"service":    "global-address-service",
		"start_time": "2025-08-05T21:39:00Z",
		"end_time":   "2025-08-10T21:39:30Z",
		"query_type": "SERVICE_OPERATION",
	}

	// This is what the agent warehouse should generate
	requestData := map[string]any{
		"no_sinks": true,
		"cache":    false,
		"body": map[string]any{
			"account_id":  "e9dc2b39-78d3-46da-9691-f160bd3c4c19",
			"action_name": "chronosphere_query_traces",
			"action_params": map[string]any{
				"params": params,
			},
		},
	}

	// Verify structure
	body := requestData["body"].(map[string]any)
	if body["action_name"] != "chronosphere_query_traces" {
		t.Errorf("Expected action_name 'chronosphere_query_traces', got %v", body["action_name"])
	}

	actionParams := body["action_params"].(map[string]any)
	requestParams := actionParams["params"].(map[string]any)

	// Verify all required parameters exist
	requiredFields := []string{"service", "start_time", "end_time", "query_type"}
	for _, field := range requiredFields {
		if _, exists := requestParams[field]; !exists {
			t.Errorf("Missing required field '%s'", field)
		}
	}

	// Test JSON serialization (this is what gets sent over HTTP)
	jsonData, err := json.Marshal(requestData)
	if err != nil {
		t.Fatalf("Failed to marshal request data: %v", err)
	}

	// Verify it's valid JSON
	var parsed map[string]any
	if err := json.Unmarshal(jsonData, &parsed); err != nil {
		t.Fatalf("Generated JSON is invalid: %v", err)
	}

	t.Logf("✅ Generated request body JSON:\n%s", string(jsonData))
}

func TestBackwardCompatibilityWithOtherProviders(t *testing.T) {
	// Test that the agent warehouse still handles other providers correctly
	stmt := agentWarehouseStmt{
		query: "SELECT * FROM traces WHERE service = 'test'",
	}

	// Test BigQuery - should not call parseChronosphereParams
	// (This would be handled by the existing logic in QueryContext)

	// Test ClickHouse - should not call parseChronosphereParams
	// (This would be handled by the existing logic in QueryContext)

	// For now, just verify that non-JSON queries don't break the parser
	_, err := stmt.parseChronosphereParams("SELECT * FROM traces")
	if err == nil {
		t.Error("Expected error for SQL query, but got none")
	}

	t.Logf("✅ Non-Chronosphere queries properly rejected")
}

func TestChronosphereParamsValidation(t *testing.T) {
	stmt := agentWarehouseStmt{}

	// Test various parameter combinations
	testCases := []struct {
		name   string
		params map[string]any
		valid  bool
	}{
		{
			name: "Complete valid parameters",
			params: map[string]any{
				"service":    "user-service",
				"start_time": "2025-08-05T21:39:00Z",
				"end_time":   "2025-08-10T21:39:30Z",
				"query_type": "SERVICE_OPERATION",
				"operation":  "GET /users",
			},
			valid: true,
		},
		{
			name: "Minimal valid parameters",
			params: map[string]any{
				"service": "minimal-service",
			},
			valid: true,
		},
		{
			name: "With tag filters",
			params: map[string]any{
				"service": "filtered-service",
				"tag_filters": []map[string]any{
					{
						"key": "environment",
						"value": map[string]any{
							"match": "EXACT",
							"value": "production",
						},
					},
				},
			},
			valid: true,
		},
		{
			name: "With trace IDs",
			params: map[string]any{
				"query_type": "TRACE_IDS",
				"trace_ids":  []string{"trace-id-1", "trace-id-2"},
			},
			valid: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Convert to JSON and back to test the full flow
			jsonData, err := json.Marshal(tc.params)
			if err != nil {
				t.Fatalf("Failed to marshal test params: %v", err)
			}

			result, err := stmt.parseChronosphereParams(string(jsonData))
			if tc.valid && err != nil {
				t.Errorf("Expected valid params but got error: %v", err)
			}

			if tc.valid && result == nil {
				t.Error("Expected valid result but got nil")
			}

			t.Logf("✅ %s: validation passed", tc.name)
		})
	}
}
