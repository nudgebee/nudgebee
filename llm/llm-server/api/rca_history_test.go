package api

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/events"
	"testing"
	"time"
)

func TestRCAHistoryResponse(t *testing.T) {
	generated := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	attempted := generated.Add(time.Hour)
	for _, status := range []string{"IN_PROGRESS", "FAILED"} {
		t.Run(status, func(t *testing.T) {
			response := EventAnalysisResponse{}
			applyRCAHistory(&response, []events.RCAReportVersion{{ID: "good", Analysis: "last good", GeneratedAt: &generated}}, &events.RCAAttempt{ID: "attempt", Status: status, StatusReason: "secret provider diagnostics", UpdatedAt: attempted})
			require.Equal(t, status, response.Status)
			require.Equal(t, "last good", response.Analysis)
			require.Equal(t, generated, *response.GeneratedAt)
			require.Equal(t, attempted, *response.AttemptedAt)
			raw, err := json.Marshal(response)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "secret provider")
		})
	}
	t.Run("legacy failed report has unknown success time", func(t *testing.T) {
		response := EventAnalysisResponse{}
		applyRCAHistory(&response, []events.RCAReportVersion{{ID: "legacy", Analysis: "old"}}, &events.RCAAttempt{Status: "FAILED", UpdatedAt: attempted})
		require.Nil(t, response.GeneratedAt)
		require.Equal(t, "old", response.Analysis)
	})
	t.Run("old failure cannot hide later success", func(t *testing.T) {
		response := EventAnalysisResponse{}
		applyRCAHistory(&response, []events.RCAReportVersion{{ID: "good", Analysis: "good", GeneratedAt: &attempted}}, &events.RCAAttempt{Status: "FAILED", UpdatedAt: generated})
		require.Equal(t, "COMPLETED", response.Status)
		require.Nil(t, response.AttemptedAt)
	})
}

func TestRCARecoverySession(t *testing.T) {
	require.Equal(t, "event-rca-fp", rcaRecoverySession(events.InProgressAnalysis{AnalysisType: events.AnalysisTypeRCA, EventFingerprint: "fp"}))
	require.Equal(t, events.RCAAttemptSessionID("attempt"), rcaRecoverySession(events.InProgressAnalysis{ID: "attempt", AnalysisType: events.AnalysisTypeRCAAttempt, EventFingerprint: "fp"}))
	var request EventRCAAnalysisRequest
	require.NoError(t, json.Unmarshal([]byte(`{"AttemptID":"spoof","attempt_id":"spoof"}`), &request))
	require.Empty(t, request.AttemptID)
}

func TestRCAReportTextLegacy(t *testing.T) {
	require.Equal(t, "old report", rcaReportText(`{"analysis":"old report","status":"COMPLETED"}`))
	require.Equal(t, "plain report", rcaReportText("plain report"))
}
