package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memoryForecastMeta is a container-scoped request, as the memory forecast sends.
func memoryForecastMeta() RequestMetadata {
	return RequestMetadata{
		Kind: "deployment", Namespace: "shop", Name: "orders", ContainerName: "api",
		PodNames: []string{"orders-6d9f-abcde", "orders-6d9f-fghij"}, PodNamesSupplied: true,
		RangeWindow: "3d", Step: "300s", RateWindow: "300s",
	}
}

func memoryForecastQuery(t *testing.T, key UtilisationKey) string {
	t.Helper()
	q := buildPrometheusWorkloadQueries(memoryForecastMeta(), []string{string(key)})[string(key)]
	require.NotEmpty(t, q)
	_, err := promQLParser.ParseExpr(stripClusterPlaceholder(q))
	require.NoError(t, err, "must be valid PromQL:\n%s", q)
	assert.Contains(t, q, `container="api"`, "every memory forecast key answers for one container")
	assert.Contains(t, q, `pod=~"orders-6d9f-abcde|orders-6d9f-fghij"`)
	assert.NotContains(t, q, "sum(max by (namespace, pod, container) (kube_pod_container_resource_limits",
		"a summed limit under-reads by the replica count and moves with every scale")
	return q
}

// The ratio is RSS over each pod's own limit, then the highest pod: pods with
// different limits are each measured against their own.
func TestContainerMemoryRSSLimitRatio_IsPerPodRSSOverItsOwnLimit(t *testing.T) {
	q := memoryForecastQuery(t, KeyContainerMemoryRSSLimitRatio)
	assert.Contains(t, q, "container_memory_rss", "RSS, not working set: page cache is reclaimable")
	assert.NotContains(t, q, "working_set")
	assert.Contains(t, q, "/ on (namespace, pod, container)", "divided per pod, not as totals")
	assert.Contains(t, q, `resource="memory"`)
	assert.Contains(t, q, `> 0)`, "a container without a memory limit has no series")
	assert.Contains(t, q, "max_over_time(container_memory_rss", "the trailing-hour peak: spikes are what OOM-kill a container")
	assert.Contains(t, q, "[1h])", "an hour, so the hourly buckets the forecast averages are each an hour's peak")
}

func TestContainerMemoryLimitAndRSS_ArePerPodInMiB(t *testing.T) {
	limit := memoryForecastQuery(t, KeyContainerMemoryLimitPerPodMiB)
	assert.Contains(t, limit, `max(max by (namespace, pod, container) (kube_pod_container_resource_limits`)
	assert.Contains(t, limit, "/ 1048576")
	rss := memoryForecastQuery(t, KeyContainerMemoryRSSMiB)
	assert.Contains(t, rss, `max(max by (namespace, pod, container) (max_over_time(container_memory_rss`, "the same peak as the ratio")
	assert.Contains(t, rss, "/ 1048576")
}

// Both halves come from kube-state-metrics; joining on job keeps two scrapers from
// counting one restart twice, as KeyContainerOOMKilledLimitByPod does.
func TestContainerRestartsLastOOM_JoinsOnJob(t *testing.T) {
	q := memoryForecastQuery(t, KeyContainerRestartsLastOOM6h)
	assert.Contains(t, q, "kube_pod_container_status_restarts_total")
	assert.Contains(t, q, `reason="OOMKilled"`)
	assert.Contains(t, q, "on (namespace, pod, container, job)")
	assert.Contains(t, q, "[6h]")
}
