package scan_orchestrator

import (
	"encoding/json"
	"fmt"
)

// Phase 2a stub parsers. Each one packages the raw scanner stdout into a
// single Recommendation row keyed by the rule_name. Real Go ports of the
// per-scanner parsers ship in Phase 2b PRs (one per scanner) using fixtures
// captured from a real Phase 2a run against dev — same pattern as popeye:
// the schemas Robusta documented are stale enough that empirical validation
// matters more than translating the Python verbatim.
//
// The stub format keeps the cron path unblocked: the orchestrator can still
// schedule + poll + fetch + UPSERT against the recommendation table for all
// four scanners while the real parsers land separately. UI surfaces show a
// single "raw report attached" row per scanner rather than per-issue rows;
// that's a temporary regression we accept in dev/canary while parsers ship.

const (
	TrivyCISRuleName         = "trivy_cis_scan"
	KubeBenchRuleName        = "cis"
	ImageScanRuleName        = "image_scan"
	HelmChartUpgradeRuleName = "helm_chart_upgrade"
)

// stubParser builds a one-row Recommendation that wraps the raw stdout. The
// recommendation column holds {"raw": "<stdout>", "stub": true, "scanner": "<name>"}
// so a downstream consumer can both display "report attached" and tell the
// stub apart from a real parser output.
func stubParser(scannerName, ruleName, category string) func(stdout string, account ScanAccount) ([]Recommendation, error) {
	return func(stdout string, account ScanAccount) ([]Recommendation, error) {
		payload, err := json.Marshal(map[string]any{
			"raw":     stdout,
			"stub":    true,
			"scanner": scannerName,
		})
		if err != nil {
			return nil, fmt.Errorf("%s: encode stub payload: %w", scannerName, err)
		}
		return []Recommendation{{
			CloudAccountID:       account.AccountID,
			TenantID:             account.TenantID,
			Category:             category,
			RuleName:             ruleName,
			RecommendationAction: "Modify",
			Recommendation:       string(payload),
			Severity:             "Info",
			Status:               "Open",
			AccountObjectID:      fmt.Sprintf("%s/raw-report", scannerName),
		}}, nil
	}
}

// ParseTrivyCIS — stub. Real port: ports collector handle_trivy_cis_scan
// (event_handler.py:2332-2363) walking Results[].Results[] vulnerabilities,
// keyed on `<ID>-<Class>-<Target>`, severity from Results[].Severity.
var ParseTrivyCIS = stubParser("trivy_cis_scan", TrivyCISRuleName, "Security")

// ParseKubeBench — stub. Real port: ports collector handle_kube_bench
// (event_handler.py:1993-2048) walking Controls[].tests[].results[] using
// test_number+test_desc as the object key.
var ParseKubeBench = stubParser("kube_bench_scan", KubeBenchRuleName, "Security")

// ParseImageScan — stub. Real port: ports collector handle_image_scan
// (event_handler.py:2119-2197) walking Results[].Vulnerabilities[] keyed on
// VulnerabilityID, severity from Vulnerabilities[].Severity.
var ParseImageScan = stubParser("image_scanner", ImageScanRuleName, "Security")

// ParseHelmChartUpgrade — stub. Real port: ports collector
// handle_helm_chart_upgrade_report (event_handler.py:2287-2331). Nova's `find`
// output is a flat array of charts with name/installed/latest/release.
var ParseHelmChartUpgrade = stubParser("helm_chart_upgrade", HelmChartUpgradeRuleName, "InfraUpgrade")
