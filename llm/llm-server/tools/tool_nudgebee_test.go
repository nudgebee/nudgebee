package tools

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
		func() (core.NBToolResponse, error) {
			return NudgebeeIntegrationDiagnoseTool{}.Call(nudgebeeContextWithoutUser(), core.NBToolCallRequest{Arguments: map[string]any{"id": "integration-1"}})
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
		NudgebeeIntegrationDiagnoseTool{},
		NudgebeeDocsSearchTool{},
	}
	for _, tool := range tools {
		requestType, err := tool.InferToolRequestType(nil, "", "")
		require.NoError(t, err)
		assert.Equal(t, core.ToolRequestTypeRead, requestType)
	}
}

func TestNudgebeeIntegrationDiagnoseTool(t *testing.T) {
	cleanup := startRPCStub(t, "/rpc/integration", func(action string, input map[string]any) (int, string) {
		assert.Equal(t, "integrations_diagnose_connection", action)
		request := input["request"].(map[string]any)
		assert.Equal(t, "integration-1", request["integration_id"])
		return http.StatusOK, `{"success":false,"health":"unhealthy","stage":"authentication","reason_code":"AUTHENTICATION_FAILED","summary":"The integration endpoint rejected authentication."}`
	})
	defer cleanup()

	resp, err := NudgebeeIntegrationDiagnoseTool{}.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Arguments: map[string]any{"id": "  integration-1  "}})
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Contains(t, resp.Data, `"reason_code":"AUTHENTICATION_FAILED"`)
}

func TestNudgebeeIntegrationDiagnoseToolRequiresID(t *testing.T) {
	resp, err := NudgebeeIntegrationDiagnoseTool{}.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Arguments: map[string]any{}})
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "exact integration id")
}

func TestNudgebeeDocsSearchFiltersAndFormatsProductDocs(t *testing.T) {
	previous := queryNudgebeeDocs
	t.Cleanup(func() { queryNudgebeeDocs = previous })
	queryNudgebeeDocs = func(userID, accountID, query, module string, count int, conversationID, messageID, agentID string, track bool, filters ...map[string]any) core.RAGSearchResults {
		assert.Equal(t, "user-1", userID)
		assert.Equal(t, "acc-1", accountID)
		assert.Equal(t, "what is an account?", query)
		assert.Equal(t, nudgebeeDocsModule, module)
		assert.Equal(t, nudgebeeDocsResultLimit, count)
		assert.True(t, track)
		require.Equal(t, []map[string]any{{"source": nudgebeeDocsSource}}, filters)
		return core.RAGSearchResults{
			{Document: "<p>An account&nbsp;groups   resources.</p>", Metadata: map[string]any{
				"title": "Accounts &amp; access", "section": " Concepts ", "url": "https://docs.nudgebee.com/accounts",
			}},
			{Document: "<p>An account&nbsp;groups   resources.</p>", Metadata: map[string]any{
				"title": "Accounts &amp; access", "section": " Concepts ", "url": "https://docs.nudgebee.com/accounts",
			}},
			{Document: "Accounts can contain integrations.", Metadata: map[string]any{
				"title": "Accounts &amp; access", "section": "Integrations", "url": "https://docs.nudgebee.com/accounts",
			}},
			{Document: "A second useful result.", Metadata: map[string]any{
				"title": 123, "section": []string{"bad"}, "url": 42,
			}},
		}
	}

	resp, err := NudgebeeDocsSearchTool{}.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Command: "  what is an account?  "})
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Equal(t, core.NBToolResponseTypeText, resp.Type)
	assert.Contains(t, resp.Data, "Title: Accounts & access")
	assert.Contains(t, resp.Data, "Evidence: An account groups resources.")
	assert.Contains(t, resp.Data, "A second useful result.")
	assert.NotContains(t, resp.Data, "<p>")
	assert.Contains(t, resp.Data, "Accounts can contain integrations.")
	assert.Equal(t, 1, strings.Count(resp.Data, "Evidence: An account groups resources."))
	require.Len(t, resp.References, 1)
	assert.Equal(t, "https://docs.nudgebee.com/accounts", resp.References[0].Url)
	assert.Equal(t, "Accounts & access", resp.References[0].Text)
}

func TestNudgebeeDocsSearchNoResultDoesNotFallback(t *testing.T) {
	previous := queryNudgebeeDocs
	t.Cleanup(func() { queryNudgebeeDocs = previous })
	calls := 0
	queryNudgebeeDocs = func(_, _, _, _ string, _ int, _, _, _ string, _ bool, _ ...map[string]any) core.RAGSearchResults {
		calls++
		return nil
	}

	resp, err := NudgebeeDocsSearchTool{}.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Command: "unknown feature"})
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Equal(t, nudgebeeDocsNoResult, resp.Data)
	assert.Empty(t, resp.References)
}

func TestFormatNudgebeeDocsResultsBoundsAndDeduplicates(t *testing.T) {
	longDocument := strings.Repeat("évidence ", nudgebeeDocsExcerptMaxRunes)
	data, references, selected, truncated := formatNudgebeeDocsResults(core.RAGSearchResults{
		{Document: longDocument, Metadata: map[string]any{"title": "Long", "url": "https://docs.nudgebee.com/long"}},
		{Document: longDocument, Metadata: map[string]any{"title": "Long", "url": "https://docs.nudgebee.com/long"}},
		{Document: "unsafe URL remains evidence-only", Metadata: map[string]any{"url": "javascript:alert(1)"}},
	})

	assert.True(t, truncated)
	assert.Equal(t, 2, selected)
	assert.LessOrEqual(t, len([]rune(data)), nudgebeeDocsOutputMaxRunes)
	assert.Contains(t, data, "…")
	assert.NotContains(t, data, "ignored duplicate")
	assert.NotContains(t, data, "javascript:")
	require.Len(t, references, 1)
}

func TestFormatNudgebeeDocsResultsDeduplicatesTruncatedEvidence(t *testing.T) {
	sharedPrefix := strings.Repeat("same ", nudgebeeDocsExcerptMaxRunes)
	_, _, selected, truncated := formatNudgebeeDocsResults(core.RAGSearchResults{
		{Document: sharedPrefix + "first suffix"},
		{Document: sharedPrefix + "second suffix"},
	})

	assert.True(t, truncated)
	assert.Equal(t, 1, selected)
}

func TestNormalizeNudgebeeDocsTextHandlesEscapedAndAttributedTags(t *testing.T) {
	input := `2 < 3 &amp;&amp; &lt;p note="latency > 500ms"&gt;healthy&nbsp;now&lt;/p&gt;`
	assert.Equal(t, "2 < 3 && healthy now", normalizeNudgebeeDocsText(input))
}

func TestNudgebeeDocsURLPreservesQueryParameters(t *testing.T) {
	assert.Equal(t,
		"https://docs.nudgebee.com/search?q=accounts&section=setup",
		nudgebeeDocsURL(map[string]any{"url": "  https://docs.nudgebee.com/search?q=accounts&amp;section=setup  "}),
	)
}

func TestTruncateNudgebeeDocsTextPreservesRuneBound(t *testing.T) {
	assert.Empty(t, truncateNudgebeeDocsText("content", 0))
	assert.Equal(t, "…", truncateNudgebeeDocsText("évidence", 1))
	assert.Equal(t, "év…", truncateNudgebeeDocsText("évidence", 3))
	assert.Equal(t, "short", truncateNudgebeeDocsText("short", 5))
}
