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
		if !strings.Contains(got, "Collected Logs") {
			t.Errorf("expected Collected Logs section, got %q", got)
		}
		if !strings.Contains(got, "20.89.250.108") || !strings.Contains(got, "RATE_BASED_BAN") {
			t.Errorf("expected error log lines in output, got %q", got)
		}
	})

	t.Run("LogData preferred over ErrorLogData", func(t *testing.T) {
		ev := events.InvestigateData{
			LogData:      "full log body here",
			ErrorLogData: []string{"error line"},
		}
		got := buildInvestigationEvidenceContext(ev)
		if !strings.Contains(got, "full log body here") {
			t.Errorf("expected LogData in output, got %q", got)
		}
		if strings.Contains(got, "error line") {
			t.Errorf("did not expect ErrorLogData when LogData present, got %q", got)
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
