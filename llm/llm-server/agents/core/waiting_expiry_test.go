package core

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpireWaitingConversations(t *testing.T) {
	tests := []struct {
		name            string
		expireDays      int
		unattendedHours int
		wantSources     []string
	}{
		{
			name:            "alert and automation conversations get the short window",
			expireDays:      14,
			unattendedHours: 12,
			wantSources:     []string{"Investigation", "InstantNotification", "Automation"},
		},
		{
			name:            "unattended window off leaves only the days rule",
			expireDays:      14,
			unattendedHours: 0,
			wantSources:     []string{},
		},
		{
			name:            "days rule off keeps the unattended window",
			expireDays:      0,
			unattendedHours: 12,
			wantSources:     []string{"Investigation", "InstantNotification", "Automation"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			// Only WAITING rows; the days clause is guarded by its threshold, and
			// the hours clause is scoped to the unattended sources.
			mock.ExpectExec(`UPDATE llm_conversations\s+SET status = \$1, updated_at = NOW\(\)\s+WHERE status = \$2\s+AND \(\(\$3 > 0 AND .*days => \$3.*\)\s+OR \(source = ANY\(\$4\) AND .*hours => \$5`).
				WithArgs("TERMINATED", "WAITING", tt.expireDays, pq.Array(tt.wantSources), tt.unattendedHours).
				WillReturnResult(sqlmock.NewResult(0, 3))

			expireWaitingConversations(context.Background(), db, tt.expireDays, tt.unattendedHours)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestExpireWaitingConversations_DBErrorIsLoggedNotPanicked(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec(`UPDATE llm_conversations`).WillReturnError(errors.New("boom"))

	expireWaitingConversations(context.Background(), db, 14, 12)
	assert.NoError(t, mock.ExpectationsWereMet())
}
