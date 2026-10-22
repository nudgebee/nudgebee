package api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestForecastTrendGuidance_OnlyForForecastAlerts(t *testing.T) {
	g := forecastTrendGuidance("forecast", "cpu_throttling")
	assert.Contains(t, g, "## CPU SLO Forecast Alert")
	assert.Contains(t, g, "by day (daily mean)", "points at the insight the forecast alert carries")
	assert.Contains(t, g, "inside Quick Investigation", "the report's section headers are a fixed contract")
	assert.Contains(t, g, "projected daily mean", "points at the forecast insight that carries the projected days")
	assert.Contains(t, g, "(projected)", "projected rows are told apart from observed ones")
	assert.False(t, strings.Contains(g, "TODO"))

	for _, src := range []string{"", "slo", "webhook", "prometheus"} {
		assert.Empty(t, forecastTrendGuidance(src, "memory_saturation"), src)
	}
}

func TestForecastTrendGuidance_MemoryNamesTheContainerAndKeepsOOMAsEvidence(t *testing.T) {
	g := forecastTrendGuidance("forecast", "memory_saturation")
	assert.Contains(t, g, "## Memory SLO Forecast Alert")
	assert.Contains(t, g, "ONE container")
	assert.Contains(t, g, "MiB")
	assert.Contains(t, g, "by day (daily mean of hourly peaks)", "quotes the memory insight exactly")
	assert.Contains(t, g, "projected daily mean")
	assert.Contains(t, g, "(projected)")
	assert.Contains(t, g, "never present that count as a number of OOM kills")
	assert.NotContains(t, g, "CPU")
	assert.False(t, strings.Contains(g, "TODO"))

	cpu := forecastTrendGuidance("forecast", "")
	assert.Contains(t, cpu, "## CPU SLO Forecast Alert", "an event without a condition label keeps the CPU text")
	assert.Equal(t, cpu, forecastTrendGuidance("forecast", "cpu_saturation"))
}
