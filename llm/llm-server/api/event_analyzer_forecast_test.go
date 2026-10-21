package api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestForecastTrendGuidance_OnlyForForecastAlerts(t *testing.T) {
	g := forecastTrendGuidance("forecast")
	assert.Contains(t, g, "## CPU SLO Forecast Alert")
	assert.Contains(t, g, "by day (daily mean)", "points at the insight the forecast alert carries")
	assert.Contains(t, g, "inside Quick Investigation", "the report's section headers are a fixed contract")
	assert.Contains(t, g, "projected daily mean", "points at the forecast insight that carries the projected days")
	assert.Contains(t, g, "(projected)", "projected rows are told apart from observed ones")
	assert.False(t, strings.Contains(g, "TODO"))

	for _, src := range []string{"", "slo", "webhook", "prometheus"} {
		assert.Empty(t, forecastTrendGuidance(src), src)
	}
}
