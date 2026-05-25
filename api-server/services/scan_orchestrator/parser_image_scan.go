package scan_orchestrator

import (
	"encoding/json"
	"fmt"
)

// Trivy `image --format json --quiet <image>` shape:
//
//	{
//	  "SchemaVersion": 2,
//	  "ArtifactName": "registry.example.com/foo:tag",
//	  "ArtifactType": "container_image",
//	  "Results": [
//	    {
//	      "Target": "registry.example.com/foo:tag (alpine 3.18.4)",
//	      "Class":  "os-pkgs",
//	      "Type":   "alpine",
//	      "Vulnerabilities": [
//	        {
//	          "VulnerabilityID": "CVE-2024-…",
//	          "PkgID":           "openssl@3.1.4-r0",
//	          "PkgName":         "openssl",
//	          "InstalledVersion": "3.1.4-r0",
//	          "FixedVersion":     "3.1.4-r1",
//	          "Severity":         "HIGH",
//	          "Title":            "…",
//	          "Description":      "…",
//	          ...
//	        },
//	        ...
//	      ]
//	    },
//	    ...
//	  ]
//	}
//
// Mirrors the collector's parse_image_scan (event_handler.py:2144-2170):
// one row per vulnerability; rule_name = image_scan; severity translated
// from Trivy's UPPER_CASE; account_object_id keyed on
// "<image>-<PkgID>-<VulnerabilityID>" so existing rows UPSERT in place
// after the per-image migration.

type imageScanReport struct {
	ArtifactName string             `json:"ArtifactName"`
	Results      []imageScanResults `json:"Results"`
}

type imageScanResults struct {
	Target          string           `json:"Target"`
	Class           string           `json:"Class"`
	Type            string           `json:"Type"`
	Vulnerabilities []map[string]any `json:"Vulnerabilities"`
}

// ParseImageScan turns Trivy image-scan stdout into Recommendation rows.
//
// One row per vulnerability. Vulnerability dict is enriched with `image_name`
// so the UI's detail surface has the image inline (collector did the same).
//
// The caller's accountObjectID format (`image_name-pkgID-vulnID`) lets the
// existing UPSERT path collapse same-vuln-different-scan into one row, which
// matches what the collector wrote. Without `image_name` in the key two
// different images sharing the same CVE on the same package would collapse
// into a single recommendation — wrong.
//
// Variable assignment (replaces the parser_stubs.go stub).
var ParseImageScan = parseImageScanImpl

func parseImageScanImpl(stdout string, account ScanAccount) ([]Recommendation, error) {
	jsonStart := stripPreamble(stdout)
	if jsonStart == "" {
		return nil, fmt.Errorf("image_scan: stdout has no JSON object (size=%d, first %d bytes: %q)",
			len(stdout), sampleByteCap, truncateUTF8Safe(stdout, sampleByteCap))
	}
	var report imageScanReport
	if err := json.Unmarshal([]byte(jsonStart), &report); err != nil {
		return nil, fmt.Errorf("image_scan: parse json: %w", err)
	}
	out := make([]Recommendation, 0)
	image := report.ArtifactName
	for _, result := range report.Results {
		for _, v := range result.Vulnerabilities {
			if v == nil {
				continue
			}
			vulnID, okID := v["VulnerabilityID"].(string)
			pkgID, _ := v["PkgID"].(string)
			sev, _ := v["Severity"].(string)
			if !okID || vulnID == "" {
				// Some Trivy entries have just PkgName + InstalledVersion with no
				// CVE assignment yet. Skip — keying on ("", pkgID) collapses all
				// such entries into one row per package and silently hides them.
				continue
			}
			v["image_name"] = image

			body, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("image_scan: encode recommendation: %w", err)
			}
			out = append(out, Recommendation{
				CloudAccountID:       account.AccountID,
				TenantID:             account.TenantID,
				Category:             "Security",
				RuleName:             ImageScanRuleName,
				RecommendationAction: "Modify",
				Recommendation:       string(body),
				Severity:             trivySeverity(sev),
				Status:               "Open",
				AccountObjectID:      formatImageScanObjectID(image, pkgID, vulnID),
			})
		}
	}
	return out, nil
}

// formatImageScanObjectID matches the collector's get_vulnerability_id
// (event_handler.py:2173) — "<image_name>-<PkgID>-<VulnerabilityID>" so
// existing rows UPSERT in place after the migration.
func formatImageScanObjectID(image, pkgID, vulnID string) string {
	return fmt.Sprintf("%s-%s-%s", image, pkgID, vulnID)
}
