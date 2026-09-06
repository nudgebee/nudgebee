package tools

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

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
		func() (core.NBToolResponse, error) {
			return NudgebeeAgentHealthGetTool{}.Call(nudgebeeContextWithoutUser(), core.NBToolCallRequest{})
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
		NudgebeeAgentHealthGetTool{},
		NudgebeeDocsSearchTool{},
	}
	for _, tool := range tools {
		requestType, err := tool.InferToolRequestType(nil, "", "")
		require.NoError(t, err)
		assert.Equal(t, core.ToolRequestTypeRead, requestType)
	}
}

func TestNudgebeeAgentHealthGetSanitizesConnectionStatus(t *testing.T) {
	cleanup := startNudgebeeQueryStub(t, func(action string, input map[string]any) (int, string) {
		assert.Equal(t, "agents_list_health", action)
		where := input["where"].(map[string]any)
		assert.Equal(t, "acc-1", where["cloud_account_id"].(map[string]any)["_eq"])
		return http.StatusOK, `{"rows":[null,{"cloud_account_id":"acc-1","type":"k8s","status":"CONNECTED","connection_status":{"prometheusConnection":false,"logsConnection":true,"nodeAgentCount":3,"prometheusUrl":"http://secret.internal","logProviderConfig":{"token":"secret"},"schedule_jobs":[{"id":"internal"}]}}]}`
	})
	defer cleanup()

	resp, err := NudgebeeAgentHealthGetTool{}.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{})
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Contains(t, resp.Data, `"prometheusConnection":false`)
	assert.Contains(t, resp.Data, `"nodeAgentCount":3`)
	assert.Contains(t, resp.Data, `"deployment_model":"kubernetes_agent"`)
	assert.Contains(t, resp.Data, `"overall_health":"degraded"`)
	assert.Contains(t, resp.Data, `"prometheus":"disconnected"`)
	assert.Contains(t, resp.Data, `"kind":"heartbeat"`)
	assert.NotContains(t, resp.Data, "prometheusUrl")
	assert.NotContains(t, resp.Data, "logProviderConfig")
	assert.NotContains(t, resp.Data, "schedule_jobs")
	assert.NotContains(t, resp.Data, "secret")
	var normalized struct {
		Rows []map[string]any `json:"rows"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Data), &normalized))
	require.Len(t, normalized.Rows, 1, "null rows must be removed before normalization")
	require.NotNil(t, normalized.Rows[0])
}

func TestNudgebeeHealthNormalization(t *testing.T) {
	healthyKubernetesFeatures := map[string]any{
		"relayConnection": true, "prometheusConnection": true,
		"alertManagerConnection": true, "logsConnection": true,
		"nodeAgentConnection": true, "opencostConnection": true,
	}
	tests := []struct {
		name      string
		agentType any
		status    any
		features  map[string]any
		model     string
		signal    string
		overall   string
		opencost  string
	}{
		{name: "healthy kubernetes", agentType: "k8s", status: "CONNECTED", features: healthyKubernetesFeatures, model: "kubernetes_agent", signal: "heartbeat", overall: "healthy", opencost: "healthy"},
		{name: "degraded kubernetes", agentType: "k8s", status: "CONNECTED", features: map[string]any{"logsConnection": false}, model: "kubernetes_agent", signal: "heartbeat", overall: "degraded", opencost: "unknown"},
		{name: "incomplete kubernetes", agentType: "k8s", status: "CONNECTED", features: map[string]any{"prometheusConnection": true}, model: "kubernetes_agent", signal: "heartbeat", overall: "unknown", opencost: "unknown"},
		{name: "server managed opencost", agentType: "k8s", status: "CONNECTED", features: map[string]any{"relayConnection": true, "prometheusConnection": true, "alertManagerConnection": true, "logsConnection": true, "nodeAgentConnection": true, "opencostConnection": false, "opencostServerSide": true}, model: "kubernetes_agent", signal: "heartbeat", overall: "healthy", opencost: "server_managed"},
		{name: "disconnected proxy", agentType: "proxy", status: "NOT_CONNECTED", features: map[string]any{}, model: "vm_proxy", signal: "heartbeat", overall: "disconnected", opencost: "unknown"},
		{name: "connected proxy lacks datasource health", agentType: "proxy", status: "CONNECTED", features: map[string]any{}, model: "vm_proxy", signal: "heartbeat", overall: "unknown", opencost: "unknown"},
		{name: "agentless cloud lacks sync evidence", agentType: "AWS", status: "CONNECTED", features: map[string]any{}, model: "agentless_cloud", signal: "synchronization", overall: "unknown", opencost: "unknown"},
		{name: "agentless disconnected record lacks sync evidence", agentType: "Azure", status: "NOT_CONNECTED", features: map[string]any{}, model: "agentless_cloud", signal: "synchronization", overall: "unknown", opencost: "unknown"},
		{name: "agentless stale record lacks sync evidence", agentType: "GCP", status: "STALE", features: map[string]any{}, model: "agentless_cloud", signal: "synchronization", overall: "unknown", opencost: "unknown"},
		{name: "unknown type and status", agentType: "other", status: nil, features: map[string]any{}, model: "unknown", signal: "unknown", overall: "unknown", opencost: "unknown"},
		{name: "nil feature evidence", agentType: "k8s", status: "CONNECTED", features: nil, model: "kubernetes_agent", signal: "heartbeat", overall: "unknown", opencost: "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			featureHealth := nudgebeeFeatureHealth(tc.features)
			assert.Equal(t, tc.model, nudgebeeDeploymentModel(tc.agentType))
			assert.Equal(t, tc.signal, nudgebeeHealthSignalKind(tc.agentType))
			assert.Equal(t, tc.overall, nudgebeeRowHealth(tc.agentType, tc.status, featureHealth, nil, nil))
			assert.Equal(t, tc.opencost, featureHealth["opencost"])
		})
	}
	assert.Equal(t, "unknown", nudgebeeHealthSignalStatus("AWS", "CONNECTED", nil))
	assert.Equal(t, "2026-09-06T00:00:00Z", nudgebeeHealthSignalObservedAt("AWS", nil, "2026-09-06T00:00:00Z"))
	for _, status := range []string{"CONNECTED", "NOT_CONNECTED"} {
		assert.Equal(t, "unknown", nudgebeeHealthSignalStatus("future_agent", status, nil))
		assert.Nil(t, nudgebeeHealthSignalObservedAt("future_agent", "2026-09-06T00:00:00Z", "2026-09-06T01:00:00Z"))
	}
	model, overall := nudgebeeAggregateHealth(nil)
	assert.Equal(t, "unknown", model)
	assert.Equal(t, "unknown", overall)

	rows := []map[string]any{
		nil,
		{"deployment_model": "kubernetes_agent", "overall_health": "degraded"},
		nil,
		{"deployment_model": "vm_proxy", "overall_health": "stale"},
	}
	model, overall = nudgebeeAggregateHealth(rows)
	assert.Equal(t, "mixed", model)
	assert.Equal(t, "degraded", overall, "aggregate severity must not depend on row order")
	slices.Reverse(rows)
	_, reversedOverall := nudgebeeAggregateHealth(rows)
	assert.Equal(t, overall, reversedOverall)
}

func TestNudgebeeHealthErrorClassification(t *testing.T) {
	tests := []struct {
		message string
		want    string
	}{
		{message: "ClientSecretCredential authentication failed: RESPONSE 401 Unauthorized; permission denied", want: "AUTHENTICATION_FAILED"},
		{message: "operation error STS: InvalidClientTokenId", want: "AUTHENTICATION_FAILED"},
		{message: "ExpiredTokenException: the security token expired", want: "AUTHENTICATION_FAILED"},
		{message: "request failed: HTTP 403 Forbidden", want: "AUTHORIZATION_FAILED"},
		{message: "AccessDeniedException on the CUR bucket", want: "AUTHORIZATION_FAILED"},
		{message: "rpc error: code = PermissionDenied", want: "AUTHORIZATION_FAILED"},
		{message: "AuthorizationFailed for subscription", want: "AUTHORIZATION_FAILED"},
		{message: "dial tcp: DNS lookup: no such host", want: "DNS_FAILED"},
		{message: "x509: certificate signed by unknown authority", want: "TLS_FAILED"},
		{message: "context deadline exceeded", want: "TIMEOUT"},
		{message: "dial tcp: connection refused", want: "ENDPOINT_UNREACHABLE"},
		{message: "invalid configuration: missing required region", want: "INVALID_CONFIGURATION"},
		{message: "503 Service Unavailable", want: "PROVIDER_UNAVAILABLE"},
		{message: "no recent data was received", want: "NO_RECENT_DATA"},
		{message: "provider returned an unfamiliar failure", want: "UNKNOWN_FAILURE"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			got, ok := nudgebeeHealthError(tc.message)
			require.True(t, ok)
			assert.Equal(t, tc.want, got["reason_code"])
			assert.NotEmpty(t, got["summary"])
		})
	}
	for _, empty := range []any{nil, "", "   ", float64(403)} {
		_, ok := nudgebeeHealthError(empty)
		assert.False(t, ok)
	}

	for _, nearMiss := range []string{
		"provider connected through port 5030",
		"request id 403abc completed",
		"certificate rotation is scheduled",
		"using timeout-service.example.com",
	} {
		got, ok := nudgebeeHealthError(nearMiss)
		require.True(t, ok)
		assert.Equal(t, "UNKNOWN_FAILURE", got["reason_code"])
	}
}

func TestNudgebeeAgentHealthGetSanitizesProviderError(t *testing.T) {
	rawError := "ClientSecretCredential authentication failed at https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token: 401 Unauthorized: invalid_client, invalid client secret; trace ID secret-trace-id"
	cleanup := startNudgebeeQueryStub(t, func(action string, input map[string]any) (int, string) {
		assert.Equal(t, "agents_list_health", action)
		body, err := json.Marshal(map[string]any{"rows": []map[string]any{
			{
				"cloud_account_id": "acc-1", "type": "Azure", "status": "CONNECTED",
				"last_connected_at": "2026-09-06T00:00:00Z", "status_message": rawError,
			},
			{
				"cloud_account_id": "acc-1", "type": "k8s", "status": "CONNECTED",
				"status_message": "certificate rotation is scheduled",
			},
		}})
		require.NoError(t, err)
		return http.StatusOK, string(body)
	})
	defer cleanup()

	resp, err := NudgebeeAgentHealthGetTool{}.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{})
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.NotContains(t, resp.Data, "login.microsoftonline.com")
	assert.NotContains(t, resp.Data, "tenant-id")
	assert.NotContains(t, resp.Data, "secret-trace-id")

	var normalized struct {
		Rows []struct {
			StatusMessage string            `json:"status_message"`
			HealthError   map[string]string `json:"health_error"`
			HealthSignal  map[string]any    `json:"health_signal"`
			OverallHealth string            `json:"overall_health"`
		} `json:"rows"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Data), &normalized))
	require.Len(t, normalized.Rows, 2)
	row := normalized.Rows[0]
	assert.Equal(t, "AUTHENTICATION_FAILED", row.HealthError["reason_code"])
	assert.Equal(t, "The provider rejected authentication.", row.HealthError["summary"])
	assert.Equal(t, row.HealthError["summary"], row.StatusMessage)
	assert.Equal(t, "unknown", row.HealthSignal["status"])
	assert.Nil(t, row.HealthSignal["observed_at"])
	assert.Equal(t, "unknown", row.OverallHealth)
	assert.Equal(t, "certificate rotation is scheduled", normalized.Rows[1].StatusMessage)
	assert.Empty(t, normalized.Rows[1].HealthError)
}

func TestNudgebeeAgentHealthGetNormalizesAgentlessSynchronization(t *testing.T) {
	previousNow := nudgebeeNow
	t.Cleanup(func() { nudgebeeNow = previousNow })
	nudgebeeNow = func() time.Time { return time.Date(2026, 9, 6, 10, 5, 0, 0, time.UTC) }
	rawError := "AccessDeniedException for https://provider.example/account/secret-id"
	cleanup := startNudgebeeQueryStub(t, func(action string, input map[string]any) (int, string) {
		assert.Equal(t, "agents_list_health", action)
		assert.Contains(t, input["columns"], "last_synced_at")
		return http.StatusOK, `{"rows":[{"cloud_account_id":"acc-1","type":"AWS","status":"CONNECTED","last_synced_at":"2026-09-06T10:00:00Z","connection_status":{"events":{"end":"2026-09-06T09:59:00Z","err":""},"resources":{"updated_at":"2026-09-06T09:58:00Z","err":"` + rawError + `"},"recommendations":{"updated_at":"2026-09-06T09:57:00Z","err":""},"spends":{"updated_at":"2026-09-06T09:56:00Z","err":""},"account_number":"123456789012","cf_stack":{"stack_name":"secret"}}}]} `
	})
	defer cleanup()

	resp, err := NudgebeeAgentHealthGetTool{}.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{})
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.NotContains(t, resp.Data, "provider.example")
	assert.NotContains(t, resp.Data, "123456789012")
	assert.NotContains(t, resp.Data, "cf_stack")

	var normalized struct {
		Rows []struct {
			OverallHealth         string                    `json:"overall_health"`
			HealthSignal          map[string]any            `json:"health_signal"`
			SynchronizationHealth map[string]map[string]any `json:"synchronization_health"`
		} `json:"rows"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Data), &normalized))
	require.Len(t, normalized.Rows, 1)
	row := normalized.Rows[0]
	assert.Equal(t, "degraded", row.OverallHealth)
	assert.Equal(t, "degraded", row.HealthSignal["status"])
	assert.Equal(t, "2026-09-06T10:00:00Z", row.HealthSignal["observed_at"])
	assert.Equal(t, "healthy", row.SynchronizationHealth["events"]["status"])
	assert.Equal(t, "2026-09-06T09:59:00Z", row.SynchronizationHealth["events"]["observed_at"])
	assert.Equal(t, "disconnected", row.SynchronizationHealth["resources"]["status"])
	resourceError := row.SynchronizationHealth["resources"]["health_error"].(map[string]any)
	assert.Equal(t, "AUTHORIZATION_FAILED", resourceError["reason_code"])
}

func TestNudgebeeAgentHealthGetKeepsIncompleteSynchronizationUnknown(t *testing.T) {
	previousNow := nudgebeeNow
	t.Cleanup(func() { nudgebeeNow = previousNow })
	nudgebeeNow = func() time.Time { return time.Date(2026, 9, 6, 10, 5, 0, 0, time.UTC) }
	cleanup := startNudgebeeQueryStub(t, func(string, map[string]any) (int, string) {
		return http.StatusOK, `{"rows":[{"cloud_account_id":"acc-1","type":"GCP","status":"CONNECTED","last_synced_at":"2026-09-06T10:00:00Z","connection_status":{"events":{"end":"2026-09-06T09:59:00Z","err":""},"resources":{"updated_at":"timestamp-secret","err":""},"recommendations":{"updated_at":{"token":"object-secret"},"err":""},"spends":{"end":["list-secret"],"err":""}}}]}`
	})
	defer cleanup()

	resp, err := NudgebeeAgentHealthGetTool{}.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{})
	require.NoError(t, err)
	assert.Contains(t, resp.Data, `"overall_health":"unknown"`)
	assert.Contains(t, resp.Data, `"status":"unknown"`)
	assert.NotContains(t, resp.Data, "timestamp-secret")
	assert.NotContains(t, resp.Data, "object-secret")
	assert.NotContains(t, resp.Data, "list-secret")
}

func TestNudgebeeAgentHealthGetNormalizesProxyDatasources(t *testing.T) {
	previousNow := nudgebeeNow
	t.Cleanup(func() { nudgebeeNow = previousNow })
	nudgebeeNow = func() time.Time { return time.Date(2026, 9, 6, 10, 5, 0, 0, time.UTC) }
	rawError := "dial tcp database.internal:5432: connection refused; password=secret"
	cleanup := startNudgebeeQueryStub(t, func(string, map[string]any) (int, string) {
		return http.StatusOK, `{"rows":[{"cloud_account_id":"acc-1","type":"proxy","status":"CONNECTED","connection_status":{"datasources":{"ds-secret-id":{"name":"orders","type":"postgresql","proxy_type":"database","status":"unhealthy","last_check":"timestamp-secret","error":"` + rawError + `","token":"secret"},"ds-2":{"name":"cache","type":"redis","status":"healthy","last_check":"2026-09-06T09:59:00Z"}}}}]}`
	})
	defer cleanup()

	resp, err := NudgebeeAgentHealthGetTool{}.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{})
	require.NoError(t, err)
	assert.NotContains(t, resp.Data, "database.internal")
	assert.NotContains(t, resp.Data, "password")
	assert.NotContains(t, resp.Data, "ds-secret-id")
	assert.NotContains(t, resp.Data, `"token"`)
	assert.NotContains(t, resp.Data, "timestamp-secret")

	var normalized struct {
		Rows []struct {
			OverallHealth    string           `json:"overall_health"`
			DatasourceHealth []map[string]any `json:"datasource_health"`
		} `json:"rows"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Data), &normalized))
	require.Len(t, normalized.Rows, 1)
	assert.Equal(t, "degraded", normalized.Rows[0].OverallHealth)
	require.Len(t, normalized.Rows[0].DatasourceHealth, 2)
	assert.Equal(t, "cache", normalized.Rows[0].DatasourceHealth[0]["name"])
	assert.Equal(t, "orders", normalized.Rows[0].DatasourceHealth[1]["name"])
	assert.Equal(t, "disconnected", normalized.Rows[0].DatasourceHealth[1]["status"])
	healthError := normalized.Rows[0].DatasourceHealth[1]["health_error"].(map[string]any)
	assert.Equal(t, "ENDPOINT_UNREACHABLE", healthError["reason_code"])
}

func TestNudgebeeHealthFreshnessRejectsMissingMalformedAndExpiredTimestamps(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, "unknown", nudgebeeFreshnessVerdict(nil, time.Hour, now))
	assert.Equal(t, "unknown", nudgebeeFreshnessVerdict("not-a-time", time.Hour, now))
	assert.Equal(t, "unknown", nudgebeeFreshnessVerdict("2026-09-06T12:06:00Z", time.Hour, now))
	assert.Equal(t, "stale", nudgebeeFreshnessVerdict("2026-09-06T10:59:59Z", time.Hour, now))
	assert.Equal(t, "healthy", nudgebeeFreshnessVerdict("2026-09-06T11:00:00Z", time.Hour, now))

	missingTimestamps := map[string]any{
		"events":          map[string]any{"err": ""},
		"resources":       map[string]any{"err": ""},
		"recommendations": map[string]any{"err": ""},
		"spends":          map[string]any{"err": ""},
	}
	assert.Equal(t, "unknown", nudgebeeEvidenceHealth(nudgebeeSynchronizationHealth(missingTimestamps, now)))

	staleDatasource := nudgebeeDatasourceHealth(map[string]any{"datasources": map[string]any{
		"ds-1": map[string]any{"name": "orders", "status": "healthy", "last_check": "2026-09-06T11:29:59Z"},
	}}, now)
	require.Len(t, staleDatasource, 1)
	assert.Equal(t, "stale", staleDatasource[0]["status"])
	assert.Equal(t, "stale", nudgebeeDatasourceOverallHealth(staleDatasource))
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
	assert.Contains(t, NudgebeeIntegrationDiagnoseTool{}.Description(), "unsupported")
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
