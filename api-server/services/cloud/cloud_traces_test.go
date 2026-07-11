package cloud

import (
	"log/slog"
	"nudgebee/services/eventrule/playbooks"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCloudTracesGating verifies the cloud_traces enricher fires for the GCP managed-
// compute resource types that export to Cloud Trace, and only those. Pure unit test —
// CanAutoExecute reads event labels only.
func TestCloudTracesGating(t *testing.T) {
	action := cloudTracesAction{}
	ctxWith := func(labels map[string]string) playbooks.PlaybookActionContext {
		return playbooks.NewPlaybookActionContext("t", "a", slog.Default(),
			playbooks.PlaybookEvent{Source: "GCP_Metric_Alert", Labels: labels})
	}
	base := func(rt string) map[string]string {
		return map[string]string{
			"gcp_account": "cbp-infra", "gcp_project_id": "cbp-infra",
			"gcp_event_resource_type": rt,
		}
	}

	// Trace-emitting workloads -> enrich.
	for _, rt := range []string{"cloud_run_revision", "gae_app", "cloud_function"} {
		assert.True(t, action.CanAutoExecute(ctxWith(base(rt))),
			"cloud_traces should enrich %s (exports to Cloud Trace)", rt)
	}

	// Non-trace resources (infra / managed services / LBs) -> do NOT enrich.
	for _, rt := range []string{"gke_nodepool", "gke_cluster", "l7_lb_rule", "http_load_balancer", "gce_instance", "cloudsql_database", "cloud_tasks_queue"} {
		assert.False(t, action.CanAutoExecute(ctxWith(base(rt))),
			"cloud_traces should NOT enrich %s (no Cloud Trace data)", rt)
	}

	// A trace-emitting type but no GCP account/project -> do NOT enrich.
	assert.False(t, action.CanAutoExecute(ctxWith(map[string]string{
		"gcp_event_resource_type": "cloud_run_revision",
	})), "cloud_traces requires a gcp account or project")
}

// TestGCPTraceServiceFromLabels verifies the per-resource-type workload identifier
// resolution (GCP flattens resource.labels onto the event as resource_<key>).
func TestGCPTraceServiceFromLabels(t *testing.T) {
	cases := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{
			name:   "cloud run uses resource_service_name",
			labels: map[string]string{"gcp_event_resource_type": "cloud_run_revision", "resource_service_name": "oauth"},
			want:   "oauth",
		},
		{
			name:   "gae uses resource_module_id",
			labels: map[string]string{"gcp_event_resource_type": "gae_app", "resource_module_id": "externalcalendars"},
			want:   "externalcalendars",
		},
		{
			name:   "cloud function uses resource_function_name",
			labels: map[string]string{"gcp_event_resource_type": "cloud_function", "resource_function_name": "ingest"},
			want:   "ingest",
		},
		{
			name:   "gae falls back to cloud-run heuristics when module absent",
			labels: map[string]string{"gcp_event_resource_type": "gae_app", "gcp_event_instance": "svc", "gcp_incident_id": "0.x"},
			want:   "svc",
		},
		{
			name:   "incident-id-only yields no service",
			labels: map[string]string{"gcp_event_resource_type": "cloud_run_revision", "gcp_event_instance": "0.x", "gcp_incident_id": "0.x"},
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, gcpTraceServiceFromLabels(tc.labels))
		})
	}
}

// TestFilterSpansByService verifies traces are scoped to the incident workload, kept whole
// (any matching span pulls its whole trace), and that scoping is non-destructive.
func TestFilterSpansByService(t *testing.T) {
	spans := []collectorTraceSpan{
		// trace A: belongs to "oauth" via /http/host.
		{TraceId: "A", SpanId: "a1", Labels: map[string]string{"/http/host": "oauth-abc-uc.a.run.app"}},
		{TraceId: "A", SpanId: "a2", Labels: map[string]string{}}, // child, no host — kept via trace A.
		// trace B: an unrelated service.
		{TraceId: "B", SpanId: "b1", Labels: map[string]string{"/http/host": "billing-xyz-uc.a.run.app"}},
	}

	scoped := filterSpansByService(spans, "oauth")
	assert.Len(t, scoped, 2, "both spans of the matching trace are kept")
	for _, s := range scoped {
		assert.Equal(t, "A", s.TraceId)
	}
	assert.Equal(t, 1, distinctTraceCount(scoped))

	// No match anywhere -> non-destructive: return everything (degrade to project window).
	unmatched := filterSpansByService(spans, "does-not-exist")
	assert.Len(t, unmatched, 3, "no match keeps all spans rather than going empty")

	// Empty service -> no-op.
	assert.Len(t, filterSpansByService(spans, ""), 3)

	// Match on span name (non-HTTP spans).
	named := []collectorTraceSpan{{TraceId: "C", SpanId: "c1", Name: "GET oauth /token", Labels: map[string]string{}}}
	assert.Len(t, filterSpansByService(named, "oauth"), 1)
}
