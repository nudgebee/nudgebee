package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A gauge family is already expressed in cores and must NOT be rate()d; a counter must be.
// Getting this backwards yields a chart that is silently wrong rather than empty, which is
// the harder failure to notice.
func TestBuildOpenObserveWorkloadCPUQuery_GaugeVsCounter(t *testing.T) {
	gauge := openObserveWorkloadMetricCandidate{
		Family: "k8s_pod_cpu_usage", NamespaceLabel: "k8s_namespace_name", WorkloadLabel: "k8s_pod_name",
	}
	assert.Equal(t,
		`sum(k8s_pod_cpu_usage{k8s_namespace_name="nudgebee",k8s_pod_name=~"relay-server-.*"}) by (k8s_pod_name)`,
		buildOpenObserveWorkloadCPUQuery(gauge, "relay-server", "nudgebee"))

	counter := openObserveWorkloadMetricCandidate{
		Family: "container_cpu_usage_seconds_total", NamespaceLabel: "namespace", WorkloadLabel: "pod", Counter: true,
	}
	assert.Equal(t,
		`sum(rate(container_cpu_usage_seconds_total{namespace="nudgebee",pod=~"relay-server-.*"}[5m])) by (pod)`,
		buildOpenObserveWorkloadCPUQuery(counter, "relay-server", "nudgebee"))
}

// The workload is matched as a pod-name PREFIX. Pod names carry a replica-hash suffix, and
// several of these families carry no deployment label at all, so an exact match finds
// nothing for every workload kind.
func TestBuildOpenObserveWorkloadCPUQuery_MatchesPodPrefix(t *testing.T) {
	q := buildOpenObserveWorkloadCPUQuery(OpenObserveWorkloadCPUCandidates[0], "cart", "prod")
	assert.Contains(t, q, `k8s_pod_name=~"cart-.*"`)
	assert.NotContains(t, q, `k8s_pod_name="cart"`)
}

// A workload or namespace carrying PromQL metacharacters must not be able to break out of
// the matcher literal and alter the query.
func TestBuildOpenObserveWorkloadCPUQuery_EscapesValues(t *testing.T) {
	q := buildOpenObserveWorkloadCPUQuery(OpenObserveWorkloadCPUCandidates[0], `ev"il`, `ns"x`)
	assert.NotContains(t, q, `{k8s_namespace_name="ns"x"`, "unescaped quote would terminate the literal")
	assert.Contains(t, q, `\"`)
}

// Every candidate must name both of its identifying labels and a family; a blank one would
// render a matcher like `="ns"` and fail at query time rather than at review time.
func TestOpenObserveWorkloadCPUCandidates_WellFormed(t *testing.T) {
	assert.NotEmpty(t, OpenObserveWorkloadCPUCandidates)
	seen := map[string]bool{}
	for _, c := range OpenObserveWorkloadCPUCandidates {
		assert.NotEmpty(t, c.Family)
		assert.NotEmpty(t, c.NamespaceLabel)
		assert.NotEmpty(t, c.WorkloadLabel)
		assert.False(t, seen[c.Family], "duplicate candidate family %q", c.Family)
		seen[c.Family] = true
	}
}

// An absent family answers success-with-no-series rather than an error, so an empty
// payload is precisely the signal to keep probing.
func TestOpenObserveQueryHasSeries(t *testing.T) {
	assert.False(t, openObserveQueryHasSeries(OutputMetricQuery{}))
	assert.False(t, openObserveQueryHasSeries(OutputMetricQuery{
		Results: []QueryResult{{QueryKey: "cpu", Payload: nil}},
	}))
	assert.True(t, openObserveQueryHasSeries(OutputMetricQuery{
		Results: []QueryResult{{QueryKey: "cpu", Payload: []Result{{}}}},
	}))

	// A result carrying an error is not evidence the family exists.
	msg := "boom"
	assert.False(t, openObserveQueryHasSeries(OutputMetricQuery{
		Results: []QueryResult{{QueryKey: "cpu", Payload: []Result{{}}, Error: &msg}},
	}))
}
