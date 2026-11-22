package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
)

func TestParseMetricForecastInput(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	tests := []struct {
		name    string
		input   core.NBToolCallRequest
		want    services_server.ForecastRequest
		wantErr string
	}{
		{
			name: "no kind and no threshold",
			input: core.NBToolCallRequest{Arguments: map[string]any{
				"workload_namespace": "shop", "workload_name": "order-service", "metric": "cpu_throttling",
			}},
			want: services_server.ForecastRequest{WorkloadNamespace: "shop", WorkloadName: "order-service", Metric: "cpu_throttling"},
		},
		{
			name: "all fields, case-folded",
			input: core.NBToolCallRequest{Arguments: map[string]any{
				"workload_namespace": "db", "workload_name": "pg", "kind": "StatefulSet", "metric": "CPU_SATURATION",
				"threshold": float64(80), "end_time": float64(1760000000000),
			}},
			want: services_server.ForecastRequest{
				WorkloadNamespace: "db", WorkloadName: "pg", Kind: "statefulset", Metric: "cpu_saturation",
				Threshold: f(80), EndTime: 1760000000000,
			},
		},
		{
			name:  "JSON command and a numeric string threshold",
			input: core.NBToolCallRequest{Command: `{"workload_namespace":"ns","workload_name":"w","metric":"cpu_saturation","threshold":"30"}`},
			want:  services_server.ForecastRequest{WorkloadNamespace: "ns", WorkloadName: "w", Metric: "cpu_saturation", Threshold: f(30)},
		},
		{
			name: "memory with its container",
			input: core.NBToolCallRequest{Arguments: map[string]any{
				"workload_namespace": "shop", "workload_name": "orders", "metric": "memory_saturation", "container": " api ",
			}},
			want: services_server.ForecastRequest{WorkloadNamespace: "shop", WorkloadName: "orders", Metric: "memory_saturation", Container: "api"},
		},
		{
			name: "a CPU metric drops a container",
			input: core.NBToolCallRequest{Arguments: map[string]any{
				"workload_namespace": "shop", "workload_name": "orders", "metric": "cpu_throttling", "container": "api",
			}},
			want: services_server.ForecastRequest{WorkloadNamespace: "shop", WorkloadName: "orders", Metric: "cpu_throttling"},
		},
		{
			name:    "memory needs a container",
			input:   core.NBToolCallRequest{Arguments: map[string]any{"workload_namespace": "ns", "workload_name": "w", "metric": "memory_saturation"}},
			wantErr: "memory_saturation needs a container",
		},
		{
			name:    "missing workload",
			input:   core.NBToolCallRequest{Arguments: map[string]any{"workload_namespace": "ns"}},
			wantErr: "workload_namespace and workload_name are required",
		},
		{
			name:    "pod is not a workload kind",
			input:   core.NBToolCallRequest{Arguments: map[string]any{"workload_namespace": "ns", "workload_name": "w", "metric": "cpu_throttling", "kind": "pod"}},
			wantErr: "kind must be one of",
		},
		{
			name:    "metric is required",
			input:   core.NBToolCallRequest{Arguments: map[string]any{"workload_namespace": "ns", "workload_name": "w"}},
			wantErr: "metric must be one of",
		},
		{
			name:    "unknown metric",
			input:   core.NBToolCallRequest{Arguments: map[string]any{"workload_namespace": "ns", "workload_name": "w", "metric": "latency"}},
			wantErr: "metric must be one of",
		},
		{
			name:    "threshold out of range",
			input:   core.NBToolCallRequest{Arguments: map[string]any{"workload_namespace": "ns", "workload_name": "w", "metric": "cpu_throttling", "threshold": float64(150)}},
			wantErr: "threshold must be a percentage",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMetricForecastInput(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// stubForecast swaps the api-server call for the test and records the request.
func stubForecast(t *testing.T, data string, err error) *services_server.ForecastRequest {
	t.Helper()
	got := &services_server.ForecastRequest{}
	previous := generateForecastFn
	generateForecastFn = func(_ security.RequestContext, req services_server.ForecastRequest) (json.RawMessage, error) {
		*got = req
		if err != nil {
			return nil, err
		}
		return json.RawMessage(data), nil
	}
	t.Cleanup(func() { generateForecastFn = previous })
	return got
}

func forecastCtx() core.NbToolContext {
	return core.NbToolContext{
		Ctx:             security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"origin-id", "prod-id"}),
		AccountId:       "origin-id",
		TargetAccountId: "prod-id",
	}
}

func TestMetricForecastTool_Call(t *testing.T) {
	args := core.NBToolCallRequest{Arguments: map[string]any{"workload_namespace": "shop", "workload_name": "order-service", "metric": "cpu_throttling"}}

	t.Run("reads the target account and passes the result through", func(t *testing.T) {
		result := `{"status":"projected","eta_hours":38,"current_pct":15,"threshold_pct":25,"suggested_limit":1.5,"limit_unit":"cores","daily":[]}`
		got := stubForecast(t, result, nil)

		resp, err := MetricForecastTool{}.Call(forecastCtx(), args)

		require.NoError(t, err)
		assert.Equal(t, "prod-id", got.AccountId, "account must follow the call's target")
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		assert.Equal(t, core.NBToolResponseTypeJson, resp.Type)
		assert.JSONEq(t, result, resp.Data)
	})

	t.Run("a missing request context is an error, not a panic", func(t *testing.T) {
		got := stubForecast(t, `{"status":"projected"}`, nil)
		ctx := forecastCtx()
		ctx.Ctx = nil

		resp, err := MetricForecastTool{}.Call(ctx, args)

		require.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "request context is missing")
		assert.Empty(t, got.WorkloadName, "the forecast is never requested")
	})

	t.Run("not-projected answers are successes", func(t *testing.T) {
		for _, status := range []string{"not_projected", "breached", "insufficient_history", "no_limit", "provider_unsupported"} {
			stubForecast(t, fmt.Sprintf(`{"status":%q}`, status), nil)
			resp, err := MetricForecastTool{}.Call(forecastCtx(), args)
			require.NoError(t, err)
			assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status, status)
		}
	})

	t.Run("engine error status is a tool error", func(t *testing.T) {
		stubForecast(t, `{"status":"error","reason":"prometheus unreachable"}`, nil)
		resp, err := MetricForecastTool{}.Call(forecastCtx(), args)
		require.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "prometheus unreachable")
	})

	t.Run("403 says there is no access and not to retry", func(t *testing.T) {
		stubForecast(t, "", fmt.Errorf("%w: access denied for account: prod-id", services_server.ErrForecastForbidden))
		resp, err := MetricForecastTool{}.Call(forecastCtx(), args)
		require.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "no access to its metrics")
		assert.Contains(t, resp.Data, "Do not retry")
	})

	t.Run("transport error is a tool error", func(t *testing.T) {
		stubForecast(t, "", errors.New("services: forecast failed with status 500"))
		resp, err := MetricForecastTool{}.Call(forecastCtx(), args)
		require.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "status 500")
	})

	t.Run("invalid input never calls api-server", func(t *testing.T) {
		got := stubForecast(t, `{"status":"projected"}`, nil)
		resp, err := MetricForecastTool{}.Call(forecastCtx(), core.NBToolCallRequest{Arguments: map[string]any{"workload_name": "w"}})
		require.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Empty(t, got.AccountId)
	})
}

func TestMetricForecastTool_DeclaredFollowsTarget(t *testing.T) {
	d, ok := core.AccountScopeOf(ToolMetricForecast)
	require.True(t, ok)
	assert.Equal(t, core.AccountScopeFollowsTarget, d.Scope)
	assert.Contains(t, core.ModelInputSchema(MetricForecastTool{}).Properties, "target")
}
