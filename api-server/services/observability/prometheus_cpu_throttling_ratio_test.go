package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cpu_throttling_ratio is held against a percentage threshold, so it must be a ratio
// of CFS periods (not throttled seconds), scoped exactly like the other workload
// resource keys and sampled at the caller's rate window.
func TestCPUThrottlingRatio_IsAPeriodsRatioOnTheWorkloadScope(t *testing.T) {
	meta := RequestMetadata{
		Kind: "deployment", Namespace: "shop", Name: "order-service",
		PodNames: []string{"order-service-6d9f-abcde", "order-service-6d9f-fghij"}, PodNamesSupplied: true,
		RangeWindow: "3d", Step: "3600s", RateWindow: "1800s",
	}
	q := buildPrometheusWorkloadQueries(meta, []string{string(KeyCPUThrottlingRatio)})[string(KeyCPUThrottlingRatio)]
	require.NotEmpty(t, q)
	_, err := promQLParser.ParseExpr(stripClusterPlaceholder(q))
	require.NoError(t, err, "must be valid PromQL:\n%s", q)

	assert.Contains(t, q, "container_cpu_cfs_throttled_periods_total")
	assert.Contains(t, q, "container_cpu_cfs_periods_total")
	assert.NotContains(t, q, "throttled_seconds", "seconds per second is the cpu_throttling key, not a ratio")
	assert.Contains(t, q, "[1800s]", "the rate window follows the caller's step")
	assert.Contains(t, q, `> 0)`, "an idle window must be absent, not +Inf")
	assert.Contains(t, q, `pod=~"order-service-6d9f-abcde|order-service-6d9f-fghij"`, "an exact pod list beats the name prefix")
	assert.Contains(t, q, `container!=""`, "the pod-level cgroup rollup must not be counted twice")
}

// cpu_usage_limited is divided by cpu_limit, so it must count only the containers that
// have a CPU limit: a container without one would add usage to one side only.
func TestCPUUsageLimited_CountsOnlyContainersWithACPULimit(t *testing.T) {
	meta := RequestMetadata{
		Kind: "deployment", Namespace: "shop", Name: "order-service",
		PodNames: []string{"order-service-6d9f-abcde"}, PodNamesSupplied: true,
		RangeWindow: "3d", Step: "300s", RateWindow: "300s",
	}
	q := buildPrometheusWorkloadQueries(meta, []string{string(KeyCPUUsageLimited)})[string(KeyCPUUsageLimited)]
	require.NotEmpty(t, q)
	_, err := promQLParser.ParseExpr(stripClusterPlaceholder(q))
	require.NoError(t, err, "must be valid PromQL:\n%s", q)

	assert.Contains(t, q, "container_cpu_usage_seconds_total")
	assert.Contains(t, q, "and on (namespace, pod, container)")
	assert.Contains(t, q, `kube_pod_container_resource_limits{`)
	assert.Contains(t, q, `resource="cpu"`)
	assert.Contains(t, q, `> 0)`, "a zero limit is no limit")
	assert.Contains(t, q, `pod=~"order-service-6d9f-abcde"`)
}
