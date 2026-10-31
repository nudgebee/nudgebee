package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOpenObserveConfig(t *testing.T) {
	ctx := core.NbToolContext{ToolConfig: core.ToolConfig{Values: []core.ToolConfigValue{
		{Name: "openobserve_url", Value: "https://observe.example.test/base/path"},
		{Name: "openobserve_org_id", Value: "default"},
		{Name: "openobserve_username", Value: "agent"},
		{Name: "openobserve_password", Value: "token"},
		{Name: "openobserve_log_stream", Value: "app_logs"},
	}}}

	cfg, err := getOpenObserveConfig(ctx)
	require.NoError(t, err)
	assert.Equal(t, "https://observe.example.test", cfg.URL)
	assert.Equal(t, "default", cfg.OrgID)
	assert.Equal(t, "agent", cfg.Username)
	assert.Equal(t, "token", cfg.Password)
	assert.Equal(t, "app_logs", cfg.LogStream)
	assert.Equal(t, openObserveDefaultTraceStream, cfg.TraceStream)

	schema := getOpenObserveConfigSchema()
	assert.Equal(t, "openobserve", schema.ConfigType)
	assert.Contains(t, schema.Required, "openobserve_password")
	assert.True(t, schema.Properties["openobserve_password"].IsEncrypted)
}

func TestGetOpenObserveConfigRejectsMissingAndUnsafeValues(t *testing.T) {
	tests := []struct {
		name   string
		values []core.ToolConfigValue
	}{
		{
			name: "missing credentials",
			values: []core.ToolConfigValue{
				{Name: "openobserve_url", Value: "https://observe.example.test"},
				{Name: "openobserve_org_id", Value: "default"},
			},
		},
		{
			name: "invalid URL scheme",
			values: []core.ToolConfigValue{
				{Name: "openobserve_url", Value: "file:///tmp/observe"},
				{Name: "openobserve_org_id", Value: "default"},
				{Name: "openobserve_username", Value: "agent"},
				{Name: "openobserve_password", Value: "token"},
			},
		},
		{
			name: "unsafe stream",
			values: []core.ToolConfigValue{
				{Name: "openobserve_url", Value: "https://observe.example.test"},
				{Name: "openobserve_org_id", Value: "default"},
				{Name: "openobserve_username", Value: "agent"},
				{Name: "openobserve_password", Value: "token"},
				{Name: "openobserve_log_stream", Value: "logs;drop"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := getOpenObserveConfig(core.NbToolContext{ToolConfig: core.ToolConfig{Values: tt.values}})
			assert.Error(t, err)
		})
	}
}

func TestPrepareOpenObserveSQL(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		stream    string
		want      string
		wantError bool
	}{
		{name: "full select", query: "SELECT * FROM default WHERE level = 'error'", stream: "default", want: "SELECT * FROM default WHERE level = 'error'"},
		{name: "keyword in string literal", query: "SELECT * FROM default WHERE message = 'union select from other'", stream: "default", want: "SELECT * FROM default WHERE message = 'union select from other'"},
		{name: "escaped apostrophe in E string", query: `SELECT * FROM default WHERE message = E'it\'s fine'`, stream: "default", want: `SELECT * FROM default WHERE message = E'it\'s fine'`},
		{name: "predicate", query: "service = 'api'", stream: "app_logs", want: "SELECT * FROM app_logs WHERE service = 'api'"},
		{name: "where predicate", query: "WHERE service = 'api'", stream: "app_logs", want: "SELECT * FROM app_logs WHERE service = 'api'"},
		{name: "empty query", query: " ", stream: "default", wantError: true},
		{name: "multiple statements", query: "SELECT * FROM logs; DROP TABLE logs", stream: "default", wantError: true},
		{name: "CTE is not accepted at the query boundary", query: "WITH x AS (SELECT 1) SELECT * FROM x", stream: "default", wantError: true},
		{name: "full select from another stream", query: "SELECT * FROM private_logs", stream: "default", wantError: true},
		{name: "multiple stream select", query: "SELECT * FROM default UNION SELECT * FROM private_logs", stream: "default", wantError: true},
		{name: "predicate union injection", query: "service = 'api' UNION SELECT * FROM private_logs", stream: "default", wantError: true},
		{name: "predicate subquery injection", query: "service IN (SELECT service FROM private_logs)", stream: "default", wantError: true},
		{name: "escaped-string stream injection", query: `SELECT * FROM default WHERE x = E'a\'' UNION SELECT * FROM private_logs WHERE y = E'b\''`, stream: "default", wantError: true},
		{name: "comments are rejected", query: "service = 'api' -- UNION SELECT", stream: "default", wantError: true},
		{name: "unsafe stream", query: "service = 'api'", stream: "logs;drop", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := prepareOpenObserveSQL(tt.query, tt.stream)
			if tt.wantError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOpenObserveTimeRangeDefaultsAndValidates(t *testing.T) {
	before := time.Now().UTC()
	start, end, err := openObserveTimeRange(core.NbToolContext{}, nil, time.Hour)
	require.NoError(t, err)
	assert.WithinDuration(t, before, end, 2*time.Second)
	assert.InDelta(t, float64(time.Hour), float64(end.Sub(start)), float64(2*time.Second))

	start, end, err = openObserveTimeRange(core.NbToolContext{}, map[string]any{"range": "2h"}, time.Hour)
	require.NoError(t, err)
	assert.InDelta(t, float64(2*time.Hour), float64(end.Sub(start)), float64(2*time.Second))

	_, _, err = openObserveTimeRange(core.NbToolContext{}, map[string]any{
		"start_time": "not-a-time", "end_time": "also-not-a-time",
	}, time.Hour)
	assert.Error(t, err)
}

func TestOpenObserveSearchRequestAndTraceType(t *testing.T) {
	start := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	for _, tc := range []struct {
		name       string
		signalType string
		wantQuery  string
	}{
		{name: "logs"},
		{name: "traces", signalType: "traces", wantQuery: "type=traces"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/api/default/_search", r.URL.Path)
				assert.Equal(t, tc.wantQuery, r.URL.RawQuery)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				username, password, ok := r.BasicAuth()
				assert.True(t, ok)
				assert.Equal(t, "agent", username)
				assert.Equal(t, "token", password)

				var body struct {
					Query struct {
						SQL       string `json:"sql"`
						StartTime int64  `json:"start_time"`
						EndTime   int64  `json:"end_time"`
						From      int    `json:"from"`
						Size      int    `json:"size"`
					} `json:"query"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				assert.Equal(t, "SELECT * FROM default", body.Query.SQL)
				assert.Equal(t, start.UnixMicro(), body.Query.StartTime)
				assert.Equal(t, end.UnixMicro(), body.Query.EndTime)
				assert.Equal(t, 0, body.Query.From)
				assert.Equal(t, 25, body.Query.Size)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"hits":[{"service":"api"}]}`))
			}))
			defer server.Close()

			client := newOpenObserveClient(openObserveConfig{
				URL: server.URL, OrgID: "default", Username: "agent", Password: "token",
			})
			result, err := client.search(context.Background(), "SELECT * FROM default", start, end, 25, tc.signalType)
			require.NoError(t, err)
			assert.Len(t, result["hits"], 1)
		})
	}
}

func TestOpenObserveClientSurfacesActionableAPIErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"unknown field 'servce'","hint":"Use the stream schema endpoint","suggestions":["service"]}`))
	}))
	defer server.Close()

	client := newOpenObserveClient(openObserveConfig{URL: server.URL, OrgID: "default", Username: "agent", Password: "token"})
	_, err := client.search(context.Background(), "SELECT * FROM default", time.Now().Add(-time.Hour), time.Now(), 10, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 400")
	assert.Contains(t, err.Error(), "Use the stream schema endpoint")
	assert.Contains(t, err.Error(), "service")
}

func TestOpenObserveClientRejectsMalformedAndProviderErrorResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid JSON", body: "not-json"},
		{name: "Prometheus error payload", body: `{"status":"error","error":"bad PromQL"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := newOpenObserveClient(openObserveConfig{URL: server.URL, OrgID: "default", Username: "agent", Password: "token"})
			_, err := client.search(context.Background(), "SELECT * FROM default", time.Now().Add(-time.Hour), time.Now(), 10, "")
			require.Error(t, err)
			if strings.Contains(tc.body, "bad PromQL") {
				assert.Contains(t, err.Error(), "bad PromQL")
			} else {
				assert.Contains(t, err.Error(), "decode JSON")
			}
		})
	}
}

func TestNormalizeOpenObserveMetricTimestampsUsesSeconds(t *testing.T) {
	response := core.ObservabilityMetricsQueryResponse{Results: []core.ObservabilityMetricsQueryResult{{
		Payload: []core.ObservabilityMetricsQuerySeries{{Timestamps: []int64{1793440800000, 1793440860000}}},
	}}}
	normalizeOpenObserveMetricTimestamps(&response)
	assert.Equal(t, []int64{1793440800, 1793440860}, response.Results[0].Payload[0].Timestamps)
}
