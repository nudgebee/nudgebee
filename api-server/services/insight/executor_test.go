package insight

import (
	"nudgebee/services/internal/testenv"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWorkloadKey_NoCollision(t *testing.T) {
	// The separator must keep ("a", "b-c") distinct from ("a-b", "c") etc.
	assert.NotEqual(t, workloadKey("a", "b-c"), workloadKey("a-b", "c"))
	assert.Equal(t, workloadKey("checkout", "demo"), workloadKey("checkout", "demo"))
}

func TestSelectRelevantTraceApplications_FiltersToKnownWorkloads(t *testing.T) {
	rule := InsightRule{AggregateColumn: "p95_latency", Threshold: 5000000000}

	// Mirrors the live data classes: real otel + eBPF-only workloads are in the
	// inventory; node names, ephemeral pod names, and uncollapsed ReplicaSet
	// names are not.
	rows := []query.QueryRow{
		{"workload_name": "frontend", "workload_namespace": "demo", "p95_latency": 6e9},                                     // otel, known
		{"workload_name": "temporal-history", "workload_namespace": "nudgebee", "p95_latency": 6e9},                         // ebpf-only, known
		{"workload_name": "product-catalog-6c7ffcdc64", "workload_namespace": "demo", "p95_latency": 6e9},                   // replicaset, unknown
		{"workload_name": "k8s-action-runner-x-p8zd5", "workload_namespace": "actions-runner-system-1", "p95_latency": 6e9}, // pod, unknown
		{"workload_name": "gke-node-abc", "workload_namespace": "node", "p95_latency": 6e9},                                 // node, unknown
		{"workload_name": "cart", "workload_namespace": "demo", "p95_latency": 1e9},                                         // known but under threshold
	}
	known := map[string]struct{}{
		workloadKey("frontend", "demo"):             {},
		workloadKey("temporal-history", "nudgebee"): {},
		workloadKey("product-catalog", "demo"):      {},
		workloadKey("cart", "demo"):                 {},
	}

	got := selectRelevantTraceApplications(rows, rule, known)

	assert.ElementsMatch(t, []RelevantApplications{
		{Name: "frontend", Namespace: "demo"},
		{Name: "temporal-history", Namespace: "nudgebee"},
	}, got)
}

func TestSelectRelevantTraceApplications_NilInventoryDisablesFilter(t *testing.T) {
	rule := InsightRule{AggregateColumn: "error_rate", Threshold: 0.5}
	rows := []query.QueryRow{
		{"workload_name": "gke-node-abc", "workload_namespace": "node", "error_count": 9.0, "count": 10.0},
		{"workload_name": "", "workload_namespace": "demo", "error_count": 9.0, "count": 10.0}, // empty name always skipped
	}

	// nil inventory (e.g. lookup failed) must fall back to no filtering so a
	// transient inventory gap never silently blanks the insight.
	got := selectRelevantTraceApplications(rows, rule, nil)

	assert.Equal(t, []RelevantApplications{{Name: "gke-node-abc", Namespace: "node"}}, got)
}

func TestComputeRedirectURL_TraceAggregation(t *testing.T) {
	// TraceAggregation insights carry Source="Event" but must route to the traces
	// tab (not events/all-events) so the home-page application chips can scope the
	// traces view to a workload.
	rule := InsightRule{
		UniqueID:           "1",
		Type:               InsightTypeTraceAggregation,
		Source:             InsightSourceEvent,
		InsightCategory:    Troubleshooting,
		InsightSubCategory: "Trace",
		InsightFormat:      "API Latency (>5000ms)",
	}

	got := computeRedirectURL(rule, "acct-123", "K8s")
	assert.Equal(t, "/kubernetes/details/acct-123#monitoring/traces", got)
}

func TestK8sListObjects2(t *testing.T) {
	testenv.RequireWarehouse(t)
	insightQuery := "with resource_allocation as ( select crm2.cloud_account_id, crm2.tenant_id, sum(case when crm2.metric = 'memory_capacity' then crm2.value else 0 end) as memory_capacity, sum(case when crm2.metric = 'cpu_capacity' then crm2.value else 0 end) as cpu_capacity, sum(case when crm2.metric = 'memory_allocated' then crm2.value else 0 end) as memory_allocated , sum(case when crm2.metric = 'cpu_allocated' then crm2.value else 0 end) as cpu_allocated, row_number() over (partition by crm2.cloud_account_id order by crm2.timestamp desc) as rn from k8s_nodes ksn2 inner join cloud_resource_metrics crm2 on ksn2.cloud_resource_id = crm2.cloud_resource_id group by crm2.timestamp, crm2.cloud_account_id,crm2.tenant_id ), resorce_utilization_percent as ( select 100 - (case when memory_capacity > 0 then memory_allocated / memory_capacity * 100 else 0 end) as memory_utilize_percent, cloud_account_id, tenant_id from resource_allocation where rn = 1 )"
	rule := InsightRule{UniqueID: "1", Source: InsightSourceMetric, ViewName: "resorce_utilization_percent", With: insightQuery, GroupedBy: []string{"cloud_account_id", "tenant_id"}, AggregateColumn: "sum(memory_utilize_percent)", Type: InsightTypeRatio, Threshold: 50, InsightFormat: "{}%% memory is not allocated"}
	ctx := security.RequestContext{}
	executor, err := newRuleExecutor(&ctx, rule)
	assert.Nil(t, err)
	_, err = executor.ExecuteRule(rule, []string{""})
	assert.Nil(t, err)
}
