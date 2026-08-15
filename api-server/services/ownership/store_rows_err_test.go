package ownership

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A driver error raised part-way through a result set ends rows.Next() exactly
// like end-of-rows. These two helpers back the check that BLOCKS creating an
// ownership rule that overlaps an existing one, so reading a truncated set as if
// it were complete lets two rules that both really match a resource both be
// created. The rows.Err() guard is what tells the two cases apart.
func mockRowsWithMidScanError(t *testing.T, column string, boom error) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Row 0 is delivered, row 1 aborts the stream: a partial, plausible-looking read.
	mock.ExpectQuery("SELECT " + column + " FROM").WillReturnRows(
		sqlmock.NewRows([]string{column}).
			AddRow([]byte(`{"team":"payments"}`)).
			AddRow([]byte(`{"team":"payments"}`)).
			RowError(1, boom),
	)
	return sqlx.NewDb(db, "postgres"), mock
}

func TestLoadCloudResourceTagsMatchingSurfacesMidScanError(t *testing.T) {
	boom := errors.New("read tcp: connection reset by peer")
	db, mock := mockRowsWithMidScanError(t, "tags", boom)

	out, err := loadCloudResourceTagsMatching(db, "11111111-1111-1111-1111-111111111111", "team", "payments", "")

	require.ErrorIs(t, err, boom, "a truncated read must not be reported as success")
	assert.Nil(t, out, "no partial tag set may reach the conflict check")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadWorkloadLabelsMatchingSurfacesMidScanError(t *testing.T) {
	boom := errors.New("read tcp: connection reset by peer")
	db, mock := mockRowsWithMidScanError(t, "labels", boom)

	out, err := loadWorkloadLabelsMatching(db, "11111111-1111-1111-1111-111111111111", "team", "payments", "")

	require.ErrorIs(t, err, boom, "a truncated read must not be reported as success")
	assert.Nil(t, out, "no partial label set may reach the conflict check")
	require.NoError(t, mock.ExpectationsWereMet())
}

// The guard must not change the happy path: a fully drained cursor has a nil
// rows.Err(), so the caller still sees every row it saw before.
func TestLoadCloudResourceTagsMatchingReturnsAllRowsOnCleanRead(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("SELECT tags FROM").WillReturnRows(
		sqlmock.NewRows([]string{"tags"}).
			AddRow([]byte(`{"team":"payments"}`)).
			AddRow([]byte(`{"team":"search"}`)),
	)

	out, err := loadCloudResourceTagsMatching(sqlx.NewDb(db, "postgres"), "11111111-1111-1111-1111-111111111111", "team", "payments", "")

	require.NoError(t, err)
	assert.Equal(t, []map[string]string{{"team": "payments"}, {"team": "search"}}, out)
	require.NoError(t, mock.ExpectationsWereMet())
}
