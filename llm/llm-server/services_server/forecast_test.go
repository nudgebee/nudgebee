package services_server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// forecastCall is what the fake api-server received.
type forecastCall struct {
	path    string
	header  http.Header
	payload map[string]any
}

// forecastServer stands up a fake api-server that answers with the given
// status and body and hands back what it received on the returned channel.
func forecastServer(t *testing.T, status int, body string) <-chan forecastCall {
	t.Helper()
	calls := make(chan forecastCall, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := forecastCall{path: r.URL.Path, header: r.Header.Clone()}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &call.payload)
		calls <- call
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	orig := config.Config.ServiceEndpoint
	config.Config.ServiceEndpoint = server.URL
	t.Cleanup(func() { config.Config.ServiceEndpoint = orig })
	return calls
}

func TestGenerateForecast_SendsActionEnvelopeAndReturnsData(t *testing.T) {
	calls := forecastServer(t, http.StatusOK, `{"data":{"status":"projected","eta_hours":38}}`)
	threshold := 30.0
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acc-1"})

	data, err := GenerateForecast(*ctx, ForecastRequest{
		AccountId: "acc-1", WorkloadNamespace: "shop", WorkloadName: "order-service",
		Kind: "deployment", Metric: "cpu_saturation", Threshold: &threshold,
	})

	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"projected","eta_hours":38}`, string(data))
	got := <-calls
	assert.Equal(t, "/rpc/slo", got.path)
	assert.Equal(t, "tenant-1", got.header.Get("x-tenant-id"))
	assert.Equal(t, "user-1", got.header.Get("x-user-id"))
	assert.JSONEq(t, `{
		"action": {"name": "forecast_generate"},
		"input": {"request": {"account_id": "acc-1", "workload_namespace": "shop", "workload_name": "order-service",
			"kind": "deployment", "metric": "cpu_saturation", "threshold": 30}}
	}`, mustJSON(t, got.payload))
}

// Event auto-investigation runs as the system user; the sentinel must go out
// as an empty x-user-id so api-server takes its tenant-admin branch.
func TestGenerateForecast_SystemUserSendsEmptyUser(t *testing.T) {
	calls := forecastServer(t, http.StatusOK, `{"data":{"status":"not_projected"}}`)
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant-1", security.GetSystemUserId(), []string{"acc-1"})

	_, err := GenerateForecast(*ctx, ForecastRequest{AccountId: "acc-1", WorkloadNamespace: "ns", WorkloadName: "w"})

	require.NoError(t, err)
	got := <-calls
	assert.Equal(t, "", got.header.Get("x-user-id"))
	assert.Equal(t, "tenant-1", got.header.Get("x-tenant-id"))
}

func TestGenerateForecast_Errors(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acc-1"})
	req := ForecastRequest{AccountId: "acc-1", WorkloadNamespace: "ns", WorkloadName: "w"}

	t.Run("403 is the forbidden sentinel with the server's message", func(t *testing.T) {
		forecastServer(t, http.StatusForbidden, `{"message":"access denied for account: acc-1"}`)
		_, err := GenerateForecast(*ctx, req)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrForecastForbidden))
		assert.Contains(t, err.Error(), "access denied")
	})
	t.Run("400 carries the server's body", func(t *testing.T) {
		forecastServer(t, http.StatusBadRequest, `{"message":"workload_name is required"}`)
		_, err := GenerateForecast(*ctx, req)
		require.Error(t, err)
		assert.False(t, errors.Is(err, ErrForecastForbidden))
		assert.Contains(t, err.Error(), "workload_name is required")
	})
	t.Run("500 is an error", func(t *testing.T) {
		forecastServer(t, http.StatusInternalServerError, `boom`)
		_, err := GenerateForecast(*ctx, req)
		require.Error(t, err)
	})
	t.Run("missing data is an error", func(t *testing.T) {
		forecastServer(t, http.StatusOK, `{}`)
		_, err := GenerateForecast(*ctx, req)
		require.Error(t, err)
	})
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
