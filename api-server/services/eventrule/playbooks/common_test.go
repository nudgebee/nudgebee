package playbooks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPrometheusExtractLabels_SeriesInvariant locks the contract the event-label
// _series guard (eventrule/service.go) depends on: ExtractLabels MUST keep
// returning the full matched-series array under the "_series" key so playbook
// for_each templates ({{ extracted_labels['action_0']['_series'] | ... }}) can
// still resolve it from extractedLabels. The service.go merge loops skip
// "_series" so this (potentially hundreds of KB) array never lands on
// event.Labels — but it must remain available here. If a future change drops
// "_series" from the producer, for_each silently breaks; this test catches that.
func TestPrometheusExtractLabels_SeriesInvariant(t *testing.T) {
	resp := PrometheusActionResponse{
		Data: map[string]any{
			"series_list_result": []any{
				map[string]any{"metric": map[string]any{"pod": "app-a", "status": "200"}},
				map[string]any{"metric": map[string]any{"pod": "app-b", "status": "500"}},
			},
		},
	}

	labels := resp.ExtractLabels()

	// First series' labels are promoted to the top level (backward compatibility).
	assert.Equal(t, "app-a", labels["pod"])
	assert.Equal(t, "200", labels["status"])

	// The full series array is preserved under "_series" for for_each templating.
	series, ok := labels["_series"].([]map[string]any)
	assert.True(t, ok, "_series must be the full []map[string]any array, not stringified")
	assert.Len(t, series, 2)
	assert.Equal(t, "app-b", series[1]["pod"])
}

// TestPrometheusExtractLabels_Empty verifies no "_series" key is emitted when
// there are no series (so the merge never even sees it in the empty case).
func TestPrometheusExtractLabels_Empty(t *testing.T) {
	resp := PrometheusActionResponse{Data: map[string]any{}}
	labels := resp.ExtractLabels()
	_, ok := labels["_series"]
	assert.False(t, ok)
}
