package gcloud

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
)

func TestBuildLogFilter(t *testing.T) {
	// resource.type + identifying labels; project_id dropped, empty skipped, stable order.
	got := buildResourceScopeFilter("cloud_run_revision", map[string]string{
		"project_id":   "p",
		"service_name": "frontoffice",
		"location":     "us-central1",
		"revision":     "",
	})
	want := `resource.type="cloud_run_revision" AND resource.labels.location="us-central1" AND resource.labels.service_name="frontoffice"`
	if got != want {
		t.Errorf("buildResourceScopeFilter:\n got=%s\nwant=%s", got, want)
	}

	// no labels -> bare resource.type scope
	if got := buildResourceScopeFilter("gae_app", nil); got != `resource.type="gae_app"` {
		t.Errorf("bare filter = %q", got)
	}

	// quotes/backslashes in a value are escaped (no filter injection / broken query)
	got2 := buildResourceScopeFilter("cloud_run_revision", map[string]string{"service_name": `a"b\c`})
	want2 := `resource.type="cloud_run_revision" AND resource.labels.service_name="a\"b\\c"`
	if got2 != want2 {
		t.Errorf("escaping:\n got=%s\nwant=%s", got2, want2)
	}
}

func TestHasIdentifyingLabels(t *testing.T) {
	if hasIdentifyingLabels(map[string]string{"project_id": "p"}) {
		t.Error("project_id-only should NOT count as identifying")
	}
	if !hasIdentifyingLabels(map[string]string{"project_id": "p", "module_id": "default"}) {
		t.Error("module_id should count as identifying")
	}
	if hasIdentifyingLabels(nil) {
		t.Error("nil should not be identifying")
	}
}

func TestExtractLogMetricName(t *testing.T) {
	if n, ok := extractLogMetricName("logging.googleapis.com/user/log4j_exploits"); !ok || n != "log4j_exploits" {
		t.Errorf("got (%q,%v)", n, ok)
	}
	if _, ok := extractLogMetricName("compute.googleapis.com/instance/cpu/utilization"); ok {
		t.Error("non-log-metric should not match")
	}
}

func TestExtractSLOServicePath(t *testing.T) {
	// Real gae SLO expr (validated in-band).
	expr := `select_slo_burn_rate("projects/184170451616/services/gae:live-universalsignup_default/serviceLevelObjectives/_gDjkJvNTXKySXPpJbalgQ","3600s")`
	got, ok := extractSLOServicePath(expr)
	if !ok || got != "projects/184170451616/services/gae:live-universalsignup_default" {
		t.Errorf("got (%q,%v)", got, ok)
	}
	// Real Cloud Run SLO with an opaque service id.
	expr2 := `select_slo_burn_rate("projects/39964537935/services/6O_Igx-kQAG3qmT4sHxU_w/serviceLevelObjectives/abc","3600s")`
	got2, ok2 := extractSLOServicePath(expr2)
	if !ok2 || got2 != "projects/39964537935/services/6O_Igx-kQAG3qmT4sHxU_w" {
		t.Errorf("opaque-id SLO got (%q,%v)", got2, ok2)
	}
	if _, ok := extractSLOServicePath("logging.googleapis.com/user/x"); ok {
		t.Error("non-SLO should not match")
	}
}

func TestMapMonitoringService(t *testing.T) {
	ae := &monitoringpb.Service{Identifier: &monitoringpb.Service_AppEngine_{
		AppEngine: &monitoringpb.Service_AppEngine{ModuleId: "default"}}}
	if rt, labels := mapMonitoringService(ae); rt != "gae_app" || labels["module_id"] != "default" {
		t.Errorf("AppEngine -> (%q, %v)", rt, labels)
	}

	cr := &monitoringpb.Service{Identifier: &monitoringpb.Service_CloudRun_{
		CloudRun: &monitoringpb.Service_CloudRun{ServiceName: "frontoffice", Location: "us-central1"}}}
	if rt, labels := mapMonitoringService(cr); rt != "cloud_run_revision" || labels["service_name"] != "frontoffice" || labels["location"] != "us-central1" {
		t.Errorf("CloudRun -> (%q, %v)", rt, labels)
	}

	if rt, _ := mapMonitoringService(&monitoringpb.Service{}); rt != "" {
		t.Errorf("empty service should map to empty resource type, got %q", rt)
	}
}

func TestPickLogMatchFilter(t *testing.T) {
	// A policy mixing condition kinds contributes only its log-match filter.
	policy := &monitoringpb.AlertPolicy{Conditions: []*monitoringpb.AlertPolicy_Condition{
		{Condition: &monitoringpb.AlertPolicy_Condition_ConditionThreshold{
			ConditionThreshold: &monitoringpb.AlertPolicy_Condition_MetricThreshold{Filter: `metric.type="x"`}}},
		{Condition: &monitoringpb.AlertPolicy_Condition_ConditionMatchedLog{
			ConditionMatchedLog: &monitoringpb.AlertPolicy_Condition_LogMatch{
				Filter: `resource.type="gke_nodepool" AND jsonPayload.state="STARTED"`}}},
	}}
	want := `resource.type="gke_nodepool" AND jsonPayload.state="STARTED"`
	if got := pickLogMatchFilter(policy); got != want {
		t.Errorf("pickLogMatchFilter:\n got=%s\nwant=%s", got, want)
	}

	// Metric-only policy has no log scope to offer.
	metricOnly := &monitoringpb.AlertPolicy{Conditions: []*monitoringpb.AlertPolicy_Condition{
		{Condition: &monitoringpb.AlertPolicy_Condition_ConditionThreshold{
			ConditionThreshold: &monitoringpb.AlertPolicy_Condition_MetricThreshold{Filter: `metric.type="x"`}}},
	}}
	if got := pickLogMatchFilter(metricOnly); got != "" {
		t.Errorf("metric-only policy should yield no filter, got %q", got)
	}

	// A blank filter is not a scope — must fall through, not scope to everything.
	blank := &monitoringpb.AlertPolicy{Conditions: []*monitoringpb.AlertPolicy_Condition{
		{Condition: &monitoringpb.AlertPolicy_Condition_ConditionMatchedLog{
			ConditionMatchedLog: &monitoringpb.AlertPolicy_Condition_LogMatch{Filter: "  "}}},
	}}
	if got := pickLogMatchFilter(blank); got != "" {
		t.Errorf("blank filter should yield %q, got %q", "", got)
	}

	if got := pickLogMatchFilter(nil); got != "" {
		t.Errorf("nil policy should yield no filter, got %q", got)
	}
}

func TestLoggingResourceType(t *testing.T) {
	// HTTP(S) LB alerts fire on https_lb_rule / l7_lb_rule; the logs are all written
	// under http_load_balancer.
	if got := loggingResourceType("https_lb_rule"); got != "http_load_balancer" {
		t.Errorf("https_lb_rule -> %q", got)
	}
	if got := loggingResourceType("l7_lb_rule"); got != "http_load_balancer" {
		t.Errorf("l7_lb_rule -> %q", got)
	}
	// Types where both catalogs agree pass through untouched.
	for _, rt := range []string{"cloud_run_revision", "cloudsql_database", "gae_app", "gke_cluster"} {
		if got := loggingResourceType(rt); got != rt {
			t.Errorf("%s should pass through, got %q", rt, got)
		}
	}
}

// TestResolveGcloudScopeOfflinePaths covers the resolver branches that reach no GCP API:
// the instance-scoped path that already works in production (regression guard) and the
// fallback that must remap the resource type.
func TestResolveGcloudScopeOfflinePaths(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name       string
		in         GCPScopeInput
		wantFilter string
		wantSource string
	}{
		{
			// https_lb_rule metric alert: no identifying labels, no log metric, no SLO.
			// Before the remap this produced resource.type="https_lb_rule", which matches
			// no Cloud Logging entry at all.
			name:       "lb fallback remaps to the logging resource type",
			in:         GCPScopeInput{Project: "example-project", ResourceType: "https_lb_rule", MetricType: "loadbalancing.googleapis.com/https/request_count", AlertType: "metric"},
			wantFilter: `resource.type="http_load_balancer"`,
			wantSource: "resource_type_fallback",
		},
		{
			name:       "log alert without a policy id still falls back by resource type",
			in:         GCPScopeInput{Project: "p", ResourceType: "gke_nodepool", AlertType: "log"},
			wantFilter: `resource.type="gke_nodepool"`,
			wantSource: "native_log",
		},
		{
			// Regression guard: the instance-scoped path serving 1300+ prod events daily.
			name: "identifying labels still scope to the instance, unmapped",
			in: GCPScopeInput{Project: "p", ResourceType: "cloud_run_revision", AlertType: "metric",
				ResourceLabels: map[string]string{"project_id": "p", "service_name": "frontoffice", "location": "us-central1"}},
			wantFilter: `resource.type="cloud_run_revision" AND resource.labels.location="us-central1" AND resource.labels.service_name="frontoffice"`,
			wantSource: "resource_labels",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveGcloudScope(ctx, account, tt.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.LogFilter != tt.wantFilter {
				t.Errorf("filter:\n got=%s\nwant=%s", got.LogFilter, tt.wantFilter)
			}
			if got.Source != tt.wantSource {
				t.Errorf("source = %q, want %q", got.Source, tt.wantSource)
			}
		})
	}

	// No resource type and nothing else to go on is an error, not a project-wide scope.
	if _, err := resolveGcloudScope(ctx, account, GCPScopeInput{Project: "p"}); err == nil {
		t.Error("expected an error when nothing identifies a scope")
	}
}
