package mq

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient builds a ClientImpl with only the fields the delivery paths
// touch, so these cases need no broker.
func newTestClient() *ClientImpl {
	return &ClientImpl{instanceID: "instance-a"}
}

// waitFor registers a pending caller the way Call does and returns its channel.
func waitFor(c *ClientImpl, corrID string) chan []byte {
	call := &pendingCall{resp: make(chan []byte, 1), abandon: make(chan struct{})}
	c.pending.Store(corrID, call)
	return call.resp
}

func TestDeliverLocalReachesWaitingCaller(t *testing.T) {
	c := newTestClient()
	respCh := waitFor(c, "corr-1")

	assert.True(t, c.DeliverLocal("corr-1", []byte(`{"ok":true}`)))
	assert.Equal(t, []byte(`{"ok":true}`), <-respCh)

	_, stillPending := c.pending.Load("corr-1")
	assert.False(t, stillPending, "delivery should retire the pending entry")
}

func TestDeliverLocalReportsNoWaiter(t *testing.T) {
	c := newTestClient()
	assert.False(t, c.DeliverLocal("nobody-is-waiting", []byte(`{}`)),
		"caller must fall back to publishing when the waiter is on another replica")
}

func TestDeliverLocalAfterCallerGaveUp(t *testing.T) {
	c := newTestClient()
	waitFor(c, "corr-2")

	// Call deletes its pending entry when ctx expires.
	c.pending.Delete("corr-2")

	assert.False(t, c.DeliverLocal("corr-2", []byte(`{}`)),
		"a timed-out caller must not be reported as delivered")
}

func TestDeliverLocalIsNotReentrant(t *testing.T) {
	c := newTestClient()
	respCh := waitFor(c, "corr-3")

	assert.True(t, c.DeliverLocal("corr-3", []byte(`first`)))
	assert.False(t, c.DeliverLocal("corr-3", []byte(`second`)),
		"a second delivery must not send on the closed channel")
	assert.Equal(t, []byte(`first`), <-respCh)
}

// Both the AMQP dispatch loop and the in-process handover can race for one
// correlation ID. Exactly one must win; neither may send on a closed channel.
func TestDeliverLocalRacesDispatchLoopSafely(t *testing.T) {
	for i := 0; i < 200; i++ {
		c := newTestClient()
		respCh := waitFor(c, "corr-race")

		var delivered atomic.Int32
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if c.DeliverLocal("corr-race", []byte(`local`)) {
				delivered.Add(1)
			}
		}()
		go func() {
			defer wg.Done()
			if c.deliver("corr-race", []byte(`amqp`)) {
				delivered.Add(1)
			}
		}()
		wg.Wait()

		require.Equal(t, int32(1), delivered.Load(), "exactly one path must deliver")
		body := <-respCh
		require.Contains(t, []string{"local", "amqp"}, string(body))
	}
}

func TestInstanceIDIsStableAndUnique(t *testing.T) {
	c := newTestClient()
	assert.Equal(t, c.InstanceID(), c.InstanceID(), "instance id must not change per call")
	assert.NotEmpty(t, c.InstanceID())
}
