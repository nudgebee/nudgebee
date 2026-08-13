package scan_orchestrator

import (
	"encoding/json"
	"fmt"
	"sort"

	"nudgebee/services/internal/database/models"
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
// One row per vulnerability. recommendation.recommendation stores only
// {"image_name": ...} — the CVE+package data Trivy reported lives on the
// linked vulnerabilities row instead (see buildImageScanVulnerabilityRow).
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
	// The fs-scan path runs `trivy fs /`, so report.ArtifactName is "/" rather than
	// the image ref. The orchestrator carries the real image on account.TargetImage;
	// prefer it. (Falls back to ArtifactName for the legacy `trivy image` shape and
	// for fixtures that embed the image in ArtifactName.)
	image := report.ArtifactName
	if account.TargetImage != "" {
		image = account.TargetImage
	}
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
			vuln := buildImageScanVulnerabilityRow(v, result.Type, vulnID, sev)

			// recommendation.recommendation carries only image_name (the ONE
			// row-level field — which image this finding is about, not
			// CVE+package-level), FixedVersion, and InstalledVersion (the
			// version on THIS image — not a property of the vulnerability, so
			// it's kept here rather than on the shared vulnerabilities row).
			// Everything else Trivy reported now lives on the linked
			// vulnerabilities row (columns + Details), reachable via
			// VulnerabilityId; readers get the full legacy shape back via the
			// vulnerabilities join at query time (see metadata.go's
			// recommendations_v2/recommendation_security_v2).
			body, err := json.Marshal(map[string]string{
				"image_name":       image,
				"FixedVersion":     stringField(v, "FixedVersion"),
				"InstalledVersion": stringField(v, "InstalledVersion"),
			})
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
				Vulnerability:        vuln,
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

// buildImageScanVulnerabilityRow flattens one Trivy vulnerability dict into
// the shared vulnerabilities table's shape, reading fields out of v — it
// never writes to v. Returns nil when PkgName is missing (rare — the
// vulnerabilities table's natural key requires it) — the caller still writes
// the recommendation row, just without a vulnerability_id link. Unlike the
// old byte-identical Phase 1 design, InstalledVersion is NOT required here:
// it's the version installed on this one image, not a property of the
// vulnerability (the same CVE+package can show up at many installed
// versions across different images), so it's never part of this row —
// it stays in recommendation.recommendation instead.
func buildImageScanVulnerabilityRow(v map[string]any, pkgType, vulnID, severity string) *models.Vulnerability {
	pkgName, _ := v["PkgName"].(string)
	if pkgName == "" {
		return nil
	}

	row := &models.Vulnerability{
		Source:       models.VulnerabilitySourceImageScan,
		VulnId:       vulnID,
		PackageName:  pkgName,
		PackageType:  imageScanNonEmptyPtr(pkgType),
		FixedVersion: imageScanNonEmptyPtr(stringField(v, "FixedVersion")),
		DataSource:   imageScanDataSource(v),
		Details:      models.NewJsonObject(imageScanVulnerabilityDetails(v)),
	}
	if severity != "" {
		mapped := trivySeverity(severity)
		row.Severity = &mapped
	}
	if desc := stringField(v, "Description"); desc != "" {
		row.Description = &desc
	} else if title := stringField(v, "Title"); title != "" {
		row.Description = &title
	}
	if score, vector, ok := imageScanBestCVSS(v); ok {
		row.CVSSScore = &score
		row.CVSSVector = imageScanNonEmptyPtr(vector)
	}
	return row
}

// imageScanBestCVSS picks a CVSS v3 score+vector from Trivy's per-source
// CVSS map. Fully general (unlike the one-time SQL backfill's 3-tier
// nvd/redhat/ghsa fallback): prefer "nvd", else the lexicographically-first
// remaining source key, so every source Trivy ever adds is covered and this
// self-heals on the next scan without a code change.
func imageScanBestCVSS(v map[string]any) (score float64, vector string, ok bool) {
	cvss, _ := v["CVSS"].(map[string]any)
	if len(cvss) == 0 {
		return 0, "", false
	}
	v3Score := func(source string) (float64, string, bool) {
		entry, _ := cvss[source].(map[string]any)
		if entry == nil {
			return 0, "", false
		}
		s, hasScore := entry["V3Score"].(float64)
		if !hasScore {
			return 0, "", false
		}
		vec, _ := entry["V3Vector"].(string)
		return s, vec, true
	}
	// "nvd" wins if it actually carries a V3Score. If it's present but only
	// has e.g. a V2Score, fall through to the other sources instead of
	// giving up — a source existing isn't the same as it having what we need.
	if s, vec, ok := v3Score("nvd"); ok {
		return s, vec, true
	}
	keys := make([]string, 0, len(cvss))
	for k := range cvss {
		if k != "nvd" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, vec, ok := v3Score(k); ok {
			return s, vec, true
		}
	}
	return 0, "", false
}

// imageScanDataSource extracts DataSource.ID (falling back to .Name) from
// Trivy's vulnerability dict, matching the migration backfill's precedence.
func imageScanDataSource(v map[string]any) *string {
	ds, _ := v["DataSource"].(map[string]any)
	if ds == nil {
		return nil
	}
	if id := stringField(ds, "ID"); id != "" {
		return &id
	}
	if name := stringField(ds, "Name"); name != "" {
		return &name
	}
	return nil
}

func stringField(v map[string]any, key string) string {
	s, _ := v[key].(string)
	return s
}

func imageScanNonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// imageScanVulnerabilityDetails mirrors the migration backfill's jsonb_build_object
// for image_scan rows — everything not promoted to its own column.
func imageScanVulnerabilityDetails(v map[string]any) map[string]any {
	return map[string]any{
		"title":              v["Title"],
		"cwe_ids":            v["CweIDs"],
		"vendor_ids":         v["VendorIDs"],
		"references":         v["References"],
		"primary_url":        v["PrimaryURL"],
		"data_source":        v["DataSource"],
		"cvss":               v["CVSS"],
		"published_date":     v["PublishedDate"],
		"last_modified_date": v["LastModifiedDate"],
		"status":             v["Status"],
		"severity_source":    v["SeveritySource"],
		"vendor_severity":    v["VendorSeverity"],
		"pkg_identifier":     v["PkgIdentifier"],
		"pkg_id":             v["PkgID"],
		"layer":              v["Layer"],
	}
}
