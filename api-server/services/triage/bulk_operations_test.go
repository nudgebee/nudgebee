package triage

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MqConsume requeues on any returned error, with no delivery limit, and this
// queue is consumed one message at a time. A job that can never be processed
// must therefore return nil (ack), or it blocks every job behind it.
func TestProcessBulkClassification_UnprocessableJobIsDropped(t *testing.T) {
	const validID = "0b6f4f0e-5d0a-4a52-9a43-3c1f6f1f2a01"

	tests := []struct {
		name    string
		payload string
	}{
		{"malformed JSON", `{not json`},
		{"wrong JSON type", `[]`},
		{"empty object", `{}`},
		{"null", `null`},
		{"account_id is not a UUID", `{"job_id":"` + validID + `","fingerprint":"fp","account_id":"acc-1","exclude_event_id":"` + validID + `","new_status":"SUPPRESSED"}`},
		{"exclude_event_id is missing", `{"job_id":"` + validID + `","fingerprint":"fp","account_id":"` + validID + `","new_status":"SUPPRESSED"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A nil db makes any database access panic, so this also proves the
			// job is dropped before the first statement.
			var err error
			require.NotPanics(t, func() {
				err = ProcessBulkClassification(context.Background(), nil, []byte(tt.payload))
			}, "an unprocessable job must not touch the database")
			assert.NoError(t, err, "an unprocessable job must be acked, not requeued")
		})
	}
}

// A database failure can succeed on a later delivery, so it must still be
// returned: the requeued job resumes where it stopped.
func TestProcessBulkClassification_DatabaseErrorIsRequeued(t *testing.T) {
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	db := sqlx.NewDb(raw, "postgres")

	const jobID = "0b6f4f0e-5d0a-4a52-9a43-3c1f6f1f2a02"
	payload := `{"job_id":"` + jobID + `","fingerprint":"fp","account_id":"0b6f4f0e-5d0a-4a52-9a43-3c1f6f1f2a03","exclude_event_id":"0b6f4f0e-5d0a-4a52-9a43-3c1f6f1f2a04","new_status":"SUPPRESSED"}`
	dbErr := errors.New("dial tcp: connect: connection refused")

	mock.ExpectExec("UPDATE event_bulk_operations").
		WithArgs(BulkStatusProcessing, "", jobID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id FROM events").WillReturnError(dbErr)
	mock.ExpectExec("UPDATE event_bulk_operations").
		WithArgs(BulkStatusFailed, dbErr.Error(), jobID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = ProcessBulkClassification(context.Background(), db, []byte(payload))
	assert.ErrorIs(t, err, dbErr)
	require.NoError(t, mock.ExpectationsWereMet())
}
