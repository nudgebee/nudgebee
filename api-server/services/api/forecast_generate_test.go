package api

import (
	"testing"
	"time"

	"nudgebee/services/forecast"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForecastGenerateRequest_Preset(t *testing.T) {
	req, err := forecastGenerateRequest{AccountId: "acc", Namespace: "shop", Name: "order", Metric: "cpu_throttling"}.toForecastRequest()
	require.NoError(t, err)
	assert.Equal(t, forecast.ConditionThrottling, req.Condition)
	assert.Zero(t, req.ThresholdPct, "zero lets the engine apply the preset's default")
	assert.True(t, req.EndTime.IsZero(), "no end_time means now")
}

func TestForecastGenerateRequest_Explicit(t *testing.T) {
	req, err := forecastGenerateRequest{
		AccountId: "acc", Namespace: "shop", Name: "order", Kind: "StatefulSet",
		Metric: "CPU_Saturation", Threshold: 85, EndTime: 1_790_000_000_000,
	}.toForecastRequest()
	require.NoError(t, err)
	assert.Equal(t, forecast.ConditionSaturation, req.Condition)
	assert.Equal(t, 85.0, req.ThresholdPct)
	assert.Equal(t, "StatefulSet", req.Kind)
	assert.True(t, req.EndTime.Equal(time.UnixMilli(1_790_000_000_000)))
}

func TestForecastGenerateRequest_Rejects(t *testing.T) {
	for name, r := range map[string]forecastGenerateRequest{
		"missing workload":     {AccountId: "acc", Namespace: "shop", Metric: "cpu_throttling"},
		"no metric":            {AccountId: "acc", Namespace: "shop", Name: "o"},
		"unknown metric":       {AccountId: "acc", Namespace: "shop", Name: "o", Metric: "latency"},
		"threshold above":      {AccountId: "acc", Namespace: "shop", Name: "o", Metric: "cpu_throttling", Threshold: 150},
		"negative threshold":   {AccountId: "acc", Namespace: "shop", Name: "o", Metric: "cpu_throttling", Threshold: -1},
		"memory, no container": {AccountId: "acc", Namespace: "shop", Name: "o", Metric: "memory_saturation"},
	} {
		_, err := r.toForecastRequest()
		assert.Error(t, err, name)
	}
	_, err := forecastGenerateRequest{AccountId: "acc", Namespace: "shop", Name: "o", Metric: "latency"}.toForecastRequest()
	assert.ErrorContains(t, err, "cpu_saturation, cpu_throttling", "names the valid presets")
}

func TestForecastGenerateRequest_MemoryCarriesItsContainer(t *testing.T) {
	req, err := forecastGenerateRequest{AccountId: "acc", Namespace: "shop", Name: "orders", Metric: "memory_saturation", Container: " api "}.toForecastRequest()
	require.NoError(t, err)
	assert.Equal(t, forecast.ConditionMemorySaturation, req.Condition)
	assert.Equal(t, "api", req.Container)

	cpu, err := forecastGenerateRequest{AccountId: "acc", Namespace: "shop", Name: "orders", Metric: "cpu_throttling"}.toForecastRequest()
	require.NoError(t, err)
	assert.Empty(t, cpu.Container, "a workload-wide preset needs no container")
}
