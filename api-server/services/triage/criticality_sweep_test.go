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

// The sweep's prune is `cloud_resource_id <> ALL($2)`. With an empty array that predicate is TRUE for
// every row, so any sweep that reaches the prune with nothing tiered DELETES the account's entire
// derived criticality. These tests pin the guards that stop an unobserved input from doing that:
// sqlmock fails the test if an unexpected statement (the DELETE) is executed.

const testAccount = "aaaaaaaa-0000-0000-0000-000000000001"

func newSweepMock(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return sqlx.NewDb(db, "postgres"), mock
}

// expectGraphFacts stubs fetchAccountGraphFacts with the given rows.
func expectGraphFacts(mock sqlmock.Sqlmock, crids ...string) {
	rows := sqlmock.NewRows([]string{"crid", "image", "customer_facing", "fan_in"})
	for _, c := range crids {
		rows.AddRow(c, "registry/app:v1", true, 3)
	}
	mock.ExpectQuery("knowledge_graph_node").WillReturnRows(rows)
}

// expectWorkloads stubs the k8s_workloads inventory read.
func expectWorkloads(mock sqlmock.Sqlmock, crids ...string) {
	expectWorkloadsWithLabels(mock, "{}", crids...)
}

func expectWorkloadsWithLabels(mock sqlmock.Sqlmock, labels string, crids ...string) {
	rows := sqlmock.NewRows([]string{"cloud_resource_id", "namespace", "name", "kind", "labels"})
	for i, c := range crids {
		rows.AddRow(c, "prod", "svc-"+string(rune('a'+i)), "Deployment", labels)
	}
	mock.ExpectQuery("FROM k8s_workloads").WillReturnRows(rows)
}

// expectHasAutoRows stubs the "do we have something to lose?" check.
func expectHasAutoRows(mock sqlmock.Sqlmock, exists bool) {
	mock.ExpectQuery("workload_criticality").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(exists))
}

func TestSweepHoldsWhenGraphIsEmpty(t *testing.T) {
	db, mock := newSweepMock(t)

	// The knowledge graph returned nothing for an account that definitely has workloads — it is
	// missing or mid-rebuild. Deriving zero candidates from that and pruning would wipe the account.
	expectGraphFacts(mock)
	expectWorkloads(mock, "11111111-1111-1111-1111-111111111111")

	st, err := DiscoverWorkloadCriticality(context.Background(), db, testAccount, "tenant-1")

	require.NoError(t, err)
	assert.True(t, st.Skipped, "sweep must hold when it observed no graph facts at all")
	assert.Equal(t, "no knowledge-graph facts for any workload", st.SkipReason)
	assert.Equal(t, 0, st.Tiered)
	assert.NoError(t, mock.ExpectationsWereMet(), "no DELETE may be issued")
}

// The graph guard must gate the PRUNE, not the whole sweep. An operator-declared tier= label needs no
// knowledge graph at all and takes precedence over every topology signal, so on the many accounts
// that have no service graph it is the only way criticality can be expressed — it must still be
// honoured, while the prune still holds.
func TestSweepStillTiersDeclaredLabelsWhenGraphIsEmpty(t *testing.T) {
	db, mock := newSweepMock(t)
	const crid = "11111111-1111-1111-1111-111111111111"
	stubClassifier(t, func() (map[string]llmCriticalityVerdict, error) {
		return map[string]llmCriticalityVerdict{}, nil
	})

	expectGraphFacts(mock)
	expectWorkloadsWithLabels(mock, `{"tier":"critical"}`, crid)
	mock.ExpectExec("INSERT INTO workload_criticality").
		WithArgs("tenant-1", testAccount, crid, "prod", CriticalityCritical, CriticalitySourceFact,
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	st, err := DiscoverWorkloadCriticality(context.Background(), db, testAccount, "tenant-1")

	require.NoError(t, err)
	assert.Equal(t, 1, st.Tiered, "an operator-declared tier must not need a knowledge graph")
	assert.True(t, st.Skipped, "the prune still holds — topology was unobserved")
	assert.NoError(t, mock.ExpectationsWereMet(), "no DELETE may be issued")
}

func TestSweepHoldsWhenNothingTieredButRowsExist(t *testing.T) {
	db, mock := newSweepMock(t)

	// A graph was observed, but nothing cleared the recall bar this run while the account still has
	// derived rows — the edges changed shape, workloads did not stop mattering overnight.
	mock.ExpectQuery("knowledge_graph_node").WillReturnRows(
		sqlmock.NewRows([]string{"crid", "image", "customer_facing", "fan_in"}).
			AddRow("11111111-1111-1111-1111-111111111111", "registry/app:v1", false, 0))
	expectWorkloads(mock, "11111111-1111-1111-1111-111111111111")
	expectHasAutoRows(mock, true)

	st, err := DiscoverWorkloadCriticality(context.Background(), db, testAccount, "tenant-1")

	require.NoError(t, err)
	assert.True(t, st.Skipped)
	assert.Equal(t, "nothing tiered while derived rows exist", st.SkipReason)
	assert.NoError(t, mock.ExpectationsWereMet(), "no DELETE may be issued")
}

// The mirror case: an account with nothing stored has nothing to lose, so a genuinely empty result
// must still be allowed to prune (and stay idempotent) rather than holding forever.
func TestSweepPrunesWhenNoCandidatesAndNoRows(t *testing.T) {
	db, mock := newSweepMock(t)

	mock.ExpectQuery("knowledge_graph_node").WillReturnRows(
		sqlmock.NewRows([]string{"crid", "image", "customer_facing", "fan_in"}).
			AddRow("11111111-1111-1111-1111-111111111111", "registry/app:v1", false, 0))
	expectWorkloads(mock, "11111111-1111-1111-1111-111111111111")
	expectHasAutoRows(mock, false)
	mock.ExpectExec("DELETE FROM workload_criticality").WillReturnResult(sqlmock.NewResult(0, 0))

	st, err := DiscoverWorkloadCriticality(context.Background(), db, testAccount, "tenant-1")

	require.NoError(t, err)
	assert.False(t, st.Skipped)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// An account with no workloads at all is not an anomaly — it must not trip the empty-graph guard.
func TestSweepWithNoWorkloadsIsNotTreatedAsUnobserved(t *testing.T) {
	db, mock := newSweepMock(t)

	expectGraphFacts(mock)
	expectWorkloads(mock)
	expectHasAutoRows(mock, false)
	mock.ExpectExec("DELETE FROM workload_criticality").WillReturnResult(sqlmock.NewResult(0, 0))

	st, err := DiscoverWorkloadCriticality(context.Background(), db, testAccount, "tenant-1")

	require.NoError(t, err)
	assert.False(t, st.Skipped)
	assert.Equal(t, 0, st.Scanned)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// stubClassifier replaces the LLM review for the duration of one test.
func stubClassifier(t *testing.T, fn func() (map[string]llmCriticalityVerdict, error)) {
	t.Helper()
	original := classifyWorkloads
	classifyWorkloads = func(context.Context, string, string, []llmWorkload) (map[string]llmCriticalityVerdict, error) {
		return fn()
	}
	t.Cleanup(func() { classifyWorkloads = original })
}

// llm-server is unreachable. An account that already has a reviewed tiering must hold it rather than
// flip every LLM-assigned `low` back to the recall stage's `high` for the night and back again
// tomorrow.
func TestSweepHoldsExistingRowsWhenLLMReviewFails(t *testing.T) {
	db, mock := newSweepMock(t)
	stubClassifier(t, func() (map[string]llmCriticalityVerdict, error) {
		return nil, errors.New("llm-server unreachable")
	})

	expectGraphFacts(mock, "11111111-1111-1111-1111-111111111111")
	expectWorkloads(mock, "11111111-1111-1111-1111-111111111111")
	expectHasAutoRows(mock, true)

	st, err := DiscoverWorkloadCriticality(context.Background(), db, testAccount, "tenant-1")

	require.NoError(t, err)
	assert.Equal(t, 1, st.Candidate, "the candidate was still recalled")
	assert.True(t, st.Skipped, "an account with rows must hold them through an LLM outage")
	assert.Equal(t, "llm review failed", st.SkipReason)
	assert.Equal(t, 0, st.Tiered)
	assert.NoError(t, mock.ExpectationsWereMet(), "no DELETE may be issued")
}

// The mirror case: an account with no rows yet has nothing to lose, so a failed review still seeds it
// from the deterministic verdicts — better a recall-only baseline than no criticality at all.
func TestSweepSeedsFromDeterministicWhenLLMFailsAndNoRowsExist(t *testing.T) {
	db, mock := newSweepMock(t)
	stubClassifier(t, func() (map[string]llmCriticalityVerdict, error) {
		return nil, errors.New("llm-server unreachable")
	})

	expectGraphFacts(mock, "11111111-1111-1111-1111-111111111111")
	expectWorkloads(mock, "11111111-1111-1111-1111-111111111111")
	expectHasAutoRows(mock, false)
	mock.ExpectExec("INSERT INTO workload_criticality").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("DELETE FROM workload_criticality").WillReturnResult(sqlmock.NewResult(0, 0))

	st, err := DiscoverWorkloadCriticality(context.Background(), db, testAccount, "tenant-1")

	require.NoError(t, err)
	assert.False(t, st.Skipped)
	assert.Equal(t, 1, st.Tiered)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// End-to-end through the sweep: a `medium` verdict on an ingress-backed candidate must still persist
// the measured `high`, with fact_signal provenance. This is the reported bug, at the sweep level.
func TestSweepKeepsMeasuredTierWhenClassifierAnswersMedium(t *testing.T) {
	db, mock := newSweepMock(t)
	const crid = "11111111-1111-1111-1111-111111111111"
	stubClassifier(t, func() (map[string]llmCriticalityVerdict, error) {
		return map[string]llmCriticalityVerdict{
			crid: {Criticality: CriticalityMedium, Reason: "standard application service"},
		}, nil
	})

	expectGraphFacts(mock, crid)
	expectWorkloads(mock, crid)
	mock.ExpectExec("INSERT INTO workload_criticality").
		WithArgs("tenant-1", testAccount, crid, "prod", CriticalityHigh, CriticalitySourceFact,
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("DELETE FROM workload_criticality").WillReturnResult(sqlmock.NewResult(0, 0))

	st, err := DiscoverWorkloadCriticality(context.Background(), db, testAccount, "tenant-1")

	require.NoError(t, err)
	assert.Equal(t, 1, st.Tiered, "the ingress-backed workload must keep its row")
	assert.Equal(t, 1, st.NoOpinion)
	assert.Equal(t, 0, st.Demoted)
	assert.NoError(t, mock.ExpectationsWereMet())
}
