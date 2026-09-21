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
