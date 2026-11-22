package scan_orchestrator

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCertExpiryWarningDays(t *testing.T) {
	const accountID = "acc-1"
	tests := []struct {
		name  string
		setup func(q *sqlmock.ExpectedQuery)
		want  int
	}{
		{"configured value", func(q *sqlmock.ExpectedQuery) {
			q.WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("180"))
		}, 180},
		{"no setting saved", func(q *sqlmock.ExpectedQuery) {
			q.WillReturnError(sql.ErrNoRows)
		}, DefaultCertExpiryWarningDays},
		{"null value", func(q *sqlmock.ExpectedQuery) {
			q.WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(nil))
		}, DefaultCertExpiryWarningDays},
		{"empty value", func(q *sqlmock.ExpectedQuery) {
			q.WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("  "))
		}, DefaultCertExpiryWarningDays},
		{"invalid value", func(q *sqlmock.ExpectedQuery) {
			q.WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("12.5"))
		}, DefaultCertExpiryWarningDays},
		{"query error", func(q *sqlmock.ExpectedQuery) {
			q.WillReturnError(fmt.Errorf("connection reset"))
		}, DefaultCertExpiryWarningDays},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := mockMetastore(t)
			tt.setup(mock.ExpectQuery(`SELECT value FROM cloud_account_attrs WHERE cloud_account_id = \$1 AND name = \$2`).
				WithArgs(accountID, certExpiryWarningDaysAttr))

			assert.Equal(t, tt.want, certExpiryWarningDays(persistTestCtx(), accountID))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
