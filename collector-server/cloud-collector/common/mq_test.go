package common

import (
	"errors"
	"testing"
	"time"
)

// resetConsumerHealthState clears the package-level consumer-health registry so each
// test starts from a clean slate.
func resetConsumerHealthState(t *testing.T) {
	t.Helper()
	rbmqConsumerHealthMux.Lock()
	rbmqConsumerHealth = make(map[string]*consumerHealth)
	rbmqConsumerHealthMux.Unlock()
}

func TestIsRabbitMQConnectionError_ChannelExhausted(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"channel id space exhausted", errors.New(`Exception (504) Reason: "channel id space exhausted"`), true},
		{"channel not open", errors.New("channel/connection is not open"), true},
		{"eof", errors.New("EOF"), true},
		{"app error", errors.New("usagereport: account not found"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isRabbitMQConnectionError(c.err); got != c.want {
				t.Fatalf("isRabbitMQConnectionError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestConsumerHealth_WedgeDetection(t *testing.T) {
	resetConsumerHealthState(t)
	const q = "cloud_account_post_report"

	// A fresh failure is not yet "wedged" (within the timeout grace).
	markConsumerFailing(q, errors.New("channel id space exhausted"))
	if _, wedged := anyConsumerWedged(); wedged {
		t.Fatal("a just-now failure should not be reported as wedged")
	}

	// Backdate failingSince past the wedge timeout: now it should be wedged.
	rbmqConsumerHealthMux.Lock()
	rbmqConsumerHealth[q].failingSince = time.Now().Add(-rbmqConsumerWedgeTimeout - time.Minute)
	rbmqConsumerHealthMux.Unlock()
	if queue, wedged := anyConsumerWedged(); !wedged || queue != q {
		t.Fatalf("expected %q wedged, got queue=%q wedged=%v", q, queue, wedged)
	}

	// Recovery clears the wedge: a fresh run becomes the active session and survives the
	// healthy threshold.
	session := time.Now()
	markConsumerRunning(q, session)
	markConsumerHealthy(q, session)
	if _, wedged := anyConsumerWedged(); wedged {
		t.Fatal("a recovered consumer should not be reported as wedged")
	}
}

// TestConsumerHealth_StaleHealthyTimerIgnored verifies that a healthy-timer left over
// from an exited run cannot re-mark a now-failing consumer healthy (the markConsumerFailing
// on exit clears the session, so the stale markConsumerHealthy is a no-op).
func TestConsumerHealth_StaleHealthyTimerIgnored(t *testing.T) {
	resetConsumerHealthState(t)
	const q = "cloud_account_post_report"

	staleSession := time.Now()
	markConsumerRunning(q, staleSession)
	// The run exits before the timer fires.
	markConsumerFailing(q, errors.New("connection lost"))
	// The stale timer fires after exit — must be ignored.
	markConsumerHealthy(q, staleSession)

	rbmqConsumerHealthMux.Lock()
	healthy := rbmqConsumerHealth[q].healthy
	rbmqConsumerHealthMux.Unlock()
	if healthy {
		t.Fatal("a stale healthy-timer must not re-mark an exited consumer healthy")
	}
}

// TestConsumerHealth_FailingSinceStable verifies failingSince is stamped once on the
// failing transition and not advanced by subsequent failures, so the wedge timer
// reflects how long the consumer has actually been down.
func TestConsumerHealth_FailingSinceStable(t *testing.T) {
	resetConsumerHealthState(t)
	const q = "cloud_account_events"

	markConsumerFailing(q, errors.New("boom"))
	rbmqConsumerHealthMux.Lock()
	first := rbmqConsumerHealth[q].failingSince
	rbmqConsumerHealthMux.Unlock()

	time.Sleep(5 * time.Millisecond)
	markConsumerFailing(q, errors.New("boom again"))

	rbmqConsumerHealthMux.Lock()
	second := rbmqConsumerHealth[q].failingSince
	rbmqConsumerHealthMux.Unlock()

	if !first.Equal(second) {
		t.Fatalf("failingSince advanced on repeated failure: first=%v second=%v", first, second)
	}
}

func TestMqHealthy_ConsumerWedgeTripsLiveness(t *testing.T) {
	resetConsumerHealthState(t)
	// Make the heartbeat look fresh so only the consumer-level check is exercised.
	mqLastHeartbeatNanos.Store(time.Now().UnixNano())
	t.Cleanup(func() { mqLastHeartbeatNanos.Store(0) })

	if !MqHealthy() {
		t.Fatal("expected healthy with fresh heartbeat and no consumers registered")
	}

	const q = "cloud_account_post_report"
	markConsumerFailing(q, errors.New("channel id space exhausted"))
	rbmqConsumerHealthMux.Lock()
	rbmqConsumerHealth[q].failingSince = time.Now().Add(-rbmqConsumerWedgeTimeout - time.Minute)
	rbmqConsumerHealthMux.Unlock()

	if MqHealthy() {
		t.Fatal("expected unhealthy when a consumer is wedged past the timeout")
	}

	session := time.Now()
	markConsumerRunning(q, session)
	markConsumerHealthy(q, session)
	if !MqHealthy() {
		t.Fatal("expected healthy after the consumer recovered")
	}
}

func TestMqHealthy_StaleHeartbeatUnhealthy(t *testing.T) {
	resetConsumerHealthState(t)
	mqLastHeartbeatNanos.Store(time.Now().Add(-mqHeartbeatTimeout - time.Second).UnixNano())
	t.Cleanup(func() { mqLastHeartbeatNanos.Store(0) })

	if MqHealthy() {
		t.Fatal("expected unhealthy with a stale heartbeat")
	}
}

func TestIsQueueDurabilityMismatch(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"precondition durable", errors.New(`declare queue failed: Exception (406) Reason: "PRECONDITION_FAILED - inequivalent arg 'durable' for queue 'cloud_account_post_report' in vhost '/': received 'true' but current is 'false'"`), true},
		{"precondition other arg", errors.New(`Exception (406) Reason: "PRECONDITION_FAILED - inequivalent arg 'x-message-ttl'"`), false},
		{"connection error", errors.New("channel id space exhausted"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isQueueDurabilityMismatch(c.err); got != c.want {
				t.Fatalf("isQueueDurabilityMismatch(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// TestMigrateQueueDurabilityIfMismatch_NonMismatch verifies a non-mismatch error is a
// no-op (returns false without attempting any broker interaction).
func TestMigrateQueueDurabilityIfMismatch_NonMismatch(t *testing.T) {
	if migrateQueueDurabilityIfMismatch("cloud_account_events", errors.New("some transient channel error")) {
		t.Fatal("expected false for a non-durability-mismatch error")
	}
	if migrateQueueDurabilityIfMismatch("cloud_account_events", nil) {
		t.Fatal("expected false for a nil error")
	}
}
