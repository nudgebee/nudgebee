package mq

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// canceledCtx returns a context that is already done, so a declare attempt fails
// immediately instead of waiting on a broker that is not there.
func canceledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// settled builds a declaration that has already finished with err, the way a
// completed declare leaves it in the cache.
func settled(err error) *declaration {
	d := &declaration{done: make(chan struct{}), err: err}
	close(d.done)
	return d
}

// declaredKeys lists the queue names currently marked as declared.
func declaredKeys(t *Topology) []string {
	var keys []string
	t.declared.Range(func(k, _ any) bool {
		keys = append(keys, k.(string))
		return true
	})
	return keys
}

func TestForgetTenantDropsTheDeclaredRecord(t *testing.T) {
	topo := &Topology{}
	qName := RelayQueueName("acct-1", "k8s")
	topo.declared.Store(qName, settled(nil))

	topo.ForgetTenant("acct-1", "k8s")

	assert.Empty(t, declaredKeys(topo),
		"a forgotten tenant must really re-declare, not short-circuit on the cache")
}

// Proxy agents get their own queue, so forgetting one agent type must not
// silently drop the other's record.
func TestForgetTenantIsScopedToAgentType(t *testing.T) {
	topo := &Topology{}
	topo.declared.Store(RelayQueueName("acct-1", "k8s"), settled(nil))
	topo.declared.Store(RelayQueueName("acct-1", "proxy"), settled(nil))

	topo.ForgetTenant("acct-1", "proxy")

	require.Equal(t, []string{RelayQueueName("acct-1", "k8s")}, declaredKeys(topo))
}

// The bug this guards: EnsureTenantForAgentType stakes the cache entry before
// the declare runs. When the declare fails the entry must be retracted, or a
// queue that does not exist is recorded as present and every later call returns
// success without touching the broker — leaving that tenant unroutable until the
// process restarts.
func TestFailedDeclareDoesNotPoisonTheCache(t *testing.T) {
	// No connection manager state means GetChannel can never hand back a
	// channel, so the declare is guaranteed to fail.
	topo := &Topology{connMgr: &ConnectionManager{}}
	qName := RelayQueueName("acct-1", "k8s")

	err := topo.EnsureTenantForAgentType(canceledCtx(), "acct-1", "k8s")

	require.Error(t, err, "declare must fail without a broker")
	assert.NotContains(t, declaredKeys(topo), qName,
		"a failed declare must leave no trace, so the next attempt really retries")
}

// A retry after a failure must still reach for the broker rather than report
// success from a stale entry.
func TestRetryAfterFailedDeclareStillAttempts(t *testing.T) {
	topo := &Topology{connMgr: &ConnectionManager{}}

	first := topo.EnsureTenantForAgentType(canceledCtx(), "acct-1", "k8s")
	second := topo.EnsureTenantForAgentType(canceledCtx(), "acct-1", "k8s")

	require.Error(t, first)
	assert.Error(t, second, "the retry must not short-circuit to success on a stale entry")
}

// A successful declare should still be cached, or every request would pay a
// round trip to the broker.
func TestSuccessfulDeclareIsRemembered(t *testing.T) {
	topo := &Topology{}
	qName := RelayQueueName("acct-1", "k8s")
	topo.declared.Store(qName, settled(nil))

	// Second call short-circuits on the cache and never touches connMgr, which
	// is nil here — so reaching the broker at all would panic.
	require.NoError(t, topo.EnsureTenantForAgentType(context.Background(), "acct-1", "k8s"))
}

// The race the review caught: a second caller for the same queue must not be
// told "declared" while the first is still talking to the broker. Publishing
// then goes to an exchange with nothing bound yet and is discarded silently.
func TestConcurrentCallersWaitForTheDeclareToFinish(t *testing.T) {
	topo := &Topology{}
	qName := RelayQueueName("acct-1", "proxy")

	inFlight := &declaration{done: make(chan struct{})}
	topo.declared.Store(qName, inFlight)

	result := make(chan error, 1)
	go func() {
		result <- topo.EnsureTenantForAgentType(context.Background(), "acct-1", "proxy")
	}()

	select {
	case <-result:
		t.Fatal("second caller returned while the declare was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	// The declare finishes and its outcome is shared, not re-derived.
	inFlight.err = errDeclareFailed
	close(inFlight.done)

	select {
	case err := <-result:
		assert.Equal(t, errDeclareFailed, err, "the waiter must see the real outcome")
	case <-time.After(2 * time.Second):
		t.Fatal("second caller never woke after the declare finished")
	}
}

// Waiting must honour the waiter's own deadline. The declaring caller may be a
// long-lived session while the waiter is a short HTTP request, and inheriting
// the declarer's deadline would block it far past its own.
func TestWaiterIsNotHeldPastItsOwnDeadline(t *testing.T) {
	topo := &Topology{}
	qName := RelayQueueName("acct-1", "proxy")
	// Never closed: the declare is still running.
	topo.declared.Store(qName, &declaration{done: make(chan struct{})})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := topo.EnsureTenantForAgentType(ctx, "acct-1", "proxy")

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), time.Second, "must give up on its own deadline, not the declarer's")
}

var errDeclareFailed = errors.New("declare failed")
