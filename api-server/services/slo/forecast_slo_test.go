package slo

import (
	"context"
	"log/slog"
	"testing"

	"nudgebee/services/forecast"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type forecastFakes struct {
	evaluated []forecast.Request
	result    forecast.Result
	synced    []forecast.Target
	stored    []string
	closed    []forecast.Condition
}

func installForecastFakes(t *testing.T) *forecastFakes {
	t.Helper()
	f := &forecastFakes{result: forecast.Result{Status: forecast.StatusNotProjected}}
	origEval, origSync, origClose, origStore, origKind, origName :=
		evaluateForecast, syncForecast, closeForecastAll, storeLastForecast, workloadKindOf, accountNameOf
	t.Cleanup(func() {
		evaluateForecast, syncForecast, closeForecastAll, storeLastForecast, workloadKindOf, accountNameOf =
			origEval, origSync, origClose, origStore, origKind, origName
	})
	evaluateForecast = func(_ *security.RequestContext, req forecast.Request) (forecast.Result, forecast.Series) {
		f.evaluated = append(f.evaluated, req)
		return f.result, nil
	}
	syncForecast = func(tg forecast.Target, _ forecast.Result, _ forecast.Series) error {
		f.synced = append(f.synced, tg)
		return nil
	}
	closeForecastAll = func(_ forecast.Target, _, _, _ string, c forecast.Condition) error {
		f.closed = append(f.closed, c)
		return nil
	}
	storeLastForecast = func(_ *database.DatabaseManager, id string, _ forecast.Result) error {
		f.stored = append(f.stored, id)
		return nil
	}
	workloadKindOf = func(string, string, string, string) string { return "statefulset" }
	accountNameOf = func(*database.DatabaseManager, string) (string, error) { return "prod-cluster", nil }
	return f
}

var cpuConfig = DBSLOConfig{
	Id: "cfg-1", Name: "cpu_throttling", Method: forecastMethod, ThresholdBucket: 30,
	TenantId: "ten", CloudAccountId: "acc", WorkloadName: "order-service", Namespace: "shop", Enabled: true,
}

func TestExecuteSLOConfig_CPUSLOGoesToTheForecastNotTheAgent(t *testing.T) {
	f := installForecastFakes(t)
	require.NoError(t, executeSLOConfig(cpuConfig, "acc", nil))

	require.Len(t, f.evaluated, 1)
	req := f.evaluated[0]
	assert.Equal(t, forecast.ConditionThrottling, req.Condition)
	assert.Equal(t, 30.0, req.ThresholdPct, "the user's threshold, in percent")
	assert.Equal(t, "statefulset", req.Kind)
	assert.Equal(t, []string{"cfg-1"}, f.stored)
	require.Len(t, f.synced, 1)
	assert.Equal(t, forecast.Target{TenantId: "ten", AccountId: "acc", AccountName: "prod-cluster", SLOConfigId: "cfg-1"}, f.synced[0])
}

// tenantCtx is a tenant-scoped context that needs no database to build.
func tenantCtx() *security.RequestContext {
	return security.NewRequestContext(context.Background(), security.NewSecurityContextForAuditRelay("ten", "user-1"), slog.Default(), nil, nil)
}

func TestValidateForecastConfig(t *testing.T) {
	req := SLORequest{AccountId: "acc", WorkloadName: "order-service", Namespace: "shop"}

	f := installForecastFakes(t)
	for _, bad := range []float64{0, -5, 101} {
		assert.ErrorContains(t, validateForecastConfig(tenantCtx(), req, SLOConfigRequest{Name: "cpu_throttling", Threshold: bad, Enabled: true}), "percentage")
	}
	assert.Empty(t, f.evaluated, "a bad threshold is refused before any fetch")

	f = installForecastFakes(t)
	f.result = forecast.Result{Status: forecast.StatusNoLimit, Reason: "the workload has no CPU limit"}
	assert.ErrorContains(t, validateForecastConfig(tenantCtx(), req, SLOConfigRequest{Name: "cpu_saturation", Threshold: 90, Enabled: true}), "no CPU limit")

	f = installForecastFakes(t)
	f.result = forecast.Result{Status: forecast.StatusInsufficientHistory}
	assert.NoError(t, validateForecastConfig(tenantCtx(), req, SLOConfigRequest{Name: "cpu_saturation", Threshold: 90, Enabled: true}),
		"too little history yet is fine: it is a reason to wait, not to refuse")

	f = installForecastFakes(t)
	assert.NoError(t, validateForecastConfig(tenantCtx(), req, SLOConfigRequest{Name: "cpu_throttling", Threshold: 0, Enabled: false}),
		"disabling is allowed with no threshold and needs no forecast, so open warnings can be closed")
	assert.Empty(t, f.evaluated, "disabling needs no evaluation")
}

func TestCloseForecastEvents_UsesTheForecastClose(t *testing.T) {
	f := installForecastFakes(t)
	closeForecastEvents("ten", "acc", "CPU_Saturation", "order-service", "shop")
	assert.Equal(t, []forecast.Condition{forecast.ConditionSaturation}, f.closed)
}

func TestIsForecastType(t *testing.T) {
	assert.True(t, isForecastType("cpu_throttling"))
	assert.True(t, isForecastType("CPU_SATURATION"))
	assert.False(t, isForecastType("latency"))
	assert.False(t, isForecastType("availability"))
}
