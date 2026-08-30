package api

import (
	"encoding/json"
	"testing"
	"time"

	"nudgebee/llm/events"

	"github.com/stretchr/testify/require"
)

func TestBuildStoredEventAnalysisVersionPreservesDecodedAnalysis(t *testing.T) {
	current := &EventAnalysisResponse{EventFingerprint: "fingerprint", EventAggregationKey: "aggregation"}
	stored := events.EventAnalysisEventVersion{
		EventID: "event-1", VersionRank: 2, GeneratedAt: time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC),
		Analysis: `{"analysis":"clean markdown","title":"Stored title"}`,
	}

	version, err := buildStoredEventAnalysisVersion(current, stored)
	require.NoError(t, err)

	var payload EventAnalysisResponse
	require.NoError(t, json.Unmarshal(version.Data, &payload))
	require.Equal(t, "clean markdown", payload.Analysis)
	require.Equal(t, "Stored title", payload.Title)
}
