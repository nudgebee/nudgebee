package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// An evidence card whose keys returned no series still names the query that ran,
// in key preference order; a key the provider has no query for is skipped.
func TestFirstQueryAsked(t *testing.T) {
	out := OutputMetricQuery{Results: []QueryResult{
		{QueryKey: "cpu_usage", Query: "sum(rate(container_cpu_usage_seconds_total[5m]))"},
		{QueryKey: "cpu_usage_by_pod", Query: ""},
	}}
	assert.Equal(t, "sum(rate(container_cpu_usage_seconds_total[5m]))", firstQueryAsked(out, []string{"cpu_usage_by_pod", "cpu_usage"}))
	assert.Equal(t, "", firstQueryAsked(out, []string{"memory_usage"}))
	assert.Equal(t, "", firstQueryAsked(OutputMetricQuery{}, []string{"cpu_usage"}))

	dup := OutputMetricQuery{Results: []QueryResult{{QueryKey: "cpu_usage", Query: "first"}, {QueryKey: "cpu_usage", Query: "second"}}}
	assert.Equal(t, "first", firstQueryAsked(dup, []string{"cpu_usage"}))
}
