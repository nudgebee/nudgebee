package mq

import (
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isClosed reports whether ch is already closed, without blocking.
func isClosed(t *testing.T, ch <-chan struct{}) bool {
	t.Helper()
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// An unknown correlation ID must yield a closed channel, never nil: the
// register session selects on it, and nil would block that select forever —
// holding the prefetch slot this whole mechanism exists to release.
func TestAbandonChUnknownCorrIDIsClosedNotNil(t *testing.T) {
	c := &ClientImpl{}

	ch := c.AbandonCh("never-published")

	require.NotNil(t, ch)
	assert.True(t, isClosed(t, ch), "unknown corrID should read as already abandoned")
}

func TestAbandonChTracksPendingCallLifecycle(t *testing.T) {
	c := &ClientImpl{}
	call := &pendingCall{resp: make(chan []byte, 1), abandon: make(chan struct{})}
	c.pending.Store("corr-1", call)

	ch := c.AbandonCh("corr-1")
	assert.False(t, isClosed(t, ch), "a live caller must not read as abandoned")

	// This is what Call's ctx.Done() branch does when the caller gives up.
	_, ours := c.pending.LoadAndDelete("corr-1")
	require.True(t, ours)
	close(call.abandon)

	assert.True(t, isClosed(t, ch), "giving up must close the channel handed out earlier")
}

// A reply retires the pending entry without closing abandon, so the delivery
// side never sees a successful request as abandoned.
func TestDeliverDoesNotSignalAbandon(t *testing.T) {
	c := &ClientImpl{}
	call := &pendingCall{resp: make(chan []byte, 1), abandon: make(chan struct{})}
	c.pending.Store("corr-1", call)
	abandon := c.AbandonCh("corr-1")

	require.True(t, c.deliver("corr-1", []byte(`{"ok":true}`)))

	assert.Equal(t, []byte(`{"ok":true}`), <-call.resp)
	assert.False(t, isClosed(t, abandon), "a delivered reply must not look like abandonment")

	// The entry is retired, so a second delivery finds no caller.
	assert.False(t, c.deliver("corr-1", []byte(`{}`)))
}

func TestDeadlineFromHeaders(t *testing.T) {
	want := time.Unix(0, 1789551763195453466)

	tests := []struct {
		name    string
		headers amqp.Table
		ok      bool
	}{
		{"int64 as published", amqp.Table{HeaderRelayDeadline: want.UnixNano()}, true},
		{"int as a field table may normalise it", amqp.Table{HeaderRelayDeadline: int(want.UnixNano())}, true},
		{"absent header, pre-upgrade publisher", amqp.Table{}, false},
		{"nil headers", nil, false},
		{"wrong type", amqp.Table{HeaderRelayDeadline: "soon"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := DeadlineFromHeaders(tt.headers)
			assert.Equal(t, tt.ok, ok)
			if tt.ok {
				assert.True(t, got.Equal(want), "want %v, got %v", want, got)
			}
		})
	}
}

// int32 is covered separately because it can only carry a deadline far in the
// past; the point is that the type is accepted, not the value.
func TestDeadlineFromHeadersAcceptsInt32(t *testing.T) {
	got, ok := DeadlineFromHeaders(amqp.Table{HeaderRelayDeadline: int32(1_000_000_000)})

	require.True(t, ok)
	assert.True(t, got.Equal(time.Unix(0, 1_000_000_000)))
}
