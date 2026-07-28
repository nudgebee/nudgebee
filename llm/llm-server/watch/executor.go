package watch

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
)

// SecurityContextBuilder constructs a SecurityContext suitable for running
// a watch poll. Wired in from a higher layer at startup so the watch
// package does not need to embed the security package's tenant resolution
// rules. Returning an error short-circuits the poll.
type SecurityContextBuilder func(w Watch) (*security.SecurityContext, error)

var (
	securityContextBuilderMu sync.RWMutex
	securityContextBuilder   SecurityContextBuilder
)

// SetSecurityContextBuilder installs the security context builder. Called
// from a higher init layer (typically agents/core or cmd) so the watch
// package stays low in the import graph. Mutex-guarded to satisfy the
// race detector across re-bootstrap in tests.
func SetSecurityContextBuilder(b SecurityContextBuilder) {
	securityContextBuilderMu.Lock()
	defer securityContextBuilderMu.Unlock()
	securityContextBuilder = b
}

func getSecurityContextBuilder() SecurityContextBuilder {
	securityContextBuilderMu.RLock()
	defer securityContextBuilderMu.RUnlock()
	return securityContextBuilder
}

// managerWriteTimeout bounds each metastore write the executor performs
// (UpdatePollResult / IncFailures / MarkTerminal). RunOnePoll is submitted
// with context.Background() as its parent, so these writes would otherwise
// have no deadline — a hung/slow metastore connection on any of them would
// pin a worker indefinitely and, at WatchWorkerCount, stall all watch
// progress. This mirrors the explicit bounds already on the observe,
// summarizer (30s), responder (5s) and notifier (10s) phases.
const managerWriteTimeout = 5 * time.Second

// Executor runs a single poll for a single watch. It is stateless across
// polls; all state lives on the Watch row.
//
// pollTimeoutOverride and summarizerTimeoutOverride exist so tests can
// inject short timeouts without writing to the package-global config —
// a global write under test's t.Cleanup races with concurrent reads from
// the leader-election scheduler under -race -count=N. The production
// path (override == 0) reads config.Config at call time; the test path
// (override > 0) returns the override and never touches config.
type Executor struct {
	manager                   *Manager
	notifier                  Notifier
	pollTimeoutOverride       time.Duration
	summarizerTimeoutOverride time.Duration
}

// NewExecutor builds the default Executor with HTTP notifier.
func NewExecutor(m *Manager) *Executor {
	return &Executor{manager: m, notifier: HTTPNotifier{}}
}

// SetNotifier overrides the notifier (used by tests).
func (e *Executor) SetNotifier(n Notifier) { e.notifier = n }

// SetPollTimeout overrides the poll timeout for this Executor only.
// Test-only; production uses the config-backed default. Set to 0 to
// restore the config-backed default.
func (e *Executor) SetPollTimeout(d time.Duration) { e.pollTimeoutOverride = d }

// SetSummarizerTimeout overrides the summarizer timeout for this Executor
// only. Test-only; production uses the config-backed default. Set to 0 to
// restore the config-backed default.
func (e *Executor) SetSummarizerTimeout(d time.Duration) { e.summarizerTimeoutOverride = d }

// RunOnePoll executes one observation cycle. The function is the unit of
// work submitted to the watch worker pool — it must be self-contained: any
// transient state needed across polls lives in the DB row.
//
// Lifecycle:
//  1. expiry check     — if past expires_at, mark EXPIRED + notify
//  2. observe          — run the configured Source.Observe()
//  3. predicate eval   — terminate / reschedule / record failure
//  4. terminal notify  — single message to the conversation, no per-poll noise
//
// Budget enforcement is intentionally delegated: any LLM-backed call made
// inside a predicate or source already runs through the existing budget
// guards in the LLM client layer, so a tenant that exceeds its limit will
// see the watch surface a real "budget" error and fail naturally.
func (e *Executor) RunOnePoll(parent context.Context, w Watch) {
	// Panic-safety wrapper: RunOnePoll runs inside the dispatcher's worker
	// pool goroutines. A panic here (predicate bug, nil deref in a tool, etc.)
	// would otherwise propagate up the worker and crash the llm-server
	// process. Convert to a logged error so the watch is dropped from this
	// tick but other watches keep polling. Consistent with the recover()
	// pattern used by common.NewWorkerPool's job runner.
	defer func() {
		if r := recover(); r != nil {
			slog.Error("watch.executor: panic in RunOnePoll",
				"watch_id", w.ID.String(),
				"tenant_id", w.TenantID.String(),
				"panic", r)
		}
	}()

	rctx, err := e.buildRequestContext(parent, w)
	if err != nil {
		slog.Error("watch.executor: failed to build request context",
			"watch_id", w.ID.String(), "error", err)
		return
	}
	logger := rctx.GetLogger().With(
		slog.String("watch_id", w.ID.String()),
		slog.String("source_kind", string(w.SourceKind)),
		slog.String("predicate_kind", string(w.PredicateKind)),
	)

	if e.handleExpiry(rctx, logger, w) {
		return
	}

	src, ok := GetSource(w.SourceKind)
	if !ok {
		e.terminate(rctx, logger, w, StatusFailed, "", fmt.Sprintf("source_kind %q is not registered", w.SourceKind))
		return
	}

	pollCtx, cancel := context.WithTimeout(rctx.GetContext(), e.pollTimeout())
	defer cancel()
	pollRctx := security.NewRequestContext(pollCtx, rctx.GetSecurityContext(), logger, rctx.GetTracer(), rctx.GetMeter())

	obs, obsErr := src.Observe(pollRctx, w)
	if obsErr != nil {
		e.recordFailure(rctx, logger, w, fmt.Sprintf("observe failed: %v", obsErr))
		return
	}

	pred, predErr := CompilePredicate(w.PredicateKind, w.PredicateExpr)
	if predErr != nil {
		// Compilation passed at create time but somehow not now — treat
		// as terminal failure; agent re-create is the recovery path.
		e.terminate(rctx, logger, w, StatusFailed, "", fmt.Sprintf("predicate compile: %v", predErr))
		return
	}

	result, evalErr := pred.Eval(pollRctx, w, obs)
	if evalErr != nil {
		e.recordFailure(rctx, logger, w, fmt.Sprintf("predicate eval failed: %v", evalErr))
		return
	}
	met := result.Met
	if w.PredicateNegate {
		met = !met
	}

	if met {
		summary := result.Summary
		if w.PredicateNegate {
			summary = "[negated] " + summary
		}
		e.terminate(rctx, logger, w, StatusCompleted, summary, "")
		return
	}

	// Reschedule; reset failure_count since the source itself was healthy.
	next := time.Now().Add(time.Duration(w.PollIntervalSec) * time.Second)
	writeCtx, writeCancel := context.WithTimeout(rctx.GetContext(), managerWriteTimeout)
	defer writeCancel()
	if err := e.manager.UpdatePollResult(writeCtx, w.TenantID, w.ID, obs.Data, next, true); err != nil {
		logger.Error("watch.executor: failed to update poll result", "error", err)
	}
}

// handleExpiry returns true if the watch should stop polling due to
// expires_at. The notification carries whatever summary we can produce
// from the most recent state.
func (e *Executor) handleExpiry(rctx *security.RequestContext, logger *slog.Logger, w Watch) bool {
	if !time.Now().After(w.ExpiresAt) {
		return false
	}
	summary := "watch expired before condition was met"
	if w.LastPollResult != nil && *w.LastPollResult != "" {
		summary = "expired; last observation: " + truncate(*w.LastPollResult, 500)
	}
	e.terminate(rctx, logger, w, StatusExpired, summary, "")
	return true
}

func (e *Executor) recordFailure(rctx *security.RequestContext, logger *slog.Logger, w Watch, msg string) {
	maxFailures := config.Config.WatchMaxFailures
	if maxFailures <= 0 {
		maxFailures = 3
	}
	logger.Warn("watch.executor: poll failed", "failure_count", w.FailureCount+1, "msg", msg)

	if w.FailureCount+1 >= maxFailures {
		e.terminate(rctx, logger, w, StatusFailed, "", msg)
		return
	}

	backoff := time.Duration(w.PollIntervalSec) * time.Second
	mult := math.Pow(2, math.Min(float64(w.FailureCount), 4))
	backoff = time.Duration(float64(backoff) * mult)
	next := time.Now().Add(backoff)
	writeCtx, writeCancel := context.WithTimeout(rctx.GetContext(), managerWriteTimeout)
	defer writeCancel()
	if err := e.manager.IncFailures(writeCtx, w.TenantID, w.ID, next, msg); err != nil {
		logger.Error("watch.executor: failed to record failure", "error", err)
	}
}

func (e *Executor) terminate(rctx *security.RequestContext, logger *slog.Logger, w Watch, status Status, summary, errMsg string) {
	markCtx, markCancel := context.WithTimeout(rctx.GetContext(), managerWriteTimeout)
	rows, err := e.manager.MarkTerminal(markCtx, w.TenantID, w.ID, status, summary, errMsg)
	markCancel()
	if err != nil {
		logger.Error("watch.executor: mark terminal failed", "error", err, "status", string(status))
		return
	}
	// Lost-race short-circuit: the WHERE clause on MarkTerminal restricts
	// the UPDATE to rows still in PENDING/ACTIVE. If Cancel (or another
	// terminate) committed first, rows == 0 — the DB row is already in a
	// (different) terminal state and we must NOT proceed to summarizer,
	// responder, or notifier. Otherwise the user receives a contradictory
	// "✅ COMPLETED" follow-up after clicking Cancel.
	if rows == 0 {
		logger.Info("watch.executor: terminal transition no-op (row already terminal); skipping summarize/append/notify",
			"status", string(status))
		recordLostRace(rctx.GetContext(), status)
		return
	}
	recordTerminalTransition(rctx.GetContext(), status)

	// Generate the user-facing follow-up via the LLM summarizer (when wired)
	// so the message in the chat thread reads naturally and references the
	// original action. Falls back to the raw `summary` (matched-evidence
	// string from the predicate, or empty for non-COMPLETED states) if the
	// LLM call fails or no summarizer is wired — preserves the pre-summarizer
	// behaviour where the notifier rendered a canned template.
	deliverable := summary
	if factory := getSummarizerFactory(); factory != nil {
		summarizer, sErr := factory(rctx, w)
		if sErr != nil {
			logger.Warn("watch.executor: summarizer factory failed, using raw summary", "error", sErr)
		} else if summarizer != nil {
			// Bound the summarizer LLM call: pollCtx was already cancelled
			// by RunOnePoll's `defer cancel()` and the dispatcher passes
			// `context.Background()` as the parent, so without this wrap
			// a hung Gemini/Bedrock call would pin a watch worker
			// indefinitely. At WatchWorkerCount=10 ten such hangs drain
			// the pool and every subsequent terminal transition stalls.
			summarizerCtx, summarizerCancel := context.WithTimeout(rctx.GetContext(), e.summarizerTimeout())
			summarizerRctx := security.NewRequestContext(summarizerCtx, rctx.GetSecurityContext(), logger, rctx.GetTracer(), rctx.GetMeter())
			rich, sumErr := summarizer.Summarize(summarizerRctx, w, status, summary, errMsg)
			summarizerCancel()
			if sumErr != nil {
				// Timeout / LLM error → fall back to the raw predicate
				// summary so the responder still appends a usable block
				// instead of a silent skip.
				logger.Warn("watch.executor: summarize failed, using raw summary", "error", sumErr)
				reason := "error"
				if summarizerCtx.Err() != nil {
					reason = "timeout"
				}
				recordSummarizerFailure(rctx.GetContext(), reason)
			} else if strings.TrimSpace(rich) != "" {
				deliverable = rich
			}
		}
	}

	// Append the rendered block to the parent conversation's most recent
	// response message so the user sees the follow-up inline. Idempotent
	// (keyed on watch.ID inside the rendered marker), so a duplicate
	// terminate call cannot double-append.
	if err := e.manager.AppendWatchUpdateToConversation(rctx.GetContext(), w, status, deliverable); err != nil {
		logger.Error("watch.executor: append watch update to conversation failed", "error", err, "status", string(status))
		recordResponderFailure(rctx.GetContext())
	}

	// Continue to fire the notifications-server hook with the same
	// LLM-generated text so any external channel (Slack/email, when the
	// notifier server is up) gets the same polished message. Failure
	// here is silent for the user (no retry, no DLQ in v1) — emit a
	// counter so on-call can alert when a tenant's notifications dry up
	// even though watches keep terminating in the DB.
	if err := e.notifier.Notify(rctx, w, status, deliverable); err != nil {
		logger.Error("watch.executor: notify failed", "error", err, "status", string(status))
		recordNotifierFailure(rctx.GetContext(), status)
	}
	logger.Info("watch.executor: terminated", "status", string(status))
}

// buildRequestContext constructs a watch-scoped *security.RequestContext.
// Crucially the conversation_id is NOT injected — polls run in an
// isolated session so they do not pollute the user's conversation
// history. Only the terminal notification touches the conversation.
func (e *Executor) buildRequestContext(parent context.Context, w Watch) (*security.RequestContext, error) {
	builder := getSecurityContextBuilder()
	if builder == nil {
		return nil, fmt.Errorf("watch: no security context builder registered")
	}
	sc, err := builder(w)
	if err != nil {
		return nil, fmt.Errorf("watch: build security context: %w", err)
	}
	logger := slog.Default().With(
		slog.String("watch_id", w.ID.String()),
		slog.String("tenant_id", w.TenantID.String()),
		slog.String("account_id", w.AccountID.String()),
	)
	tracer := otel.GetTracerProvider().Tracer("nudgebee-watch")
	return security.NewRequestContext(parent, sc, logger, tracer, nil), nil
}

// pollTimeout returns the bound on a single Source.Observe call. The
// per-Executor override (set via SetPollTimeout) wins over the config-
// backed default. Decoupling the timeout from package globals lets tests
// inject short timeouts without writing to config.Config, which was
// previously racy under -race -count=N because the cluster's gocron
// scheduler reads config concurrently with the test's t.Cleanup restore.
func (e *Executor) pollTimeout() time.Duration {
	if e.pollTimeoutOverride > 0 {
		return e.pollTimeoutOverride
	}
	if s := config.Config.WatchPollTimeoutSec; s > 0 {
		return time.Duration(s) * time.Second
	}
	return 15 * time.Second
}

// summarizerTimeout caps the LLM call that renders the terminal block.
// The terminate path runs inside the watch worker pool — without an
// explicit deadline a hung model call holds the worker forever (parent
// context comes from dispatcher.RunOnePoll(context.Background(), …)).
// Configurable via env override; 30s is a generous default for the
// expected ~1-2KB summarizer prompt. Per-Executor override (test-only)
// wins for the same race-avoidance reason as pollTimeout above.
func (e *Executor) summarizerTimeout() time.Duration {
	if e.summarizerTimeoutOverride > 0 {
		return e.summarizerTimeoutOverride
	}
	if s := config.Config.WatchSummarizerTimeoutSec; s > 0 {
		return time.Duration(s) * time.Second
	}
	return 30 * time.Second
}
