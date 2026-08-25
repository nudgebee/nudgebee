package scan_orchestrator

import (
	"encoding/json"
	"reflect"
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
		sev              string
		fixedVersion     string
		installedVersion string
	}{
		"registry.dev.example.com/foo:v1-openssl@3.1.4-r0-CVE-2024-9999":      {sev: "High", fixedVersion: "3.1.4-r1", installedVersion: "3.1.4-r0"},
		"registry.dev.example.com/foo:v1-musl@1.2.4-r2-CVE-2024-7777":         {sev: "Medium"},
		"registry.dev.example.com/foo:v1-log4j-core-2.17.0.jar-CVE-2024-0001": {sev: "Critical"},
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
		// recommendation.recommendation now carries only image_name,
		// FixedVersion, and InstalledVersion — every other field
		// (VulnerabilityID, PkgID, Severity, ...) moved to the linked
		// vulnerabilities row, asserted by
		// TestParseImageScan_BuildsVulnerabilityRow below.
		var body map[string]any
		if err := json.Unmarshal([]byte(r.Recommendation), &body); err != nil {
			t.Errorf("[%s] body not JSON: %v", r.AccountObjectID, err)
			continue
		}
		wantBody := map[string]any{
			"image_name":       "registry.dev.example.com/foo:v1",
			"FixedVersion":     want.fixedVersion,
			"InstalledVersion": want.installedVersion,
		}
		if !reflect.DeepEqual(wantBody, body) {
			t.Errorf("[%s] body = %v; want %v", r.AccountObjectID, body, wantBody)
		}
	}
}

// TestParseImageScan_BuildsVulnerabilityRow pins two things: the openssl and
// musl entries (both have PkgName) get a populated Vulnerability without a
// PackageVersion field — installed version isn't part of the vulnerabilities
// row (see buildImageScanVulnerabilityRow), so musl gets one even without an
// InstalledVersion — while log4j (missing PkgName entirely) gets
// Vulnerability == nil rather than a row with an empty natural-key column.
func TestParseImageScan_BuildsVulnerabilityRow(t *testing.T) {
	account := ScanAccount{AccountID: "acc-1", TenantID: "tenant-1"}
	recs, err := ParseImageScan(imageScanFixture, account)
	if err != nil {
		t.Fatal(err)
	}

	byObjectID := make(map[string]Recommendation, len(recs))
	for _, r := range recs {
		byObjectID[r.AccountObjectID] = r
	}

	openssl := byObjectID["registry.dev.example.com/foo:v1-openssl@3.1.4-r0-CVE-2024-9999"]
	if openssl.Vulnerability == nil {
		t.Fatal("expected a Vulnerability row for openssl")
	}
	v := openssl.Vulnerability
	if v.Source != "image_scan" {
		t.Errorf("source = %q; want image_scan", v.Source)
	}
	if v.VulnId != "CVE-2024-9999" || v.PackageName != "openssl" {
		t.Errorf("unexpected identity: %+v", v)
	}
	if v.PackageType == nil || *v.PackageType != "alpine" {
		t.Errorf("package_type = %v; want alpine", v.PackageType)
	}
	if v.FixedVersion == nil || *v.FixedVersion != "3.1.4-r1" {
		t.Errorf("fixed_version = %v; want 3.1.4-r1", v.FixedVersion)
	}
	if v.Severity == nil || *v.Severity != "High" {
		t.Errorf("severity = %v; want High", v.Severity)
	}
	if v.Description == nil || *v.Description != "openssl: privilege escalation" {
		t.Errorf("description = %v; want the Title fallback", v.Description)
	}

	musl := byObjectID["registry.dev.example.com/foo:v1-musl@1.2.4-r2-CVE-2024-7777"]
	if musl.Vulnerability == nil {
		t.Fatal("expected a Vulnerability row for musl even without InstalledVersion")
	}
	if musl.Vulnerability.PackageName != "musl" {
		t.Errorf("musl package_name = %q; want musl", musl.Vulnerability.PackageName)
	}

	log4j := byObjectID["registry.dev.example.com/foo:v1-log4j-core-2.17.0.jar-CVE-2024-0001"]
	if log4j.Vulnerability != nil {
		t.Errorf("expected nil Vulnerability for log4j (no PkgName), got %+v", log4j.Vulnerability)
	}
}

// TestImageScanBestCVSS_PrefersNVDThenLexicographicFallback pins the fully
// general fallback (unlike the migration backfill's 3-tier nvd/redhat/ghsa):
// "nvd" wins when present, otherwise the lexicographically-first remaining
// source key — so a source Trivy adds later still gets picked up.
func TestImageScanBestCVSS_PrefersNVDThenLexicographicFallback(t *testing.T) {
	v := map[string]any{
		"CVSS": map[string]any{
			"redhat": map[string]any{"V3Score": 5.5, "V3Vector": "redhat-vector"},
			"nvd":    map[string]any{"V3Score": 7.5, "V3Vector": "nvd-vector"},
		},
	}
	score, vector, ok := imageScanBestCVSS(v)
	if !ok || score != 7.5 || vector != "nvd-vector" {
		t.Errorf("got (%v, %q, %v); want (7.5, \"nvd-vector\", true)", score, vector, ok)
	}

	v = map[string]any{
		"CVSS": map[string]any{
			"redhat": map[string]any{"V3Score": 5.5, "V3Vector": "redhat-vector"},
			"ghsa":   map[string]any{"V3Score": 6.5, "V3Vector": "ghsa-vector"},
		},
	}
	score, vector, ok = imageScanBestCVSS(v)
	if !ok || score != 6.5 || vector != "ghsa-vector" {
		t.Errorf("got (%v, %q, %v); want (6.5, \"ghsa-vector\", true) — ghsa < redhat lexicographically", score, vector, ok)
	}

	if _, _, ok := imageScanBestCVSS(map[string]any{}); ok {
		t.Error("expected ok=false when CVSS is absent")
	}

	// nvd present but without a V3Score (e.g. only a V2Score) must not short
	// -circuit the fallback — a source existing isn't the same as it having
	// what we need.
	v = map[string]any{
		"CVSS": map[string]any{
			"nvd":    map[string]any{"V2Score": 4.0},
			"redhat": map[string]any{"V3Score": 5.5, "V3Vector": "redhat-vector"},
		},
	}
	score, vector, ok = imageScanBestCVSS(v)
	if !ok || score != 5.5 || vector != "redhat-vector" {
		t.Errorf("got (%v, %q, %v); want (5.5, \"redhat-vector\", true) — nvd lacks V3Score, must fall through", score, vector, ok)
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
