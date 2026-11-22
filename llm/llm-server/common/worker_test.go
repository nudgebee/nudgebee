package common

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkerPool_Submit_AcceptsWhenCapacityAvailable(t *testing.T) {
	pool := NewWorkerPool("test-pool-capacity", 1, 1)
	defer pool.Stop()

	done := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := pool.Submit(ctx, func() { close(done) })
	require.NoError(t, err)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("submitted task never ran")
	}
}

// Regression test for #36378: Submit must respect ctx's deadline instead of
// blocking forever when the pool is saturated.
func TestWorkerPool_Submit_TimesOutWhenSaturated(t *testing.T) {
	pool := NewWorkerPool("test-pool-saturated", 1, 1)
	defer pool.Stop()

	release := make(chan struct{})
	workerBusy := make(chan struct{})

	require.NoError(t, pool.Submit(context.Background(), func() {
		close(workerBusy)
		<-release
	}))
	<-workerBusy // wait until the worker is actually occupied

	require.NoError(t, pool.Submit(context.Background(), func() { <-release })) // fills the queue

	timeout := 150 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	err := pool.Submit(ctx, func() {})
	elapsed := time.Since(start)

	close(release) // let the blocked tasks finish so pool.Stop() doesn't hang

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrWorkerPoolTimeout), "expected ErrWorkerPoolTimeout, got %v", err)
	assert.Less(t, elapsed, 2*time.Second, "Submit blocked well past its context deadline")
}

// Regression test for #41826: Submit called after Stop must return ErrWorkerPoolStopped,
// and must never panic with "send on closed channel".
func TestWorkerPool_Submit_AfterStopReturnsError(t *testing.T) {
	pool := NewWorkerPool("test-pool-stopped", 1, 1)
	pool.Stop()

	// Calling Submit after Stop must return ErrWorkerPoolStopped, not panic
	err := pool.Submit(context.Background(), func() {})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrWorkerPoolStopped), "expected ErrWorkerPoolStopped, got %v", err)
}

// Regression test for #41826: A submitter blocked on a full queue must unblock promptly
// when Stop() is called, returning ErrWorkerPoolStopped without panic.
func TestWorkerPool_Submit_BlockedReturnsOnStop(t *testing.T) {
	pool := NewWorkerPool("test-pool-blocked-stop", 1, 0) // 0 queue size: every second submit blocks

	release := make(chan struct{})
	workerStarted := make(chan struct{})

	require.NoError(t, pool.Submit(context.Background(), func() {
		close(workerStarted)
		<-release
	}))
	<-workerStarted // worker is now occupied

	errChan := make(chan error, 1)
	go func() {
		// This submit blocks because queue size is 0 and worker is busy
		errChan <- pool.Submit(context.Background(), func() {})
	}()

	// Ensure Submit has acquired p.mu.RLock() and is actively waiting in select.
	// Since Submit holds RLock while waiting in select, TryLock will return false.
	require.Eventually(t, func() bool {
		if pool.mu.TryLock() {
			pool.mu.Unlock()
			return false
		}
		return true
	}, time.Second, 5*time.Millisecond, "Submit never acquired RLock/blocked in select")

	// Call pool.Stop() in a goroutine while the worker is still occupied.
	// close(p.quit) fires before workers drain, so the blocked Submit must return promptly.
	stopDone := make(chan struct{})
	go func() {
		pool.Stop()
		close(stopDone)
	}()

	select {
	case err := <-errChan:
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrWorkerPoolStopped), "expected ErrWorkerPoolStopped, got %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("blocked Submit did not return after pool.Stop()")
	}

	// Release worker so Stop() can complete cleanly and join its WaitGroup
	close(release)
	select {
	case <-stopDone:
		// Pool successfully stopped and workers drained
	case <-time.After(2 * time.Second):
		t.Fatal("pool.Stop() failed to finish")
	}
}

func TestWorkerPool_Stop_MultipleCallsSafe(t *testing.T) {
	pool := NewWorkerPool("test-pool-multi-stop", 1, 1)
	assert.NotPanics(t, func() {
		pool.Stop()
		pool.Stop()
	})
}
