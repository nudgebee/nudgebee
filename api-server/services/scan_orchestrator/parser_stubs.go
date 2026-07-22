package scan_orchestrator

// Rule names match the literals the collector wrote and the UI queries on
// (collector-server/k8s-collector/app/handlers/event_handler.py: RuleName enum
// + rule_name string assignments). Changing any of these is a UI-visible break.
//
// Each scanner's real Go parser lives in its own parser_*.go file. This file
// once also held a `stubParser` helper used while parsers were ported one at a
// time; with image_scan now landing real, all four scanners use their proper
// parser_*.go implementation and the stub is gone.
const (
	TrivyCISRuleName         = "k8s-cis-1.23"       // collector RuleName.TRIVY_CIS_SCAN
	KubeBenchRuleName        = "CIS"                // collector RuleName.CIS — uppercase!
	ImageScanRuleName        = "image_scan"         // collector RuleName.IMAGE_SCAN
	HelmChartUpgradeRuleName = "helm_chart_upgrade" // collector RuleName.HELM_CHART_UPGRADE

	// ImageScanSummaryRuleName is the per-image scan-bookkeeping row (one row per
	// scanned image, keyed on the image name) written alongside the per-CVE
	// image_scan rows. A DISTINCT rule_name keeps it out of every existing
	// image_scan CVE query; see image_scan_summary.go for what it unlocks.
	ImageScanSummaryRuleName = "image_scan_summary"
)
