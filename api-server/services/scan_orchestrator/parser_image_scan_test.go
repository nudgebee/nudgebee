package scan_orchestrator

import (
	"encoding/json"
	"strings"
	"testing"
)

// Minimal Trivy `image --format json` fixture covering the keys ParseImageScan
// reads: ArtifactName at top, Results[].Vulnerabilities[] with PkgID,
// VulnerabilityID, Severity. The shape is reduced to what the parser needs;
// real Trivy stdout adds many more fields per-vulnerability, all of which we
// preserve verbatim in the recommendation body via the round-trip marshal.
const imageScanFixture = `{
  "SchemaVersion": 2,
  "ArtifactName": "registry.dev.example.com/foo:v1",
  "ArtifactType": "container_image",
  "Results": [
    {
      "Target": "registry.dev.example.com/foo:v1 (alpine 3.18.4)",
      "Class": "os-pkgs",
      "Type": "alpine",
      "Vulnerabilities": [
        {
          "VulnerabilityID": "CVE-2024-9999",
          "PkgID": "openssl@3.1.4-r0",
          "PkgName": "openssl",
          "InstalledVersion": "3.1.4-r0",
          "FixedVersion": "3.1.4-r1",
          "Severity": "HIGH",
          "Title": "openssl: privilege escalation"
        },
        {
          "VulnerabilityID": "CVE-2024-7777",
          "PkgID": "musl@1.2.4-r2",
          "PkgName": "musl",
          "Severity": "MEDIUM"
        }
      ]
    },
    {
      "Target": "Java",
      "Class": "lang-pkgs",
      "Type": "jar",
      "Vulnerabilities": [
        {
          "VulnerabilityID": "CVE-2024-0001",
          "PkgID": "log4j-core-2.17.0.jar",
          "Severity": "CRITICAL"
        }
      ]
    }
  ]
}`

func TestParseImageScan_ProducesRowPerVulnerability(t *testing.T) {
	account := ScanAccount{AccountID: "acc-1", TenantID: "tenant-1"}
	recs, err := ParseImageScan(imageScanFixture, account)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("expected 3 vulnerability rows, got %d", len(recs))
	}

	wantByObjectID := map[string]struct {
		sev  string
		body map[string]any
	}{
		"registry.dev.example.com/foo:v1-openssl@3.1.4-r0-CVE-2024-9999": {
			sev: "High",
			body: map[string]any{
				"VulnerabilityID":  "CVE-2024-9999",
				"PkgID":            "openssl@3.1.4-r0",
				"Severity":         "HIGH",
				"InstalledVersion": "3.1.4-r0",
				"FixedVersion":     "3.1.4-r1",
				"image_name":       "registry.dev.example.com/foo:v1",
			},
		},
		"registry.dev.example.com/foo:v1-musl@1.2.4-r2-CVE-2024-7777": {
			sev:  "Medium",
			body: map[string]any{"VulnerabilityID": "CVE-2024-7777", "Severity": "MEDIUM"},
		},
		"registry.dev.example.com/foo:v1-log4j-core-2.17.0.jar-CVE-2024-0001": {
			sev:  "Critical",
			body: map[string]any{"VulnerabilityID": "CVE-2024-0001", "Severity": "CRITICAL"},
		},
	}

	for _, r := range recs {
		want, ok := wantByObjectID[r.AccountObjectID]
		if !ok {
			t.Errorf("unexpected row keyed on %s", r.AccountObjectID)
			continue
		}
		if r.RuleName != ImageScanRuleName {
			t.Errorf("rule_name = %q; want %q", r.RuleName, ImageScanRuleName)
		}
		if r.Category != "Security" {
			t.Errorf("category = %q; want Security", r.Category)
		}
		if r.Severity != want.sev {
			t.Errorf("[%s] severity = %q; want %q", r.AccountObjectID, r.Severity, want.sev)
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(r.Recommendation), &body); err != nil {
			t.Errorf("[%s] body not JSON: %v", r.AccountObjectID, err)
			continue
		}
		for k, v := range want.body {
			if body[k] != v {
				t.Errorf("[%s] body[%s] = %v; want %v", r.AccountObjectID, k, body[k], v)
			}
		}
	}
}

func TestParseImageScan_SkipsVulnsWithoutID(t *testing.T) {
	// PkgID-only entries (Trivy emits these for some package types) must be
	// dropped — otherwise the account_object_id collapses on ("", pkgID) and
	// the UPSERT silently overwrites neighbours.
	stdout := `{
		"ArtifactName": "img",
		"Results": [{
			"Vulnerabilities": [
				{"VulnerabilityID": "", "PkgID": "p@1", "Severity": "LOW"},
				{"VulnerabilityID": "CVE-x", "PkgID": "p@1", "Severity": "LOW"}
			]
		}]
	}`
	recs, err := ParseImageScan(stdout, ScanAccount{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 row (empty-ID dropped), got %d", len(recs))
	}
	if !strings.HasSuffix(recs[0].AccountObjectID, "-CVE-x") {
		t.Errorf("kept the wrong row: %s", recs[0].AccountObjectID)
	}
}

func TestParseImageScan_RejectsGarbage(t *testing.T) {
	_, err := ParseImageScan("trivy: not authenticated\nplain text\n", ScanAccount{})
	if err == nil {
		t.Fatal("expected error on non-JSON stdout")
	}
}

func TestParseImageScan_StripsPreamble(t *testing.T) {
	// Trivy occasionally prints warnings/errors to stdout before the JSON
	// (e.g. "WARN unable to find image-locking lockfile"). stripPreamble
	// handles that — same as the trivy-cis parser.
	stdout := "WARN trivy: registry token missing\n" + imageScanFixture
	recs, err := ParseImageScan(stdout, ScanAccount{AccountID: "a", TenantID: "t"})
	if err != nil {
		t.Fatalf("ParseImageScan with preamble: %v", err)
	}
	if len(recs) != 3 {
		t.Errorf("expected 3, got %d", len(recs))
	}
}
