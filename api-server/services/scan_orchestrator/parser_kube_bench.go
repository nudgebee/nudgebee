package scan_orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// kube-bench --json output shape (v0.10.4):
//
//	{
//	  "Controls": [
//	    {
//	      "id":             "3",
//	      "version":        "...",
//	      "text":           "Worker Node Security Configuration",
//	      "node_type":      "node",
//	      "tests": [
//	        {
//	          "section":  "3.1",
//	          "type":     "...",
//	          "desc":     "...",
//	          "results": [
//	            {
//	              "test_number":      "3.1.1",
//	              "test_desc":        "...",
//	              "audit":            "...",
//	              "remediation":      "...",
//	              "status":           "PASS" | "FAIL" | "WARN" | "INFO",
//	              "actual_value":     "...",
//	              "scored":           true,
//	              "expected_result":  "...",
//	              ...
//	            }, ...
//	          ]
//	        }, ...
//	      ],
//	      "total_pass": ..., "total_fail": ..., "total_warn": ..., "total_info": ...
//	    }, ...
//	  ],
//	  "Totals": {...}
//	}
//
// Mirrors the collector's handle_kube_bench
// (collector-server/k8s-collector/app/handlers/event_handler.py:1993-2018):
// one row per result, account_object_id is the CIS test_number, severity
// always "Info" (the same fixed value the collector wrote).

type kubeBenchReport struct {
	Controls []kubeBenchControl `json:"Controls"`
}

type kubeBenchControl struct {
	Tests []kubeBenchTest `json:"tests"`
}

type kubeBenchTest struct {
	Results []map[string]any `json:"results"`
}

// ParseKubeBench turns kube-bench's stdout JSON into Recommendation rows.
// Walks Controls[].tests[].results[], emits one row per result. Stripped of
// any leading non-JSON lines (kube-bench occasionally writes warnings to
// stdout before the report).
func ParseKubeBench(stdout string, account ScanAccount) ([]Recommendation, error) {
	jsonStart := stripPreamble(stdout)
	if jsonStart == "" {
		return nil, fmt.Errorf("kube_bench: stdout has no JSON object (size=%d, first %d bytes: %q)",
			len(stdout), sampleByteCap, truncateUTF8Safe(stdout, sampleByteCap))
	}
	var report kubeBenchReport
	if err := json.Unmarshal([]byte(jsonStart), &report); err != nil {
		return nil, fmt.Errorf("kube_bench: parse json: %w", err)
	}
	out := make([]Recommendation, 0)
	for _, ctrl := range report.Controls {
		for _, test := range ctrl.Tests {
			for _, result := range test.Results {
				testNumber, _ := result["test_number"].(string)
				if testNumber == "" {
					continue
				}
				recommendationJSON, err := json.Marshal(result)
				if err != nil {
					return nil, fmt.Errorf("kube_bench: encode recommendation: %w", err)
				}
				out = append(out, Recommendation{
					CloudAccountID:       account.AccountID,
					TenantID:             account.TenantID,
					Category:             "Security",
					RuleName:             KubeBenchRuleName,
					RecommendationAction: "Modify",
					Recommendation:       string(recommendationJSON),
					// Collector hardcoded "Info" for every kube-bench row even when
					// the result Status was FAIL — keeping parity so existing UI
					// treatment doesn't shift. A future PR may switch to mapping
					// FAIL/WARN/PASS → severity.
					Severity:        "Info",
					Status:          "Open",
					AccountObjectID: testNumber,
				})
			}
		}
	}
	return out, nil
}

// sampleByteCap is the max bytes of stdout we include in a parse-error
// message. Big enough to show several ERROR lines + their context, small
// enough that one rejected payload doesn't blow up a log line.
const sampleByteCap = 500

// truncateUTF8Safe returns a prefix of s no longer than max bytes, ending
// at a rune boundary so the resulting string is always valid UTF-8. Avoids
// the trap of slicing through a multi-byte character (which produces a
// half-rune that breaks downstream JSON loggers).
func truncateUTF8Safe(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// stripPreamble lifts leading non-JSON text the way stripTrivyPreamble does
// for trivy. Both scanners can leak warnings/errors to stdout before the
// JSON object starts. Kept here so kube_bench doesn't depend on parser_trivy_cis.go.
func stripPreamble(s string) string {
	idx := strings.Index(s, "\n{")
	if idx >= 0 {
		return s[idx+1:]
	}
	if strings.HasPrefix(s, "{") {
		return s
	}
	return ""
}
