package events

import (
	"testing"
	"time"

	"nudgebee/llm/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestListCompletedEventAnalysisVersionsReturnsFullStoredPayload(t *testing.T) {
	repo, mock := newClaimTestRepo(t)
	ctx := security.NewRequestContextForSuperAdmin()
	generatedAt := time.Date(2026, 8, 27, 11, 0, 0, 0, time.UTC)

	mock.ExpectQuery("WITH ranked AS").
		WithArgs(wFp, wAcct, wAggKey,
			AnalysisTypeSummary, AnalysisTypeInvestigation, AnalysisTypeLog, AnalysisTypeDetailedResponse,
			AnalysisStatusCompleted, eventAnalysisHistoryLimit).
		WillReturnRows(sqlmock.NewRows([]string{
			"event_id", "related_event_id", "version_rank", "generated_at", "summary", "investigation", "analysis", "detailed_response",
		}).AddRow("evt-previous", "evt-previous", 2, generatedAt, "summary", "investigation", `{"title":"stored details"}`, "full report"))

	versions, err := repo.ListCompletedEventAnalysisVersions(ctx, wFp, wAcct, wAggKey)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	require.Equal(t, "evt-previous", versions[0].EventID)
	require.Equal(t, 2, versions[0].VersionRank)
	require.Equal(t, "full report", versions[0].DetailedResponse)
	require.JSONEq(t, `{"title":"stored details"}`, versions[0].Analysis)
	require.NoError(t, mock.ExpectationsWereMet())
}
