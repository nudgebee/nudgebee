package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"nudgebee/services/localagent"
)

// attemptTimeout bounds each first-run attempt. The first one runs before the
// server listens and the liveness and readiness probes declare no
// initialDelaySeconds, so an unbounded query against an unresponsive database
// would burn the probe budget and get the pod killed before it ever served.
const attemptTimeout = 10 * time.Second

// retryDelays is the wait before each background retry; the last entry repeats
// until retryBudget runs out. On a fresh install the migration Job starts only
// after these pods do (it is a post-install hook) and a full schema takes a
// minute or two, so the early retries are what usually succeed.
var retryDelays = []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, time.Minute}

// retryBudget stops the retries for an error that will not clear on its own —
// an invalid agent.clusterName, say — instead of logging it for the life of the pod.
const retryBudget = 30 * time.Minute

// RunFirstRun provisions the first admin and registers the bundled cluster.
// It makes one bounded attempt before returning and, if that fails, keeps
// retrying in the background until both steps succeed, ctx is cancelled, or
// retryBudget is spent.
//
// The retry is what makes a fresh install converge. The migration Job runs
// after these pods start, so the first attempt can meet a half-migrated
// schema; it used to be the only attempt, and only a pod restart ran it again.
// Both steps converge rather than duplicate, so repeating them is safe.
func RunFirstRun(ctx context.Context, logger *slog.Logger) {
	runWithRetry(ctx, logger, firstRunAttempt, retryDelays, retryBudget)
}

// firstRunAttempt runs both steps every time rather than skipping one that
// already succeeded: Provision registers the cluster inside OnboardUser but
// only logs a failure there, so the Reconcile that follows is the step that
// reports whether the cluster actually landed.
func firstRunAttempt(ctx context.Context, logger *slog.Logger) error {
	return errors.Join(Provision(ctx, logger), localagent.Reconcile(ctx, logger))
}

func runWithRetry(ctx context.Context, logger *slog.Logger, attempt func(context.Context, *slog.Logger) error, delays []time.Duration, budget time.Duration) {
	err := attemptOnce(ctx, logger, attempt)
	if err == nil {
		return
	}
	logger.Warn("first run: setup did not complete, retrying in the background — expected on a fresh install while the database migration is still running", "error", err)

	go func() {
		deadline := time.Now().Add(budget)
		for i := 0; ; i++ {
			delay := delays[min(i, len(delays)-1)]
			if time.Now().Add(delay).After(deadline) {
				logger.Error("first run: gave up retrying setup; the admin or the bundled cluster may be missing until services-server restarts", "error", err)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			if err = attemptOnce(ctx, logger, attempt); err == nil {
				logger.Info("first run: setup completed on retry", "attempt", i+2)
				return
			}
			if ctx.Err() != nil {
				return
			}
		}
	}()
}

func attemptOnce(ctx context.Context, logger *slog.Logger, attempt func(context.Context, *slog.Logger) error) error {
	attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	return attempt(attemptCtx, logger)
}
