package api

import (
	"strings"
	"testing"

	"nudgebee/llm/events"
)

func TestBuildInvestigationEvidenceContext(t *testing.T) {
	t.Run("empty evidence returns empty string", func(t *testing.T) {
		if got := buildInvestigationEvidenceContext(events.InvestigateData{}); got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("error log data is surfaced when LogData empty", func(t *testing.T) {
		ev := events.InvestigateData{
			ErrorLogData: []string{"429 from 20.89.250.108", "enforcedSecurityPolicy: RATE_BASED_BAN"},
		}
		got := buildInvestigationEvidenceContext(ev)
		if !strings.Contains(got, "Error Log Lines") {
			t.Errorf("expected Error Log Lines section, got %q", got)
		}
		if !strings.Contains(got, "20.89.250.108") || !strings.Contains(got, "RATE_BASED_BAN") {
			t.Errorf("expected error log lines in output, got %q", got)
		}
	})

	t.Run("error lines and raw logs both surfaced", func(t *testing.T) {
		ev := events.InvestigateData{
			LogData:      "full log body here",
			ErrorLogData: []string{"error line"},
		}
		got := buildInvestigationEvidenceContext(ev)
		if !strings.Contains(got, "full log body here") {
			t.Errorf("expected LogData in output, got %q", got)
		}
		if !strings.Contains(got, "error line") {
			t.Errorf("expected ErrorLogData section alongside LogData, got %q", got)
		}
	})

	// Regression for event 15d3e867 (wrong RCA on "High HTTP 504 for app-dev"):
	// the raw log body used to be rendered first under a single head-truncation
	// cap, so a large body (481KB of access-log noise) consumed the whole
	// budget and the decisive insights ("Deployment occurred 35 minutes before
	// the event") never reached the agent. Insights and the pattern digest must
	// survive regardless of log size, and the oversized raw body must be left
	// out (the caller saves it to the conversation workspace instead).
	t.Run("insights and digest survive a huge log body", func(t *testing.T) {
		ev := events.InvestigateData{
			LogData: strings.Repeat("10.64.6.253 - - GET /_next/static/chunk.js 200\n", 10000),
			Deployment: events.InvestigateDataInsight{
				Insight: []events.Insight{{Message: "Deployment occurred 35 minutes before the event - potential cause"}},
			},
			LogSummary: map[string]any{
				"level_breakdown": map[string]any{"error": float64(65), "info": float64(12)},
				"log_patterns": []any{
					map[string]any{"count": float64(32), "template": "upstream timed out (110: Operation timed out)", "example": "2026/07/27 09:16:48 [error] upstream timed out"},
				},
			},
		}
		got := buildInvestigationEvidenceContext(ev)
		if !strings.Contains(got, "Deployment occurred 35 minutes before the event") {
			t.Fatalf("expected insight to survive large log body, got %q", got[:min(500, len(got))])
		}
		if !strings.Contains(got, "upstream timed out (110: Operation timed out)") {
			t.Errorf("expected log pattern digest in output")
		}
		if !strings.Contains(got, "error=65") {
			t.Errorf("expected level breakdown in output")
		}
		if len(got) > maxInvestigationEvidenceBytes+1024 {
			t.Errorf("expected output near the evidence budget, got %d bytes", len(got))
		}
		if insightsIdx, logsIdx := strings.Index(got, "Collected Insights"), strings.Index(got, "Collected Logs"); logsIdx != -1 && insightsIdx > logsIdx {
			t.Errorf("expected insights before raw logs")
		}
	})

	t.Run("long error lines are middle-truncated", func(t *testing.T) {
		lines := make([]string, 200)
		for i := range lines {
			lines[i] = strings.Repeat("e", 100)
		}
		lines[0] = "FIRST-ERROR"
		lines[len(lines)-1] = "LAST-ERROR"
		got := buildInvestigationEvidenceContext(events.InvestigateData{ErrorLogData: lines})
		if !strings.Contains(got, "FIRST-ERROR") || !strings.Contains(got, "LAST-ERROR") {
			t.Errorf("expected both ends of the error lines to survive middle truncation")
		}
		if !strings.Contains(got, "output truncated") {
			t.Errorf("expected truncation marker for oversized error lines")
		}
	})

	t.Run("insights surfaced", func(t *testing.T) {
		ev := events.InvestigateData{
			MetricsData: []events.InvestigateDataInsight{
				{Insight: []events.Insight{{Message: "CPU saturated at 98%"}}},
			},
			AlertData: events.InvestigateDataInsight{
				Insight: []events.Insight{{Message: "Threshold 650 exceeded"}},
			},
		}
		got := buildInvestigationEvidenceContext(ev)
		if !strings.Contains(got, "Collected Insights") {
			t.Errorf("expected Collected Insights section, got %q", got)
		}
		if !strings.Contains(got, "CPU saturated at 98%") || !strings.Contains(got, "Threshold 650 exceeded") {
			t.Errorf("expected insight messages in output, got %q", got)
		}
	})

	// Regression for event 7425e5de (GCP Cloud Armor 429): the load-balancer
	// access log — source IP, Cloud Armor policy decision — is attached to the
	// event as ErrorLogData but the investigation prompt previously never
	// received it, so the agent reported "Data Assessment: Insufficient" for data
	// the event actually carried. This asserts the evidence now reaches the block
	// that generateEventAnalysisPrompt appends to the investigation prompt.
	t.Run("real cloud-armor 429 log evidence is surfaced", func(t *testing.T) {
		ev := events.InvestigateData{
			ErrorLogData: []string{
				`{"@type":"type.googleapis.com/google.cloud.loadbalancing.type.LoadBalancerLogEntry",` +
					`"enforcedSecurityPolicy":{"configuredAction":"RATE_BASED_BAN","name":"production-cbp-armor-policy",` +
					`"outcome":"DENY","priority":199,"rateLimitAction":{"outcome":"BAN_THRESHOLD_EXCEED"}},` +
					`"remoteIp":"20.89.250.108","securityPolicyRequestData":{"remoteIpInfo":{"asn":8075,"regionCode":"JP"}}}`,
			},
		}
		got := buildInvestigationEvidenceContext(ev)
		for _, want := range []string{"20.89.250.108", "production-cbp-armor-policy", "RATE_BASED_BAN", "BAN_THRESHOLD_EXCEED"} {
			if !strings.Contains(got, want) {
				t.Errorf("expected evidence to contain %q, got %q", want, got)
			}
		}
	})
}

func TestCollectEvidenceInsights(t *testing.T) {
	t.Run("dedupes and skips empty", func(t *testing.T) {
		ev := events.InvestigateData{
			PodMetrics: []events.InvestigateDataInsight{
				{Insight: []events.Insight{{Message: "dup"}, {Message: ""}}},
			},
			NodeMetrics: []events.InvestigateDataInsight{
				{Insight: []events.Insight{{Message: "dup"}, {Message: "unique"}}},
			},
		}
		got := collectEvidenceInsights(ev)
		if len(got) != 2 {
			t.Fatalf("expected 2 unique insights, got %d: %v", len(got), got)
		}
		if got[0] != "dup" || got[1] != "unique" {
			t.Errorf("expected order [dup unique], got %v", got)
		}
	})

	t.Run("no insights returns nil", func(t *testing.T) {
		if got := collectEvidenceInsights(events.InvestigateData{}); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
}

func TestRenderLogPatternDigest(t *testing.T) {
	if got := renderLogPatternDigest(nil); got != "" {
		t.Errorf("expected empty digest for nil summary, got %q", got)
	}
	if got := renderLogPatternDigest("not a map"); got != "" {
		t.Errorf("expected empty digest for non-map summary, got %q", got)
	}
	got := renderLogPatternDigest(map[string]any{
		"log_patterns": []any{
			map[string]any{"count": float64(391), "template": "* GET * 200 *", "example": "10.0.0.1 GET /health 200 12ms"},
			map[string]any{"count": float64(2), "template": ""},
		},
	})
	if !strings.Contains(got, "391× `* GET * 200 *`") {
		t.Errorf("expected rendered pattern with count, got %q", got)
	}
	if !strings.Contains(got, "10.0.0.1 GET /health 200 12ms") {
		t.Errorf("expected example line, got %q", got)
	}
}
