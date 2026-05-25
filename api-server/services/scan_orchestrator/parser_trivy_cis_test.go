package scan_orchestrator

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// trivyCISFixture mirrors the real shape captured from a `trivy k8s
// --compliance=k8s-cis-1.23 --report=all --format=json` run against dev.
// Top-level has compliance metadata + Results[] (the compliance checks);
// each check has its own ID/Name/Severity + nested Results[] (per-resource
// findings). Some checks resolve to no findings (Results: null) and must be
// skipped — represented here by check 1.2.3.
const trivyCISFixture = `{
  "ID": "k8s-cis-1.23",
  "Title": "Kubernetes CIS Benchmark v1.23",
  "Description": "...",
  "Version": "1.23",
  "Results": [
    {
      "ID": "5.1.1",
      "Name": "Ensure that the cluster-admin role is only used where required",
      "Description": "...",
      "Severity": "HIGH",
      "Results": [
        {
          "Target": "ClusterRoleBinding/cluster-admin",
          "Class": "config",
          "Type": "kubernetes",
          "Misconfigurations": [
            {"ID": "KCV0070", "Severity": "HIGH", "Status": "FAIL", "Title": "..."}
          ]
        },
        {
          "Target": "ClusterRoleBinding/edit",
          "Class": "config",
          "Type": "kubernetes",
          "Misconfigurations": [
            {"ID": "KCV0071", "Severity": "HIGH", "Status": "FAIL", "Title": "..."}
          ]
        }
      ]
    },
    {
      "ID": "1.2.3",
      "Name": "Ensure that the --insecure-port argument is set to 0",
      "Severity": "CRITICAL",
      "Results": null
    },
    {
      "ID": "5.2.5",
      "Name": "Minimize the admission of containers with allowPrivilegeEscalation",
      "Severity": "MEDIUM",
      "Results": [
        {
          "Target": "Pod kube-system/kube-proxy-abc",
          "Class": "config",
          "Misconfigurations": [
            {"ID": "KSV001", "Severity": "MEDIUM", "Status": "FAIL"}
          ]
        }
      ]
    }
  ]
}`

func TestParseTrivyCIS_RealSchema(t *testing.T) {
	account := ScanAccount{AccountID: "acc-1", TenantID: "tenant-1"}
	recs, err := ParseTrivyCIS(trivyCISFixture, account)
	if err != nil {
		t.Fatalf("ParseTrivyCIS: %v", err)
	}
	// Expected: 2 findings under 5.1.1, 0 under 1.2.3 (Results: null is skipped),
	// 1 under 5.2.5 = 3 rows.
	if len(recs) != 3 {
		t.Fatalf("recs = %d; want 3", len(recs))
	}

	sort.Slice(recs, func(i, j int) bool { return recs[i].AccountObjectID < recs[j].AccountObjectID })

	want := []struct {
		objectID string
		severity string
	}{
		{"5.1.1-config-ClusterRoleBinding/cluster-admin", "High"},
		{"5.1.1-config-ClusterRoleBinding/edit", "High"},
		{"5.2.5-config-Pod kube-system/kube-proxy-abc", "Medium"},
	}
	for i, w := range want {
		if recs[i].AccountObjectID != w.objectID {
			t.Errorf("rec[%d].AccountObjectID = %q; want %q", i, recs[i].AccountObjectID, w.objectID)
		}
		if recs[i].Severity != w.severity {
			t.Errorf("rec[%d].Severity = %q; want %q", i, recs[i].Severity, w.severity)
		}
		if recs[i].Category != "Security" {
			t.Errorf("rec[%d].Category = %q", i, recs[i].Category)
		}
		if recs[i].RuleName != TrivyCISRuleName {
			t.Errorf("rec[%d].RuleName = %q", i, recs[i].RuleName)
		}
	}

	// Sanity: parent check's Id/Name/Description must be spliced into the
	// recommendation JSON so the UI surface has them inline (matches the
	// collector's behaviour at event_handler.py:2343-2345).
	var finding map[string]any
	if err := json.Unmarshal([]byte(recs[0].Recommendation), &finding); err != nil {
		t.Fatalf("recommendation not valid JSON: %v", err)
	}
	if finding["Id"] != "5.1.1" {
		t.Errorf("finding.Id = %v; want 5.1.1", finding["Id"])
	}
	if name, _ := finding["Name"].(string); !strings.Contains(name, "cluster-admin") {
		t.Errorf("finding.Name = %v", finding["Name"])
	}
}

func TestParseTrivyCIS_StripsTrivyPreamble(t *testing.T) {
	// Trivy emits ERROR log lines to stdout (not stderr) before the JSON. We
	// have to skip them or json.Unmarshal chokes on "Extra data".
	stdout := `2026-05-09T05:13:12Z	ERROR	Unable to list resources	error="resourcequotas is forbidden"
2026-05-09T05:13:12Z	ERROR	Unable to list resources	error="limitranges is forbidden"
` + trivyCISFixture
	recs, err := ParseTrivyCIS(stdout, ScanAccount{AccountID: "a", TenantID: "t"})
	if err != nil {
		t.Fatalf("ParseTrivyCIS with preamble: %v", err)
	}
	if len(recs) != 3 {
		t.Errorf("recs = %d; want 3", len(recs))
	}
}

func TestParseTrivyCIS_SkipsFindingsMissingIdentity(t *testing.T) {
	// A finding without Class or Target would generate "<checkID>--" as its
	// AccountObjectID; dedupeByConflictKey in Persist would then collapse all
	// such findings to one row silently. Parser should skip them up-front.
	stdout := `{"Results":[{"ID":"5.1.1","Name":"x","Severity":"HIGH","Results":[
		{"Class":"","Target":""},
		{"Target":"only-target"},
		{"Class":"only-class"},
		{"Class":"config","Target":"valid"}
	]}]}`
	recs, err := ParseTrivyCIS(stdout, ScanAccount{AccountID: "a", TenantID: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Errorf("recs = %d; want 1 (3 findings dropped for missing Class/Target)", len(recs))
	}
	if recs[0].AccountObjectID != "5.1.1-config-valid" {
		t.Errorf("rec[0].AccountObjectID = %q", recs[0].AccountObjectID)
	}
}

func TestParseTrivyCIS_RejectsNonJSON(t *testing.T) {
	if _, err := ParseTrivyCIS("only error logs, no json", ScanAccount{}); err == nil {
		t.Fatal("expected error when stdout has no JSON")
	}
}

func TestTrivySeverity(t *testing.T) {
	cases := map[string]string{
		"CRITICAL": "Critical",
		"HIGH":     "High",
		"MEDIUM":   "Medium",
		"LOW":      "Low",
		"UNKNOWN":  "Info",
		"":         "Info",
	}
	for in, want := range cases {
		if got := trivySeverity(in); got != want {
			t.Errorf("trivySeverity(%q) = %q; want %q", in, got, want)
		}
	}
}
