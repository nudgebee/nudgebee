package observability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Test buildBinaryClause function with all operators
func TestBuildBinaryClause(t *testing.T) {
	testCases := []struct {
		name           string
		binary         query.BinaryWhereClause
		expectedResult string
	}{
		{
			name: "Eq operator with string value",
			binary: query.BinaryWhereClause{
				"service": {Eq: "api-service"},
			},
			expectedResult: `service:"api-service"`,
		},
		{
			name: "Eq operator with numeric value",
			binary: query.BinaryWhereClause{
				"status_code": {Eq: 200},
			},
			expectedResult: `status_code:"200"`,
		},
		{
			name: "Nq operator",
			binary: query.BinaryWhereClause{
				"environment": {Nq: "staging"},
			},
			expectedResult: `-environment:"staging"`,
		},
		{
			name: "Gt operator",
			binary: query.BinaryWhereClause{
				"duration": {Gt: 1000},
			},
			expectedResult: `duration:>1000`,
		},
		{
			name: "Lt operator",
			binary: query.BinaryWhereClause{
				"duration": {Lt: 500},
			},
			expectedResult: `duration:<500`,
		},
		{
			name: "Gte operator",
			binary: query.BinaryWhereClause{
				"status_code": {Gte: 200},
			},
			expectedResult: `status_code:>=200`,
		},
		{
			name: "Lte operator",
			binary: query.BinaryWhereClause{
				"status_code": {Lte: 500},
			},
			expectedResult: `status_code:<=500`,
		},
		{
			name: "Like operator",
			binary: query.BinaryWhereClause{
				"message": {Like: "%error%"},
			},
			expectedResult: `message:"%error%"`,
		},
		{
			name: "ILike operator",
			binary: query.BinaryWhereClause{
				"message": {ILike: "%Error%"},
			},
			expectedResult: `message:"%Error%"`,
		},
		{
			name: "In operator with array",
			binary: query.BinaryWhereClause{
				"status": {In: []any{"ok", "error", "warning"}},
			},
			expectedResult: `(status:"ok" OR status:"error" OR status:"warning")`,
		},
		{
			name: "NotIn operator with array",
			binary: query.BinaryWhereClause{
				"env": {NotIn: []any{"dev", "test"}},
			},
			expectedResult: `-env:"dev" AND -env:"test"`,
		},
		{
			name: "Contains operator",
			binary: query.BinaryWhereClause{
				"message": {Contains: "exception"},
			},
			expectedResult: `message:*exception*`,
		},
		{
			name: "HasKey operator",
			binary: query.BinaryWhereClause{
				"custom_field": {HasKey: true},
			},
			expectedResult: `has:custom_field`,
		},
		{
			name: "IsNull operator with true",
			binary: query.BinaryWhereClause{
				"error": {IsNull: true},
			},
			expectedResult: `!has:error`,
		},
		{
			name: "IsNull operator with false",
			binary: query.BinaryWhereClause{
				"error": {IsNull: false},
			},
			expectedResult: `has:error`,
		},
		{
			name: "Multiple fields with AND logic",
			binary: query.BinaryWhereClause{
				"service": {Eq: "api"},
				"env":     {Eq: "prod"},
			},
			expectedResult: `service:"api" AND env:"prod"`,
		},
		{
			name: "Field with multiple operators",
			binary: query.BinaryWhereClause{
				"status_code": {
					Gte: 200,
					Lt:  300,
				},
			},
			expectedResult: `status_code:>=200 AND status_code:<300`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := buildBinaryClause(tc.binary)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedResult, result)
		})
	}
}

// Test buildWhereClause function with different clause types
func TestBuildWhereClause(t *testing.T) {
	testCases := []struct {
		name           string
		whereClause    query.QueryWhereClause
		expectedResult string
		expectError    bool
	}{
		{
			name: "Simple binary clause",
			whereClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"service": {Eq: "api"},
				},
			},
			expectedResult: `service:"api"`,
			expectError:    false,
		},
		{
			name: "AND clause with multiple conditions",
			whereClause: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{
						Binary: query.BinaryWhereClause{
							"service": {Eq: "api"},
						},
					},
					{
						Binary: query.BinaryWhereClause{
							"env": {Eq: "prod"},
						},
					},
				},
			},
			expectedResult: `(service:"api" AND env:"prod")`,
			expectError:    false,
		},
		{
			name: "OR clause with multiple conditions",
			whereClause: query.QueryWhereClause{
				Or: []query.QueryWhereClause{
					{
						Binary: query.BinaryWhereClause{
							"status": {Eq: "error"},
						},
					},
					{
						Binary: query.BinaryWhereClause{
							"status": {Eq: "warning"},
						},
					},
				},
			},
			expectedResult: `(status:"error" OR status:"warning")`,
			expectError:    false,
		},
		{
			name: "Nested AND and OR clauses",
			whereClause: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{
						Binary: query.BinaryWhereClause{
							"service": {Eq: "api"},
						},
					},
					{
						Or: []query.QueryWhereClause{
							{
								Binary: query.BinaryWhereClause{
									"env": {Eq: "prod"},
								},
							},
							{
								Binary: query.BinaryWhereClause{
									"env": {Eq: "staging"},
								},
							},
						},
					},
				},
			},
			expectedResult: `(service:"api" AND (env:"prod" OR env:"staging"))`,
			expectError:    false,
		},
		{
			name: "NOT clause should return error",
			whereClause: query.QueryWhereClause{
				Not: &query.QueryWhereClause{
					Binary: query.BinaryWhereClause{
						"service": {Eq: "api"},
					},
				},
			},
			expectedResult: "",
			expectError:    true,
		},
		{
			name:           "Empty where clause",
			whereClause:    query.QueryWhereClause{},
			expectedResult: "",
			expectError:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := buildWhereClause(tc.whereClause)
			if tc.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "NOT clauses are not supported")
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedResult, result)
			}
		})
	}
}

// Test ConvertToDatadogTraceQuery
func TestConvertToDatadogTraceQuery(t *testing.T) {
	testCases := []struct {
		name          string
		request       TracesQueryBuilderRequest
		expectedQuery string
		expectError   bool
	}{
		{
			name: "Simple trace query",
			request: TracesQueryBuilderRequest{
				Where: query.QueryWhereClause{
					Binary: query.BinaryWhereClause{
						"service": {Eq: "api-service"},
					},
				},
			},
			expectedQuery: `service:"api-service"`,
			expectError:   false,
		},
		{
			name: "Trace query with multiple conditions",
			request: TracesQueryBuilderRequest{
				Where: query.QueryWhereClause{
					And: []query.QueryWhereClause{
						{
							Binary: query.BinaryWhereClause{
								"service": {Eq: "api"},
							},
						},
						{
							Binary: query.BinaryWhereClause{
								"@http.status_code": {Gte: 400},
							},
						},
					},
				},
			},
			expectedQuery: `(service:"api" AND @http.status_code:>=400)`,
			expectError:   false,
		},
		{
			name: "Empty trace query",
			request: TracesQueryBuilderRequest{
				Where: query.QueryWhereClause{},
			},
			expectedQuery: "",
			expectError:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ConvertToDatadogTraceQuery(tc.request)
			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedQuery, result)
			}
		})
	}
}

// Test ConvertToDatadogLogQuery
func TestConvertToDatadogLogQuery(t *testing.T) {
	testCases := []struct {
		name          string
		request       LogsQueryBuilderRequest
		expectedQuery string
		expectError   bool
	}{
		{
			name: "Simple log query",
			request: LogsQueryBuilderRequest{
				Where: query.QueryWhereClause{
					Binary: query.BinaryWhereClause{
						"host": {Eq: "server-01"},
					},
				},
			},
			expectedQuery: `host:"server-01"`,
			expectError:   false,
		},
		{
			name: "Log query with OR condition",
			request: LogsQueryBuilderRequest{
				Where: query.QueryWhereClause{
					Or: []query.QueryWhereClause{
						{
							Binary: query.BinaryWhereClause{
								"status": {Eq: "error"},
							},
						},
						{
							Binary: query.BinaryWhereClause{
								"status": {Eq: "critical"},
							},
						},
					},
				},
			},
			expectedQuery: `(status:"error" OR status:"critical")`,
			expectError:   false,
		},
		{
			name: "Empty log query",
			request: LogsQueryBuilderRequest{
				Where: query.QueryWhereClause{},
			},
			expectedQuery: "",
			expectError:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ConvertToDatadogLogQuery(tc.request)
			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedQuery, result)
			}
		})
	}
}

// Test DatadogSource.GetLabelMapping
func TestDatadogSource_GetLabelMapping(t *testing.T) {
	s := &DatadogSource{}
	mapping := s.GetLabelMapping()

	expectedMapping := map[string]string{
		"timestamp": "@timestamp",
		"body":      "",
		"namespace": "kube_namespace",
		"container": "container_name",
		"pod":       "pod_name",
		"node":      "kube_node",
	}

	assert.Equal(t, expectedMapping, mapping)
}

// Test DatadogTraceSource.GetLabelMapping
func TestDatadogTraceSource_GetLabelMapping(t *testing.T) {
	s := &DatadogTraceSource{}
	mapping := s.GetLabelMapping()

	// Test a few key mappings
	assert.Equal(t, "kube_namespace", mapping["workload_namespace"])
	assert.Equal(t, "service", mapping["workload_name"])
	assert.Equal(t, "@http.status_code", mapping["http_status_code"])
	assert.Equal(t, "operation_name", mapping["span_name"])
	assert.Equal(t, "resource_name", mapping["resource"])
	assert.Equal(t, "status", mapping["status_code"])

	// Ensure mapping contains expected number of entries
	assert.Greater(t, len(mapping), 10)
}

// Test DatadogSource.ConvertDatadogToOutputLogs
func TestDatadogSource_ConvertDatadogToOutputLogs(t *testing.T) {
	s := &DatadogSource{}

	testCases := []struct {
		name           string
		inputLogs      []integrations.DatadogLog
		expectedOutput []OutputLog
	}{
		{
			name: "Convert single log entry",
			inputLogs: []integrations.DatadogLog{
				{
					Type: "log",
					ID:   "log-123",
					Attributes: integrations.DatadogLogAttributes{
						Host:      "server-01",
						Message:   "Application started",
						Service:   "api-service",
						Status:    "info",
						Timestamp: "2023-01-01T12:00:00Z",
						Tags:      []string{"env:prod", "version:1.0"},
					},
				},
			},
			expectedOutput: []OutputLog{
				{
					Timestamp: "2023-01-01T12:00:00Z",
					Message:   "Application started",
					Labels: map[string]interface{}{
						"host":    "server-01",
						"service": "api-service",
						"type":    "log",
						"id":      "log-123",
						"env":     "prod",
						"version": "1.0",
					},
					Severity: "info",
				},
			},
		},
		{
			name: "Convert log with tags without colons",
			inputLogs: []integrations.DatadogLog{
				{
					Type: "log",
					ID:   "log-456",
					Attributes: integrations.DatadogLogAttributes{
						Host:      "server-02",
						Message:   "Error occurred",
						Service:   "worker",
						Status:    "error",
						Timestamp: "2023-01-01T13:00:00Z",
						Tags:      []string{"critical", "team:backend"},
					},
				},
			},
			expectedOutput: []OutputLog{
				{
					Timestamp: "2023-01-01T13:00:00Z",
					Message:   "Error occurred",
					Labels: map[string]interface{}{
						"host":     "server-02",
						"service":  "worker",
						"type":     "log",
						"id":       "log-456",
						"critical": true,
						"team":     "backend",
					},
					Severity: "error",
				},
			},
		},
		{
			name: "Convert multiple log entries",
			inputLogs: []integrations.DatadogLog{
				{
					Type: "log",
					ID:   "log-1",
					Attributes: integrations.DatadogLogAttributes{
						Host:      "host-1",
						Message:   "Message 1",
						Service:   "service-1",
						Status:    "info",
						Timestamp: "2023-01-01T10:00:00Z",
						Tags:      []string{},
					},
				},
				{
					Type: "log",
					ID:   "log-2",
					Attributes: integrations.DatadogLogAttributes{
						Host:      "host-2",
						Message:   "Message 2",
						Service:   "service-2",
						Status:    "warn",
						Timestamp: "2023-01-01T11:00:00Z",
						Tags:      []string{"app:frontend"},
					},
				},
			},
			expectedOutput: []OutputLog{
				{
					Timestamp: "2023-01-01T10:00:00Z",
					Message:   "Message 1",
					Labels: map[string]interface{}{
						"host":    "host-1",
						"service": "service-1",
						"type":    "log",
						"id":      "log-1",
					},
					Severity: "info",
				},
				{
					Timestamp: "2023-01-01T11:00:00Z",
					Message:   "Message 2",
					Labels: map[string]interface{}{
						"host":    "host-2",
						"service": "service-2",
						"type":    "log",
						"id":      "log-2",
						"app":     "frontend",
					},
					Severity: "warn",
				},
			},
		},
		{
			name:           "Empty log list",
			inputLogs:      []integrations.DatadogLog{},
			expectedOutput: []OutputLog{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := s.ConvertDatadogToOutputLogs(tc.inputLogs)
			assert.Equal(t, len(tc.expectedOutput), len(result))

			for i := range result {
				assert.Equal(t, tc.expectedOutput[i].Timestamp, result[i].Timestamp)
				assert.Equal(t, tc.expectedOutput[i].Message, result[i].Message)
				assert.Equal(t, tc.expectedOutput[i].Severity, result[i].Severity)
				assert.Equal(t, tc.expectedOutput[i].Labels, result[i].Labels)
			}
		})
	}
}

// Test DatadogTraceSource.GetTimeFilter
func TestDatadogTraceSource_GetTimeFilter(t *testing.T) {
	s := &DatadogTraceSource{}

	testCases := []struct {
		name              string
		request           TracesV3Request
		expectedStartTime int64
		expectedEndTime   int64
		expectError       bool
	}{
		{
			name: "Using StartTime and EndTime from request",
			request: TracesV3Request{
				StartTime: 1672531200000, // 2023-01-01 00:00:00
				EndTime:   1672534800000, // 2023-01-01 01:00:00
			},
			expectedStartTime: 1672531200000,
			expectedEndTime:   1672534800000,
			expectError:       false,
		},
		{
			name: "Using Gte and Lte from timestamp in Where clause",
			request: TracesV3Request{
				QueryRequest: TracesQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: query.BinaryWhereClause{
							"timestamp": {
								Gte: "2023-01-01T00:00:00Z",
								Lte: "2023-01-01T01:00:00Z",
							},
						},
					},
				},
			},
			expectedStartTime: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(),
			expectedEndTime:   time.Date(2023, 1, 1, 1, 0, 0, 0, time.UTC).UnixMilli(),
			expectError:       false,
		},
		{
			name: "Using Between in timestamp",
			request: TracesV3Request{
				QueryRequest: TracesQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: query.BinaryWhereClause{
							"timestamp": {
								Between: map[query.BinaryWhereClauseType]string{
									Gte: "2023-01-01T00:00:00Z",
									Lte: "2023-01-01T02:00:00Z",
								},
							},
						},
					},
				},
			},
			expectedStartTime: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(),
			expectedEndTime:   time.Date(2023, 1, 1, 2, 0, 0, 0, time.UTC).UnixMilli(),
			expectError:       false,
		},
		{
			name: "Default time range when nothing specified",
			request: TracesV3Request{
				QueryRequest: TracesQueryBuilderRequest{},
			},
			// Will use default: 1 hour ago to now
			// We'll just check that start < end and they're reasonable
			expectedStartTime: 0, // Will be validated differently
			expectedEndTime:   0, // Will be validated differently
			expectError:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			startTime, endTime, err := s.GetTimeFilter(tc.request)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)

				if tc.name == "Default time range when nothing specified" {
					// For default range, just verify start < end and they're recent
					assert.Less(t, startTime, endTime)
					now := time.Now().UnixMilli()
					assert.InDelta(t, now, endTime, float64(5*time.Second.Milliseconds()))
					assert.InDelta(t, now-time.Hour.Milliseconds(), startTime, float64(5*time.Second.Milliseconds()))
				} else {
					assert.Equal(t, tc.expectedStartTime, startTime)
					assert.Equal(t, tc.expectedEndTime, endTime)
				}
			}
		})
	}
}

// Test datadogMapToOutputMetricWithKey
func TestDatadogMapToOutputMetricWithKey(t *testing.T) {
	testCases := []struct {
		name           string
		inputJSON      string
		queryKey       string
		expectedOutput OutputMetricQuery
		expectError    bool
	}{
		{
			name: "Single metric series",
			inputJSON: `{
				"status": "ok",
				"res_type": "time_series",
				"series": [
					{
						"metric": "system.cpu.idle",
						"tag_set": ["host:server-01", "env:prod"],
						"unit": [{"name": "percent", "short_name": "%", "family": "percentage"}],
						"pointlist": [[1672531200000, 45.5], [1672531260000, 50.2]]
					}
				]
			}`,
			queryKey: "cpu_query",
			expectedOutput: OutputMetricQuery{
				Results: []QueryResult{
					{
						QueryKey: "cpu_query",
						Payload: []Result{
							{
								Metric: map[string]string{
									"__name__": "system.cpu.idle",
									"host":     "server-01",
									"env":      "prod",
									"unit":     "percent",
								},
								Timestamps: []int64{1672531200, 1672531260},
								Values:     []float64{45.5, 50.2},
							},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "Multiple metric series",
			inputJSON: `{
				"status": "ok",
				"res_type": "time_series",
				"series": [
					{
						"metric": "system.memory.used",
						"tag_set": ["host:server-01"],
						"pointlist": [[1672531200000, 1024.0]]
					},
					{
						"metric": "system.memory.free",
						"tag_set": ["host:server-01"],
						"pointlist": [[1672531200000, 2048.0]]
					}
				]
			}`,
			queryKey: "memory_query",
			expectedOutput: OutputMetricQuery{
				Results: []QueryResult{
					{
						QueryKey: "memory_query",
						Payload: []Result{
							{
								Metric: map[string]string{
									"__name__": "system.memory.used",
									"host":     "server-01",
								},
								Timestamps: []int64{1672531200},
								Values:     []float64{1024.0},
							},
							{
								Metric: map[string]string{
									"__name__": "system.memory.free",
									"host":     "server-01",
								},
								Timestamps: []int64{1672531200},
								Values:     []float64{2048.0},
							},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "Metric with no tags",
			inputJSON: `{
				"status": "ok",
				"series": [
					{
						"metric": "app.requests",
						"tag_set": [],
						"pointlist": [[1672531200000, 100.0], [1672531260000, 150.0]]
					}
				]
			}`,
			queryKey: "requests_query",
			expectedOutput: OutputMetricQuery{
				Results: []QueryResult{
					{
						QueryKey: "requests_query",
						Payload: []Result{
							{
								Metric: map[string]string{
									"__name__": "app.requests",
								},
								Timestamps: []int64{1672531200, 1672531260},
								Values:     []float64{100.0, 150.0},
							},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name:      "Empty series",
			inputJSON: `{"status": "ok", "series": []}`,
			queryKey:  "empty_query",
			expectedOutput: OutputMetricQuery{
				Results: []QueryResult{
					{
						QueryKey: "empty_query",
						Payload:  []Result{},
					},
				},
			},
			expectError: false,
		},
		{
			name:           "Invalid JSON",
			inputJSON:      `{invalid json}`,
			queryKey:       "error_query",
			expectedOutput: OutputMetricQuery{},
			expectError:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := datadogMapToOutputMetricWithKey([]byte(tc.inputJSON), tc.queryKey)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, len(tc.expectedOutput.Results), len(result.Results))

				if len(result.Results) > 0 {
					assert.Equal(t, tc.expectedOutput.Results[0].QueryKey, result.Results[0].QueryKey)
					assert.Equal(t, len(tc.expectedOutput.Results[0].Payload), len(result.Results[0].Payload))

					for i, expectedResult := range tc.expectedOutput.Results[0].Payload {
						actualResult := result.Results[0].Payload[i]
						assert.Equal(t, expectedResult.Metric, actualResult.Metric)
						assert.Equal(t, expectedResult.Timestamps, actualResult.Timestamps)
						assert.Equal(t, expectedResult.Values, actualResult.Values)
					}
				}
			}
		})
	}
}

// Test edge cases for buildBinaryClause
func TestBuildBinaryClause_EdgeCases(t *testing.T) {
	testCases := []struct {
		name           string
		binary         query.BinaryWhereClause
		expectedResult string
	}{
		{
			name: "In operator with empty array",
			binary: query.BinaryWhereClause{
				"status": {In: []any{}},
			},
			expectedResult: `(status:)`,
		},
		{
			name: "In operator with non-array value (should not panic)",
			binary: query.BinaryWhereClause{
				"status": {In: "not-an-array"},
			},
			// When value is not an array, the In case doesn't add anything
			expectedResult: ``,
		},
		{
			name: "NotIn operator with empty array",
			binary: query.BinaryWhereClause{
				"status": {NotIn: []any{}},
			},
			expectedResult: ``,
		},
		{
			name: "IsNull with non-boolean value",
			binary: query.BinaryWhereClause{
				"field": {IsNull: "not-a-bool"},
			},
			expectedResult: `field:*`,
		},
		{
			name:           "Empty binary clause",
			binary:         query.BinaryWhereClause{},
			expectedResult: ``,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := buildBinaryClause(tc.binary)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedResult, result)
		})
	}
}

// Test DatadogResponse JSON unmarshaling
func TestDatadogResponse_Unmarshal(t *testing.T) {
	jsonData := `{
		"status": "ok",
		"res_type": "time_series",
		"series": [
			{
				"unit": [{"family": "time", "id": 1, "name": "second", "short_name": "s", "plural": "seconds", "scale_factor": 1.0}],
				"query_index": 0,
				"aggr": "avg",
				"metric": "system.cpu.user",
				"tag_set": ["host:myhost"],
				"expression": "system.cpu.user{*}",
				"scope": "*",
				"interval": 60,
				"start": 1672531200,
				"end": 1672534800,
				"pointlist": [[1672531200000, 10.5]],
				"display_name": "system.cpu.user"
			}
		],
		"values": [],
		"times": [],
		"group_by": ["host"]
	}`

	var response DatadogResponse
	err := json.Unmarshal([]byte(jsonData), &response)

	assert.NoError(t, err)
	assert.Equal(t, "ok", response.Status)
	assert.Equal(t, "time_series", response.ResType)
	assert.Equal(t, 1, len(response.Series))
	assert.Equal(t, "system.cpu.user", response.Series[0].Metric)
	assert.Equal(t, "avg", response.Series[0].Aggr)
	assert.Equal(t, []string{"host:myhost"}, response.Series[0].TagSet)
	assert.Equal(t, []string{"host"}, response.GroupBy)

	// Verify unit
	assert.NotNil(t, response.Series[0].Unit)
	assert.Equal(t, 1, len(response.Series[0].Unit))
	assert.Equal(t, "second", response.Series[0].Unit[0].Name)
	assert.Equal(t, "s", response.Series[0].Unit[0].ShortName)
}

func TestRetryAfterDelay(t *testing.T) {
	fallback := 250 * time.Millisecond

	tests := []struct {
		name   string
		header string
		expect time.Duration
	}{
		{"empty falls back", "", fallback},
		{"whitespace falls back", "   ", fallback},
		{"malformed falls back", "soon-please", fallback},
		{"delta seconds parsed", "5", 5 * time.Second},
		{"delta seconds with whitespace", "  3 ", 3 * time.Second},
		{"negative seconds falls back", "-5", fallback},
		{"large delta clamped to max", "999999", datadogTraceMaxBackoff},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := retryAfterDelay(tc.header, fallback)
			assert.Equal(t, tc.expect, got)
		})
	}

	t.Run("http-date in future", func(t *testing.T) {
		future := time.Now().Add(2 * time.Second).UTC().Format(http.TimeFormat)
		got := retryAfterDelay(future, fallback)
		assert.Greater(t, got, time.Duration(0))
		assert.LessOrEqual(t, got, datadogTraceMaxBackoff)
	})

	t.Run("http-date in past falls back", func(t *testing.T) {
		past := time.Now().Add(-1 * time.Hour).UTC().Format(http.TimeFormat)
		got := retryAfterDelay(past, fallback)
		assert.Equal(t, fallback, got)
	})
}

func TestDatadogBackoffMonotonicAndCapped(t *testing.T) {
	// Walk a few attempts; allow ±25% jitter when comparing.
	prev := time.Duration(0)
	for attempt := 0; attempt < 8; attempt++ {
		d := datadogBackoff(attempt)
		assert.LessOrEqual(t, d, datadogTraceMaxBackoff+datadogTraceMaxBackoff/4,
			"attempt %d backoff %s exceeds cap+jitter", attempt, d)
		if attempt > 0 && prev < datadogTraceMaxBackoff/2 {
			// Earlier attempts should generally grow; once we hit the cap they level off.
			assert.GreaterOrEqual(t, d, prev/2,
				"attempt %d backoff regressed too far: prev=%s now=%s", attempt, prev, d)
		}
		prev = d
	}
}

func TestFetchDatadogTraceAPI_RetriesOn429ThenSucceeds(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"errors":["rate limited"]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	body, err := fetchDatadogTraceAPI(ctx, server.URL, map[string]string{})
	assert.NoError(t, err)
	assert.JSONEq(t, `{"data":[]}`, string(body))
	assert.Equal(t, int32(3), atomic.LoadInt32(&calls))
}

func TestFetchDatadogTraceAPI_ExhaustsOn429(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"errors":["rate limited"]}`))
	}))
	defer server.Close()

	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	_, err := fetchDatadogTraceAPI(ctx, server.URL, map[string]string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rate-limited")
	assert.Equal(t, int32(datadogTraceMaxRetries+1), atomic.LoadInt32(&calls))
}

func TestFetchDatadogTraceAPI_RetriesOn5xx(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 2 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`upstream error`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	body, err := fetchDatadogTraceAPI(ctx, server.URL, map[string]string{})
	assert.NoError(t, err)
	assert.Equal(t, `{"data":[]}`, string(body))
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
}

func TestFetchDatadogTraceAPI_NoRetryOn4xx(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["forbidden"]}`))
	}))
	defer server.Close()

	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	_, err := fetchDatadogTraceAPI(ctx, server.URL, map[string]string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), strconv.Itoa(http.StatusForbidden))
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "4xx (non-429) must not retry")
}
