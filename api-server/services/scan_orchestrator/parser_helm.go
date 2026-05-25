package scan_orchestrator

import (
	"encoding/json"
	"fmt"
)

// Nova `./nova find` output shape:
//
//	[
//	  {
//	    "release":      "actions-runner-controller",
//	    "chartName":    "actions-runner-controller",
//	    "namespace":    "actions-runner-system-1",
//	    "description":  "...",
//	    "Installed":    {"version": "0.23.7", "appVersion": "0.27.6", ...},
//	    "Latest":       {"version": "0.23.7", "appVersion": "0.27.6", ...},
//	    "outdated":     false,
//	    "deprecated":   false,
//	    "helmVersion":  "3",
//	    "overridden":   false,
//	    "repositoryUrl": "..."
//	  }, ...
//	]
//
// Mirrors the collector's handle_helm_chart_upgrade_report
// (collector-server/k8s-collector/app/handlers/event_handler.py:2287-2329):
// one row per outdated release; severity is "Info" (the collector ran
// get_severity on a row.priority field that doesn't exist in the raw Nova
// output — Robusta added it via the playbook wrap, not Nova). Rows with
// outdated=false are skipped entirely.

// ParseHelmChartUpgrade turns Nova `find` stdout into Recommendation rows.
// Only outdated releases get a row; current releases produce nothing.
func ParseHelmChartUpgrade(stdout string, account ScanAccount) ([]Recommendation, error) {
	var releases []map[string]any
	if err := json.Unmarshal([]byte(stdout), &releases); err != nil {
		return nil, fmt.Errorf("helm_chart_upgrade: parse json: %w", err)
	}
	out := make([]Recommendation, 0)
	for _, release := range releases {
		outdated, _ := release["outdated"].(bool)
		if !outdated {
			continue
		}
		objectID := formatHelmObjectID(release)
		if objectID == "" {
			continue
		}
		recommendationJSON, err := json.Marshal(release)
		if err != nil {
			return nil, fmt.Errorf("helm_chart_upgrade: encode recommendation: %w", err)
		}
		out = append(out, Recommendation{
			CloudAccountID:       account.AccountID,
			TenantID:             account.TenantID,
			Category:             "InfraUpgrade",
			RuleName:             HelmChartUpgradeRuleName,
			RecommendationAction: "Modify",
			Recommendation:       string(recommendationJSON),
			// Collector hardcoded severity from the Robusta wrap's priority
			// (which was always 1 for the helm playbook). Info matches the
			// effective behaviour the UI saw.
			Severity:        "Info",
			Status:          "Open",
			AccountObjectID: objectID,
		})
	}
	return out, nil
}

// formatHelmObjectID matches the collector's
// f"{release}-{chartName}-{namespace}-{Installed.version}" key so
// existing recommendation rows for outdated releases UPSERT in place
// after the migration.
func formatHelmObjectID(release map[string]any) string {
	rel, _ := release["release"].(string)
	chart, _ := release["chartName"].(string)
	ns, _ := release["namespace"].(string)
	installed, _ := release["Installed"].(map[string]any)
	version, _ := installed["version"].(string)
	if rel == "" || chart == "" {
		return ""
	}
	return fmt.Sprintf("%s-%s-%s-%s", rel, chart, ns, version)
}
