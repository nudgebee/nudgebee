package integrations

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/event"
)

func TestMapGrafanaAlertToEvent_Firing(t *testing.T) {
	alert := map[string]any{
		"status":      "firing",
		"fingerprint": "abc123",
		"startsAt":    "2026-06-19T10:00:00Z",
		"endsAt":      "0001-01-01T00:00:00Z",
		"labels": map[string]any{
			"alertname": "HighCPU",
			"severity":  "critical",
			"namespace": "prod",
			"cluster":   "us-east",
			"pod":       "api-0",
		},
		"annotations": map[string]any{
			"summary":     "CPU is high",
			"description": "CPU usage above 90%",
		},
		"generatorURL": "https://grafana.example/d/abc",
	}

	got, err := mapGrafanaAlertToEvent("acct-1", alert)
	require.NoError(t, err)

	assert.Equal(t, "acct-1", got.AccountId)
	assert.Equal(t, "abc123", got.EventId)
	assert.Equal(t, "HighCPU", got.EventType)
	assert.Equal(t, "CPU is high", got.EventTitle)
	assert.Equal(t, "CPU usage above 90%", got.EventDescription)
	assert.Equal(t, "https://grafana.example/d/abc", got.EventUrl)
	assert.Equal(t, string(event.EventStatusFiring), got.EventStatus)
	assert.Equal(t, string(event.EventPriorityHigh), got.EventPriority)
	assert.Equal(t, "pod", got.EventSubjectKind)
	assert.Equal(t, "api-0", got.EventSubjectName)
	assert.Equal(t, "prod", got.EventSubjectNamespace)
	assert.Equal(t, time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC), got.EventCreatedAt)
	assert.Contains(t, got.EventTags, "prod")
	assert.Contains(t, got.EventTags, "us-east")
	assert.Equal(t, "abc123", got.Investigation.Fingerprint)
	assert.Equal(t, event.EventStatusFiring, got.Investigation.Status)
	assert.Equal(t, "grafana", got.Investigation.RuleType)
	// generatorURL should be copied into labels for downstream consumers.
	assert.Equal(t, "https://grafana.example/d/abc", got.Investigation.Labels["generatorURL"])
}

func TestMapGrafanaAlertToEvent_TitleFallsBackToAlertname(t *testing.T) {
	alert := map[string]any{
		"status":      "resolved",
		"fingerprint": "def456",
		"labels": map[string]any{
			"alertname": "DiskFull",
		},
		"annotations": map[string]any{},
	}

	got, err := mapGrafanaAlertToEvent("acct-2", alert)
	require.NoError(t, err)

	assert.Equal(t, "DiskFull", got.EventTitle)
	assert.Equal(t, string(event.EventStatusResolved), got.EventStatus)
	assert.Empty(t, got.EventSubjectKind)
	assert.Empty(t, got.EventSubjectName)
}

func TestMapGrafanaAlertToEvent_DashboardURLFallback(t *testing.T) {
	alert := map[string]any{
		"status":       "firing",
		"fingerprint":  "ghi789",
		"dashboardURL": "https://grafana.example/dashboard",
		"labels": map[string]any{
			"alertname": "MemPressure",
		},
		"annotations": map[string]any{},
	}

	got, err := mapGrafanaAlertToEvent("acct-3", alert)
	require.NoError(t, err)

	assert.Equal(t, "https://grafana.example/dashboard", got.EventUrl)
	assert.Equal(t, "https://grafana.example/dashboard", got.Investigation.SourceUrl)
}

func TestMapGrafanaAlertToEvent_MissingFingerprintErrors(t *testing.T) {
	alert := map[string]any{
		"status": "firing",
		"labels": map[string]any{
			"alertname": "NoFingerprint",
		},
		"annotations": map[string]any{},
	}

	got, err := mapGrafanaAlertToEvent("acct-4", alert)
	require.Error(t, err)
	assert.Nil(t, got)
}
