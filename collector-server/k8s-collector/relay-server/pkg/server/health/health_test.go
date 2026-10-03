package health

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeBroker struct{ connected bool }

func (f *fakeBroker) IsConnected() bool { return f.connected }

// trackerAt builds a Tracker whose clock the test drives.
func trackerAt(broker BrokerState, clock *time.Time) *Tracker {
	t := NewTracker(broker)
	t.now = func() time.Time { return *clock }
	return t
}

func TestHealthyWhenConsumersAreAttached(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tr := trackerAt(&fakeBroker{connected: true}, &now)

	tr.ConsumerAttached("relay_requests_a")

	wedged, _ := tr.Wedged(5 * time.Minute)
	assert.False(t, wedged)
}

// The customer's failure: the broker is reachable, but a tenant has had no
// consumer for a long time. That is the only shape worth restarting for.
func TestWedgedWhenConsumeFailsWhileBrokerIsReachable(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tr := trackerAt(&fakeBroker{connected: true}, &now)

	tr.ConsumeFailed("relay_requests_a")
	now = now.Add(6 * time.Minute)

	wedged, reason := tr.Wedged(5 * time.Minute)
	require.True(t, wedged)
	assert.Contains(t, reason, "relay_requests_a")
	assert.Contains(t, reason, "no consumer")
}

// A broker outage is not this relay's fault and a restart cannot fix it.
// Restarting every replica while RabbitMQ is down would destroy the agent
// WebSockets for nothing.
func TestNeverWedgedWhileTheBrokerIsUnreachable(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tr := trackerAt(&fakeBroker{connected: false}, &now)

	tr.ConsumeFailed("relay_requests_a")
	now = now.Add(1 * time.Hour)

	wedged, _ := tr.Wedged(5 * time.Minute)
	assert.False(t, wedged, "a dependency outage must not restart the relay")
}

func TestBriefFailureDoesNotTripTheThreshold(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tr := trackerAt(&fakeBroker{connected: true}, &now)

	tr.ConsumeFailed("relay_requests_a")
	now = now.Add(30 * time.Second)

	wedged, _ := tr.Wedged(5 * time.Minute)
	assert.False(t, wedged, "a reconnect blip must not restart the pod")
}

// The clock must measure the whole outage, not the gap between retries — the
// consume loop calls ConsumeFailed roughly once a second.
func TestRepeatedFailuresDoNotResetTheClock(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tr := trackerAt(&fakeBroker{connected: true}, &now)

	for i := 0; i < 400; i++ {
		tr.ConsumeFailed("relay_requests_a")
		now = now.Add(time.Second)
	}

	wedged, _ := tr.Wedged(5 * time.Minute)
	assert.True(t, wedged, "retrying every second must still accumulate toward the threshold")
}

func TestRecoveryClearsTheFailureStreak(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tr := trackerAt(&fakeBroker{connected: true}, &now)

	tr.ConsumeFailed("relay_requests_a")
	now = now.Add(10 * time.Minute)
	tr.ConsumerAttached("relay_requests_a")

	wedged, _ := tr.Wedged(5 * time.Minute)
	assert.False(t, wedged, "a relay that recovered on its own must not then be restarted")
}

// An agent that simply disconnects leaves its last failure behind. Without
// clearing it the relay would restart over a tenant that is no longer there.
func TestSessionEndClearsTheFailureStreak(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tr := trackerAt(&fakeBroker{connected: true}, &now)

	tr.ConsumeFailed("relay_requests_a")
	now = now.Add(10 * time.Minute)
	tr.SessionEnded("relay_requests_a")

	wedged, _ := tr.Wedged(5 * time.Minute)
	assert.False(t, wedged)
}

// One stuck tenant is enough: the relay is not doing its job even if others are.
func TestOneStuckTenantAmongHealthyOnesTrips(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tr := trackerAt(&fakeBroker{connected: true}, &now)

	tr.ConsumerAttached("relay_requests_healthy")
	tr.ConsumeFailed("relay_requests_stuck")
	now = now.Add(6 * time.Minute)

	wedged, reason := tr.Wedged(5 * time.Minute)
	require.True(t, wedged)
	assert.Contains(t, reason, "relay_requests_stuck")
}

// The escape hatch. A false positive restarts every replica in a loop, so it
// must be switchable off by config without a rollback.
func TestZeroThresholdDisablesTheCheck(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tr := trackerAt(&fakeBroker{connected: true}, &now)

	tr.ConsumeFailed("relay_requests_a")
	now = now.Add(24 * time.Hour)

	wedged, _ := tr.Wedged(0)
	assert.False(t, wedged)
}
