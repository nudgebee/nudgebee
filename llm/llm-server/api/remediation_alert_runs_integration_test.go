package api

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A write approved in the alert's chat that is no step of the plan stays recorded, but never supersedes the run of
// a plan step: the step's follow-up still judges the step. A chat write tied to a plan option does supersede it.
func TestChatWriteOutsideThePlanDoesNotSupersedeAStep(t *testing.T) {
	f := newIncidentFixture(t)
	_, err := f.db.Exec(`CREATE TABLE event_resolution (id uuid PRIMARY KEY, event_id uuid NOT NULL, type text NOT NULL, status text NOT NULL,
		data jsonb, created_at timestamp NOT NULL)`)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Second)
	event := f.event("OPEN", now.Add(-time.Hour))
	run := func(at time.Time, data string) string {
		id := uuid.NewString()
		_, err := f.db.Exec(`INSERT INTO event_resolution (id, event_id, type, status, data, created_at) VALUES ($1, $2, 'CommandExecution', 'Success', $3, $4)`,
			id, event, data, at)
		require.NoError(t, err)
		return id
	}
	stepAt := now.Add(-30 * time.Minute)
	step := followupRun{Id: run(stepAt, `{"source":"run_step","step":"pv-1:1","plan_version":"pv-1"}`), EventId: event,
		AccountId: f.account, Fingerprint: incidentFingerprint, CreatedAt: stepAt}

	run(now.Add(-20*time.Minute), `{"source":"chat","tool":"kubectl"}`)
	newer, _, err := alertRuns(f.db, step)
	require.NoError(t, err)
	assert.False(t, newer, "a chat write outside the plan must not supersede the step's run")

	run(now.Add(-10*time.Minute), `{"source":"chat","tool":"kubectl","option":1}`)
	newer, _, err = alertRuns(f.db, step)
	require.NoError(t, err)
	assert.True(t, newer, "a chat write of a plan option is a newer attempt")
}
