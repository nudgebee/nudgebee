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
