package scan_orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Trivy CIS output (trivy k8s --compliance=k8s-cis-1.23 --report=all --format=json) shape:
//
//	{
//	  "ClusterName": "...",
//	  "Resources": [...],
//	  "Results": [
//	    {
//	      "ID":          "1.1.1",
//	      "Name":        "Ensure that the API server pod ...",
//	      "Description": "...",
//	      "Severity":    "HIGH" | "CRITICAL" | "MEDIUM" | "LOW" | "UNKNOWN",
//	      "Results": [   // per-resource findings; may be null when nothing matched
//	        {
//	          "Target":        "Pod kube-system/kube-apiserver",
//	          "Class":         "config",
//	          "Type":          "kubernetes",
//	          "Misconfigurations": [...]
//	        },
//	        ...
//	      ]
//	    },
//	    ...
//	  ]
//	}
//
// Mirrors the collector's handle_trivy_cis_scan
// (collector-server/k8s-collector/app/handlers/event_handler.py:2332-2363):
// one row per (compliance check, per-resource finding); account_object_id is
// `<ID>-<Class>-<Target>`. The vulnerability dict gets the parent check's
// Id/Name/Description spliced in so the UI surface has them inline.

type trivyCISReport struct {
	Results []trivyCISCheck `json:"Results"`
}

type trivyCISCheck struct {
	ID          string            `json:"ID"`
	Name        string            `json:"Name"`
	Description string            `json:"Description"`
	Severity    string            `json:"Severity"`
	Results     []trivyCISFinding `json:"Results"`
}

// trivyCISFinding is intentionally a raw map so the entire Robusta-shaped
// vulnerability blob (Class/Target/Misconfigurations/etc.) round-trips into
// the recommendation row's JSON. The two fields we care about for keying are
// pulled out via the typed shadow.
type trivyCISFinding map[string]any

// TrivyCISRuleName already declared in parser_stubs.go — keep that one as the
// source of truth so flipping the catalog from stub → real parser doesn't
// require touching ScannerCatalog.

// ParseTrivyCIS turns trivy CIS k8s scan stdout into Recommendation rows.
// One row per (compliance-check, per-resource finding). Checks with null or
// empty nested Results are skipped (no resources matched the check).
func ParseTrivyCIS(stdout string, account ScanAccount) ([]Recommendation, error) {
	jsonStart := stripTrivyPreamble(stdout)
	if jsonStart == "" {
		// Diagnostic: include the first ~500 bytes of stdout so an operator can
		// tell trivy-bailed-out (only ERROR lines) from agent-truncation from
		// empty-stdout. Without this the error is unactionable because the
		// Job's TTLSecondsAfterFinished=600 means logs are gone in 10 min.
		return nil, fmt.Errorf("trivy_cis: stdout has no JSON object (size=%d, first %d bytes: %q)",
			len(stdout), sampleByteCap, truncateUTF8Safe(stdout, sampleByteCap))
	}
	var report trivyCISReport
	if err := json.Unmarshal([]byte(jsonStart), &report); err != nil {
		return nil, fmt.Errorf("trivy_cis: parse json: %w", err)
	}
	out := make([]Recommendation, 0)
	for _, check := range report.Results {
		for _, finding := range check.Results {
			class, _ := finding["Class"].(string)
			target, _ := finding["Target"].(string)
			// Without Class+Target the AccountObjectID collapses to "<checkID>--"
			// for every such finding inside the check, and dedupeByConflictKey
			// in Persist would silently drop all but one. Skip explicitly so the
			// loss is visible in row counts (and we don't pretend the data is OK).
			if class == "" || target == "" {
				continue
			}

			// Splice the parent check's metadata into the finding so the UI
			// row has Id/Name/Description without a join — same shape the
			// collector wrote.
			finding["Id"] = check.ID
			finding["Name"] = check.Name
			finding["Description"] = check.Description

			recommendationJSON, err := json.Marshal(finding)
			if err != nil {
				return nil, fmt.Errorf("trivy_cis: encode recommendation: %w", err)
			}
			out = append(out, Recommendation{
				CloudAccountID:       account.AccountID,
				TenantID:             account.TenantID,
				Category:             "Security",
				RuleName:             TrivyCISRuleName,
				RecommendationAction: "Modify",
				Recommendation:       string(recommendationJSON),
				Severity:             trivySeverity(check.Severity),
				Status:               "Open",
				AccountObjectID:      formatTrivyCISObjectID(check.ID, class, target),
			})
		}
	}
	return out, nil
}

// trivySeverity maps trivy's UPPER_CASE severity to the recommendation_severity
// enum the rest of the system uses. Mirrors get_severity in the collector
// (event_handler.py:349-358).
func trivySeverity(s string) string {
	switch strings.ToUpper(s) {
	case "CRITICAL":
		return "Critical"
	case "HIGH":
		return "High"
	case "MEDIUM":
		return "Medium"
	case "LOW":
		return "Low"
	}
	return "Info"
}

// formatTrivyCISObjectID matches the collector's
// f"{result.ID}-{vulnerability.Class}-{vulnerability.Target}" format so
// existing recommendation rows keyed this way upsert in place after the
// migration.
func formatTrivyCISObjectID(checkID, class, target string) string {
	return fmt.Sprintf("%s-%s-%s", checkID, class, target)
}

// stripTrivyPreamble is an alias for stripPreamble — kept for the explicit
// trivy_cis call site to read self-documenting. trivy writes RBAC errors
// to stdout before the JSON; kube_bench can do the same with warnings.
func stripTrivyPreamble(s string) string { return stripPreamble(s) }
