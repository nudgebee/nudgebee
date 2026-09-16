// Package health tracks whether the relay can actually serve tenant traffic,
// as opposed to merely being a running process.
//
// The relay can reach a state where every outward sign is healthy — the pod is
// Running, /status answers, the agent WebSocket is open — while it serves
// nothing, because its RabbitMQ consumer cannot attach. Kubernetes has no way
// to see that, so the pod is never restarted and the condition persists until
// someone intervenes by hand.
package health

import (
	"fmt"
	"sync"
	"time"
)

// BrokerState reports whether the AMQP connection is currently usable. It is
// the difference between "our dependency is down" and "our dependency is fine
// and we still cannot work", and only the second is worth restarting for:
// restarting during a broker outage would crashloop every replica and destroy
// the agent WebSockets for no gain.
type BrokerState interface {
	IsConnected() bool
}

// Tracker records, per tenant queue, how long the session has been unable to
// attach a consumer.
type Tracker struct {
	mu           sync.Mutex
	failingSince map[string]time.Time
	broker       BrokerState
	now          func() time.Time
}

func NewTracker(broker BrokerState) *Tracker {
	return &Tracker{
		failingSince: make(map[string]time.Time),
		broker:       broker,
		now:          time.Now,
	}
}

// ConsumeFailed records that a session could not attach its consumer. The first
// failure starts the clock; later ones leave it alone, so the duration measures
// the whole outage rather than the gap between retries.
func (t *Tracker) ConsumeFailed(queue string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.failingSince[queue]; !ok {
		t.failingSince[queue] = t.now()
	}
}

// ConsumerAttached records that a session is consuming again, clearing any
// failure streak for that queue.
func (t *Tracker) ConsumerAttached(queue string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failingSince, queue)
}

// SessionEnded forgets a queue entirely. A session that has gone away is not a
// stuck session, and leaving the entry behind would keep the relay unhealthy
// forever over a tenant that simply disconnected.
func (t *Tracker) SessionEnded(queue string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failingSince, queue)
}

// Wedged reports whether some tenant has been unable to consume for longer than
// threshold while the broker itself is reachable, along with a description for
// the operator reading the probe failure.
//
// A zero threshold disables the check entirely, which is the escape hatch if
// this ever misjudges a healthy relay: the cost of a false positive is a
// restart loop across every replica.
func (t *Tracker) Wedged(threshold time.Duration) (bool, string) {
	// Nil-safe so an unconfigured or test wiring never panics the relay.
	if t == nil || threshold <= 0 {
		return false, ""
	}
	// A broker outage is not this relay's fault and restarting cannot fix it.
	if t.broker == nil || !t.broker.IsConnected() {
		return false, ""
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	for queue, since := range t.failingSince {
		if stuck := now.Sub(since); stuck > threshold {
			return true, fmt.Sprintf(
				"queue %s has had no consumer for %s while the broker is reachable", queue, stuck.Truncate(time.Second))
		}
	}
	return false, ""
}
