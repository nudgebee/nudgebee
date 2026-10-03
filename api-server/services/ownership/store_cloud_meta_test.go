package ownership

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadCloudResourceMetas must compare the uuid PK column directly against a
// single bound uuid[] parameter. The old id::text IN (?) form cast the column
// (non-sargable -> seq scan of the 15GB cloud_resourses) and expanded the id
// list into one placeholder per element. Both are pinned here: the query text
// carries `id = ANY($2::uuid[])`, and exactly two args reach the driver
// regardless of how many ids were requested.
func TestLoadCloudResourceMetasIsSargableAndSingleArrayParam(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	const tenant = "11111111-1111-1111-1111-111111111111"
	ids := []string{
		"22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333",
		"44444444-4444-4444-4444-444444444444",
	}

	mock.ExpectQuery(`id = ANY\(\$2::uuid\[\]\)`).
		WithArgs(tenant, sqlmock.AnyArg()). // two args only: tenant + the uuid[] — no IN expansion
		WillReturnRows(sqlmock.NewRows([]string{"id", "account", "region", "rtype", "service", "tags"}).
			AddRow("22222222-2222-2222-2222-222222222222", "acct-a", "us-east-1", "ec2", "compute", []byte(`{"team":"payments"}`)).
			AddRow("33333333-3333-3333-3333-333333333333", "acct-b", "eu-west-1", "", "", []byte(`{}`)))

	out, err := loadCloudResourceMetas(sqlx.NewDb(db, "postgres"), tenant, ids)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, map[string]cloudResourceMeta{
		"22222222-2222-2222-2222-222222222222": {Account: "acct-a", Region: "us-east-1", Type: "ec2", ServiceName: "compute", Tags: map[string]string{"team": "payments"}},
		"33333333-3333-3333-3333-333333333333": {Account: "acct-b", Region: "eu-west-1", Type: "", ServiceName: "", Tags: map[string]string{}},
	}, out)
}

// No ids -> no query at all (unchanged fast-out).
func TestLoadCloudResourceMetasEmptyIdsRunsNoQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	out, err := loadCloudResourceMetas(sqlx.NewDb(db, "postgres"), "11111111-1111-1111-1111-111111111111", nil)

	require.NoError(t, err)
	assert.Empty(t, out)
	require.NoError(t, mock.ExpectationsWereMet()) // no ExpectQuery registered => a query would fail
}
