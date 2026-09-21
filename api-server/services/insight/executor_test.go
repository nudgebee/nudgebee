package insight

import (
	"net/url"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	got := computeRedirectURL(rule, rule.InsightFormat, "acct-123", "K8s")
	assert.Equal(t, "/kubernetes/details/acct-123#monitoring/traces", got)
}

// normalizeRedirectURL drops the volatile start_time/end_time params and sorts
// the remaining query keys so table expectations can be written by content, not
// by emit order or wall-clock time.
func normalizeRedirectURL(t *testing.T, raw string) string {
	t.Helper()
	frag := ""
	if i := strings.Index(raw, "#"); i >= 0 {
		frag, raw = raw[i:], raw[:i]
	}
	u, err := url.Parse(raw)
	require.NoError(t, err)
	q := u.Query()
	q.Del("start_time")
	q.Del("end_time")
	u.RawQuery = q.Encode()
	return u.String() + frag
}

func TestComputeRedirectURL_Families(t *testing.T) {
	const acctID = "acct-1"

	cases := []struct {
		name          string
		rule          InsightRule
		title         string
		cloudProvider string
		want          string
	}{
		{
			name: "event webhook (pagerduty)",
			rule: InsightRule{
				UniqueID: "118", Source: InsightSourceEvent, Type: InsightTypeAddition, Range: 1,
				Filters: []InsightFilters{{Column: "source", Value: "pagerduty_webhook", Operator: "="}, {Column: "status", Value: "FIRING", Operator: "="}},
			},
			title: "6 PagerDuty incidents are FIRING", cloudProvider: "K8s",
			want: "/kubernetes/details/acct-1?eventStatus=FIRING&sortBy=computed_score&source=pagerduty_webhook#events/all-events",
		},
		{
			name: "event aggregation, hard-coded key (crash loop)",
			rule: InsightRule{
				UniqueID: "122", Source: InsightSourceEvent, Type: InsightTypeAddition, Range: 1,
				Filters: []InsightFilters{{Column: "aggregation_key", Value: "report_crash_loop", Operator: "="}, {Column: "status", Value: "FIRING", Operator: "="}},
			},
			title: "1 pods CrashLooping", cloudProvider: "K8s",
			want: "/kubernetes/details/acct-1?eventAggregationKey=report_crash_loop&eventStatus=FIRING#events/all-events",
		},
		{
			name: "event aggregation, previously-unhandled key (log errors)",
			rule: InsightRule{
				UniqueID: "3", Source: InsightSourceEvent, Type: InsightTypeEventAggregation, InsightSubCategory: "LogGroup", Range: 1,
				Filters: []InsightFilters{{Column: "aggregation_key", Value: "HighErrorCriticalLogs", Operator: "="}, {Column: "status", Value: "FIRING", Operator: "="}},
			},
			title: "Frequent Log Errors", cloudProvider: "K8s",
			want: "/kubernetes/details/acct-1?eventAggregationKey=HighErrorCriticalLogs&eventStatus=FIRING#events/all-events",
		},
		{
			name: "ratio + noisiest workload, value from title (uid 127)",
			rule: InsightRule{
				UniqueID: "127", Source: InsightSourceEvent, Type: InsightTypeRatio, Range: 7, RangeUnit: InsightRangeUnitDay,
				InsightFormat: "{subject_name} is noisiest with {} events this week",
				Filters:       []InsightFilters{{Column: "rn", Value: 1, Operator: "="}},
			},
			title: "calico-typha is noisiest with 234 events this week", cloudProvider: "K8s",
			want: "/kubernetes/details/acct-1?subject_name=calico-typha#events/all-events",
		},
		{
			name: "ratio + most frequent issue, key from title (uid 129)",
			rule: InsightRule{
				UniqueID: "129", Source: InsightSourceEvent, Type: InsightTypeRatio, Range: 7, RangeUnit: InsightRangeUnitDay,
				InsightFormat: "Most frequent issue: {aggregation_key} ({} FIRING events)",
				Filters:       []InsightFilters{{Column: "rn", Value: 1, Operator: "="}},
			},
			title: "Most frequent issue: image_pull_backoff_reporter (386 FIRING events)", cloudProvider: "K8s",
			want: "/kubernetes/details/acct-1?eventAggregationKey=image_pull_backoff_reporter&eventStatus=FIRING#events/all-events",
		},
		{
			name: "ratio + most frequent issue, title does not match format",
			rule: InsightRule{
				UniqueID: "129", Source: InsightSourceEvent, Type: InsightTypeRatio, Range: 7, RangeUnit: InsightRangeUnitDay,
				InsightFormat: "Most frequent issue: {aggregation_key} ({} FIRING events)",
				Filters:       []InsightFilters{{Column: "rn", Value: 1, Operator: "="}},
			},
			title: "n/a", cloudProvider: "K8s",
			want: "/kubernetes/details/acct-1#events/all-events",
		},
		{
			name: "recommendation by rule_name",
			rule: InsightRule{
				UniqueID: "15", Source: InsightSourceRecommendation, Type: InsightTypeAddition,
				Filters: []InsightFilters{{Column: "rule_name", Value: "unused_pvc", Operator: "="}, {Column: "status", Value: "Open", Operator: "="}},
			},
			title: "22 persistent volume seems to be abandoned", cloudProvider: "K8s",
			want: "/kubernetes/details/acct-1#optimize/unused-volume",
		},
		{
			name: "recommendation ratio by uid 17 (image scan, severity-scoped)",
			rule: InsightRule{
				UniqueID: "17", Source: InsightSourceRecommendation, Type: InsightTypeRatio, InsightCategory: Ops,
				InsightFormat: "{} workload has Critical/High security vulnerabilities",
			},
			title: "142 workload has Critical/High security vulnerabilities", cloudProvider: "K8s",
			want: "/kubernetes/details/acct-1?severity=Critical,High&status=Open#security/image-scan",
		},
		{
			name: "trace aggregation always routes to traces tab",
			rule: InsightRule{
				UniqueID: "1", Source: InsightSourceEvent, Type: InsightTypeTraceAggregation, InsightSubCategory: "Trace",
				InsightFormat: "API Latency (>5000ms)",
			},
			title: "API Latency (>5000ms)", cloudProvider: "K8s",
			want: "/kubernetes/details/acct-1#monitoring/traces",
		},
		{
			name: "cloud recommendation by category (security)",
			rule: InsightRule{
				UniqueID: "103", Source: InsightSourceRecommendation, Type: InsightTypeAddition, InsightCategory: Security,
				Filters: []InsightFilters{{Column: "status", Value: "Open", Operator: "="}, {Column: "category", Value: "Security", Operator: "="}},
			},
			title: "1 CRITICAL security vulnerabilities require immediate action", cloudProvider: "aws",
			want: "/cloud-account/details/acct-1?accountId=acct-1#optimize/security",
		},
		{
			name: "cloud recommendation uid 114 (missing tags, severity-scoped)",
			rule: InsightRule{
				UniqueID: "114", Source: InsightSourceRecommendation, Type: InsightTypeAddition, InsightCategory: "Configuration",
				Filters: []InsightFilters{{Column: "rule_name", Value: []interface{}{"aws_tags", "azure_missing_tags"}, Operator: "in"}, {Column: "status", Value: "Open", Operator: "="}},
			},
			title: "187 resources missing required tags/labels", cloudProvider: "aws",
			want: "/cloud-account/details/acct-1?accountId=acct-1&ruleName=aws_tags,azure_missing_tags&severity=Low,Medium#optimize/configuration",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeRedirectURL(tc.rule, tc.title, acctID, tc.cloudProvider)
			assert.Equal(t, normalizeRedirectURL(t, tc.want), normalizeRedirectURL(t, got))
		})
	}
}

func TestExtractFromFormat(t *testing.T) {
	assert.Equal(t, "calico-typha",
		extractFromFormat("{subject_name} is noisiest with {} events this week",
			"calico-typha is noisiest with 234 events this week")["subject_name"])

	assert.Equal(t, "image_pull_backoff_reporter",
		extractFromFormat("Most frequent issue: {aggregation_key} ({} FIRING events)",
			"Most frequent issue: image_pull_backoff_reporter (386 FIRING events)")["aggregation_key"])

	// Title that doesn't match the template yields nil.
	assert.Nil(t, extractFromFormat("{subject_name} is noisiest with {} events this week", "unrelated title"))

	// No placeholders → nil.
	assert.Nil(t, extractFromFormat("API Latency (>5000ms)", "API Latency (>5000ms)"))
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
