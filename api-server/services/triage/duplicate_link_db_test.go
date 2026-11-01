package triage

import (
	"context"
	"testing"
	"time"

	"nudgebee/services/internal/database/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A fingerprint has one occurrence_number = 1 row per CHAIN, not one in total: a chain
// restarts when its first event is RESOLVED or the dedup window elapses. The duplicate link
// used to be read as "the occurrence-1 row of this fingerprint" with no ordering, so once a
// fingerprint had restarted it resolved to whichever chain Postgres returned first — in
// practice the oldest — and a re-fire was recorded as a duplicate of an event from a chain
// that had already ended.
//
// Which row an unordered LIMIT 1 returns is decided by the planner, so this runs against a
// real Postgres. Named _DB to match the package's other real-database tests; skipped locally
// unless a database is configured.
func TestDuplicateLinksToFirstEventOfItsOwnChain_DB(t *testing.T) {
	// muteTestDB is the package's throwaway-schema helper. It already mirrors
	// event_triage_rules, which the rule-creation case below writes to.
	db := muteTestDB(t)
	ctx := context.Background()

	// Only the columns the duplicate-link path reads and writes. event_duplicates keeps
	// its real unique constraint and fingerprint index, since those decide the lookup.
	_, err := db.Exec(`
		CREATE TABLE events (
			id uuid PRIMARY KEY, starts_at timestamp, nb_status text,
			nb_status_changed_at timestamp, labels jsonb, updated_at timestamp
		);
		CREATE TABLE event_duplicates (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(), event_id uuid NOT NULL,
			fingerprint text NOT NULL, cloud_account_id uuid NOT NULL, tenant_id uuid NOT NULL,
			first_event_id uuid NOT NULL, previous_event_id uuid NOT NULL,
			occurrence_number integer NOT NULL, time_since_first_seconds integer NOT NULL,
			time_since_previous_seconds integer NOT NULL,
			created_at timestamp NOT NULL DEFAULT now(), absolute_first_seen_at timestamp,
			UNIQUE (event_id, cloud_account_id)
		);
		CREATE INDEX ON event_duplicates (fingerprint, cloud_account_id);
		CREATE TABLE event_classification (
			id uuid PRIMARY KEY, event_id uuid, cloud_account_id uuid, tenant_id uuid,
			classification text, original_priority text, corrected_priority text,
			priority_direction text, reason_code text, reason_text text, apply_scope text,
			apply_until timestamp, linked_event_id uuid, classified_by uuid,
			classified_at timestamp, original_score int, rule_id uuid,
			UNIQUE (event_id, cloud_account_id)
		);
		CREATE TABLE event_history (
			id uuid PRIMARY KEY, event_id uuid, cloud_account_id uuid, tenant_id uuid,
			change_type text, changed_by uuid, new_value jsonb, change_reason text
		)`)
	require.NoError(t, err)

	tenant, account := uuid.New().String(), uuid.New().String()
	const fp = "fp-duplicate-link"
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	seed := func(startsAt time.Time) *models.Event {
		id := uuid.New().String()
		_, err := db.Exec(`INSERT INTO events (id, starts_at, nb_status, labels) VALUES ($1, $2, 'OPEN', '{}')`,
			id, startsAt)
		require.NoError(t, err)
		return &models.Event{
			Id:             id,
			Tenant:         &tenant,
			CloudAccountId: &account,
			Fingerprint:    strPtr(fp),
			StartsAt:       &startsAt,
			CreatedAt:      &startsAt,
		}
	}
	record := func(event *models.Event, wantOccurrence int) {
		occ, err := detectAndRecordDuplicate(ctx, db, event)
		require.NoError(t, err)
		require.Equal(t, wantOccurrence, occ)
	}

	// Two chains of the same fingerprint, written by the production path: the gap past
	// the dedup window is what ends the first and opens the second.
	oldFirst := seed(base)
	oldRefire := seed(base.Add(time.Minute))
	newFirst := seed(base.Add(DefaultDedupWindow + time.Hour))
	newRefire := seed(base.Add(DefaultDedupWindow + 2*time.Hour))
	record(oldFirst, 1)
	record(oldRefire, 2)
	record(newFirst, 1)
	record(newRefire, 2)

	var chainHeads int
	require.NoError(t, db.Get(&chainHeads, `
		SELECT count(*) FROM event_duplicates
		WHERE fingerprint = $1 AND cloud_account_id = $2 AND occurrence_number = 1`, fp, account))
	require.Equal(t, 2, chainHeads, "each chain has its own occurrence-1 row")

	autoDuplicate := func(event *models.Event) *AutoClassification {
		ac := &AutoClassification{
			Classification: ClassificationDuplicate,
			ReasonCode:     "duplicate_incident",
			NewStatus:      NBStatusDuplicate,
		}
		require.NoError(t, applyAutoClassificationToEvent(ctx, db, event, ac))
		return ac
	}
	// What the event carries afterwards: the label the UI shows and the classification record.
	storedLinks := func(eventID string) (label, classified string) {
		require.NoError(t, db.Get(&label,
			`SELECT COALESCE(labels->>'nb_duplicate_of', '') FROM events WHERE id = $1`, eventID))
		require.NoError(t, db.Get(&classified,
			`SELECT linked_event_id::text FROM event_classification WHERE event_id = $1`, eventID))
		return label, classified
	}

	t.Run("a re-fire in the restarted chain links to that chain's first event", func(t *testing.T) {
		ac := autoDuplicate(newRefire)

		require.NotNil(t, ac.LinkedEventID)
		assert.Equal(t, newFirst.Id, *ac.LinkedEventID, "must not link to the chain that ended")
		label, classified := storedLinks(newRefire.Id)
		assert.Equal(t, newFirst.Id, label, "nb_duplicate_of label")
		assert.Equal(t, newFirst.Id, classified, "classification linked_event_id")
	})

	// Events are re-processed (rescore after an LLM verdict, a redelivered message), so the
	// link has to follow the event's own chain rather than whichever chain is newest.
	t.Run("a re-processed duplicate from the earlier chain keeps that chain's first event", func(t *testing.T) {
		ac := autoDuplicate(oldRefire)

		require.NotNil(t, ac.LinkedEventID)
		assert.Equal(t, oldFirst.Id, *ac.LinkedEventID)
		label, classified := storedLinks(oldRefire.Id)
		assert.Equal(t, oldFirst.Id, label, "nb_duplicate_of label")
		assert.Equal(t, oldFirst.Id, classified, "classification linked_event_id")
	})

	// The first event of a restarted chain is a new occurrence, not a repeat. A duplicate
	// rule with no occurrence condition still matches it, and it used to be closed as a
	// duplicate of the first event of the chain that ended.
	t.Run("the first event of the restarted chain stays open", func(t *testing.T) {
		autoDuplicate(newFirst)

		var status, label string
		require.NoError(t, db.Get(&status, `SELECT nb_status FROM events WHERE id = $1`, newFirst.Id))
		require.NoError(t, db.Get(&label,
			`SELECT COALESCE(labels->>'nb_duplicate_of', '') FROM events WHERE id = $1`, newFirst.Id))
		assert.Equal(t, "OPEN", status)
		assert.Empty(t, label, "nb_duplicate_of label")
	})

	t.Run("a rule created from a duplicate classification links to the classified event's chain", func(t *testing.T) {
		tx, err := db.BeginTxx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()

		picked := uuid.New().String()
		rule, err := createRuleFromClassificationTx(ctx, tx,
			&EventClassification{
				EventID:        newRefire.Id,
				CloudAccountID: account,
				TenantID:       tenant,
				Classification: ClassificationDuplicate,
				ReasonCode:     "duplicate_incident",
				ApplyScope:     ApplyScopeThisFingerprint,
				LinkedEventID:  &picked,
			},
			&eventBasicInfo{ID: newRefire.Id, Title: "duplicate link", Fingerprint: strPtr(fp), CloudAccountID: account, TenantID: tenant},
			uuid.New().String())
		require.NoError(t, err)

		action, err := ParseActionValue(rule.ActionValue)
		require.NoError(t, err)
		require.NotNil(t, action.LinkedEventID)
		assert.Equal(t, newFirst.Id, *action.LinkedEventID, "must not link to the chain that ended")
	})
}
