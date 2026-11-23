package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// chainStubs stubs what queueChainPlannings reads and records the plans it asks for.
type chainStubs struct {
	firings  []events.ChainFiring
	main     map[string]string // firing -> plan event
	latest   map[string]*events.PlanningRequest
	needed   map[string]bool
	asked    []string
	askedIn  []planRequestInput
	neededOf []string
}

func stubChain(t *testing.T) *chainStubs {
	t.Helper()
	s := &chainStubs{main: map[string]string{}, latest: map[string]*events.PlanningRequest{}, needed: map[string]bool{}}
	stubLoopOn(t, true)
	swap(t, &readChainFirings, func(context.Context, *sqlx.DB) ([]events.ChainFiring, error) { return s.firings, nil })
	swap(t, &planEventOf, func(_ context.Context, _ sqlx.QueryerContext, _, eventId string) (string, error) {
		if m, ok := s.main[eventId]; ok {
			return m, nil
		}
		return eventId, nil
	})
	swap(t, &latestPlanningOf, func(_ context.Context, _ *sqlx.DB, _, eventId string) (*events.PlanningRequest, error) {
		return s.latest[eventId], nil
	})
	swap(t, &chainPlanNeeded, func(_ *security.RequestContext, _, planEvent string) (bool, string) {
		s.neededOf = append(s.neededOf, planEvent)
		return s.needed[planEvent], "written more than 24 h ago"
	})
	swap(t, &requestChainPlan, func(_ *security.RequestContext, _, eventId string, in planRequestInput) (events.PlanningRequest, string, error) {
		s.asked, s.askedIn = append(s.asked, eventId), append(s.askedIn, in)
		return events.PlanningRequest{Id: "p-" + eventId}, "", nil
	})
	return s
}

// A recurring alert's firing joins the chain and runs no investigation: the tick asks for the chain's plan, on its
// plan event, once, as an automatic request that may replace the out-of-date plan.
func TestRecurringFiringAsksForTheChainsPlanOnce(t *testing.T) {
	s := stubChain(t)
	s.firings = []events.ChainFiring{{EventId: "dup-859", AccountId: "a"}, {EventId: "dup-858", AccountId: "a"}, {EventId: "dup-336", AccountId: "a"}}
	s.main = map[string]string{"dup-859": "main-oom", "dup-858": "main-oom", "dup-336": "main-5xx"}
	s.needed = map[string]bool{"main-oom": true, "main-5xx": false}

	queueChainPlannings(nil)
	assert.Equal(t, []string{"main-oom"}, s.asked, "the plan event of the chain, once; a chain with current fixes is left alone")
	assert.Equal(t, []planRequestInput{{Kind: planTurnAuto, OutOfDate: true}}, s.askedIn, "automatic: parks behind an approval, dropped after a run")
	assert.Equal(t, []string{"main-oom", "main-5xx"}, s.neededOf)

	// The next ticks see the request: no second one while it is open, nor for 24 h after it ended.
	s.latest["main-oom"] = &events.PlanningRequest{State: events.PlanningQueued, RequestedAt: time.Now().UTC()}
	queueChainPlannings(nil)
	s.latest["main-oom"] = &events.PlanningRequest{State: events.PlanningNone, RequestedAt: time.Now().UTC().Add(-23 * time.Hour)}
	queueChainPlannings(nil)
	s.latest["main-oom"] = &events.PlanningRequest{State: events.PlanningParked, RequestedAt: time.Now().UTC().Add(-48 * time.Hour)}
	queueChainPlannings(nil)
	assert.Equal(t, []string{"main-oom"}, s.asked, "at most one request per chain per 24 h")

	s.latest["main-oom"] = &events.PlanningRequest{State: events.PlanningDone, RequestedAt: time.Now().UTC().Add(-25 * time.Hour)}
	queueChainPlannings(nil)
	assert.Equal(t, []string{"main-oom", "main-oom"}, s.asked, "a day later, with the fixes out of date again, it asks again")
}

// With the loop off nothing is asked.
func TestRecurringFiringWithTheLoopOff(t *testing.T) {
	s := stubChain(t)
	s.firings = []events.ChainFiring{{EventId: "dup", AccountId: "a"}}
	s.needed = map[string]bool{"dup": true}
	stubLoopOn(t, false)
	queueChainPlannings(nil)
	assert.Empty(t, s.asked)
}

// An automatic request is refused for an event whose fixes are current, unless the caller found them out of date.
func TestAutomaticRequestForOutOfDateFixes(t *testing.T) {
	f := newPlanningFixture(t)
	f.storePlan(time.Hour)
	_, refusal, err := requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnAuto})
	require.NoError(t, err)
	assert.Contains(t, refusal, "already has fixes prepared")

	row, refusal, err := requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnAuto, OutOfDate: true})
	require.NoError(t, err)
	require.Empty(t, refusal)
	assert.Equal(t, planTurnAuto, row.Kind)
	assert.NotEmpty(t, row.BaseVersion, "made against the plan it replaces")
}

// The scan: the newest notified (or P1/P2) firing of each chain in the window, of tenants with the loop on.
func TestRecentChainFiringsDB(t *testing.T) {
	db := remediationScratchDB(t, "chainfirings", `
		CREATE TABLE events (id uuid PRIMARY KEY, cloud_account_id uuid, tenant uuid, fingerprint text, nb_status text, computed_priority text,
			created_at timestamp NOT NULL DEFAULT LOCALTIMESTAMP);
		CREATE TABLE event_duplicates (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), event_id uuid, cloud_account_id uuid, first_event_id uuid,
			occurrence_number int, created_at timestamp NOT NULL DEFAULT LOCALTIMESTAMP);
		CREATE TABLE sent_notifications (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id uuid, event_id uuid, platform text, kind text,
			status text, created_at timestamp NOT NULL DEFAULT LOCALTIMESTAMP);
		CREATE TABLE feature_flag (feature_id text, tenant_id uuid, account_id uuid, status text)`)
	tenant, off, account, offAccount := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := db.Exec(`INSERT INTO feature_flag VALUES ($1, $2, NULL, 'enabled')`, events.RemediationLoopFeature, tenant)
	require.NoError(t, err)
	firing := func(acct, tnt, main, priority string, ago time.Duration, posted string) string {
		id := uuid.NewString()
		// Times on the database's clock, as the events' are.
		at := ago.Seconds()
		_, err := db.Exec(`INSERT INTO events (id, cloud_account_id, tenant, fingerprint, nb_status, computed_priority, created_at)
			VALUES ($1, $2, $3, 'fp', 'DUPLICATE', $4, LOCALTIMESTAMP - make_interval(secs => $5))`, id, acct, tnt, priority, at)
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO event_duplicates (event_id, cloud_account_id, first_event_id, occurrence_number, created_at) VALUES ($1, $2, $3, 2, LOCALTIMESTAMP - make_interval(secs => $4))`,
			id, acct, main, at)
		require.NoError(t, err)
		if posted != "" {
			_, err = db.Exec(`INSERT INTO sent_notifications (tenant_id, event_id, platform, kind, status, created_at) VALUES ($1, $2, $3, 'finding', 'sent', LOCALTIMESTAMP - make_interval(secs => $4))`,
				tnt, id, posted, at-10)
			require.NoError(t, err)
		}
		return id
	}
	chainA, chainB, chainC, chainD := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	firing(account, tenant, chainA, "P3", 10*time.Minute, "slack")
	newestA := firing(account, tenant, chainA, "P3", 2*time.Minute, "ms_teams")
	p1 := firing(account, tenant, chainB, "P1", time.Minute, "")
	firing(account, tenant, chainC, "P3", time.Minute, "")                // not told anyone
	firing(account, tenant, chainD, "P1", time.Hour, "slack")             // before the window
	firing(offAccount, off, uuid.NewString(), "P1", time.Minute, "slack") // the loop is off for its tenant

	got, err := events.RecentChainFirings(context.Background(), db, 15*time.Minute, 50)
	require.NoError(t, err)
	assert.ElementsMatch(t, []events.ChainFiring{{EventId: newestA, AccountId: account}, {EventId: p1, AccountId: account}}, got)
}
