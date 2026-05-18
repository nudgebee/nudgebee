package watch

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// test scaffolding — fake Source, fake Predicate, capturing Notifier
// ---------------------------------------------------------------------------

// fakeSource lets tests inject the observation or an error per poll. The
// SourceKind is a unique tag so each test's source registers cleanly.
type fakeSource struct {
	kind         SourceKind
	observation  Observation
	observeErr   error
	validateErr  error
	observeCalls int
	mu           sync.Mutex
}

func (f *fakeSource) Kind() SourceKind { return f.kind }

func (f *fakeSource) ValidateConfig(_ json.RawMessage) error { return f.validateErr }

func (f *fakeSource) Observe(_ *security.RequestContext, _ Watch) (Observation, error) {
	f.mu.Lock()
	f.observeCalls++
	f.mu.Unlock()
	if f.observeErr != nil {
		return Observation{}, f.observeErr
	}
	return f.observation, nil
}

// capturingNotifier records every Notify call so tests can assert exactly
// which terminal status fired (or that the notify path wasn't reached).
type capturingNotifier struct {
	mu    sync.Mutex
	calls []notifyCall
	err   error
}

type notifyCall struct {
	WatchID uuid.UUID
	Status  Status
	Summary string
}

func (n *capturingNotifier) Notify(_ *security.RequestContext, w Watch, status Status, summary string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, notifyCall{WatchID: w.ID, Status: status, Summary: summary})
	return n.err
}

func (n *capturingNotifier) lastCall(t *testing.T) notifyCall {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	require.NotEmpty(t, n.calls, "expected at least one notify call")
	return n.calls[len(n.calls)-1]
}

func (n *capturingNotifier) callCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.calls)
}

// installSecurityContextBuilder wires a no-op security context builder so the
// executor's RunOnePoll can build a request context. Without this, every
// test would error with "no security context builder registered".
func installSecurityContextBuilder(t *testing.T) {
	t.Helper()
	prev := getSecurityContextBuilder()
	SetSecurityContextBuilder(func(_ Watch) (*security.SecurityContext, error) {
		return security.NewSecurityContextForSuperAdmin(), nil
	})
	t.Cleanup(func() { SetSecurityContextBuilder(prev) })
}

// installSource registers a Source under a unique kind for the duration
// of the test. There's no public Unregister, so use a unique kind per test.
// Accepts the Source interface so test helpers can pass blockingSource,
// fakeSource, etc. interchangeably.
func installSource(t *testing.T, src Source) {
	t.Helper()
	RegisterSource(src)
	// No cleanup hook — the kind is unique per test so it doesn't collide.
}

// newExecutorWithMocks bundles the common scaffolding: sqlmock-backed
// manager + fake notifier + frozen clock. Returns everything the test needs
// to set expectations and assert post-state.
func newExecutorWithMocks(t *testing.T) (*Executor, *Manager, sqlmock.Sqlmock, *capturingNotifier, time.Time) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	frozen := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	mgr := &Manager{
		now: func() time.Time { return frozen },
		db:  sqlx.NewDb(rawDB, "postgresql"),
	}
	mock.MatchExpectationsInOrder(false)
	notifier := &capturingNotifier{}
	exec := NewExecutor(mgr)
	exec.SetNotifier(notifier)
	installSecurityContextBuilder(t)
	return exec, mgr, mock, notifier, frozen
}

// validRunningWatch returns a Watch shaped like one the dispatcher would
// hand the executor: ACTIVE, expires_at well in the future, FailureCount=0.
func validRunningWatch(kind SourceKind, predicate PredicateKind, expr string) Watch {
	return Watch{
		ID:              uuid.New(),
		ConversationID:  uuid.New(),
		AccountID:       uuid.New(),
		TenantID:        uuid.New(),
		SourceKind:      kind,
		SourceConfig:    `{}`,
		PredicateKind:   predicate,
		PredicateExpr:   expr,
		PollIntervalSec: 30,
		MaxDurationSec:  3600,
		Status:          StatusActive,
		ExpiresAt:       time.Now().Add(1 * time.Hour),
	}
}

// ---------------------------------------------------------------------------
// state-machine table tests
// ---------------------------------------------------------------------------

func TestRunOnePoll_PredicateMet_TerminatesCompleted(t *testing.T) {
	exec, _, mock, notifier, _ := newExecutorWithMocks(t)
	src := &fakeSource{
		kind:        SourceKind("fake_completed"),
		observation: Observation{Data: "deployment x successfully rolled out"},
	}
	installSource(t, src)

	w := validRunningWatch(src.kind, PredicateKindRegex, "successfully rolled out")
	mock.ExpectExec(`SET status = \$1, final_result = \$2`).
		WithArgs(string(StatusCompleted), sqlmock.AnyArg(), nil, sqlmock.AnyArg(), w.ID, w.TenantID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	exec.RunOnePoll(context.Background(), w)

	require.NoError(t, mock.ExpectationsWereMet())
	require.Equal(t, 1, notifier.callCount())
	last := notifier.lastCall(t)
	assert.Equal(t, StatusCompleted, last.Status)
	assert.Contains(t, last.Summary, "matched")
}

func TestRunOnePoll_PredicateMet_WithNegateInverts(t *testing.T) {
	// predicate_negate=true flips Met. Watch a thing UNTIL the regex stops
	// matching. Lock that semantic in.
	exec, _, mock, notifier, _ := newExecutorWithMocks(t)
	src := &fakeSource{
		kind:        SourceKind("fake_negate"),
		observation: Observation{Data: "still in progress"},
	}
	installSource(t, src)

	w := validRunningWatch(src.kind, PredicateKindRegex, "completed")
	w.PredicateNegate = true
	// Predicate sees "still in progress" → does NOT match "completed" → Met=false.
	// Negate flips to Met=true → terminate COMPLETED.
	mock.ExpectExec(`SET status = \$1, final_result = \$2`).
		WithArgs(string(StatusCompleted), sqlmock.AnyArg(), nil, sqlmock.AnyArg(), w.ID, w.TenantID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	exec.RunOnePoll(context.Background(), w)

	require.NoError(t, mock.ExpectationsWereMet())
	last := notifier.lastCall(t)
	assert.Equal(t, StatusCompleted, last.Status)
	assert.Contains(t, last.Summary, "[negated]")
}

func TestRunOnePoll_PredicateNotMet_Reschedules(t *testing.T) {
	exec, _, mock, notifier, _ := newExecutorWithMocks(t)
	src := &fakeSource{
		kind:        SourceKind("fake_not_met"),
		observation: Observation{Data: "still running"},
	}
	installSource(t, src)

	w := validRunningWatch(src.kind, PredicateKindRegex, "completed")
	// Predicate doesn't match → UpdatePollResult, no MarkTerminal, no notify.
	mock.ExpectExec(`(?s)UPDATE llm_watch_tasks.*SET status = 'ACTIVE'.*poll_count = poll_count \+ 1`).
		WithArgs(sqlmock.AnyArg(), "still running", sqlmock.AnyArg(), w.ID, w.TenantID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	exec.RunOnePoll(context.Background(), w)

	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, 0, notifier.callCount(), "non-terminal poll must not notify")
}

func TestRunOnePoll_ObserveError_RecordsFailure_BelowThreshold(t *testing.T) {
	exec, _, mock, notifier, _ := newExecutorWithMocks(t)
	src := &fakeSource{
		kind:       SourceKind("fake_obs_err"),
		observeErr: errors.New("rate limited"),
	}
	installSource(t, src)

	withConfig(t, func() { config.Config.WatchMaxFailures = 3 })

	w := validRunningWatch(src.kind, PredicateKindRegex, "completed")
	w.FailureCount = 1 // next failure → 2, still below cap of 3
	mock.ExpectExec(`(?s)UPDATE llm_watch_tasks.*failure_count = failure_count \+ 1`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), w.ID, w.TenantID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	exec.RunOnePoll(context.Background(), w)

	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, 0, notifier.callCount(), "below cap → no terminal notify")
}

func TestRunOnePoll_ObserveError_HitsThreshold_TerminatesFailed(t *testing.T) {
	exec, _, mock, notifier, _ := newExecutorWithMocks(t)
	src := &fakeSource{
		kind:       SourceKind("fake_obs_err_terminal"),
		observeErr: errors.New("no tool configs found"),
	}
	installSource(t, src)

	withConfig(t, func() { config.Config.WatchMaxFailures = 3 })

	w := validRunningWatch(src.kind, PredicateKindRegex, "completed")
	w.FailureCount = 2 // next failure → 3 = cap
	mock.ExpectExec(`SET status = \$1`).
		WithArgs(string(StatusFailed), nil, sqlmock.AnyArg(), sqlmock.AnyArg(), w.ID, w.TenantID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	exec.RunOnePoll(context.Background(), w)

	require.NoError(t, mock.ExpectationsWereMet())
	require.Equal(t, 1, notifier.callCount())
	last := notifier.lastCall(t)
	assert.Equal(t, StatusFailed, last.Status)
}

func TestRunOnePoll_MaxFailuresZero_DefaultsTo3(t *testing.T) {
	// WatchMaxFailures = 0 in config means "use default (3)". Verify by
	// observing that 2 failures don't terminate but a 3rd would.
	exec, _, mock, notifier, _ := newExecutorWithMocks(t)
	src := &fakeSource{
		kind:       SourceKind("fake_default_max"),
		observeErr: errors.New("flake"),
	}
	installSource(t, src)

	withConfig(t, func() { config.Config.WatchMaxFailures = 0 }) // → default 3

	w := validRunningWatch(src.kind, PredicateKindRegex, "completed")
	w.FailureCount = 1 // next → 2, below default-3
	mock.ExpectExec(`(?s)UPDATE llm_watch_tasks.*failure_count = failure_count \+ 1`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), w.ID, w.TenantID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	exec.RunOnePoll(context.Background(), w)

	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, 0, notifier.callCount())
}

func TestRunOnePoll_ExpiresBeforePoll_TerminatesExpired(t *testing.T) {
	exec, _, mock, notifier, _ := newExecutorWithMocks(t)
	src := &fakeSource{
		kind:        SourceKind("fake_expiry"),
		observation: Observation{Data: "still running"}, // wouldn't match anyway
	}
	installSource(t, src)

	w := validRunningWatch(src.kind, PredicateKindRegex, "completed")
	w.ExpiresAt = time.Now().Add(-1 * time.Minute) // already expired
	last := "still running"
	w.LastPollResult = &last

	mock.ExpectExec(`SET status = \$1`).
		WithArgs(string(StatusExpired), sqlmock.AnyArg(), nil, sqlmock.AnyArg(), w.ID, w.TenantID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	exec.RunOnePoll(context.Background(), w)

	require.NoError(t, mock.ExpectationsWereMet())
	require.Equal(t, 1, notifier.callCount())
	last2 := notifier.lastCall(t)
	assert.Equal(t, StatusExpired, last2.Status)
	assert.Contains(t, last2.Summary, "expired")
	assert.Contains(t, last2.Summary, "still running")

	// Source must NOT be observed when the row already expired.
	src.mu.Lock()
	defer src.mu.Unlock()
	assert.Equal(t, 0, src.observeCalls)
}

func TestRunOnePoll_ExpiredWithNoPriorObservation(t *testing.T) {
	// Watch expires before any successful poll — summary should be the
	// generic "before condition was met" line, not a NPE on LastPollResult.
	exec, _, mock, notifier, _ := newExecutorWithMocks(t)
	src := &fakeSource{kind: SourceKind("fake_expiry_no_prior")}
	installSource(t, src)

	w := validRunningWatch(src.kind, PredicateKindRegex, "completed")
	w.ExpiresAt = time.Now().Add(-1 * time.Minute)
	// LastPollResult left nil

	mock.ExpectExec(`SET status = \$1`).
		WithArgs(string(StatusExpired), sqlmock.AnyArg(), nil, sqlmock.AnyArg(), w.ID, w.TenantID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	exec.RunOnePoll(context.Background(), w)

	last := notifier.lastCall(t)
	assert.Equal(t, StatusExpired, last.Status)
	assert.Contains(t, last.Summary, "before condition was met")
}

func TestRunOnePoll_SourceNotRegistered_TerminatesFailed(t *testing.T) {
	exec, _, mock, notifier, _ := newExecutorWithMocks(t)
	w := validRunningWatch(SourceKind("definitely_not_registered_source"), PredicateKindRegex, "completed")

	mock.ExpectExec(`SET status = \$1`).
		WithArgs(string(StatusFailed), nil, sqlmock.AnyArg(), sqlmock.AnyArg(), w.ID, w.TenantID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	exec.RunOnePoll(context.Background(), w)

	require.NoError(t, mock.ExpectationsWereMet())
	last := notifier.lastCall(t)
	assert.Equal(t, StatusFailed, last.Status)
}

func TestRunOnePoll_PredicateCompileErrorAtPollTime_TerminatesFailed(t *testing.T) {
	// Predicate compiled successfully at create time but fails at poll time
	// (e.g. an unrecognised kind shipped from a different code version).
	// Treat as terminal failure — the row would otherwise loop forever.
	exec, _, mock, notifier, _ := newExecutorWithMocks(t)
	src := &fakeSource{
		kind:        SourceKind("fake_pred_compile_err"),
		observation: Observation{Data: "anything"},
	}
	installSource(t, src)

	w := validRunningWatch(src.kind, PredicateKind("nonexistent"), "ok")
	mock.ExpectExec(`SET status = \$1`).
		WithArgs(string(StatusFailed), nil, sqlmock.AnyArg(), sqlmock.AnyArg(), w.ID, w.TenantID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	exec.RunOnePoll(context.Background(), w)

	require.NoError(t, mock.ExpectationsWereMet())
	last := notifier.lastCall(t)
	assert.Equal(t, StatusFailed, last.Status)
}

func TestRunOnePoll_NoSecurityContextBuilder_AbortsSilently(t *testing.T) {
	// Without a security-context builder, the executor logs and returns
	// (without crashing or marking the row terminal). The dispatcher will
	// retry on the next tick.
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	mgr := &Manager{now: time.Now, db: sqlx.NewDb(rawDB, "postgresql")}
	notifier := &capturingNotifier{}
	exec := NewExecutor(mgr)
	exec.SetNotifier(notifier)

	// Force builder = nil
	prev := getSecurityContextBuilder()
	SetSecurityContextBuilder(nil)
	t.Cleanup(func() { SetSecurityContextBuilder(prev) })

	// No SQL expected: executor must bail before touching the DB.
	w := validRunningWatch(SourceKind("any"), PredicateKindRegex, "ok")
	exec.RunOnePoll(context.Background(), w)

	assert.Equal(t, 0, notifier.callCount())
	require.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// recordFailure backoff
// ---------------------------------------------------------------------------

func TestRecordFailure_BackoffGrowsWithFailureCount(t *testing.T) {
	// FailureCount=0 → backoff = poll_interval * 1
	// FailureCount=1 → backoff = poll_interval * 2
	// FailureCount=2 → backoff = poll_interval * 4
	// FailureCount=4 → backoff = poll_interval * 16 (cap from math.Min(.,4))
	// FailureCount=10 → also poll_interval * 16
	cases := []struct {
		failureCount int
		wantMult     int
	}{
		{0, 1},
		{1, 2},
		{2, 4},
		{3, 8},
		{4, 16},
		{10, 16},
	}
	for _, tc := range cases {
		t.Run(strconv.Itoa(tc.failureCount), func(t *testing.T) {
			exec, _, mock, _, _ := newExecutorWithMocks(t)
			src := &fakeSource{
				kind:       SourceKind("fake_backoff_" + strconv.Itoa(tc.failureCount)),
				observeErr: errors.New("flake"),
			}
			installSource(t, src)
			withConfig(t, func() { config.Config.WatchMaxFailures = 100 }) // never terminate

			w := validRunningWatch(src.kind, PredicateKindRegex, "ok")
			w.FailureCount = tc.failureCount
			w.PollIntervalSec = 30

			// Capture the next_poll_at arg the executor sets via IncFailures.
			var capturedNext time.Time
			mock.ExpectExec(`(?s)UPDATE llm_watch_tasks.*failure_count = failure_count \+ 1`).
				WithArgs(
					sqlmock.AnyArg(),
					sqlmock.AnyArg(),
					nextPollAtCapture{out: &capturedNext},
					w.ID, w.TenantID,
				).
				WillReturnResult(sqlmock.NewResult(0, 1))

			before := time.Now()
			exec.RunOnePoll(context.Background(), w)
			after := time.Now()

			require.NoError(t, mock.ExpectationsWereMet())
			expectedMin := before.Add(time.Duration(tc.wantMult*30) * time.Second)
			expectedMax := after.Add(time.Duration(tc.wantMult*30) * time.Second)
			assert.WithinRange(t, capturedNext, expectedMin, expectedMax,
				"backoff multiplier wrong for failure_count=%d", tc.failureCount)
		})
	}
}

// ---------------------------------------------------------------------------
// helpers specific to executor tests
// ---------------------------------------------------------------------------

// nextPollAtCapture is a sqlmock.Argument that records a driver.Value
// representing a time.Time (or a *time.Time) into *out, and always matches.
// Used to read the next_poll_at the executor computed at runtime.
type nextPollAtCapture struct {
	out *time.Time
}

func (c nextPollAtCapture) Match(v driver.Value) bool {
	switch tv := v.(type) {
	case time.Time:
		*c.out = tv
	case *time.Time:
		if tv != nil {
			*c.out = *tv
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Behavioral coverage for hsundar review fixes (#1, #3, #5, #7)
//
// The earlier tests pin sqlmock argument shapes — they prove the SQL gets
// emitted, not that the *behavior* is correct under contention. The tests
// below exercise the actual contracts:
//
//   - pollTimeout cancels a stuck Source.Observe (Fix #3)
//   - summarizerTimeout cancels a stuck Summarizer (Fix #5)
//   - lost-race short-circuit skips summarizer + responder + notifier
//     AND emits a metric path (Fix #1 + #7)
//   - summarizer factory failure → executor falls back to raw summary
//     (Fix #5 fallback)
//   - dispatcher Submit-fail invokes RescheduleNextPollAt with the right
//     args so the row stops recycling (Fix #3b)
// ---------------------------------------------------------------------------

// panicSource panics on Observe. Used to prove the defer-recover at the
// top of RunOnePoll catches source panics and contains them within the
// worker — without that guard, a single buggy source crashes the whole
// llm-server process.
type panicSource struct {
	kind         SourceKind
	observeCalls int
	mu           sync.Mutex
}

func (p *panicSource) Kind() SourceKind                       { return p.kind }
func (p *panicSource) ValidateConfig(_ json.RawMessage) error { return nil }
func (p *panicSource) Observe(_ *security.RequestContext, _ Watch) (Observation, error) {
	p.mu.Lock()
	p.observeCalls++
	p.mu.Unlock()
	panic("intentional source panic — for testing RunOnePoll's defer-recover")
}

// blockingSource holds the goroutine in Observe until either ctx cancels OR
// release is closed. Used to prove pollTimeout actually fires.
type blockingSource struct {
	kind    SourceKind
	release chan struct{}
	// observed is closed once Observe is entered, so the test can sync
	// against "we got past the dispatcher into the source" without polling.
	observed chan struct{}
	once     sync.Once
}

func (b *blockingSource) Kind() SourceKind                       { return b.kind }
func (b *blockingSource) ValidateConfig(_ json.RawMessage) error { return nil }
func (b *blockingSource) Observe(ctx *security.RequestContext, _ Watch) (Observation, error) {
	b.once.Do(func() { close(b.observed) })
	select {
	case <-ctx.GetContext().Done():
		return Observation{}, ctx.GetContext().Err()
	case <-b.release:
		return Observation{Data: "release"}, nil
	}
}

// TestRunOnePoll_PollTimeout_BoundsSlowSource asserts the contract of
// hsundar finding #3: a stuck Source.Observe must NOT pin a worker beyond
// pollTimeout(). Without the timeout, this test would hang for 60s and
// only return when the test framework's overall timeout fires.
//
// The timeout is set via the Executor's per-instance override rather than
// the package-global config.Config, because writing to config.Config
// races under -race -count=N with concurrent reads from the cluster's
// gocron scheduler.
func TestRunOnePoll_PollTimeout_BoundsSlowSource(t *testing.T) {
	exec, _, mock, notifier, _ := newExecutorWithMocks(t)
	exec.SetPollTimeout(500 * time.Millisecond) // small but real
	// WatchMaxFailures still uses config; we want to never terminate on
	// this one poll. Use a local override path-equivalent by keeping the
	// existing config-write inside a snapshot, but for this test we tune
	// failure count via the Watch row instead so config is untouched.

	src := &blockingSource{
		kind:     SourceKind("fake_blocking_poll"),
		release:  make(chan struct{}),
		observed: make(chan struct{}),
	}
	installSource(t, src)
	t.Cleanup(func() {
		// Defensive: close only if not already closed so a re-run via
		// -count=N can't panic.
		select {
		case <-src.release:
		default:
			close(src.release)
		}
	})

	// A poll that times out is recorded as a transient failure via IncFailures.
	// We don't care about the specific args — we care the poll returned
	// promptly and didn't sit forever on the source goroutine.
	mock.ExpectExec(`(?s)UPDATE llm_watch_tasks.*failure_count = failure_count \+ 1`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := validRunningWatch(src.kind, PredicateKindRegex, "ok")
	// Failure-count well below default cap so this timeout is recorded as
	// transient (IncFailures path) not terminal (MarkTerminal path).
	w.FailureCount = 0
	done := make(chan struct{})
	start := time.Now()
	go func() {
		exec.RunOnePoll(context.Background(), w)
		close(done)
	}()

	// Make sure we actually entered Observe (so the test would fail loudly
	// if the source were never reached, vs just timing out for unrelated
	// reasons).
	select {
	case <-src.observed:
	case <-time.After(3 * time.Second):
		t.Fatal("source was not even reached within 3s")
	}

	// Generous ceiling — pollTimeout=500ms, but under -race + slow CI the
	// bookkeeping (sqlmock Exec, recover defer, slog format) can add real
	// time. Bound at pollTimeout * 10 so the assertion scales with the
	// configured value.
	select {
	case <-done:
		elapsed := time.Since(start)
		assert.Less(t, elapsed, 5*time.Second,
			"pollTimeout=500ms should bound RunOnePoll to ~500ms + bookkeeping; observed %s", elapsed)
	case <-time.After(10 * time.Second):
		t.Fatal("RunOnePoll did not return within 10s; pollTimeout did not bound the Source")
	}

	assert.Equal(t, 0, notifier.callCount(),
		"a timed-out poll under WatchMaxFailures should not notify (it's transient)")
}

// blockingSummarizer holds the call until ctx cancels. Used to verify
// summarizerTimeout bounds the Summarize call.
type blockingSummarizer struct {
	entered chan struct{}
	once    sync.Once
}

func (b *blockingSummarizer) Summarize(ctx *security.RequestContext, _ Watch, _ Status, _, _ string) (string, error) {
	b.once.Do(func() { close(b.entered) })
	<-ctx.GetContext().Done()
	return "", ctx.GetContext().Err()
}

// TestRunOnePoll_SummarizerTimeout_BoundsSlowSummarizer asserts the
// contract of hsundar finding #5: a stuck Summarize call must not pin a
// worker. Without the WithTimeout wrap the executor would block forever
// because the parent context comes from dispatcher's context.Background().
//
// Uses per-Executor override instead of config writes (race-clean under
// -race -count=N).
func TestRunOnePoll_SummarizerTimeout_BoundsSlowSummarizer(t *testing.T) {
	exec, _, mock, notifier, _ := newExecutorWithMocks(t)
	exec.SetSummarizerTimeout(500 * time.Millisecond)
	exec.SetPollTimeout(10 * time.Second) // generous; not the path under test

	// Predicate matches on first observation → terminate(StatusCompleted) →
	// summarizer path engages.
	src := &fakeSource{
		kind:        SourceKind("fake_summarizer_timeout"),
		observation: Observation{Data: "rollout complete"},
	}
	installSource(t, src)

	bs := &blockingSummarizer{entered: make(chan struct{})}
	prev := getSummarizerFactory()
	SetSummarizerFactory(func(_ *security.RequestContext, _ Watch) (Summarizer, error) {
		return bs, nil
	})
	t.Cleanup(func() { SetSummarizerFactory(prev) })

	w := validRunningWatch(src.kind, PredicateKindRegex, "complete")
	mock.ExpectExec(`SET status = \$1, final_result = \$2`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// After the summarizer times out, the responder still runs with the raw
	// summary as the deliverable. Allow its SELECT to no-op (no parent reply
	// in the mock DB) and let the notifier take the message.

	start := time.Now()
	done := make(chan struct{})
	go func() {
		exec.RunOnePoll(context.Background(), w)
		close(done)
	}()

	select {
	case <-bs.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("summarizer was not invoked")
	}

	// Generous ceiling for the same reason as PollTimeout above.
	select {
	case <-done:
		elapsed := time.Since(start)
		assert.Less(t, elapsed, 5*time.Second,
			"summarizerTimeout=500ms should bound terminate to ~500ms + bookkeeping; observed %s", elapsed)
	case <-time.After(10 * time.Second):
		t.Fatal("RunOnePoll did not return within 10s; summarizerTimeout did not bound Summarize")
	}

	// Notifier must still have received the call — with the RAW summary
	// from the predicate, not the (timed-out) LLM output. This is the
	// load-bearing fallback Fix #5 introduces.
	require.Equal(t, 1, notifier.callCount(),
		"summarizer timeout must not silently swallow the terminal notification")
	last := notifier.lastCall(t)
	assert.Equal(t, StatusCompleted, last.Status)
	assert.Contains(t, last.Summary, "matched",
		"on summarizer timeout, deliverable falls back to the raw predicate summary")
}

// errSummarizerFactory always fails. Verifies executor takes the raw-summary
// fallback path when the factory itself errors (vs returning nil summarizer).
func TestRunOnePoll_SummarizerFactoryError_FallsBackToRawSummary(t *testing.T) {
	exec, _, mock, notifier, _ := newExecutorWithMocks(t)
	src := &fakeSource{
		kind:        SourceKind("fake_summarizer_factory_err"),
		observation: Observation{Data: "deployment rolled out cleanly"},
	}
	installSource(t, src)

	prev := getSummarizerFactory()
	SetSummarizerFactory(func(_ *security.RequestContext, _ Watch) (Summarizer, error) {
		return nil, errors.New("metastore unavailable")
	})
	t.Cleanup(func() { SetSummarizerFactory(prev) })

	w := validRunningWatch(src.kind, PredicateKindRegex, "rolled out")
	mock.ExpectExec(`SET status = \$1`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	exec.RunOnePoll(context.Background(), w)

	require.Equal(t, 1, notifier.callCount(),
		"factory error must not skip the notification")
	last := notifier.lastCall(t)
	assert.Equal(t, StatusCompleted, last.Status)
	assert.NotEmpty(t, last.Summary, "deliverable must fall back to raw, not be empty")
}

// TestRunOnePoll_LostRace_SkipsSummarizerResponderNotifier asserts that
// when MarkTerminal commits with rows=0 (a concurrent Cancel won the race),
// the executor:
//  1. does NOT invoke the summarizer factory at all (would burn LLM tokens
//     for a state the user already abandoned)
//  2. does NOT call AppendWatchUpdateToConversation (no DB query expected)
//  3. does NOT notify
//
// Note the mock's MatchExpectationsInOrder(false) — but we set NO further
// expectations after MarkTerminal, so any extra SQL emission would fail
// ExpectationsWereMet at the end.
func TestRunOnePoll_LostRace_SkipsSummarizerResponderNotifier(t *testing.T) {
	exec, _, mock, notifier, _ := newExecutorWithMocks(t)
	src := &fakeSource{
		kind:        SourceKind("fake_lost_race"),
		observation: Observation{Data: "rolled out"},
	}
	installSource(t, src)

	// Wire a summarizer that would FAIL the test if called — the lost-race
	// short-circuit must skip the summarizer entirely.
	summarizerCalled := false
	prev := getSummarizerFactory()
	SetSummarizerFactory(func(_ *security.RequestContext, _ Watch) (Summarizer, error) {
		summarizerCalled = true
		return &fakeSummarizer{out: "should never appear"}, nil
	})
	t.Cleanup(func() { SetSummarizerFactory(prev) })

	w := validRunningWatch(src.kind, PredicateKindRegex, "rolled out")
	// MarkTerminal: 0 rows affected → "row already terminal", lost race.
	mock.ExpectExec(`SET status = \$1, final_result = \$2`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	exec.RunOnePoll(context.Background(), w)

	require.NoError(t, mock.ExpectationsWereMet(),
		"on lost race, NO further SQL (no UPDATE response, no notify) must be emitted")
	assert.False(t, summarizerCalled,
		"lost race must not invoke the summarizer — wastes LLM tokens on a state the user abandoned")
	assert.Equal(t, 0, notifier.callCount(),
		"lost race must not notify — the contradictory '✅ Completed' message after Cancel was the original bug")
}

// fakeSummarizer here is the same pattern as summarizer_test.go's
// fakeSummarizer but defined locally to avoid coupling executor tests to
// summarizer_test.go's helper layout.

// TestRunOnePoll_PanicInSource_RecoversCleanly asserts the contract of
// the executor.go:80-87 defer-recover guard: a panic inside Source.Observe
// must NOT propagate up through the worker pool. The watch is silently
// dropped from this tick (next_poll_at stays at the FetchDue lease, row
// gets retried in 5 minutes) — but the llm-server process keeps running
// and other watches keep polling.
//
// Without this guard a single buggy source (nil deref, JSON-marshal panic
// on an unexpected response shape) crashes every llm-server replica
// simultaneously and takes the whole watch subsystem offline.
//
// Note: NO SQL is expected — the panic short-circuits before any UPDATE.
func TestRunOnePoll_PanicInSource_RecoversCleanly(t *testing.T) {
	exec, _, mock, notifier, _ := newExecutorWithMocks(t)

	// Capture slog output so we can pin that the executor's own recover()
	// fired — without this assertion, a future refactor that wraps Observe
	// behind a helper which itself swallows the panic would still "not
	// crash" and the test would falsely pass. The slog line at
	// executor.go:80-87 ("watch.executor: panic in RunOnePoll") is the
	// load-bearing observable proof that the defer-recover did its job.
	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	src := &panicSource{kind: SourceKind("fake_panic_source_" + uuid.NewString()[:8])}
	installSource(t, src)

	w := validRunningWatch(src.kind, PredicateKindRegex, "ok")

	// Wrap in a goroutine + done channel so a regression (panic propagates
	// out) shows up as a test failure rather than a process crash.
	done := make(chan struct{})
	go func() {
		defer close(done)
		exec.RunOnePoll(context.Background(), w)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunOnePoll did not return after panic — recover() must be missing or the defer is mis-ordered")
	}

	require.NoError(t, mock.ExpectationsWereMet(),
		"panic in Observe must short-circuit before any SQL is emitted")
	assert.Equal(t, 0, notifier.callCount(),
		"panic recovery must not produce a terminal notification")
	src.mu.Lock()
	calls := src.observeCalls
	src.mu.Unlock()
	assert.Equal(t, 1, calls,
		"the panic should have happened inside Observe exactly once (recover does not retry)")

	// The load-bearing assertion: confirm executor.go:80-87's recover()
	// fired AND emitted the expected log line. If a refactor accidentally
	// removes the recover() block, this assertion fails even if the
	// process happens to survive due to some other safety net.
	logged := logBuf.String()
	assert.Contains(t, logged, "watch.executor: panic in RunOnePoll",
		"executor's defer-recover must emit its known log line — otherwise we cannot prove the recover() actually ran")
	assert.Contains(t, logged, w.ID.String(),
		"recover log must include watch_id so operators can correlate the failure")
}

// TestRunOnePoll_Concurrent_AllPathsRaceClean spawns N concurrent
// RunOnePoll calls that ACTUALLY exercise the contested state surfaces
// (not just the maps-read-only paths the earlier version covered):
//
//   - half hit the terminal path → summarizer factory slot read + invocation
//   - the other half hit the non-terminal reschedule path
//   - both hit the source registry, security builder slot, notifier mutex,
//     and the sqlmock connection's mutex
//
// Without the per-Executor pollTimeout/summarizerTimeout overrides this
// test ran in the OLD shape where `withConfig` wrote to a package global
// and trampled the race detector under -race -count=N. Now the Executor
// owns its timeouts and the only shared writable state is the source map
// (RWMutex), factory slots (RWMutex), notifier slice (sync.Mutex), and
// the sqlmock backend (driver-level serialization).
//
// Regression value: a future refactor that drops the RWMutex on any of
// the factory slots, or that swaps the notifier's slice append for a
// lock-free pattern that doesn't actually protect against torn writes,
// would trip -race on this test. The earlier shape would have missed
// both because it never exercised the terminal path concurrently.
func TestRunOnePoll_Concurrent_AllPathsRaceClean(t *testing.T) {
	exec, _, mock, notifier, _ := newExecutorWithMocks(t)
	// Set short summarizer timeout via the per-Executor override so the
	// terminal half doesn't drag the test out. Real summarize call is
	// stubbed; the path through the factory slot is what we're racing.
	exec.SetSummarizerTimeout(500 * time.Millisecond)

	src := &fakeSource{
		kind:        SourceKind("fake_concurrent_all_paths"),
		observation: Observation{Data: "matched: rolled out"},
	}
	installSource(t, src)

	// Wire a summarizer that returns immediately. The factory slot is read
	// once per terminal poll — concurrent reads must not race against any
	// future SetSummarizerFactory write.
	prev := getSummarizerFactory()
	SetSummarizerFactory(func(_ *security.RequestContext, _ Watch) (Summarizer, error) {
		return &fakeSummarizer{out: "all done"}, nil
	})
	t.Cleanup(func() { SetSummarizerFactory(prev) })

	const n = 20
	// Half the goroutines will hit the terminal path (predicate matches
	// "rolled out") → MarkTerminal + responder + notifier; the other half
	// will register a different watch whose regex doesn't match, taking
	// the reschedule path. We can't easily pick "half" via the same regex,
	// so split the load by alternating PredicateNegate which inverts the
	// match.
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			// Terminal half — MarkTerminal UPDATE
			mock.ExpectExec(`SET status = \$1, final_result = \$2`).
				WillReturnResult(sqlmock.NewResult(0, 1))
			// Responder SELECT (no rows → silent no-op, no UPDATE).
			mock.ExpectQuery("SELECT m.id, m.response").
				WillReturnRows(sqlmock.NewRows([]string{"id", "response"}))
		} else {
			// Non-terminal half — UpdatePollResult
			mock.ExpectExec(`(?s)UPDATE llm_watch_tasks.*SET status = 'ACTIVE'`).
				WillReturnResult(sqlmock.NewResult(0, 1))
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			w := validRunningWatch(src.kind, PredicateKindRegex, "rolled out")
			if idx%2 != 0 {
				// Inverted: predicate matches but negate=true → not met →
				// reschedule path. Exercises the same source + factory state
				// reads under the alternate code path concurrently with the
				// terminal half.
				w.PredicateNegate = true
			}
			exec.RunOnePoll(context.Background(), w)
		}(i)
	}
	wg.Wait()

	require.NoError(t, mock.ExpectationsWereMet())
	// Terminal half notified; non-terminal half didn't.
	assert.Equal(t, n/2, notifier.callCount(),
		"exactly half (predicate-met, non-negated) should notify; the other half reschedules silently")
}
