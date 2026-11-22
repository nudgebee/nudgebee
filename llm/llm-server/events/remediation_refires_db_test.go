package events

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Re-fires are read newest first, one already compared on its current plan
// is not read again (until the plan changes), and a malformed label is skipped without failing the statement.
func TestRefiresToCompare(t *testing.T) {
	db := planEventDB(t)
	_, err := db.Exec(`ALTER TABLE events ADD COLUMN tenant uuid, ADD COLUMN created_at timestamp;
		ALTER TABLE event_log_analysis ADD COLUMN updated_at timestamp, ADD COLUMN recorded_at timestamp;
		CREATE TABLE feature_flag (feature_id text, tenant_id uuid, account_id uuid, status text);
		CREATE TABLE event_resolution (id uuid PRIMARY KEY, event_id uuid)`)
	require.NoError(t, err)
	tenant, account := uuid.NewString(), uuid.NewString()
	_, err = db.Exec(`INSERT INTO feature_flag VALUES ($1, $2, NULL, 'enabled')`, RemediationLoopFeature, tenant)
	require.NoError(t, err)

	// The old chain: its resolved event and the run that resolved it.
	resolved, run := uuid.NewString(), uuid.NewString()
	_, err = db.Exec(`INSERT INTO events (id, cloud_account_id, tenant, created_at, labels) VALUES ($1, $2, $3, LOCALTIMESTAMP - interval '2 hours',
		jsonb_build_object('nb_resolved_by', $4::text))`, resolved, account, tenant, `{"run_id":"`+run+`","verdict":"fixed","cause_key":null}`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO event_resolution VALUES ($1, $2)`, run, resolved)
	require.NoError(t, err)

	plan := `{"cause_key":{"what":"deploy api","how":"oom","against":"memory limit"}}`
	refire := func(ago time.Duration, label string) string {
		id := uuid.NewString()
		_, err := db.Exec(`INSERT INTO events (id, cloud_account_id, tenant, created_at, labels) VALUES ($1, $2, $3,
			LOCALTIMESTAMP - make_interval(secs => $4), jsonb_build_object('nb_refire', $5::text))`, id, account, tenant, ago.Seconds(), label)
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO event_log_analysis (id, event_id, cloud_account_id, analysis_type, status, analysis, updated_at)
			VALUES ($1, $2, $3, 'remediation', 'COMPLETED', $4, LOCALTIMESTAMP)`, uuid.NewString(), id, account, plan)
		require.NoError(t, err)
		return id
	}
	older := refire(3*time.Hour, `{"kind":"unknown","previous_run_id":"`+run+`"}`)
	newer := refire(time.Hour, `{"kind":"unknown","previous_run_id":"`+run+`"}`)
	refire(30*time.Minute, `{kind: unknown, previous_run_id: {not json`)                                                                   // malformed
	refire(20*time.Minute, `{"compared_plan":"`+PlanVersion(plan)+`","kind":"unknown","previous_run_id":"`+run+`"}`)                       // compared on this plan
	changed := refire(10*time.Minute, `{"compared_plan":"`+PlanVersion("an older plan")+`","kind":"unknown","previous_run_id":"`+run+`"}`) // plan changed since
	refire(5*time.Minute, `{"kind":"fix_did_not_hold","previous_run_id":"`+run+`"}`)                                                       // already decided

	got, err := RefiresToCompare(t.Context(), db, 50)
	require.NoError(t, err, "a malformed label does not fail the statement")
	ids := []string{}
	for _, r := range got {
		ids = append(ids, r.EventId)
		assert.Equal(t, run, r.PreviousRunId)
		assert.Equal(t, PlanVersion(plan), r.PlanVersion, "the SQL hash is PlanVersion")
		assert.Contains(t, r.ResolvedBy, run)
	}
	assert.Equal(t, []string{changed, newer, older}, ids, "newest first; compared on the current plan, malformed and decided ones left out")

	limited, err := RefiresToCompare(t.Context(), db, 1)
	require.NoError(t, err)
	require.Len(t, limited, 1)
	assert.Equal(t, changed, limited[0].EventId, "under the limit the newest re-fire is compared first")
}
