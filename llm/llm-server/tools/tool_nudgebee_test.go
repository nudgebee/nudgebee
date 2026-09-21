package tools

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startNudgebeeQueryStub(t *testing.T, handler func(action string, input map[string]any) (int, string)) func() {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/rpc/query", r.URL.Path)
		assert.Equal(t, "tenant-123", r.Header.Get("x-tenant-id"))
		assert.Equal(t, "user-1", r.Header.Get("x-user-id"))

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

	previous := config.Config.ServiceEndpoint
	config.Config.ServiceEndpoint = srv.URL
	return func() {
		config.Config.ServiceEndpoint = previous
		srv.Close()
	}
}

func nudgebeeContextWithoutUser() core.NbToolContext {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secCtx := &security.SecurityContext{}
	if err := json.Unmarshal([]byte(`{"TenantId":"tenant-123","Roles":["tenant_admin"]}`), secCtx); err != nil {
		panic(err)
	}
	return core.NbToolContext{
		AccountId: "acc-1",
		Ctx:       security.NewRequestContext(context.Background(), secCtx, logger, nil, nil),
	}
}

func TestNudgebeeToolsRequireRequestingUser(t *testing.T) {
	calls := []func() (core.NBToolResponse, error){
		func() (core.NBToolResponse, error) {
			return NudgebeeAccountsListTool{}.Call(nudgebeeContextWithoutUser(), core.NBToolCallRequest{Arguments: map[string]any{}})
		},
		func() (core.NBToolResponse, error) {
			return NudgebeeDocsSearchTool{}.Call(nudgebeeContextWithoutUser(), core.NBToolCallRequest{Command: "what is an account?"})
		},
	}
	for _, call := range calls {
		resp, err := call()
		require.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "refusing tenant-admin fallback")
	}
}

func TestNudgebeeAccountsListTool(t *testing.T) {
	var gotAction string
	var gotInput map[string]any
	cleanup := startNudgebeeQueryStub(t, func(action string, input map[string]any) (int, string) {
		gotAction, gotInput = action, input
		return http.StatusOK, `{"rows":[{"id":"a1","account_name":"prod","status":"active"}]}`
	})
	defer cleanup()

	resp, err := NudgebeeAccountsListTool{}.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Arguments: map[string]any{
		"status": "active", "cloud_provider": "aws", "name": "prod", "limit": float64(500),
	}})
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Equal(t, "accounts_list", gotAction)
	assert.Equal(t, float64(100), gotInput["limit"])
	where := gotInput["where"].(map[string]any)
	assert.Equal(t, "active", where["status"].(map[string]any)["_eq"])
	assert.Equal(t, "aws", where["cloud_provider"].(map[string]any)["_eq"])
	assert.Equal(t, "%prod%", where["account_name"].(map[string]any)["_ilike"])
	assert.NotContains(t, resp.Data, "account_access")
}

func TestNudgebeeCountsUseAggregateActions(t *testing.T) {
	tests := []struct {
		name        string
		tool        core.NBTool
		action      string
		groupBy     string
		filterKey   string
		filterValue string
	}{
		{name: "accounts", tool: NudgebeeAccountsCountTool{}, action: "accounts_aggregate", groupBy: "cloud_provider", filterKey: "cloud_provider", filterValue: "K8s"},
		{name: "integrations", tool: NudgebeeIntegrationsCountTool{}, action: "integrations_aggregate", groupBy: "type", filterKey: "type", filterValue: "github"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cleanup := startNudgebeeQueryStub(t, func(action string, input map[string]any) (int, string) {
				assert.Equal(t, test.action, action)
				assert.Equal(t, []any{test.groupBy}, input["group_by"])
				assert.Equal(t, []any{test.groupBy, "count"}, input["columns"])
				where := input["where"].(map[string]any)
				assert.Equal(t, test.filterValue, where[test.filterKey].(map[string]any)["_eq"])
				return http.StatusOK, `{"rows":[{"count":2}]}`
			})
			defer cleanup()

			resp, err := test.tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Arguments: map[string]any{
				"group_by": test.groupBy, test.filterKey: test.filterValue,
			}})
			require.NoError(t, err)
			assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		})
	}
}

func TestNudgebeeIntegrationGetStatusTool(t *testing.T) {
	cleanup := startNudgebeeQueryStub(t, func(action string, input map[string]any) (int, string) {
		assert.Equal(t, "integrations_list", action)
		where := input["where"].(map[string]any)
		assert.Equal(t, "%Datadog%", where["name"].(map[string]any)["_ilike"])
		assert.Equal(t, []any{"id", "name", "type", "status", "updated_at"}, input["columns"])
		return http.StatusOK, `{"rows":[{"name":"Datadog","status":"active"}]}`
	})
	defer cleanup()

	resp, err := NudgebeeIntegrationGetStatusTool{}.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Arguments: map[string]any{"name": "Datadog"}})
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Contains(t, resp.Data, `"status":"active"`)
}

func TestNudgebeeLookupToolsRequireIdentifier(t *testing.T) {
	for _, tool := range []core.NBTool{NudgebeeAccountGetTool{}, NudgebeeIntegrationGetStatusTool{}} {
		for _, arguments := range []map[string]any{{}, {"id": "  ", "name": "\t"}} {
			resp, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Arguments: arguments})
			require.NoError(t, err)
			assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		}
	}
}

func TestNudgebeeStringArgumentsAreTrimmed(t *testing.T) {
	cleanup := startNudgebeeQueryStub(t, func(action string, input map[string]any) (int, string) {
		assert.Equal(t, "accounts_list", action)
		where := input["where"].(map[string]any)
		assert.Len(t, where, 1)
		assert.Equal(t, "active", where["status"].(map[string]any)["_eq"])
		return http.StatusOK, `{"rows":[]}`
	})
	defer cleanup()

	resp, err := NudgebeeAccountsListTool{}.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Arguments: map[string]any{
		"status": "  active  ", "cloud_provider": "  ", "name": "\t",
	}})
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
}

func TestNudgebeeToolsAreReadOnly(t *testing.T) {
	tools := []interface {
		InferToolRequestType(*security.RequestContext, string, string) (core.ToolRequestType, error)
	}{
		NudgebeeAccountsListTool{}, NudgebeeAccountsCountTool{}, NudgebeeAccountGetTool{},
		NudgebeeIntegrationsListTool{}, NudgebeeIntegrationsCountTool{}, NudgebeeIntegrationGetStatusTool{},
		NudgebeeDocsSearchTool{},
	}
	for _, tool := range tools {
		requestType, err := tool.InferToolRequestType(nil, "", "")
		require.NoError(t, err)
		assert.Equal(t, core.ToolRequestTypeRead, requestType)
	}
}
