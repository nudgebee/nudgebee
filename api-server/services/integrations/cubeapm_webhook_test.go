package integrations

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"nudgebee/services/integrations/core"
)

// cubeAPMSamplePayload is CubeAPM's default (Alertmanager-compatible) webhook
// body, matching the template published in its webhook-alerting docs — including
// the four fields Alertmanager has no equivalent for.
const cubeAPMSamplePayload = `{
  "receiver": "nudgebee",
  "status": "firing",
  "alerts": [
    {
      "status": "firing",
      "labels": {
        "alertname": "HighErrorRate",
        "severity": "critical",
        "namespace": "payments",
        "deployment": "checkout-api",
        "env": "prod"
      },
      "annotations": {
        "summary": "Checkout error rate above 5%",
        "description": "5xx responses exceeded the threshold for 5 minutes",
        "runbook_url": "https://runbooks.example.com/checkout"
      },
      "startsAt": "2026-09-04T01:15:00Z",
      "endsAt": "0001-01-01T00:00:00Z",
      "generatorURL": "https://cube.example.com/alerts/2",
      "fingerprint": "8c1f2a9b",
      "cubeImageURL": "https://cube.example.com/render/alert/2.png",
      "cubeSampleLog": "2026-09-04T01:14:59Z ERROR checkout failed: upstream timeout"
    }
  ],
  "groupLabels": {"alertname": "HighErrorRate"},
  "commonLabels": {"cluster": "prod-eu"},
  "commonAnnotations": {"team": "payments"},
  "externalURL": "https://cube.example.com",
  "groupKeyHash": "abc123",
  "incidentTime": "2026-09-04T01:15:00Z",
  "notifyTime": "2026-09-04T01:15:30Z"
}`

// TestCubeAPMWebhookPayloadShape pins the fields the parser depends on. If
// CubeAPM's default template changes, this is the test that should fail first —
// before the mapping logic starts silently producing empty events.
func TestCubeAPMWebhookPayloadShape(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(cubeAPMSamplePayload), &payload); err != nil {
		t.Fatalf("sample payload is not valid JSON: %v", err)
	}

	alerts, ok := payload["alerts"].([]any)
	if !ok || len(alerts) != 1 {
		t.Fatalf("expected exactly one alert, got %T", payload["alerts"])
	}
	alert := alerts[0].(map[string]any)

	for _, field := range []string{"status", "labels", "annotations", "startsAt", "endsAt",
		"generatorURL", "fingerprint", "cubeImageURL", "cubeSampleLog"} {
		if _, present := alert[field]; !present {
			t.Errorf("alert is missing %q", field)
		}
	}
	for _, field := range []string{"externalURL", "groupKeyHash", "commonLabels", "commonAnnotations"} {
		if _, present := payload[field]; !present {
			t.Errorf("payload is missing %q", field)
		}
	}
}

func TestCubeAPMWebhookRejectsNonAlertmanagerPayload(t *testing.T) {
	tests := []struct {
		name        string
		payload     string
		wantErr     error
		wantContain string
	}{
		{
			name:        "no alerts key",
			payload:     `{"message": "something happened"}`,
			wantContain: "no 'alerts' array",
		},
		{
			name:        "alerts not an array",
			payload:     `{"alerts": {"status": "firing"}}`,
			wantContain: "not an array",
		},
		{
			// A well-formed delivery carrying no alerts is a no-op, not a failure:
			// recording it as failed would make ingestion read as broken.
			name:    "empty alerts array is skipped, not failed",
			payload: `{"alerts": []}`,
			wantErr: core.ErrEventNotSupported,
		},
		{
			name:        "malformed json",
			payload:     `{"alerts": [`,
			wantContain: "failed to parse payload",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CubeAPMWebhook{}.ProcessEventWebook(nil, nil, "acct", tt.payload)
			if err == nil {
				t.Fatal("expected an error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantContain != "" && !strings.Contains(err.Error(), tt.wantContain) {
				t.Errorf("error %q does not mention %q", err, tt.wantContain)
			}
		})
	}
}

func TestCubeAPMWebhookString(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"plain value", "https://cube/x.png", "https://cube/x.png"},
		{"trimmed", "  value  ", "value"},
		{"empty", "", ""},
		{"nil", nil, ""},
		// Go templates print an unset field as "<no value>"; treating that as a
		// real value would put literal template noise into the investigation.
		{"unrendered template", "<no value>", ""},
		{"nil pointer render", "<nil>", ""},
		{"non-string", 42, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cubeAPMWebhookString(tt.in); got != tt.want {
				t.Errorf("cubeAPMWebhookString(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseCubeAPMWebhookTime(t *testing.T) {
	t.Run("parses RFC3339", func(t *testing.T) {
		got := parseCubeAPMWebhookTime("2026-09-04T01:15:00Z", "startsAt")
		if got.IsZero() {
			t.Fatal("expected a parsed time")
		}
		if got.Year() != 2026 || got.Month() != 9 || got.Day() != 4 {
			t.Errorf("parsed %v, want 2026-09-04", got)
		}
	})

	// CubeAPM renders a never-set endsAt through Go's MarshalText, so it arrives
	// as the year-1 zero time rather than being omitted. Treating it as a real
	// end would resolve every firing alert instantly.
	t.Run("year-1 zero time is treated as unset", func(t *testing.T) {
		if got := parseCubeAPMWebhookTime("0001-01-01T00:00:00Z", "endsAt"); !got.IsZero() {
			t.Errorf("got %v, want zero time", got)
		}
	})

	t.Run("malformed value does not panic", func(t *testing.T) {
		if got := parseCubeAPMWebhookTime("not-a-time", "startsAt"); !got.IsZero() {
			t.Errorf("got %v, want zero time", got)
		}
	})

	t.Run("empty and non-string", func(t *testing.T) {
		if got := parseCubeAPMWebhookTime("", "startsAt"); !got.IsZero() {
			t.Error("empty string should give zero time")
		}
		if got := parseCubeAPMWebhookTime(nil, "startsAt"); !got.IsZero() {
			t.Error("nil should give zero time")
		}
	})
}

func TestCubeAPMWebhookTags(t *testing.T) {
	tags := cubeAPMWebhookTags(map[string]string{
		"cluster":   "prod-eu",
		"namespace": "payments",
		"service":   "payments", // duplicate value must not render twice
		"job":       "",
		"env":       "prod",
	})

	want := []string{"prod-eu", "payments", "prod"}
	if len(tags) != len(want) {
		t.Fatalf("tags = %v, want %v", tags, want)
	}
	for i := range want {
		if tags[i] != want[i] {
			t.Errorf("tags[%d] = %q, want %q (order is fixed)", i, tags[i], want[i])
		}
	}
}

func TestBuildCubeAPMImageEvidence(t *testing.T) {
	t.Run("nil for absent url", func(t *testing.T) {
		if ev := buildCubeAPMImageEvidence(""); ev != nil {
			t.Error("expected nil for an empty image url")
		}
	})

	// The field is template-rendered, so an unvalidated value would put an
	// arbitrary URI into markdown shown to every viewer of the investigation.
	t.Run("rejects non-http schemes", func(t *testing.T) {
		for _, url := range []string{
			"javascript:alert(1)",
			"data:text/html;base64,PHNjcmlwdD4=",
			"file:///etc/passwd",
			"//evil.example.com/x.png",
		} {
			if ev := buildCubeAPMImageEvidence(url); ev != nil {
				t.Errorf("buildCubeAPMImageEvidence(%q) returned evidence; want nil", url)
			}
		}
	})

	t.Run("renders http url as markdown image", func(t *testing.T) {
		ev := buildCubeAPMImageEvidence("https://cube.example.com/render/alert/2.png")
		if ev == nil {
			t.Fatal("expected evidence")
		}
		data := ev.Data.(map[string]any)
		if !strings.Contains(data["data"].(string), "![CubeAPM alert chart](https://cube.example.com/render/alert/2.png)") {
			t.Errorf("markdown = %q", data["data"])
		}
	})

	// A closing paren terminates the markdown image early, letting the rest of
	// the value render as page text.
	t.Run("escapes closing paren", func(t *testing.T) {
		ev := buildCubeAPMImageEvidence("https://cube.example.com/r.png?q=a)b")
		if ev == nil {
			t.Fatal("expected evidence")
		}
		markdown := ev.Data.(map[string]any)["data"].(string)
		if strings.Contains(markdown, "a)b") {
			t.Errorf("raw closing paren survived into markdown: %q", markdown)
		}
		if !strings.Contains(markdown, "a%29b") {
			t.Errorf("expected percent-encoded paren, got %q", markdown)
		}
	})
}

func TestBuildCubeAPMSampleLogEvidence(t *testing.T) {
	t.Run("nil when absent", func(t *testing.T) {
		if ev := buildCubeAPMSampleLogEvidence(""); ev != nil {
			t.Error("a metric alert carries no sample log; expected nil")
		}
	})

	t.Run("wraps in a code fence", func(t *testing.T) {
		ev := buildCubeAPMSampleLogEvidence("ERROR upstream timeout")
		if ev == nil {
			t.Fatal("expected evidence")
		}
		if ev.Type != "markdown" {
			t.Errorf("Type = %q, want markdown", ev.Type)
		}
		data := ev.Data.(map[string]any)["data"].(string)
		if !strings.HasPrefix(data, "```") || !strings.Contains(data, "ERROR upstream timeout") {
			t.Errorf("data = %q", data)
		}
	})
}

func TestBuildCubeAPMAlertEvidence(t *testing.T) {
	ev := buildCubeAPMAlertEvidence("HighErrorRate", "firing",
		map[string]string{"severity": "critical", "namespace": "payments", "env": ""},
		map[string]string{"summary": "Checkout error rate above 5%"})

	if ev.Type != "table" {
		t.Fatalf("Type = %q, want table", ev.Type)
	}
	rows := ev.Data.(map[string]any)["rows"].([][]any)

	got := map[string]string{}
	for _, row := range rows {
		got[row[0].(string)] = row[1].(string)
	}
	if got["Alert"] != "HighErrorRate" {
		t.Errorf("Alert row = %q", got["Alert"])
	}
	if got["Severity"] != "critical" {
		t.Errorf("Severity row = %q", got["Severity"])
	}
	// Empty values are dropped rather than rendered as blank rows.
	if _, present := got["Environment"]; present {
		t.Error("empty label produced a table row")
	}
}

func TestCubeAPMWebhookIdentity(t *testing.T) {
	m := CubeAPMWebhook{}
	if m.Name() != IntegrationCubeAPMWebhook {
		t.Errorf("Name() = %q, want %q", m.Name(), IntegrationCubeAPMWebhook)
	}
	if m.Category() != core.IntegrationCategoryIncidentWebhook {
		t.Errorf("Category() = %q, want incident_webhook", m.Category())
	}
	// The V1828 migration registers this exact string as an event_source and
	// event_rule_source; a rename here breaks event ingestion at the DB constraint.
	if IntegrationCubeAPMWebhook != "cubeapm_webhook" {
		t.Errorf("IntegrationCubeAPMWebhook = %q, want cubeapm_webhook", IntegrationCubeAPMWebhook)
	}
}

func TestCubeAPMWebhookMergeReturnsNew(t *testing.T) {
	previous := core.EventIncomingWebhook{EventTitle: "old"}
	updated := core.EventIncomingWebhook{EventTitle: "new"}

	merged, err := CubeAPMWebhook{}.MergeEventWebhooks(nil, previous, updated)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.EventTitle != "new" {
		t.Errorf("merged title = %q, want the new event's title", merged.EventTitle)
	}
}
