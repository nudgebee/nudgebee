package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startQueryStub stands in for api-server's generic /rpc/query engine.
func startQueryStub(t *testing.T, handler func(table string, input map[string]any) (int, string)) func() {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/rpc/query", r.URL.Path)
		assert.Equal(t, "tenant-123", r.Header.Get("x-tenant-id"))

		var payload struct {
			Action struct {
				Name string `json:"name"`
			} `json:"action"`
			Input map[string]any `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))

		status, body := handler(payload.Action.Name, payload.Input)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))

	prev := config.Config.ServiceEndpoint
	config.Config.ServiceEndpoint = srv.URL
	return func() {
		config.Config.ServiceEndpoint = prev
		srv.Close()
	}
}

func TestEventRulesTool(t *testing.T) {
	t.Run("requires account context", func(t *testing.T) {
		tool := EventRulesTool{}
		resp, err := tool.Call(newTriageToolContext(""), core.NBToolCallRequest{Arguments: map[string]any{}})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	})

	t.Run("builds query with account scope and filters", func(t *testing.T) {
		var gotTable string
		var gotInput map[string]any
		cleanup := startQueryStub(t, func(table string, input map[string]any) (int, string) {
			gotTable = table
			gotInput = input
			return http.StatusOK, `{"rows":[{"alert":"HighP95Latency","enabled":true}]}`
		})
		defer cleanup()

		tool := EventRulesTool{}
		resp, err := tool.Call(newTriageToolContext("acc-7"), core.NBToolCallRequest{
			Arguments: map[string]any{"alert": "HighP95Latency", "enabled": true},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		assert.Equal(t, "event_rules_v2", gotTable)

		where, ok := gotInput["where"].(map[string]any)
		require.True(t, ok)
		// account scoping
		acc, ok := where["account_id"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "acc-7", acc["_eq"])
		// alert substring -> ilike with wildcards
		al, ok := where["alert"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "%HighP95Latency%", al["_ilike"])
		// enabled bool -> _eq
		en, ok := where["enabled"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, true, en["_eq"])

		assert.Contains(t, resp.Data, "HighP95Latency")
	})

	t.Run("read-only request type", func(t *testing.T) {
		rt, err := EventRulesTool{}.InferToolRequestType(nil, "", "")
		assert.NoError(t, err)
		assert.Equal(t, core.ToolRequestTypeRead, rt)
	})

	t.Run("surfaces engine error as tool error", func(t *testing.T) {
		cleanup := startQueryStub(t, func(table string, input map[string]any) (int, string) {
			return http.StatusBadRequest, `{"message":"bad query"}`
		})
		defer cleanup()

		tool := EventRulesTool{}
		resp, err := tool.Call(newTriageToolContext("acc-7"), core.NBToolCallRequest{Arguments: map[string]any{}})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "Error:")
	})
}
