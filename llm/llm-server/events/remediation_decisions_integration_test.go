package events

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// decisionsDB is an isolated schema on a disposable PostgreSQL database that has the app migrations
// applied: event_resolution is copied with its CHECK constraints and indexes (INCLUDING ALL), so the
// status list and the approval-key unique index are the migration's own.
func decisionsDB(t *testing.T) (*EventAnalysisRepository, *sqlx.DB) {
	t.Helper()
	dsn := os.Getenv("REMEDIATION_RUN_TEST_DB_URL")
	if dsn == "" {
		t.Skip("set REMEDIATION_RUN_TEST_DB_URL to a disposable PostgreSQL database with the app migrations applied")
	}
	admin, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	schema := "run_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	scopedURL, err := url.Parse(dsn)
	require.NoError(t, err)
	params := scopedURL.Query()
	params.Set("search_path", schema)
	scopedURL.RawQuery = params.Encode()
	db, err := sqlx.Connect("postgres", scopedURL.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close(); _, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE"); _ = admin.Close() })
	_, err = db.Exec(`CREATE TABLE event_resolution (LIKE public.event_resolution INCLUDING ALL);
		CREATE TABLE event_log_analysis (LIKE public.event_log_analysis INCLUDING ALL);
		CREATE TABLE events (id uuid PRIMARY KEY, cloud_account_id uuid NOT NULL)`)
	require.NoError(t, err)
	return NewEventAnalysisRepository(&common.DatabaseManager{Db: db}), db
}

type decisionFixture struct {
	t       *testing.T
	repo    *EventAnalysisRepository
	db      *sqlx.DB
	ctx     *security.RequestContext
	account string
	plans   map[string]planRef // event -> its stored plan
}

// planRef is an event's stored plan row and the version of its text.
type planRef struct{ id, version string }

func newDecisionFixture(t *testing.T) *decisionFixture {
	repo, db := decisionsDB(t)
	return &decisionFixture{t: t, repo: repo, db: db, ctx: security.NewRequestContextForSuperAdmin(), account: uuid.NewString(), plans: map[string]planRef{}}
}

// event creates an event with a stored plan.
func (f *decisionFixture) event() string {
	id := uuid.NewString()
	_, err := f.db.Exec(`INSERT INTO events (id, cloud_account_id) VALUES ($1, $2)`, id, f.account)
	require.NoError(f.t, err)
	f.regenerate(id, "plan 1 for "+id)
	return id
}

// regenerate stores a new plan text for the event, as plan generation does (one row, rewritten).
func (f *decisionFixture) regenerate(event, text string) {
	f.t.Helper()
	ref, ok := f.plans[event]
	if !ok {
		ref.id = uuid.NewString()
		_, err := f.db.Exec(`INSERT INTO event_log_analysis (id, event_id, analysis_type, analysis) VALUES ($1, $2, 'remediation', $3)`, ref.id, event, text)
		require.NoError(f.t, err)
	} else {
		_, err := f.db.Exec(`UPDATE event_log_analysis SET analysis = $2 WHERE id = $1`, ref.id, text)
		require.NoError(f.t, err)
	}
	ref.version = PlanVersion(text)
	f.plans[event] = ref
}

// decision is a decision on step 0:apply of the event's current plan, for command.
func (f *decisionFixture) decision(event, status, command string) RemediationDecision {
	p := f.plans[event]
	return RemediationDecision{AccountId: f.account, EventId: event, UserId: "u1", Ref: command, Status: status,
		ApprovalKey: ApprovalKey(f.account, event, p.version, "0:apply", command), Data: map[string]any{"command": command},
		PlanRowId: p.id, PlanVersion: p.version}
}

// decide records a decision on step 0:apply of the event's current plan for command.
func (f *decisionFixture) decide(event, status, command string, opts ...func(*RemediationDecision)) (string, *DecisionConflict) {
	f.t.Helper()
	d := f.decision(event, status, command)
	if status == RunStatusSuccess {
		d.Data["manual"] = true
	}
	for _, o := range opts {
		o(&d)
	}
	id, conflict, err := f.repo.ClaimRemediationDecision(f.ctx, d)
	require.NoError(f.t, err)
	return id, conflict
}

func (f *decisionFixture) settle(id, status string, patch map[string]any) {
	f.t.Helper()
	ok, err := f.repo.SettleRemediationRun(f.ctx, id, status, patch, "settled")
	require.NoError(f.t, err)
	require.True(f.t, ok)
}

func (f *decisionFixture) rows(event string) int {
	var n int
	require.NoError(f.t, f.db.Get(&n, `SELECT count(*) FROM event_resolution WHERE event_id = $1`, event))
	return n
}

// age moves a row's creation back, as if it had been claimed that long ago.
// pastUnknownRerunWait is an age just past UnknownRerunWait.
var pastUnknownRerunWait = fmt.Sprintf("%d seconds", int(UnknownRerunWait.Seconds())+60)

func (f *decisionFixture) age(id, interval string) {
	_, err := f.db.Exec(`UPDATE event_resolution SET created_at = created_at - $2::interval WHERE id = $1`, id, interval)
	require.NoError(f.t, err)
}

const restart = "kubectl rollout restart deployment/api -n shop"

func acknowledged(d *RemediationDecision) { d.AcknowledgeUnknown = true }

// A step is decided once. After a run ends Success or Failed, or the step is declined or marked done,
// the same decision again (a resent Confirm, a second Slack click, the Web tab after Slack) is refused
// and nothing new is recorded, so nothing can be sent twice.
func TestDecisionReplayAfterEveryEndingIsRefused(t *testing.T) {
	f := newDecisionFixture(t)
	endings := []struct {
		name   string
		decide func(event string) string
	}{
		{"run succeeded", func(e string) string {
			id, c := f.decide(e, RunStatusInProgress, restart)
			require.Nil(t, c)
			f.settle(id, RunStatusSuccess, map[string]any{"exit_code": 0, "exit_code_reported": true})
			return id
		}},
		{"run failed", func(e string) string {
			id, c := f.decide(e, RunStatusInProgress, restart)
			require.Nil(t, c)
			f.settle(id, RunStatusFailed, map[string]any{"exit_code": 1, "exit_code_reported": true})
			return id
		}},
		{"declined", func(e string) string {
			id, c := f.decide(e, RunStatusDeclined, restart)
			require.Nil(t, c)
			return id
		}},
		{"marked done", func(e string) string {
			id, c := f.decide(e, RunStatusSuccess, restart)
			require.Nil(t, c)
			return id
		}},
		{"still running", func(e string) string {
			id, c := f.decide(e, RunStatusInProgress, restart)
			require.Nil(t, c)
			return id
		}},
	}
	for _, ending := range endings {
		t.Run(ending.name, func(t *testing.T) {
			event := f.event()
			first := ending.decide(event)
			// Every way of deciding the same step again: Run (with and without acknowledgement), Decline,
			// "I ran this".
			for _, again := range []struct {
				status string
				opts   []func(*RemediationDecision)
			}{
				{RunStatusInProgress, nil},
				{RunStatusInProgress, []func(*RemediationDecision){acknowledged}},
				{RunStatusDeclined, nil},
				{RunStatusSuccess, nil},
			} {
				id, conflict := f.decide(event, again.status, restart, again.opts...)
				require.Empty(t, id, again.status)
				require.NotNil(t, conflict, again.status)
				require.Equal(t, DecisionAlreadyDecided, conflict.Reason, again.status)
				require.Equal(t, first, conflict.RowId, "the conflict names the decision that stands")
			}
			require.Equal(t, 1, f.rows(event))
		})
	}
}

// Concurrent identical decisions (a double click, a Slack retry racing the Web tab) record one row.
func TestConcurrentDecisionsOnOneStepRecordOne(t *testing.T) {
	f := newDecisionFixture(t)
	event := f.event()
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			status := []string{RunStatusInProgress, RunStatusDeclined, RunStatusSuccess, RunStatusInProgress}[i%4]
			d := f.decision(event, status, restart)
			id, conflict, err := f.repo.ClaimRemediationDecision(f.ctx, d)
			require.NoError(t, err)
			if conflict == nil {
				ids <- id
			}
		}(i)
	}
	wg.Wait()
	close(ids)
	require.Len(t, ids, 1)
	require.Equal(t, 1, f.rows(event))
}

// The database refuses a second decision on a step even from a writer that skips the claim.
func TestApprovalKeyIndexBacksTheClaimUp(t *testing.T) {
	f := newDecisionFixture(t)
	event := f.event()
	_, c := f.decide(event, RunStatusDeclined, restart)
	require.Nil(t, c)
	_, err := f.db.Exec(`INSERT INTO event_resolution (event_id, type, data, status, type_reference_id, resolver_type, resolver_id)
		SELECT event_id, type, data, 'InProgress', type_reference_id, resolver_type, resolver_id FROM event_resolution WHERE event_id = $1`, event)
	require.ErrorContains(t, err, "duplicate key value violates unique constraint", "the copy of uq_event_resolution_approval_key refuses it")

	// A row the claim's own reads cannot see (another event, another command) still holds the key: the
	// claim answers "already decided", not an error.
	other := f.event()
	key := ApprovalKey(f.account, other, f.plans[other].version, "0:apply", restart)
	_, err = f.db.Exec(`INSERT INTO event_resolution (event_id, type, data, status, type_reference_id, resolver_type, resolver_id)
		VALUES ($1, 'CommandExecution', jsonb_build_object('approval_key', $2::text), 'Declined', 'something else', 'User', 'u')`, f.event(), key)
	require.NoError(t, err)
	id, conflict := f.decide(other, RunStatusInProgress, restart)
	require.Empty(t, id)
	require.NotNil(t, conflict)
	require.Equal(t, DecisionAlreadyDecided, conflict.Reason)
}

// An Unknown run may be run again only with the person's acknowledgement and only UnknownRerunWait
// after it was sent; the new run is a new row and the Unknown one stays as it was.
func TestUnknownRunIsRerunOnlyAcknowledgedAndAfterTheWait(t *testing.T) {
	f := newDecisionFixture(t)
	event := f.event()
	first, c := f.decide(event, RunStatusInProgress, restart)
	require.Nil(t, c)
	f.settle(first, RunStatusUnknown, map[string]any{"reply_received": false})

	_, c = f.decide(event, RunStatusInProgress, restart)
	require.NotNil(t, c)
	require.Equal(t, DecisionUnknownOutcome, c.Reason, "no acknowledgement")

	_, c = f.decide(event, RunStatusInProgress, restart, acknowledged)
	require.NotNil(t, c)
	require.Equal(t, DecisionUnknownOutcome, c.Reason, "acknowledged, but before the wait")
	require.Positive(t, c.WaitSeconds)

	f.age(first, pastUnknownRerunWait)
	second, c := f.decide(event, RunStatusInProgress, restart, acknowledged)
	require.Nil(t, c)
	require.NotEqual(t, first, second)
	var status string
	require.NoError(t, f.db.Get(&status, `SELECT status FROM event_resolution WHERE id = $1`, first))
	require.Equal(t, RunStatusUnknown, status)

	// The re-run is a decision like any other: once it ends, the step is decided.
	f.settle(second, RunStatusSuccess, map[string]any{"exit_code": 0, "exit_code_reported": true})
	_, c = f.decide(event, RunStatusInProgress, restart, acknowledged)
	require.Equal(t, DecisionAlreadyDecided, c.Reason)
}

// A run that provably never ran (the agent was not connected) leaves its step open: it is retried as is.
func TestNotSentRunIsRetried(t *testing.T) {
	f := newDecisionFixture(t)
	event := f.event()
	first, c := f.decide(event, RunStatusInProgress, restart)
	require.Nil(t, c)
	f.settle(first, RunStatusFailed, map[string]any{"not_sent": true, "error": "agent not connected"})

	second, c := f.decide(event, RunStatusInProgress, restart)
	require.Nil(t, c)
	require.NotEqual(t, first, second)
}

// Two alerts proposing the same command: neither may send it while the other's run is in flight or
// unresolved. Each alert's step is its own decision, so the second may run once the first has ended.
func TestSameCommandOnTwoEventsIsNotSentConcurrently(t *testing.T) {
	f := newDecisionFixture(t)
	a, b := f.event(), f.event()
	run, c := f.decide(a, RunStatusInProgress, restart)
	require.Nil(t, c)

	_, c = f.decide(b, RunStatusInProgress, restart)
	require.NotNil(t, c)
	require.Equal(t, DecisionAlreadyRunning, c.Reason)
	require.Equal(t, run, c.RowId, "the run on the other alert holds the command")

	f.settle(run, RunStatusUnknown, nil)
	_, c = f.decide(b, RunStatusInProgress, restart)
	require.Equal(t, DecisionUnknownOutcome, c.Reason, "the other alert's run may have run")

	// "I ran this" and Decline send nothing, so they are not held up by another alert's run.
	_, c = f.decide(b, RunStatusDeclined, restart)
	require.Nil(t, c)
}

// A new plan version is a new decision opportunity, even for the same command.
func TestNewPlanVersionIsANewDecision(t *testing.T) {
	f := newDecisionFixture(t)
	event := f.event()
	first, c := f.decide(event, RunStatusInProgress, restart)
	require.Nil(t, c)
	f.settle(first, RunStatusFailed, map[string]any{"exit_code": 1, "exit_code_reported": true})

	f.regenerate(event, "plan 2")
	_, c = f.decide(event, RunStatusInProgress, restart)
	require.Nil(t, c)
}

// A decision binds the plan version the person saw. Once the plan is regenerated, a decision on the
// old version is refused, whatever its kind, and nothing is recorded.
func TestDecisionOnAStalePlanIsRefused(t *testing.T) {
	f := newDecisionFixture(t)
	event := f.event()
	seen := f.decision(event, RunStatusInProgress, restart)
	f.regenerate(event, "plan 2")
	for _, status := range []string{RunStatusInProgress, RunStatusDeclined, RunStatusSuccess} {
		d := seen
		d.Status = status
		id, conflict, err := f.repo.ClaimRemediationDecision(f.ctx, d)
		require.NoError(t, err)
		require.Empty(t, id)
		require.NotNil(t, conflict, status)
		require.Equal(t, DecisionPlanChanged, conflict.Reason)
	}
	require.Equal(t, 0, f.rows(event))
}

// A regenerate racing a claim waits for it: the claim holds the plan row FOR SHARE until it commits.
func TestRegenerateWaitsForAClaimInFlight(t *testing.T) {
	f := newDecisionFixture(t)
	event := f.event()
	tx, err := f.db.Beginx()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT analysis FROM event_log_analysis WHERE id = $1 FOR SHARE`, f.plans[event].id)
	require.NoError(t, err)
	goCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = f.db.ExecContext(goCtx, `UPDATE event_log_analysis SET analysis = 'plan 2' WHERE id = $1`, f.plans[event].id)
	require.Error(t, err, "the regenerate waits while a claim holds the plan")
}

// The reaper marks Unknown only runs past the wait; a settle and the reaper cannot both win, and two
// reapers running at once change a row once.
func TestReaperMarksAbandonedRunsUnknown(t *testing.T) {
	f := newDecisionFixture(t)
	event := f.event()
	old, c := f.decide(event, RunStatusInProgress, restart)
	require.Nil(t, c)
	f.age(old, pastUnknownRerunWait)
	young, c := f.decide(f.event(), RunStatusInProgress, "kubectl rollout restart deployment/web -n shop")
	require.Nil(t, c)

	var wg sync.WaitGroup
	counts := make(chan int64, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reaped, err := ReapRemediationRuns(context.Background(), f.db)
			require.NoError(t, err)
			counts <- reaped
		}()
	}
	wg.Wait()
	close(counts)
	var total int64
	for n := range counts {
		total += n
	}
	require.EqualValues(t, 1, total)

	var status string
	require.NoError(t, f.db.Get(&status, `SELECT status FROM event_resolution WHERE id = $1`, old))
	require.Equal(t, RunStatusUnknown, status)
	require.NoError(t, f.db.Get(&status, `SELECT status FROM event_resolution WHERE id = $1`, young))
	require.Equal(t, RunStatusInProgress, status)

	// A settle arriving after the reaper changes nothing.
	settled, err := f.repo.SettleRemediationRun(f.ctx, old, RunStatusSuccess, map[string]any{"exit_code": 0}, "late")
	require.NoError(t, err)
	require.False(t, settled)
}

// A claim that cannot get its lock or its database gives up at its deadline and records nothing, so
// nothing is sent; it never waits indefinitely.
func TestClaimGivesUpAtItsDeadline(t *testing.T) {
	f := newDecisionFixture(t)
	event := f.event()
	holder, err := f.db.Beginx()
	require.NoError(t, err)
	defer func() { _ = holder.Rollback() }()
	_, err = holder.Exec(`SELECT pg_advisory_xact_lock(hashtextextended('remediation-run:' || $1, 0))`, f.account)
	require.NoError(t, err)

	goCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	id, conflict, err := f.repo.ClaimRemediationDecision(f.ctx.WithContext(goCtx), f.decision(event, RunStatusInProgress, restart))
	require.Error(t, err)
	require.Empty(t, id)
	require.Nil(t, conflict)
	require.Less(t, time.Since(start), 5*time.Second)
	require.NoError(t, holder.Rollback())
	require.Equal(t, 0, f.rows(event))
}

// A settle that cannot write gives up at its deadline and says so; the row stays InProgress, so the
// reaper (not a late write) decides it.
func TestSettleGivesUpAtItsDeadline(t *testing.T) {
	f := newDecisionFixture(t)
	event := f.event()
	id, c := f.decide(event, RunStatusInProgress, restart)
	require.Nil(t, c)
	holder, err := f.db.Beginx()
	require.NoError(t, err)
	defer func() { _ = holder.Rollback() }()
	_, err = holder.Exec(`SELECT id FROM event_resolution WHERE id = $1 FOR UPDATE`, id)
	require.NoError(t, err)

	goCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	settled, err := f.repo.SettleRemediationRun(f.ctx.WithContext(goCtx), id, RunStatusSuccess, map[string]any{"exit_code": 0}, "late")
	require.Error(t, err)
	require.False(t, settled)
	require.Less(t, time.Since(start), 5*time.Second)
	require.NoError(t, holder.Rollback())
	var status string
	require.NoError(t, f.db.Get(&status, `SELECT status FROM event_resolution WHERE id = $1`, id))
	require.Equal(t, RunStatusInProgress, status)
}

// An acknowledged re-run of an Unknown step that provably never ran does not clear the Unknown: the next
// Run still needs the acknowledgement, whoever presses it.
func TestNotSentAttemptDoesNotClearAnUnknownRun(t *testing.T) {
	f := newDecisionFixture(t)
	event := f.event()
	first, c := f.decide(event, RunStatusInProgress, restart)
	require.Nil(t, c)
	f.settle(first, RunStatusUnknown, nil)
	f.age(first, pastUnknownRerunWait)

	attempt, c := f.decide(event, RunStatusInProgress, restart, acknowledged)
	require.Nil(t, c)
	f.settle(attempt, RunStatusFailed, map[string]any{"not_sent": true})

	_, c = f.decide(event, RunStatusInProgress, restart)
	require.NotNil(t, c, "a plain Run is still refused")
	require.Equal(t, DecisionUnknownOutcome, c.Reason)
	id, c := f.decide(event, RunStatusInProgress, restart, acknowledged)
	require.Nil(t, c)
	require.NotEmpty(t, id)
}

// A decision acts only on the event's own account and the event's own plan: either mismatch is refused
// before anything is recorded.
func TestDecisionOnAnotherTargetIsRefused(t *testing.T) {
	f := newDecisionFixture(t)
	event, other := f.event(), f.event()

	wrongAccount := f.decision(event, RunStatusInProgress, restart)
	wrongAccount.AccountId = uuid.NewString()
	_, conflict, err := f.repo.ClaimRemediationDecision(f.ctx, wrongAccount)
	require.NoError(t, err)
	require.Equal(t, DecisionWrongTarget, conflict.Reason)

	otherPlan := f.decision(event, RunStatusInProgress, restart)
	otherPlan.PlanRowId, otherPlan.PlanVersion = f.plans[other].id, f.plans[other].version
	_, conflict, err = f.repo.ClaimRemediationDecision(f.ctx, otherPlan)
	require.NoError(t, err)
	require.Equal(t, DecisionPlanChanged, conflict.Reason)
	require.Equal(t, 0, f.rows(event))
}
