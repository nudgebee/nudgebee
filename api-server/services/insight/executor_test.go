package insight

import (
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
