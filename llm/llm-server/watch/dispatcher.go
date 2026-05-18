package watch

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"sync"
	"time"
)

// Dispatcher is the top-level driver: a leader-elected ticker that fetches
// due watches and submits each to the worker pool. There is exactly one
// Dispatcher per process; RegisterDispatcher is idempotent.
type Dispatcher struct {
	manager  *Manager
	executor *Executor
	pool     *common.WorkerPool
}

var (
	dispatcherMu         sync.Mutex
	registeredDispatcher *Dispatcher
)

// RegisterDispatcher wires the watch dispatcher into the existing
// leader-elected scheduler. It is a no-op when LLM_SERVER_WATCH_ENABLED is
// false, so the feature can ship dark and be enabled per-environment.
// Calling more than once is safe (subsequent calls are no-ops).
func RegisterDispatcher() error {
	dispatcherMu.Lock()
	defer dispatcherMu.Unlock()

	if !config.Config.WatchEnabled {
		slog.Info("watch.dispatcher: disabled by config")
		return nil
	}
	if registeredDispatcher != nil {
		return nil
	}

	manager := NewManager()
	executor := NewExecutor(manager)
	pool := common.NewWorkerPool("watch_poll",
		coalesce(config.Config.WatchWorkerCount, 10),
		coalesce(config.Config.WatchWorkerQueueSize, 50),
	)

	d := &Dispatcher{manager: manager, executor: executor, pool: pool}

	interval := time.Duration(coalesce(config.Config.WatchDispatcherIntervalSec, 10)) * time.Second

	// Local-mode bypass: in shared-DB demos a dev cluster pod often holds the
	// leader-election lease, which means our local watch dispatcher never
	// ticks. Setting WatchBypassLeaderElection=true runs the tick loop in a
	// plain goroutine instead, so a single-machine llm-server can drive its
	// own polls without waiting for the cluster lease. NOT for production —
	// running with this flag in a multi-replica deployment causes every
	// replica to poll concurrently.
	if config.Config.WatchBypassLeaderElection {
		go func() {
			t := time.NewTicker(interval)
			defer t.Stop()
			for range t.C {
				if err := d.tick(); err != nil {
					slog.Error("watch.dispatcher: tick failed", "error", err)
				}
			}
		}()
		registeredDispatcher = d
		slog.Warn("watch.dispatcher: registered (LEADER ELECTION BYPASSED — local mode)",
			"interval_sec", interval.Seconds(),
			"worker_count", coalesce(config.Config.WatchWorkerCount, 10),
		)
		return nil
	}

	if err := common.NewLeaderIntervalJob("watch_dispatcher", d.tick, interval); err != nil {
		return fmt.Errorf("watch.dispatcher: register failed: %w", err)
	}

	registeredDispatcher = d
	slog.Info("watch.dispatcher: registered",
		"interval_sec", interval.Seconds(),
		"worker_count", coalesce(config.Config.WatchWorkerCount, 10),
		"queue_size", coalesce(config.Config.WatchWorkerQueueSize, 50),
	)
	return nil
}

// tick is the leader job body. It runs on a single process at a time
// (gocron singleton mode under the dbElector), fetches a bounded batch of
// due rows, and fans them out to the worker pool. We do not block on
// completion — failures and reschedules are persisted on the row.
func (d *Dispatcher) tick() error {
	batchSize := coalesce(config.Config.WatchDispatchBatchSize, 100)
	fetchCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	due, err := d.manager.FetchDue(fetchCtx, batchSize)
	if err != nil {
		slog.Error("watch.dispatcher: fetch due failed", "error", err)
		return fmt.Errorf("watch.dispatcher: fetch due: %w", err)
	}
	if len(due) == 0 {
		return nil
	}

	slog.Debug("watch.dispatcher: dispatching", "count", len(due))

	submitTimeout := time.Duration(coalesce(config.Config.WatchSubmitTimeoutSec, 5)) * time.Second
	for _, w := range due {
		w := w
		submitCtx, submitCancel := context.WithTimeout(context.Background(), submitTimeout)
		err := d.pool.Submit(submitCtx, func() {
			d.executor.RunOnePoll(context.Background(), w)
		})
		submitCancel()
		if err != nil {
			// Pool full: advance next_poll_at by one poll interval so the
			// row doesn't get refetched + rejected on every tick. Without
			// this bump the same backlogged row is the FetchDue head every
			// tick, starving the rest of the batch — a handful of slow
			// watches in one tenant could cascade-starve every other watch
			// in the cluster.
			next := time.Now().Add(time.Duration(w.PollIntervalSec) * time.Second)
			if rerr := d.manager.RescheduleNextPollAt(context.Background(), w.TenantID, w.ID, next); rerr != nil {
				slog.Warn("watch.dispatcher: submit failed AND reschedule failed",
					"watch_id", w.ID.String(), "submit_error", err, "reschedule_error", rerr)
			} else {
				slog.Warn("watch.dispatcher: submit failed; advanced next_poll_at",
					"watch_id", w.ID.String(), "next_poll_at", next, "error", err)
			}
		}
	}
	return nil
}

func coalesce(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}
