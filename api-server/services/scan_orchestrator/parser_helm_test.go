package scan_orchestrator

import (
	"sort"
	"testing"
)

const helmFixture = `[
  {
    "release": "actions-runner-controller",
    "chartName": "actions-runner-controller",
    "namespace": "actions-runner-system-1",
    "Installed": {"version": "0.23.7", "appVersion": "0.27.6"},
    "Latest":    {"version": "0.23.7", "appVersion": "0.27.6"},
    "outdated": false
  },
  {
    "release": "prometheus-stack",
    "chartName": "kube-prometheus-stack",
    "namespace": "monitoring",
    "Installed": {"version": "65.0.0"},
    "Latest":    {"version": "70.0.0"},
    "outdated": true
  },
  {
    "release": "loki",
    "chartName": "loki",
    "namespace": "logging",
    "Installed": {"version": "5.0.0"},
    "Latest":    {"version": "6.20.0"},
    "outdated": true
  }
]`

func TestParseHelmChartUpgrade_OnlyOutdated(t *testing.T) {
	account := ScanAccount{AccountID: "acc-1", TenantID: "tenant-1"}
	recs, err := ParseHelmChartUpgrade(helmFixture, account)
	if err != nil {
		t.Fatalf("ParseHelmChartUpgrade: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("recs = %d; want 2 (current release skipped)", len(recs))
	}

	sort.Slice(recs, func(i, j int) bool { return recs[i].AccountObjectID < recs[j].AccountObjectID })

	want := []string{
		"loki-loki-logging-5.0.0",
		"prometheus-stack-kube-prometheus-stack-monitoring-65.0.0",
	}
	for i, w := range want {
		if recs[i].AccountObjectID != w {
			t.Errorf("rec[%d].AccountObjectID = %q; want %q", i, recs[i].AccountObjectID, w)
		}
		if recs[i].RuleName != HelmChartUpgradeRuleName {
			t.Errorf("rec[%d].RuleName = %q", i, recs[i].RuleName)
		}
		if recs[i].Category != "InfraUpgrade" {
			t.Errorf("rec[%d].Category = %q; want InfraUpgrade", i, recs[i].Category)
		}
		if recs[i].Severity != "Info" {
			t.Errorf("rec[%d].Severity = %q", i, recs[i].Severity)
		}
	}
}

func TestParseHelmChartUpgrade_RejectsNonJSON(t *testing.T) {
	if _, err := ParseHelmChartUpgrade("not json", ScanAccount{}); err == nil {
		t.Fatal("expected error on non-JSON input")
	}
}

func TestParseHelmChartUpgrade_SkipsRowsMissingRelease(t *testing.T) {
	stdout := `[{"chartName":"orphan","outdated":true},{"release":"x","chartName":"y","namespace":"z","Installed":{"version":"1"},"outdated":true}]`
	recs, err := ParseHelmChartUpgrade(stdout, ScanAccount{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Errorf("recs = %d; want 1 (orphan dropped)", len(recs))
	}
}

func TestFormatHelmObjectID(t *testing.T) {
	r := map[string]any{
		"release":   "loki",
		"chartName": "loki",
		"namespace": "logging",
		"Installed": map[string]any{"version": "5.0.0"},
	}
	if got := formatHelmObjectID(r); got != "loki-loki-logging-5.0.0" {
		t.Errorf("got %q", got)
	}
}
