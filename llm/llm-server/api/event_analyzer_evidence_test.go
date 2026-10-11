package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/events"
)

func TestBuildInvestigationEvidenceContext(t *testing.T) {
	t.Run("verbose interpretations cannot evict observed errors", func(t *testing.T) {
		ev := events.InvestigateData{
			Deployment:   events.InvestigateDataInsight{Insight: []events.Insight{{Message: strings.Repeat("suspected deployment cause ", 2000)}}},
			LogSummary:   map[string]any{"log_patterns": []any{map[string]any{"count": 1, "template": strings.Repeat("pattern ", 2000)}}},
			ErrorLogData: []string{"FIRST-ERROR", strings.Repeat("error ", 2000), "LAST-ERROR"},
			LogData:      strings.Repeat("raw line\n", 2000),
		}
		got := buildInvestigationEvidenceContext(ev)
		if len(got) > maxInvestigationEvidenceBytes {
			t.Fatalf("evidence exceeds inline budget: %d", len(got))
		}
		for _, want := range []string{"FIRST-ERROR", "LAST-ERROR", "output truncated", "Collected Logs"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q after bounding evidence", want)
			}
		}
	})
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

// The investigation agent has no event-evidence tool, and the Collected Evidence
// prompt section omits metric series and pod data, so the analyzer saves them to
// the workspace itself. Logs are excluded: they are inline or in evidence_logs.
func TestStructuredEvidenceForWorkspace(t *testing.T) {
	t.Run("keeps structured sections uncut and drops logs", func(t *testing.T) {
		series := `{"data":{"memory_usage":{"values":["` + strings.Repeat("2097152000\",\"", 400) + `0"]}}}`
		ev := events.InvestigateData{
			LogData:    strings.Repeat("log line\n", 2000),
			PodMetrics: []events.InvestigateDataInsight{{Data: series}},
		}

		content, ok := structuredEvidenceForWorkspace(ev)
		if !ok {
			t.Fatal("expected large structured evidence to be saved")
		}
		var saved events.InvestigateData
		if err := json.Unmarshal([]byte(content), &saved); err != nil {
			t.Fatalf("saved content is not valid JSON: %v", err)
		}
		if saved.LogData != "" {
			t.Error("LogData must not be duplicated into the structured evidence file")
		}
		if len(saved.PodMetrics) != 1 || saved.PodMetrics[0].Data != series {
			t.Error("pod_metrics must be saved uncut")
		}
		if ev.LogData == "" {
			t.Error("caller's evidence must not be mutated")
		}
	})

	t.Run("small structured evidence is still saved", func(t *testing.T) {
		ev := events.InvestigateData{PodData: events.InvestigateDataInsight{Data: `{"status":{"phase":"Running"}}`}}
		content, ok := structuredEvidenceForWorkspace(ev)
		if !ok || !strings.Contains(content, "Running") {
			t.Errorf("small pod_data has no inline fallback and must be saved, got ok=%v content=%q", ok, content)
		}
	})

	t.Run("evidence that is only logs is not saved", func(t *testing.T) {
		if _, ok := structuredEvidenceForWorkspace(events.InvestigateData{LogData: strings.Repeat("x", 100000)}); ok {
			t.Error("expected no structured file when only logs are present")
		}
	})

	t.Run("note names the file and describes its shape", func(t *testing.T) {
		ev := events.InvestigateData{
			LogData: "not in the file",
			PodData: events.InvestigateDataInsight{Data: `{"status":{"phase":"Running"}}`},
		}
		note := structuredEvidenceNote("event_abc_evidence.json", ev)
		if !strings.Contains(note, "`event_abc_evidence.json`") {
			t.Errorf("note must point at the file, got %q", note)
		}
		if !strings.Contains(note, "Top-level keys with data: pod_data (") || strings.Contains(note, "log_data (") {
			t.Errorf("note must list the sections the saved file holds, without logs, got %q", note)
		}
		if strings.Contains(note, "jq 'keys'") {
			t.Errorf("the shape replaces exploring the keys, got %q", note)
		}
	})
}

func TestResolveInvestigationWindow(t *testing.T) {
	at := func(h, m int) *time.Time {
		v := time.Date(2026, 9, 13, h, m, 0, 0, time.UTC)
		return &v
	}
	tests := []struct {
		name       string
		event      events.Event
		chainFirst *time.Time
		chainLast  *time.Time
		wantStart  time.Time
		wantEnd    *time.Time // nil = ongoing
	}{
		{
			name:      "closed point event is padded on both sides",
			event:     events.Event{StartsAt: at(9, 39), EndsAt: at(9, 39), Status: "CLOSED"},
			wantStart: *at(9, 24),
			wantEnd:   at(9, 54),
		},
		{
			name:      "firing alarm stamped ends_at=starts_at is open-ended",
			event:     events.Event{StartsAt: at(8, 55), EndsAt: at(8, 55), Status: "FIRING"},
			wantStart: *at(8, 40),
			wantEnd:   nil,
		},
		{
			name:      "real window is kept",
			event:     events.Event{StartsAt: at(8, 0), EndsAt: at(8, 30), Status: "CLOSED"},
			wantStart: *at(8, 0),
			wantEnd:   at(8, 30),
		},
		{
			name:       "earlier chain occurrence moves the start back (re-fire of an earlier episode)",
			event:      events.Event{StartsAt: at(8, 55), EndsAt: at(8, 55), Status: "FIRING"},
			chainFirst: at(7, 38),
			wantStart:  *at(7, 23),
			wantEnd:    nil,
		},
		{
			name:      "later chain occurrence extends the end (termination after state change)",
			event:     events.Event{StartsAt: at(9, 39), EndsAt: at(9, 39), Status: "CLOSED"},
			chainLast: at(10, 5),
			wantStart: *at(9, 24),
			wantEnd:   at(10, 5),
		},
		{
			name:      "missing end on a closed event is treated as zero width",
			event:     events.Event{StartsAt: at(9, 0), Status: "CLOSED"},
			wantStart: *at(8, 45),
			wantEnd:   at(9, 15),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveInvestigationWindow(tc.event, tc.chainFirst, tc.chainLast)
			if !ok {
				t.Fatal("expected a window")
			}
			if !got.Start.Equal(tc.wantStart) {
				t.Errorf("start = %v, want %v", got.Start, tc.wantStart)
			}
			switch {
			case tc.wantEnd == nil && got.End != nil:
				t.Errorf("end = %v, want ongoing", *got.End)
			case tc.wantEnd != nil && (got.End == nil || !got.End.Equal(*tc.wantEnd)):
				t.Errorf("end = %v, want %v", got.End, *tc.wantEnd)
			}
			if tc.wantEnd == nil && got.endString() != "ongoing" {
				t.Errorf("endString = %q, want ongoing", got.endString())
			}
		})
	}

	if _, ok := resolveInvestigationWindow(events.Event{}, nil, nil); ok {
		t.Error("an event without a start time has no window")
	}
}
