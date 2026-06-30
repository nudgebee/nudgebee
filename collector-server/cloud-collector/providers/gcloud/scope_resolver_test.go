package gcloud

import (
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
