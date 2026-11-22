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

// A chain breaks at an event resolved for the remediation loop (nb_resolved_by + RESOLVED), whatever its
// occurrence, only with the tenant's loop flag on; with it off the chain query is the one it always was, so a
// flapping alert pays nothing for the loop.
func TestChainBreaksAtLoopResolveOnlyWithTheFlag_DB(t *testing.T) {
	db := muteTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(`
		CREATE TABLE events (
			id uuid PRIMARY KEY, starts_at timestamp, nb_status text,
			nb_status_changed_at timestamp, labels jsonb, updated_at timestamp,
			fingerprint text, cloud_account_id uuid
		);
		CREATE TABLE event_duplicates (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(), event_id uuid NOT NULL,
			fingerprint text NOT NULL, cloud_account_id uuid NOT NULL, tenant_id uuid NOT NULL,
			first_event_id uuid NOT NULL, previous_event_id uuid NOT NULL,
			occurrence_number integer NOT NULL, time_since_first_seconds integer NOT NULL,
			time_since_previous_seconds integer NOT NULL,
			created_at timestamp NOT NULL DEFAULT now(), absolute_first_seen_at timestamp,
			UNIQUE (event_id, cloud_account_id)
		)`)
	require.NoError(t, err)

	run := func(t *testing.T, loopOn bool, resolvedAgo time.Duration) (occurrence int, refire string) {
		orig := remediationLoopOnForTenant
		remediationLoopOnForTenant = func(string) bool { return loopOn }
		t.Cleanup(func() { remediationLoopOnForTenant = orig })

		tenant, account, fp := uuid.New().String(), uuid.New().String(), "fp-"+uuid.New().String()
		now := time.Now().UTC()
		seed := func(startsAt time.Time) *models.Event {
			id := uuid.New().String()
			_, err := db.Exec(`INSERT INTO events (id, starts_at, nb_status, labels, fingerprint, cloud_account_id)
				VALUES ($1, $2, 'OPEN', '{}', $3, $4)`, id, startsAt, fp, account)
			require.NoError(t, err)
			return &models.Event{Id: id, Tenant: &tenant, CloudAccountId: &account, Fingerprint: strPtr(fp), StartsAt: &startsAt, CreatedAt: &startsAt}
		}
		first, second := seed(now.Add(-2*time.Hour)), seed(now.Add(-90*time.Minute))
		_, err := detectAndRecordDuplicate(ctx, db, first)
		require.NoError(t, err)
		occ, err := detectAndRecordDuplicate(ctx, db, second)
		require.NoError(t, err)
		require.Equal(t, 2, occ)
		// The 2nd occurrence was resolved for the loop (a person confirmed "Likely fixed").
		_, err = db.Exec(`UPDATE events SET nb_status = 'RESOLVED', nb_status_changed_at = NOW() - make_interval(secs => $2),
			labels = jsonb_build_object($3::text, '{"run_id":"run-1","verdict":"fixed"}') WHERE id = $1`,
			second.Id, resolvedAgo.Seconds(), LabelResolvedBy)
		require.NoError(t, err)

		next := seed(now)
		occurrence, err = detectAndRecordDuplicate(ctx, db, next)
		require.NoError(t, err)
		require.NoError(t, db.Get(&refire, `SELECT COALESCE(labels->>$2, '') FROM events WHERE id = $1`, next.Id, LabelRefire))
		return occurrence, refire
	}

	t.Run("flag on: the next firing starts a new chain and is labelled a re-fire", func(t *testing.T) {
		occ, refire := run(t, true, 30*time.Minute)
		assert.Equal(t, 1, occ)
		assert.Contains(t, refire, `"previous_run_id":"run-1"`)
	})
	t.Run("flag off: the chain carries on as before the loop", func(t *testing.T) {
		occ, refire := run(t, false, 30*time.Minute)
		assert.Equal(t, 3, occ)
		assert.Empty(t, refire)
	})
	t.Run("flag on: a resolve older than the dedup window is not read", func(t *testing.T) {
		occ, refire := run(t, true, DefaultDedupWindow+time.Hour)
		assert.Equal(t, 3, occ)
		assert.Empty(t, refire)
	})
}

// The remediation target read says whether the main event a duplicate follows is still firing, so a confirmed
// resolve on the duplicate leaves a firing main open (ApplyRemediationVerdict).
func TestReadRemediationTargetReportsAFiringMain_DB(t *testing.T) {
	db := muteTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(`
		CREATE TABLE events (id uuid PRIMARY KEY, cloud_account_id uuid, nb_status text, status text, labels jsonb, fingerprint text);
		CREATE TABLE event_duplicates (event_id uuid, cloud_account_id uuid, first_event_id uuid, occurrence_number integer);
		CREATE TABLE event_log_analysis (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), event_id uuid, analysis_type text, status text, analysis text);
		CREATE TABLE event_analysis_mapping (event_id uuid, analysis_type text, analysis_id uuid)`)
	require.NoError(t, err)
	account, main, dup := uuid.New().String(), uuid.New().String(), uuid.New().String()
	_, err = db.Exec(`INSERT INTO events (id, cloud_account_id, nb_status, status, labels, fingerprint) VALUES
		($1, $3, 'OPEN', 'FIRING', '{}', 'fp-main'),
		($2, $3, 'DUPLICATE', 'RESOLVED', jsonb_build_object('nb_duplicate_of', $1::text), 'fp-dup')`, main, dup, account)
	require.NoError(t, err)

	got, err := readRemediationTarget(ctx, db, account, dup)
	require.NoError(t, err)
	assert.Equal(t, main, got.Primary)
	assert.False(t, got.Firing)
	got, err = readRemediationTarget(ctx, db, account, main)
	require.NoError(t, err)
	assert.True(t, got.Firing)
}

// A person's resolve is refused once the alert fired again after the run's check ended (the tab's "came back"), and
// goes on while every firing started before it: the rule llm-server's remediation read uses.
func TestConfirmRemediationResolveRefusedAfterAReFire_DB(t *testing.T) {
	db := muteTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(`
		CREATE TABLE events (id uuid PRIMARY KEY, cloud_account_id uuid, nb_status text, status text, labels jsonb,
			fingerprint text, starts_at timestamp, created_at timestamp DEFAULT now());
		CREATE TABLE event_resolution (id uuid PRIMARY KEY, event_id uuid, type text, data jsonb)`)
	require.NoError(t, err)
	account, ev, run := uuid.New().String(), uuid.New().String(), uuid.New().String()
	verdictAt := time.Now().UTC().Add(-10 * time.Minute)
	_, err = db.Exec(`INSERT INTO events (id, cloud_account_id, nb_status, status, labels, fingerprint, starts_at)
		VALUES ($1, $2, 'OPEN', 'RESOLVED', '{}', 'fp-refire', $3)`, ev, account, verdictAt.Add(-time.Hour))
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO event_resolution (id, event_id, type, data) VALUES ($1, $2, 'CommandExecution',
		jsonb_build_object('followup', jsonb_build_object('verdict', 'fixed', 'verdict_at', $3::text)))`, run, ev, verdictAt.Format(time.RFC3339Nano))
	require.NoError(t, err)
	fired := func() bool {
		var r confirmableRun
		require.NoError(t, db.GetContext(ctx, &r, confirmableRunSQL, run, account))
		return r.FiredAgain
	}
	assert.False(t, fired(), "only the run's own firing, which started before the check")

	// A firing of another alert, and one of this alert that started before the check, do not count.
	_, err = db.Exec(`INSERT INTO events (id, cloud_account_id, labels, fingerprint, starts_at) VALUES
		($1, $3, '{}', 'fp-other', $4), ($2, $3, '{}', 'fp-refire', $5)`,
		uuid.New().String(), uuid.New().String(), account, verdictAt.Add(time.Minute), verdictAt.Add(-time.Minute))
	require.NoError(t, err)
	assert.False(t, fired())

	// A duplicate of the run's event that started after the check counts.
	_, err = db.Exec(`INSERT INTO events (id, cloud_account_id, labels, fingerprint, starts_at)
		VALUES ($1, $2, jsonb_build_object('nb_duplicate_of', $3::text), 'fp-dup', $4)`, uuid.New().String(), account, ev, verdictAt.Add(time.Minute))
	require.NoError(t, err)
	assert.True(t, fired())
}
