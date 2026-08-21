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

func TestDedupWindowForSource(t *testing.T) {
	t.Run("nil source falls back to the default", func(t *testing.T) {
		assert.Equal(t, DefaultDedupWindow, dedupWindowForSource(nil))
	})

	t.Run("unlisted source falls back to the default", func(t *testing.T) {
		assert.Equal(t, DefaultDedupWindow, dedupWindowForSource(strPtr("prometheus")))
	})

	// Deliberately exercises the lookup against its own table rather than writing to
	// dedupWindowBySource: the package-level map is written once at init and read by every
	// event, so a test that mutates it would be the only thing making it racy.
	t.Run("listed source wins over the default", func(t *testing.T) {
		const source = "test-source-dedup-window"
		windows := map[string]time.Duration{source: 72 * time.Hour}

		assert.Equal(t, 72*time.Hour, dedupWindowFrom(windows, strPtr(source)))
		assert.Equal(t, DefaultDedupWindow, dedupWindowFrom(windows, strPtr("other-source")),
			"an override for one source must not affect another")
	})

	t.Run("production lookup reads the package-level table", func(t *testing.T) {
		assert.Empty(t, dedupWindowBySource,
			"no source overrides the default yet; add one here when that changes")
	})
}

// TestDetectAndRecordDuplicate_DedupWindow_E2E proves the window decides chain continuation:
// a re-fire inside the window extends the chain (occurrence 2, so the auto-duplicate rule
// fires), one past the window opens a fresh chain (occurrence 1, so it is triaged as a new
// incident) while still pointing back at the old chain via previous_event_id.
//
// Isolation matches TestDetectAndRecordDuplicate_ReprocessReturnsStoredOccurrence_E2E: rows
// are seeded for a real account at a fixed year-2020 timestamp inside a transaction that is
// always rolled back, so nothing persists and no real event shares the fingerprint.
func TestDetectAndRecordDuplicate_DedupWindow_E2E(t *testing.T) {
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

	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	// endsAt nil models a source that never sends an end (the common case), which is what
	// makes the window fall back to the previous occurrence's start.
	seed := func(tx *sqlx.Tx, fp string, startsAt time.Time, endsAt *time.Time) *models.Event {
		id := uuid.New().String()
		_, err := tx.ExecContext(ctx, `
			INSERT INTO events (
				id, finding_id, title, aggregation_key, finding_type, priority,
				subject_name, cluster, evidences,
				tenant, cloud_account_id, fingerprint, starts_at, ends_at, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10,$11,$12,$13,$14,$13)`,
			id, "fid-"+id, "e2e dedup window", "test-alert", "test", "HIGH",
			"svc", "test-cluster", "[]",
			tenant, account, fp, startsAt, endsAt)
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

	t.Run("gap inside the window continues the chain", func(t *testing.T) {
		tx, err := dbms.Db.BeginTxx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()

		const fp = "fp-e2e-dedup-window-inside"
		first := seed(tx, fp, base, nil)
		// One second short of the window, so the boundary itself stays a duplicate.
		second := seed(tx, fp, base.Add(DefaultDedupWindow-time.Second), nil)

		occ, err := detectAndRecordDuplicate(ctx, tx, first)
		require.NoError(t, err)
		require.Equal(t, 1, occ, "first event opens the chain")

		occ, err = detectAndRecordDuplicate(ctx, tx, second)
		require.NoError(t, err)
		assert.Equal(t, 2, occ, "a re-fire inside the window must extend the chain")

		var firstEventID string
		require.NoError(t, sqlx.GetContext(ctx, tx, &firstEventID,
			`SELECT first_event_id::text FROM event_duplicates WHERE event_id = $1`, second.Id))
		assert.Equal(t, first.Id, firstEventID, "chain head must be unchanged")
	})

	t.Run("gap past the window opens a new chain", func(t *testing.T) {
		tx, err := dbms.Db.BeginTxx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()

		const fp = "fp-e2e-dedup-window-past"
		first := seed(tx, fp, base, nil)
		second := seed(tx, fp, base.Add(DefaultDedupWindow+time.Second), nil)

		occ, err := detectAndRecordDuplicate(ctx, tx, first)
		require.NoError(t, err)
		require.Equal(t, 1, occ, "first event opens the chain")

		occ, err = detectAndRecordDuplicate(ctx, tx, second)
		require.NoError(t, err)
		assert.Equal(t, 1, occ,
			"a re-fire past the window must open a new chain, so the auto-duplicate rule (occurrence>1) does not fire")

		var row struct {
			FirstEventID    string `db:"first_event_id"`
			PreviousEventID string `db:"previous_event_id"`
		}
		require.NoError(t, sqlx.GetContext(ctx, tx, &row,
			`SELECT first_event_id::text, previous_event_id::text
			 FROM event_duplicates WHERE event_id = $1`, second.Id))
		assert.Equal(t, second.Id, row.FirstEventID, "new chain heads itself")
		assert.Equal(t, first.Id, row.PreviousEventID, "new chain keeps a reference to the old one")
	})

	// The window is start-to-start on purpose: ends_at is not trustworthy across sources
	// (see detectAndRecordDuplicate). This pins that decision — an ends_at far in the
	// future, which is what the `anomaly` source actually emits for a third of its rows,
	// must not hold the chain open past the window.
	t.Run("a far-future ends_at on the previous occurrence does not hold the chain open", func(t *testing.T) {
		tx, err := dbms.Db.BeginTxx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()

		const fp = "fp-e2e-dedup-window-bogus-endsat"
		bogusEnd := base.Add(76 * 24 * time.Hour) // the observed p95 duration on `anomaly`
		first := seed(tx, fp, base, &bogusEnd)
		second := seed(tx, fp, base.Add(DefaultDedupWindow+time.Second), nil)

		occ, err := detectAndRecordDuplicate(ctx, tx, first)
		require.NoError(t, err)
		require.Equal(t, 1, occ, "first event opens the chain")

		occ, err = detectAndRecordDuplicate(ctx, tx, second)
		require.NoError(t, err)
		assert.Equal(t, 1, occ,
			"the window is start-to-start, so an implausible ends_at cannot suppress the break")
	})

	t.Run("out-of-order delivery never opens a new chain", func(t *testing.T) {
		tx, err := dbms.Db.BeginTxx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()

		const fp = "fp-e2e-dedup-window-reorder"
		first := seed(tx, fp, base, nil)
		// Arrives after `first` but is stamped a week EARLIER: the gap is negative, which
		// must not be read as "the chain went quiet for a week".
		late := seed(tx, fp, base.Add(-7*24*time.Hour), nil)

		occ, err := detectAndRecordDuplicate(ctx, tx, first)
		require.NoError(t, err)
		require.Equal(t, 1, occ, "first event opens the chain")

		occ, err = detectAndRecordDuplicate(ctx, tx, late)
		require.NoError(t, err)
		assert.Equal(t, 2, occ, "a negative gap must extend the chain, not restart it")
	})
}
