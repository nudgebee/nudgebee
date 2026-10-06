package triage

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

// A suggestion raised through the PR path stays apply_status=pending; the list must carry
// the linked resolution's outcome so the UI can show the PR, a failure, or "no PR needed".
func TestListThresholdSuggestions_CarriesResolutionOutcome(t *testing.T) {
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	db := sqlx.NewDb(raw, "sqlmock")

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM event_threshold_suggestions`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	cols := []string{"alert_rule_key", "fingerprint", "cloud_account_id", "source", "apply_status",
		"pr_url", "resolution_status", "resolution_message", "firing_analysis", "alert_quality", "metric_stats", "query_metadata"}
	mock.ExpectQuery(`LEFT JOIN \(\s*SELECT id AS resolution_row_id`).
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow("k1", "f1", "a1", "prometheus", "pending", "https://github.com/o/r/pull/7", "InProgress", "PR raised", []byte("null"), []byte("null"), []byte("null"), []byte("null")).
			AddRow("k2", "f2", "a1", "prometheus", "pending", nil, "Failed", "boom", []byte("null"), []byte("null"), []byte("null"), []byte("null")))

	resp, err := ListThresholdSuggestions(context.Background(), db, "t1", nil, "", "", 20, 0)
	require.NoError(t, err)
	require.Len(t, resp.Suggestions, 2)

	require.NotNil(t, resp.Suggestions[0].PRURL)
	require.Equal(t, "https://github.com/o/r/pull/7", *resp.Suggestions[0].PRURL)
	require.Nil(t, resp.Suggestions[1].PRURL)
	require.Equal(t, "Failed", *resp.Suggestions[1].ResolutionStatus)
	require.Equal(t, "boom", *resp.Suggestions[1].ResolutionMessage)
	require.NoError(t, mock.ExpectationsWereMet())
}
