package core

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyCustomAgentOwnedByAccount(t *testing.T) {
	const (
		accountId = "00000000-0000-0000-0000-000000000001"
		agentId   = "00000000-0000-0000-0000-000000000002"
	)

	tests := []struct {
		name    string
		exists  bool
		dbErr   error
		wantErr string
	}{
		{name: "agent installed in the account", exists: true},
		{name: "agent of another account or tenant", exists: false, wantErr: "not owned by account"},
		{name: "query fails", dbErr: errors.New("boom"), wantErr: "failed to verify agent ownership"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()

			mock.ExpectBegin()
			q := mock.ExpectQuery(`join llm_agents_installation i on i.agent_id = a.id::text`).
				WithArgs(agentId, accountId)
			if tt.dbErr != nil {
				q.WillReturnError(tt.dbErr)
			} else {
				q.WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(tt.exists))
			}

			tx, err := sqlx.NewDb(db, "postgres").Beginx()
			require.NoError(t, err)

			err = verifyCustomAgentOwnedByAccount(tx, accountId, agentId)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
