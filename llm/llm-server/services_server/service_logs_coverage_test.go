package services_server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// QueryLogs is where both log paths (canonical and native) reach the backend,
// so it is where a read that stopped at its limit is recognised. The agent's
// response reads the coverage from here; without it a full read looks whole.
func TestQueryLogs_Coverage(t *testing.T) {
	const returned = 3
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs := make([]map[string]any, 0, returned)
		for i := 0; i < returned; i++ {
			logs = append(logs, map[string]any{"timestamp": fmt.Sprintf("2026-01-15T05:3%d:00Z", i), "message": "line"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"logs": logs, "query": `{pod="api"}`, "provider": "loki"})
	}))
	t.Cleanup(server.Close)
	origEndpoint := config.Config.ServiceEndpoint
	t.Cleanup(func() { config.Config.ServiceEndpoint = origEndpoint })
	config.Config.ServiceEndpoint = server.URL

	// Tenant set on the context so the call does not look it up in the database.
	ctx := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})
	start := time.Date(2026, 1, 15, 5, 19, 26, 0, time.UTC)
	end := time.Date(2026, 1, 15, 5, 36, 0, 0, time.UTC)
	request := LogQueryRequest{AccountId: "test-account", LogProvider: "loki", StartTime: start.UnixMilli(), EndTime: end.UnixMilli()}

	t.Run("as many lines as the limit: the read stopped there", func(t *testing.T) {
		request.Limit = returned
		resp, err := QueryLogs(*ctx, request)
		require.NoError(t, err)
		require.NotNil(t, resp.Coverage)
		assert.Equal(t, returned, resp.Coverage.Limit)
		assert.Equal(t, returned, resp.Coverage.Returned)
		assert.Equal(t, "2026-01-15T05:30:00Z", resp.Coverage.Oldest)
		assert.Equal(t, "2026-01-15T05:32:00Z", resp.Coverage.Newest)
		assert.Equal(t, "2026-01-15T05:19:26Z", resp.Coverage.RequestedStart)
		assert.Equal(t, "2026-01-15T05:36:00Z", resp.Coverage.RequestedEnd)

		encoded, err := json.Marshal(resp)
		require.NoError(t, err)
		assert.Contains(t, string(encoded), `"coverage":{"limit":3,"returned":3`, "the field the agent's response reads")
	})

	t.Run("fewer lines than the limit: nothing to say", func(t *testing.T) {
		request.Limit = returned + 1
		resp, err := QueryLogs(*ctx, request)
		require.NoError(t, err)
		assert.Len(t, resp.Logs, returned)
		assert.Nil(t, resp.Coverage)

		encoded, err := json.Marshal(resp)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "coverage")
	})
}

// services-server sets `partial` when its backend stopped before the end of the window
// without reaching the limit (a SolarWinds search out of time). The lines are then the
// newest matches, not all of them, and the coverage must say so.
func TestQueryLogs_PartialFromServicesServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"logs":     []map[string]any{{"timestamp": "2026-01-15T05:30:00Z", "message": "line"}},
			"provider": "solarwinds",
			"partial":  true,
		})
	}))
	t.Cleanup(server.Close)
	origEndpoint := config.Config.ServiceEndpoint
	t.Cleanup(func() { config.Config.ServiceEndpoint = origEndpoint })
	config.Config.ServiceEndpoint = server.URL

	ctx := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})
	resp, err := QueryLogs(*ctx, LogQueryRequest{AccountId: "test-account", LogProvider: "solarwinds", Limit: 2000})
	require.NoError(t, err)
	require.NotNil(t, resp.Coverage, "a partial read must not look whole because it is under the limit")
	assert.True(t, resp.Coverage.Partial)
	assert.Equal(t, 1, resp.Coverage.Returned)
	assert.Equal(t, 2000, resp.Coverage.Limit)
}

// services-server sends the backend's own match count (Elasticsearch `hits.total`) with the
// result. The coverage carries it so the agent can say "1,000 of 72,612", and a backend that
// returned fewer lines than the limit although more matched still gets coverage.
func TestQueryLogs_BackendTotal(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(server.Close)
	origEndpoint := config.Config.ServiceEndpoint
	t.Cleanup(func() { config.Config.ServiceEndpoint = origEndpoint })
	config.Config.ServiceEndpoint = server.URL

	ctx := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})
	lines := func(n int) []map[string]any {
		out := make([]map[string]any, n)
		for i := range out {
			out[i] = map[string]any{"timestamp": "2026-01-15T05:30:00Z", "message": "line"}
		}
		return out
	}

	t.Run("read at its limit: the total rides on the coverage", func(t *testing.T) {
		payload = map[string]any{"logs": lines(2), "provider": "ES", "truncated": true, "total": 72612, "total_relation": "eq"}
		resp, err := QueryLogs(*ctx, LogQueryRequest{AccountId: "test-account", Limit: 2})
		require.NoError(t, err)
		require.NotNil(t, resp.Coverage)
		require.NotNil(t, resp.Coverage.Total)
		assert.Equal(t, int64(72612), *resp.Coverage.Total)
		assert.Equal(t, "eq", resp.Coverage.TotalRelation)
	})

	t.Run("fewer lines than the limit although more matched: coverage anyway", func(t *testing.T) {
		payload = map[string]any{"logs": lines(2), "provider": "ES", "total": 10000, "total_relation": "gte"}
		resp, err := QueryLogs(*ctx, LogQueryRequest{AccountId: "test-account", Limit: 50})
		require.NoError(t, err)
		require.NotNil(t, resp.Coverage)
		assert.Equal(t, 2, resp.Coverage.Returned)
		assert.Equal(t, "gte", resp.Coverage.TotalRelation)
	})

	t.Run("everything that matched came back: nothing to say", func(t *testing.T) {
		payload = map[string]any{"logs": lines(2), "provider": "ES", "total": 2, "total_relation": "eq"}
		resp, err := QueryLogs(*ctx, LogQueryRequest{AccountId: "test-account", Limit: 50})
		require.NoError(t, err)
		assert.Nil(t, resp.Coverage)
	})

	t.Run("the read filled its limit and the exact count says that was everything", func(t *testing.T) {
		payload = map[string]any{"logs": lines(2), "provider": "ES", "total": 2, "total_relation": "eq"}
		resp, err := QueryLogs(*ctx, LogQueryRequest{AccountId: "test-account", Limit: 2})
		require.NoError(t, err)
		assert.Nil(t, resp.Coverage)
	})

	t.Run("a backend that reports no total: unchanged", func(t *testing.T) {
		payload = map[string]any{"logs": lines(2), "provider": "loki"}
		resp, err := QueryLogs(*ctx, LogQueryRequest{AccountId: "test-account", Limit: 2})
		require.NoError(t, err)
		require.NotNil(t, resp.Coverage)
		assert.Nil(t, resp.Coverage.Total)
	})
}

// A read the backend flagged partial keeps its note even when an exact total looks small:
// the backend stopped early, so its count is no proof the read was whole.
func TestWithBackendLogTotal_PartialKept(t *testing.T) {
	logs := []core.ObservabilityLog{{Timestamp: "2026-01-15T05:30:00Z"}}
	partial := core.NewPartialObservabilityLogCoverage(logs, 50, 0, 0)
	total := int64(1)
	got := core.WithBackendLogTotal(partial, logs, 50, 0, 0, &total, "eq")
	require.NotNil(t, got)
	assert.True(t, got.Partial)
	assert.Nil(t, got.Total)
}
