package utils

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractIndexFromConfig(t *testing.T) {
	t.Run("array format with metrics_index", func(t *testing.T) {
		rawJSON := `[
			{"name": "log_index", "value": "logs-kubernetes.container_logs-*", "has_value": true},
			{"name": "metrics_index", "value": "metricbeat*", "has_value": true},
			{"name": "trace_index", "value": "", "has_value": false}
		]`
		var rawData any
		err := json.Unmarshal([]byte(rawJSON), &rawData)
		assert.NoError(t, err)

		metricsIdx := extractIndexFromConfig(rawData, []string{"metrics_index", "metric_index", "metrics_default_index", "default_index"})
		assert.Equal(t, "metricbeat*", metricsIdx)

		logsIdx := extractIndexFromConfig(rawData, []string{"log_index", "logs_index", "default_index"})
		assert.Equal(t, "logs-kubernetes.container_logs-*", logsIdx)
	})

	t.Run("map format with metrics_index", func(t *testing.T) {
		rawJSON := `{"log_index": "logs-*", "metrics_index": "metricbeat*"}`
		var rawData any
		err := json.Unmarshal([]byte(rawJSON), &rawData)
		assert.NoError(t, err)

		metricsIdx := extractIndexFromConfig(rawData, []string{"metrics_index", "metric_index", "metrics_default_index", "default_index"})
		assert.Equal(t, "metricbeat*", metricsIdx)
	})
}
