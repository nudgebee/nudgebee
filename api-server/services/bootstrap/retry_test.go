package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// attemptFailingTimes fails its first n calls and succeeds after that, closing
// done on the first success.
func attemptFailingTimes(n int32, calls *atomic.Int32, done chan struct{}) func(context.Context, *slog.Logger) error {
	return func(context.Context, *slog.Logger) error {
		if calls.Add(1) <= n {
			return errors.New(`pq: column "access_secret_v2" of relation "cloud_accounts" does not exist`)
		}
		close(done)
		return nil
	}
}

func TestRunWithRetryDoesNotRetryAfterAFirstSuccess(t *testing.T) {
	var calls atomic.Int32
	done := make(chan struct{})
	runWithRetry(context.Background(), slog.Default(), attemptFailingTimes(0, &calls, done), []time.Duration{time.Millisecond}, time.Second)

	time.Sleep(20 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

// The fresh-install case: the schema is not migrated yet on the first attempts,
// and setup has to land once it is, without a restart.
func TestRunWithRetryKeepsTryingUntilSetupSucceeds(t *testing.T) {
	var calls atomic.Int32
	done := make(chan struct{})
	runWithRetry(context.Background(), slog.Default(), attemptFailingTimes(3, &calls, done), []time.Duration{time.Millisecond}, time.Minute)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("setup never succeeded; attempts = %d", calls.Load())
	}
	time.Sleep(20 * time.Millisecond)
	if got := calls.Load(); got != 4 {
		t.Fatalf("attempts = %d, want 4 (3 failures, then 1 success, then none)", got)
	}
}

func TestRunWithRetryGivesUpWhenTheBudgetIsSpent(t *testing.T) {
	var calls atomic.Int32
	done := make(chan struct{})
	runWithRetry(context.Background(), slog.Default(), attemptFailingTimes(1000, &calls, done), []time.Duration{10 * time.Millisecond}, 35*time.Millisecond)

	time.Sleep(150 * time.Millisecond)
	got := calls.Load()
	// One attempt up front, then retries at ~10, 20 and 30ms; the next would land past the budget.
	if got < 2 || got > 4 {
		t.Fatalf("attempts = %d, want 2-4 within a 35ms budget", got)
	}
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != got {
		t.Fatalf("retries continued after the budget was spent: %d -> %d", got, calls.Load())
	}
}

func TestRunWithRetryStopsOnShutdown(t *testing.T) {
	var calls atomic.Int32
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	runWithRetry(ctx, slog.Default(), attemptFailingTimes(1000, &calls, done), []time.Duration{10 * time.Millisecond}, time.Minute)

	cancel()
	time.Sleep(50 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry after cancel)", got)
	}
}

func TestRunWithRetryHoldsTheLastDelay(t *testing.T) {
	var calls atomic.Int32
	done := make(chan struct{})
	// The second delay repeats once the list is exhausted; a short budget proves
	// the loop indexes past the end of the list without panicking.
	runWithRetry(context.Background(), slog.Default(), attemptFailingTimes(4, &calls, done), []time.Duration{time.Millisecond, 2 * time.Millisecond}, time.Minute)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("setup never succeeded; attempts = %d", calls.Load())
	}
}
