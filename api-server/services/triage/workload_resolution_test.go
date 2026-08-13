package triage

import (
	"context"
	"testing"

	"nudgebee/services/internal/database/models"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testResolutionAccount = "aaaaaaaa-0000-0000-0000-000000000002"
	testResolutionCRID    = "22222222-2222-2222-2222-222222222222"
)

func strp(s string) *string { return &s }

func newResolutionMock(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return sqlx.NewDb(db, "postgres"), mock
}

func workloadRow() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"cloud_resource_id", "kind", "labels"}).
		AddRow(testResolutionCRID, "Deployment", `{"app":"checkout"}`)
}

// Roughly 30% of events arrive without a cloud_resource_id and are matched by
// (namespace, subject_owner). That branch used to leave the id empty, which silently skipped both the
// knowledge-graph lookup and the criticality lookup for every one of those events. The id must now be
// read back out of the matched inventory row.
func TestResolveEventWorkloadRecoversCRIDFromNameLookup(t *testing.T) {
	db, mock := newResolutionMock(t)
	mock.ExpectQuery("FROM k8s_workloads").
		WithArgs(testResolutionAccount, "shop", "checkout").
		WillReturnRows(workloadRow())

	crid, f := resolveEventWorkload(context.Background(), db, &models.Event{
		CloudAccountId:   strp(testResolutionAccount),
		SubjectNamespace: strp("shop"),
		SubjectOwner:     strp("checkout"),
	})

	assert.Equal(t, testResolutionCRID, crid, "the name-based path must recover the resource id")
	assert.True(t, f.found)
	assert.Equal(t, "Deployment", f.kind)
	assert.Equal(t, "checkout", f.appName)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestResolveEventWorkloadPrefersTheEventsOwnCRID(t *testing.T) {
	db, mock := newResolutionMock(t)
	mock.ExpectQuery("FROM k8s_workloads").
		WithArgs(testResolutionAccount, testResolutionCRID).
		WillReturnRows(workloadRow())

	crid, f := resolveEventWorkload(context.Background(), db, &models.Event{
		CloudAccountId:  strp(testResolutionAccount),
		CloudResourceId: strp(testResolutionCRID),
		// Deliberately also present: the reliable key must win, without a second query.
		SubjectNamespace: strp("shop"),
		SubjectOwner:     strp("checkout"),
	})

	assert.Equal(t, testResolutionCRID, crid)
	assert.True(t, f.found)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A cloud/non-k8s event carries neither key. It must resolve to nothing without querying at all —
// these facts never gate scoring, so a miss is normal, not an error.
func TestResolveEventWorkloadWithNoKeysQueriesNothing(t *testing.T) {
	db, mock := newResolutionMock(t)

	crid, f := resolveEventWorkload(context.Background(), db, &models.Event{
		CloudAccountId: strp(testResolutionAccount),
	})

	assert.Empty(t, crid)
	assert.False(t, f.found)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// An un-inventoried workload (no matching row) is a miss, not a failure.
func TestResolveEventWorkloadMissIsNotAnError(t *testing.T) {
	db, mock := newResolutionMock(t)
	mock.ExpectQuery("FROM k8s_workloads").
		WillReturnRows(sqlmock.NewRows([]string{"cloud_resource_id", "kind", "labels"}))

	crid, f := resolveEventWorkload(context.Background(), db, &models.Event{
		CloudAccountId:   strp(testResolutionAccount),
		SubjectNamespace: strp("shop"),
		SubjectOwner:     strp("ghost"),
	})

	assert.Empty(t, crid)
	assert.False(t, f.found)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestResolveEventWorkloadNilInputs(t *testing.T) {
	db, _ := newResolutionMock(t)

	if _, f := resolveEventWorkload(context.Background(), nil, &models.Event{CloudAccountId: strp("a")}); f.found {
		t.Error("a nil db must resolve to nothing")
	}
	if _, f := resolveEventWorkload(context.Background(), db, nil); f.found {
		t.Error("a nil event must resolve to nothing")
	}
	if _, f := resolveEventWorkload(context.Background(), db, &models.Event{}); f.found {
		t.Error("an event with no cloud account must resolve to nothing")
	}
}
