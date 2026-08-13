package triage

import (
	"context"
	"os"
	"testing"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/internal/testenv"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDetectAndRecordCorrelations_E2E exercises the full correlation path
// (recent-events query -> batched existence check -> filter -> batched insert)
// against a real Metastore, inside a transaction that is ALWAYS rolled back so
// nothing persists. It is the end-to-end proof for the N+1 fix: the unit tests
// cover the query/arg building in isolation; this asserts the wired path writes
// the correct event_correlations rows and dedups on re-run.
//
// Isolation: rows are seeded for a real account (TEST_ACCOUNT_ID, to satisfy the
// events -> cloud_accounts / tenant FKs) but at a fixed year-2020 timestamp, so
// the +/-10min correlation window contains only the seeded events and never a
// real one. All asserts are scoped to the seeded triaged event id.
func TestDetectAndRecordCorrelations_E2E(t *testing.T) {
	if os.Getenv("TEST_LIVE_CORRELATION") != "1" {
		t.Skip("set TEST_LIVE_CORRELATION=1 to run (requires local Metastore connection)")
	}

	env := testenv.RequireEnv(t, "TEST_ACCOUNT_ID")
	account := env["TEST_ACCOUNT_ID"]

	dbms, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "failed to connect to Metastore — check .env")
	ctx := context.Background()

	// Derive the account's tenant so the events.tenant FK resolves without
	// putting a real tenant id in source.
	var tenant string
	require.NoError(t, dbms.Db.GetContext(ctx, &tenant,
		`SELECT tenant::text FROM cloud_accounts WHERE id = $1`, account),
		"TEST_ACCOUNT_ID not found in cloud_accounts")

	tx, err := dbms.Db.BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }() // nothing persists, pass or fail

	const (
		nsE2E     = "ns-e2e-correlation"
		svcKeyE2E = "svc-e2e-correlation"
	)
	// Fixed old timestamp: the account has no real events near 2020, so the
	// correlation window is populated only by what we seed here.
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	seed := func(fp string, startsAt time.Time) string {
		id := uuid.New().String()
		_, err := tx.ExecContext(ctx, `
			INSERT INTO events (
				id, finding_id, title, aggregation_key, finding_type, priority,
				subject_name, subject_namespace, service_key, cluster, evidences,
				tenant, cloud_account_id, fingerprint, starts_at, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$13,$14,$15,$15)`,
			id, "fid-"+id, "e2e correlation", "test-alert", "test", "HIGH",
			"svc", nsE2E, svcKeyE2E, "test-cluster", "[]",
			tenant, account, fp, startsAt)
		require.NoError(t, err, "seed event %s", id)
		return id
	}

	// Triaged event + two candidates in the same namespace and service, within
	// 2 minutes -> each scores 0.30 (time) + 0.10 (namespace) + 0.15 (service) =
	// 0.55, above the 0.50 threshold, type "same_service".
	triagedID := seed("fp-e2e-triaged", base)
	cand1ID := seed("fp-e2e-cand1", base.Add(-2*time.Minute)) // offset +2
	cand2ID := seed("fp-e2e-cand2", base.Add(-1*time.Minute)) // offset +1

	triaged := &models.Event{
		Id:               triagedID,
		Tenant:           &tenant,
		CloudAccountId:   &account,
		Fingerprint:      strPtr("fp-e2e-triaged"),
		StartsAt:         &base,
		SubjectNamespace: strPtr(nsE2E),
		ServiceKey:       strPtr(svcKeyE2E),
	}

	// --- First run: correlations are created. ---
	corrType, corrScore, err := detectAndRecordCorrelations(ctx, tx, triaged)
	require.NoError(t, err)
	assert.Equal(t, "same_service", corrType, "highest correlation type")
	assert.GreaterOrEqual(t, corrScore, 0.50, "highest correlation score")

	fetch := func() []corrRow {
		var rows []corrRow
		require.NoError(t, sqlx.SelectContext(ctx, tx, &rows,
			`SELECT event_id, related_event_id, correlation_type, correlation_score, time_offset_minutes
			 FROM event_correlations
			 WHERE cloud_account_id = $1 AND (event_id = $2 OR related_event_id = $2)
			 ORDER BY event_id, related_event_id`, account, triagedID))
		return rows
	}

	rows := fetch()
	// 2 candidates × bidirectional = 4 rows.
	require.Len(t, rows, 4, "expected 4 bidirectional correlation rows")

	// Every row is same_service and above threshold.
	for _, r := range rows {
		assert.Equal(t, "same_service", r.Type)
		assert.GreaterOrEqual(t, r.Score, 0.50)
	}

	// Both directions exist for each candidate, with negated offsets.
	assert.True(t, hasPair(rows, triagedID, cand1ID, 2) && hasPair(rows, cand1ID, triagedID, -2),
		"cand1 must have forward(+2) and reverse(-2) rows")
	assert.True(t, hasPair(rows, triagedID, cand2ID, 1) && hasPair(rows, cand2ID, triagedID, -1),
		"cand2 must have forward(+1) and reverse(-1) rows")

	// --- Second run: fingerprint-pair dedup must add nothing. ---
	_, _, err = detectAndRecordCorrelations(ctx, tx, triaged)
	require.NoError(t, err)
	assert.Len(t, fetch(), 4, "re-run must dedup by fingerprint pair, not add rows")
}

// corrRow is a scanned event_correlations row used by the e2e assertions.
type corrRow struct {
	EventID   string  `db:"event_id"`
	RelatedID string  `db:"related_event_id"`
	Type      string  `db:"correlation_type"`
	Score     float64 `db:"correlation_score"`
	Offset    int     `db:"time_offset_minutes"`
}

func hasPair(rows []corrRow, eventID, relatedID string, offset int) bool {
	for _, r := range rows {
		if r.EventID == eventID && r.RelatedID == relatedID && r.Offset == offset {
			return true
		}
	}
	return false
}

// TestDetectAndRecordDuplicate_ReprocessReturnsStoredOccurrence_E2E reproduces the
// incident where re-processing a chain's FIRST event after later duplicates were
// recorded computed occurrence = latest+1 (instead of the stored 1), letting the
// system occurrence>1 auto-duplicate rule mark the chain's ORIGINAL as a duplicate
// of itself — with no linked event, leaving the whole chain DUPLICATE and nothing
// open. Re-processing happens in production via rescoreFreshlyMintedClass and
// redelivered post-process messages, so detectAndRecordDuplicate must be idempotent.
//
// Isolation: same pattern as the correlation e2e above — seeded for a real account
// at a fixed year-2020 timestamp inside a transaction that is ALWAYS rolled back.
func TestDetectAndRecordDuplicate_ReprocessReturnsStoredOccurrence_E2E(t *testing.T) {
	if os.Getenv("TEST_LIVE_DEDUP") != "1" {
		t.Skip("set TEST_LIVE_DEDUP=1 to run (requires local Metastore connection)")
	}

	env := testenv.RequireEnv(t, "TEST_ACCOUNT_ID")
	account := env["TEST_ACCOUNT_ID"]

	dbms, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "failed to connect to Metastore — check .env")
	ctx := context.Background()

	var tenant string
	require.NoError(t, dbms.Db.GetContext(ctx, &tenant,
		`SELECT tenant::text FROM cloud_accounts WHERE id = $1`, account),
		"TEST_ACCOUNT_ID not found in cloud_accounts")

	tx, err := dbms.Db.BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }() // nothing persists, pass or fail

	const fp = "fp-e2e-dedup-chain"
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	seed := func(startsAt time.Time) *models.Event {
		id := uuid.New().String()
		_, err := tx.ExecContext(ctx, `
			INSERT INTO events (
				id, finding_id, title, aggregation_key, finding_type, priority,
				subject_name, cluster, evidences,
				tenant, cloud_account_id, fingerprint, starts_at, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10,$11,$12,$13,$13)`,
			id, "fid-"+id, "e2e dedup", "test-alert", "test", "HIGH",
			"svc", "test-cluster", "[]",
			tenant, account, fp, startsAt)
		require.NoError(t, err, "seed event %s", id)
		return &models.Event{
			Id:             id,
			Tenant:         &tenant,
			CloudAccountId: &account,
			Fingerprint:    strPtr(fp),
			StartsAt:       &startsAt,
			CreatedAt:      &startsAt,
		}
	}

	first := seed(base)
	second := seed(base.Add(3 * time.Second))

	occ, err := detectAndRecordDuplicate(ctx, tx, first)
	require.NoError(t, err)
	assert.Equal(t, 1, occ, "first event opens the chain")

	occ, err = detectAndRecordDuplicate(ctx, tx, second)
	require.NoError(t, err)
	assert.Equal(t, 2, occ, "second event continues the chain")

	// Re-process the FIRST event (rescore-on-mint / message redelivery). Before the
	// idempotency guard this returned latest+1 = 3, which is occurrence>1 — the
	// exact value that let the auto-duplicate rule fire on the chain's original.
	occ, err = detectAndRecordDuplicate(ctx, tx, first)
	require.NoError(t, err)
	assert.Equal(t, 1, occ, "re-processing the chain-first event must return its stored occurrence")

	// Re-processing a later chain member must be stable too.
	occ, err = detectAndRecordDuplicate(ctx, tx, second)
	require.NoError(t, err)
	assert.Equal(t, 2, occ, "re-processing a duplicate must return its stored occurrence")

	// The stored chain is unchanged: exactly two rows, occurrences 1 and 2.
	var occs []int
	require.NoError(t, sqlx.SelectContext(ctx, tx, &occs,
		`SELECT occurrence_number FROM event_duplicates
		 WHERE fingerprint = $1 AND cloud_account_id = $2 ORDER BY occurrence_number`, fp, account))
	assert.Equal(t, []int{1, 2}, occs, "re-runs must not add chain rows")
}
