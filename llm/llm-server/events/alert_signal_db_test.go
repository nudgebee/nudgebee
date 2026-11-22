package events

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The alert's firings follow its workload: a rollout restart or a pod delete gives the replacement pod an alert
// of its own fingerprint, and that firing is the alert's too. Another workload's pod, or another alert on the same
// workload, is not. The rule's for: is read from event_rules.
func TestReadAlertSignal_FollowsTheWorkload(t *testing.T) {
	db := planEventDB(t)
	_, err := db.Exec(`ALTER TABLE events ADD COLUMN tenant uuid, ADD COLUMN fingerprint text, ADD COLUMN starts_at timestamp,
		ADD COLUMN ends_at timestamp, ADD COLUMN aggregation_key text, ADD COLUMN subject_type text, ADD COLUMN subject_name text,
		ADD COLUMN subject_namespace text, ADD COLUMN subject_owner text, ADD COLUMN subject_owner_kind text;
		CREATE TABLE event_rules (account_id uuid, alert text, duration text, enabled boolean, source text, updated_at timestamp)`)
	require.NoError(t, err)
	tenant, account := uuid.NewString(), uuid.NewString()
	t0 := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)
	add := func(fp, alert, pod, owner, status string, start time.Duration) string {
		id := uuid.NewString()
		_, err := db.Exec(`INSERT INTO events (id, cloud_account_id, tenant, fingerprint, starts_at, aggregation_key, status, labels,
			subject_type, subject_name, subject_namespace, subject_owner, subject_owner_kind)
			VALUES ($1, $2, $3, $4, $5, $6, $7, jsonb_build_object('alertname', $6::text), 'pod', $8, 'shop', $9, 'Deployment')`,
			id, account, tenant, fp, t0.Add(start), alert, status, pod, owner)
		require.NoError(t, err)
		return id
	}
	ev := add("fp-api-1", "KubePodCrashLooping", "api-1", "api", "RESOLVED", 0)
	replacement := add("fp-api-2", "KubePodCrashLooping", "api-2", "api", "FIRING", 20*time.Minute)
	add("fp-web-1", "KubePodCrashLooping", "web-1", "web", "FIRING", 20*time.Minute)        // another workload
	add("fp-api-2-oom", "KubeContainerOOMKilled", "api-2", "api", "FIRING", 20*time.Minute) // another alert
	add("fp-api-0", "KubePodCrashLooping", "api-0", "api", "RESOLVED", -time.Hour)          // before the event
	_, err = db.Exec(`INSERT INTO event_rules VALUES ($1, 'KubePodCrashLooping', '15m', true, 'prometheus', now())`, account)
	require.NoError(t, err)

	s, err := ReadAlertSignal(t.Context(), db, account, ev)
	require.NoError(t, err)
	ids := []string{}
	for _, f := range s.Firings {
		ids = append(ids, f.Id)
	}
	assert.Equal(t, []string{replacement, ev}, ids, "the replacement pod's firing, newest first; nothing else")
	assert.True(t, s.RuleForKnown)
	assert.Equal(t, 15*60, s.RuleForSeconds)

	// No rule: the for: is not known.
	_, err = db.Exec(`DELETE FROM event_rules`)
	require.NoError(t, err)
	s, err = ReadAlertSignal(t.Context(), db, account, ev)
	require.NoError(t, err)
	assert.False(t, s.RuleForKnown)
}

func TestRuleForSeconds(t *testing.T) {
	for in, want := range map[string]int{"15m": 900, "1h30m": 5400, "5m0s": 300, "": 0} {
		got, ok := ruleForSeconds(in)
		assert.True(t, ok, in)
		assert.Equal(t, want, got, in)
	}
	_, ok := ruleForSeconds("1d")
	assert.False(t, ok, "a duration Go cannot read is not known")
}

// The firings read are capped, newest first, but the run's own event is always among them: an alert that fired
// more often than the cap since must not lose the firing the verdict is about.
func TestAlertFiringsKeepTheRunsEventPastTheCap(t *testing.T) {
	db := planEventDB(t)
	_, err := db.Exec(`ALTER TABLE events ADD COLUMN tenant uuid, ADD COLUMN fingerprint text, ADD COLUMN starts_at timestamp,
		ADD COLUMN ends_at timestamp, ADD COLUMN aggregation_key text, ADD COLUMN subject_type text, ADD COLUMN subject_name text,
		ADD COLUMN subject_namespace text, ADD COLUMN subject_owner text, ADD COLUMN subject_owner_kind text`)
	require.NoError(t, err)
	tenant, account := uuid.NewString(), uuid.NewString()
	t0 := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)
	add := func(start time.Duration) string {
		id := uuid.NewString()
		_, err := db.Exec(`INSERT INTO events (id, cloud_account_id, tenant, fingerprint, starts_at, status, labels, subject_type, subject_name)
			VALUES ($1, $2, $3, 'fp-api', $4, 'RESOLVED', '{}', 'pod', 'api-1')`, id, account, tenant, t0.Add(start))
		require.NoError(t, err)
		return id
	}
	ev := add(0)
	newer := []string{add(3 * time.Minute), add(2 * time.Minute), add(time.Minute)}

	var ids []string
	require.NoError(t, db.Select(&ids, `SELECT id FROM (`+alertFiringsSQL+`) x`, account, ev, 2))
	assert.Equal(t, []string{newer[0], newer[1], ev}, ids, "the two newest, and the run's own event")
}
