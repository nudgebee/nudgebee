package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// routeWithin fails the test if route blocks: a blocked route is a WebSocket
// reader that has stopped reading the agent's socket for good.
func routeWithin(t *testing.T, r *replyRouter, id string, raw []byte) (found, delivered bool) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		found, delivered = r.route(id, raw)
		close(done)
	}()
	select {
	case <-done:
		return found, delivered
	case <-time.After(time.Second):
		t.Fatalf("route(%q) blocked", id)
		return false, false
	}
}

// The agent answered the same request ID more times than anyone was waiting
// for while the waiter was still busy with the first reply. The old reader
// blocked here holding the map lock, so the waiter's own cleanup deadlocked
// behind it and the session never read the socket again.
func TestReplyRouter_ExtraRepliesDoNotBlock(t *testing.T) {
	r := newReplyRouter()
	// No deferred release: with the old blocking route it would wait on the
	// lock the stuck route holds, hanging the test instead of failing it.
	ch, _ := r.register("req-1")

	found, delivered := routeWithin(t, r, "req-1", []byte("first"))
	assert.True(t, found)
	assert.True(t, delivered)

	for range 3 {
		_, delivered = routeWithin(t, r, "req-1", []byte("dup"))
		assert.False(t, delivered, "a waiter takes one reply only")
	}
	assert.Equal(t, []byte("first"), <-ch)
}

func TestReplyRouter_UnknownID(t *testing.T) {
	r := newReplyRouter()
	found, delivered := routeWithin(t, r, "nobody", []byte("x"))
	assert.False(t, found)
	assert.False(t, delivered)
}

// Two copies of one request in flight each get one reply. The first copy has
// read its reply but is still busy with it (not yet released), so its slot is
// empty again — the second reply must still go to the second copy.
func TestReplyRouter_DuplicateInFlightRequests(t *testing.T) {
	r := newReplyRouter()
	ch1, release1 := r.register("req-1")
	ch2, release2 := r.register("req-1")
	defer release1()
	defer release2()

	_, delivered := routeWithin(t, r, "req-1", []byte("a"))
	require.True(t, delivered)
	assert.Equal(t, []byte("a"), <-ch1)

	_, delivered = routeWithin(t, r, "req-1", []byte("b"))
	require.True(t, delivered, "second copy must still be registered")
	select {
	case got := <-ch2:
		assert.Equal(t, []byte("b"), got)
	case <-time.After(time.Second):
		t.Fatal("second reply went back to the first copy instead of the second")
	}
}

// A copy that already got its reply releasing late must not unregister a
// copy still waiting on the same ID.
func TestReplyRouter_LateReleaseKeepsOtherWaiter(t *testing.T) {
	r := newReplyRouter()
	_, release1 := r.register("req-1")
	ch2, release2 := r.register("req-1")
	defer release2()

	_, delivered := routeWithin(t, r, "req-1", []byte("a"))
	require.True(t, delivered)
	release1()

	_, delivered = routeWithin(t, r, "req-1", []byte("b"))
	require.True(t, delivered)
	assert.Equal(t, []byte("b"), <-ch2)
}

func TestReplyRouter_ReleaseRemovesEntry(t *testing.T) {
	r := newReplyRouter()
	_, release := r.register("req-1")
	release()
	found, _ := routeWithin(t, r, "req-1", []byte("late"))
	assert.False(t, found)
	assert.Empty(t, r.m)
}
