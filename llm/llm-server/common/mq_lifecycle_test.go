package common

import (
	"context"
	"errors"
	"net"
	"nudgebee/llm/config"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wagslane/go-rabbitmq"
)

// Broker-free lifecycle tests. fakeConsumer copies go-rabbitmq v0.16.1 locking: handler holds a read lock,
// Close waits for the write lock -> the old MqClose-from-handler deadlock.

type fakeConsumer struct {
	fleet      *fakeFleet
	runErr     error
	deliveries chan rabbitmq.Delivery
	actions    chan rabbitmq.Action
	handlerMu  sync.RWMutex
	stopped    chan struct{}
	stopOnce   sync.Once
	returnNil  chan struct{}
	closeHang  chan struct{} // non-nil: CloseWithContext blocks until closed, ignoring ctx
	closeCalls atomic.Int32
}

func (c *fakeConsumer) Run(handler rabbitmq.Handler) error {
	if c.runErr != nil {
		return c.runErr
	}
	if active := c.fleet.active.Add(1); active > c.fleet.maxActive.Load() {
		c.fleet.maxActive.Store(active)
	}
	defer c.fleet.active.Add(-1)
	go func() {
		for {
			select {
			case <-c.stopped:
				return
			case d := <-c.deliveries:
				if !c.handlerMu.TryRLock() {
					c.actions <- rabbitmq.NackRequeue
					continue
				}
				action := handler(d)
				c.handlerMu.RUnlock()
				c.actions <- action
			}
		}
	}()
	select {
	case <-c.stopped:
	case <-c.returnNil:
	}
	return nil
}

func (c *fakeConsumer) CloseWithContext(ctx context.Context) {
	c.closeCalls.Add(1)
	if c.closeHang != nil {
		<-c.closeHang // like go-rabbitmq's channel close: ignores ctx
	}
	done := make(chan struct{})
	go func() {
		c.handlerMu.Lock()
		defer c.handlerMu.Unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	c.stopOnce.Do(func() { close(c.stopped) })
}

// fakeFleet is the consumer factory; configure decides each new consumer's
// behaviour by creation order (0-based).
type fakeFleet struct {
	mu        sync.Mutex
	consumers []*fakeConsumer
	configure func(n int, c *fakeConsumer)
	created   chan *fakeConsumer
	active    atomic.Int32
	maxActive atomic.Int32
	connDown  atomic.Bool // what the fake conn's IsClosed reports
}

func (f *fakeFleet) newConsumer(_ *rabbitmq.Conn, _ string, _ ...func(*rabbitmq.ConsumerOptions)) (mqConsumer, error) {
	c := &fakeConsumer{
		fleet:      f,
		deliveries: make(chan rabbitmq.Delivery),
		actions:    make(chan rabbitmq.Action, 16),
		stopped:    make(chan struct{}),
		returnNil:  make(chan struct{}),
	}
	f.mu.Lock()
	n := len(f.consumers)
	f.consumers = append(f.consumers, c)
	f.mu.Unlock()
	if f.configure != nil {
		f.configure(n, c)
	}
	f.created <- c
	return c, nil
}

func (f *fakeFleet) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.consumers)
}

type fakePublisher struct {
	publishErr error
	published  atomic.Int32
	closeCalls atomic.Int32
}

func (p *fakePublisher) Publish(_ []byte, _ []string, _ ...func(*rabbitmq.PublishOptions)) error {
	if p.publishErr != nil {
		return p.publishErr
	}
	p.published.Add(1)
	return nil
}

func (p *fakePublisher) Close() { p.closeCalls.Add(1) }

// useFakeMq swaps the broker seams for fakes with short timings and restores
// them after the test, once MqClose has stopped every supervisor.
func useFakeMq(t *testing.T, dial func(string) (*rabbitmq.Conn, error)) *fakeFleet {
	t.Helper()
	MqClose()
	fleet := &fakeFleet{created: make(chan *fakeConsumer, 32)}

	saved := struct {
		dial         func(string) (*rabbitmq.Conn, error)
		closeConn    func(*rabbitmq.Conn) error
		newConsumer  func(*rabbitmq.Conn, string, ...func(*rabbitmq.ConsumerOptions)) (mqConsumer, error)
		newPublisher func(*rabbitmq.Conn, ...func(*rabbitmq.PublisherOptions)) (mqPublisher, error)
		isClosed     func(*rabbitmq.Conn) bool
		pollInterval time.Duration
		reconnect    time.Duration
		cooldown     time.Duration
		maxDelay     time.Duration
		shutdown     time.Duration
	}{mqDial, mqCloseConn, mqNewConsumer, mqNewPublisher, mqConnIsClosed, mqConnPollInterval, reconnectTimeDelay, mqDialCooldown, mqRearmMaxDelay, mqShutdownTimeout}

	if dial == nil {
		dial = func(string) (*rabbitmq.Conn, error) { return &rabbitmq.Conn{}, nil }
	}
	mqDial = dial
	mqCloseConn = func(*rabbitmq.Conn) error { return nil }
	mqConnIsClosed = func(*rabbitmq.Conn) bool { return fleet.connDown.Load() }
	mqConnPollInterval = 10 * time.Millisecond
	mqNewConsumer = fleet.newConsumer
	reconnectTimeDelay = 10 * time.Millisecond
	mqDialCooldown = 0
	mqRearmMaxDelay = 40 * time.Millisecond
	mqShutdownTimeout = 300 * time.Millisecond

	t.Cleanup(func() {
		MqClose()
		mqDial, mqCloseConn, mqNewConsumer, mqNewPublisher = saved.dial, saved.closeConn, saved.newConsumer, saved.newPublisher
		mqConnIsClosed, mqConnPollInterval = saved.isClosed, saved.pollInterval
		reconnectTimeDelay, mqDialCooldown, mqRearmMaxDelay, mqShutdownTimeout = saved.reconnect, saved.cooldown, saved.maxDelay, saved.shutdown
	})
	return fleet
}

func waitConsumer(t *testing.T, fleet *fakeFleet) *fakeConsumer {
	t.Helper()
	select {
	case c := <-fleet.created:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a consumer to be created")
		return nil
	}
}

func waitState(t *testing.T, queue string, want MqConsumerState) MqConsumerStatus {
	t.Helper()
	var last MqConsumerStatus
	require.Eventually(t, func() bool {
		for _, st := range MqConsumerStatuses() {
			if st.Queue == queue {
				last = st
				return st.State == want
			}
		}
		return false
	}, 2*time.Second, 5*time.Millisecond, "queue %s never reached state %s", queue, want)
	return last
}

func deliver(t *testing.T, c *fakeConsumer) rabbitmq.Action {
	t.Helper()
	select {
	case c.deliveries <- rabbitmq.Delivery{}:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer is not accepting deliveries")
	}
	select {
	case a := <-c.actions:
		return a
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not finish within 2s (deadlocked?)")
		return 0
	}
}

func noopProcessor(context.Context, []byte) error { return nil }

// Invariant 2, real dialer: TCP endpoint that accepts & closes -> every handshake fails.
// The old sync.Once dialed once, never again.
func TestMqConnection_FailedDialIsNotCached(t *testing.T) {
	MqClose()
	savedCooldown := mqDialCooldown
	savedHost, savedPort, savedUser, savedPass := config.Config.RabbitMqHost, config.Config.RabbitMqPort, config.Config.RabbitMqUsername, config.Config.RabbitMqPassword
	t.Cleanup(func() {
		MqClose()
		mqDialCooldown = savedCooldown
		config.Config.RabbitMqHost, config.Config.RabbitMqPort, config.Config.RabbitMqUsername, config.Config.RabbitMqPassword = savedHost, savedPort, savedUser, savedPass
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	var accepts atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			_ = c.Close()
		}
	}()
	config.Config.RabbitMqHost = "127.0.0.1"
	config.Config.RabbitMqPort = ln.Addr().(*net.TCPAddr).Port
	config.Config.RabbitMqUsername, config.Config.RabbitMqPassword = "u", "p"

	mqDialCooldown = 0
	for range 3 {
		assert.Nil(t, getConnection())
	}
	require.Eventually(t, func() bool { return accepts.Load() == 3 }, 2*time.Second, 10*time.Millisecond,
		"every getConnection after a failure must dial again; got %d dials", accepts.Load())

	// The cooldown only rate-limits dials; it never makes a failure permanent.
	mqDialCooldown = time.Hour
	assert.Nil(t, getConnection())
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(3), accepts.Load(), "a dial inside the cooldown window must be skipped")
}

// Invariants 1-2: broker unreachable at registration (on-prem fresh install) -> must report state,
// then attach once the broker answers, without a restart.
func TestMqConsume_BrokerUnavailableAtStartup_AttachesWhenBrokerReturns(t *testing.T) {
	var brokerUp atomic.Bool
	var dials atomic.Int32
	fleet := useFakeMq(t, func(string) (*rabbitmq.Conn, error) {
		dials.Add(1)
		if !brokerUp.Load() {
			return nil, errors.New("dial tcp: connection refused")
		}
		return &rabbitmq.Conn{}, nil
	})

	require.NoError(t, MqConsume("ex", "rk", "q-startup", noopProcessor))

	waitState(t, "q-startup", MqConsumerConnecting)
	require.Eventually(t, func() bool { return dials.Load() >= 3 }, 2*time.Second, 5*time.Millisecond)
	assert.Equal(t, 0, fleet.count(), "no consumer may be built without a connection")
	st := waitState(t, "q-startup", MqConsumerConnecting)
	assert.Contains(t, st.LastError, ErrRbmqNoConn.Error())
	healthy, _ := MqConsumersHealthy(0)
	assert.False(t, healthy, "a consumer that never attached must not be reported healthy")

	brokerUp.Store(true)
	c := waitConsumer(t, fleet)
	waitState(t, "q-startup", MqConsumerRunning)
	healthy, _ = MqConsumersHealthy(0)
	assert.True(t, healthy)
	assert.Equal(t, rabbitmq.Ack, deliver(t, c))
}

// Invariant 3, the OSS incident: completion publish finds its channel closed. Old code -> MqClose ->
// Close waits on the publishing handler -> this test hung in deliver().
func TestMqPublish_ClosedChannelInsideHandler_DoesNotStopConsumersOrDeadlock(t *testing.T) {
	fleet := useFakeMq(t, nil)

	var pubMu sync.Mutex
	var publishers []*fakePublisher
	mqNewPublisher = func(*rabbitmq.Conn, ...func(*rabbitmq.PublisherOptions)) (mqPublisher, error) {
		pubMu.Lock()
		defer pubMu.Unlock()
		p := &fakePublisher{}
		if len(publishers) == 0 {
			p.publishErr = amqp.ErrClosed
		}
		publishers = append(publishers, p)
		return p, nil
	}

	var publishErr atomic.Value
	require.NoError(t, MqConsume("ex", "rk", "q-investigate", func(ctx context.Context, _ []byte) error {
		err := MqPublishWithContext(ctx, "completed_ex", "completed", map[string]string{"status": "COMPLETED"})
		if err != nil {
			publishErr.Store(err)
		}
		return err
	}))
	investigate := waitConsumer(t, fleet)
	require.NoError(t, MqFanoutSubscribe("cache_invalidation", noopProcessor))
	fanout := waitConsumer(t, fleet)
	waitState(t, "q-investigate", MqConsumerRunning)

	assert.Equal(t, rabbitmq.Ack, deliver(t, investigate), "handler must finish and ack")
	assert.Nil(t, publishErr.Load(), "publish must succeed on a replacement publisher")

	pubMu.Lock()
	require.Len(t, publishers, 2, "the closed publisher must be replaced exactly once")
	assert.Equal(t, int32(1), publishers[0].closeCalls.Load())
	assert.Equal(t, int32(1), publishers[1].published.Load())
	pubMu.Unlock()

	assert.Zero(t, investigate.closeCalls.Load(), "a publish failure must not close consumers")
	assert.Zero(t, fanout.closeCalls.Load(), "a publish failure must not close consumers")
	assert.Equal(t, 2, fleet.count(), "no consumer may be rebuilt")
	assert.Equal(t, rabbitmq.Ack, deliver(t, investigate), "consumer must keep consuming")
}

// Invariants 1 & 5: Run returning nil without MqClose (old code: exit forever) is re-armed,
// never with two consumers attached.
func TestMqConsume_UnexpectedStopIsRearmedWithoutDuplicates(t *testing.T) {
	fleet := useFakeMq(t, nil)
	require.NoError(t, MqConsume("ex", "rk", "q-rearm", noopProcessor))

	first := waitConsumer(t, fleet)
	waitState(t, "q-rearm", MqConsumerRunning)
	close(first.returnNil)

	second := waitConsumer(t, fleet)
	st := waitState(t, "q-rearm", MqConsumerRunning)
	assert.Equal(t, 1, st.Restarts)
	assert.Contains(t, st.LastError, errMqConsumerStoppedUnexpected.Error())
	assert.Equal(t, int32(1), first.closeCalls.Load(), "the stopped handle must be released once")
	assert.Equal(t, int32(1), fleet.maxActive.Load(), "never more than one consumer attached per queue")
	assert.Equal(t, rabbitmq.Ack, deliver(t, second))
}

// Invariants 1 & 5: Run setup failure (declare/qos) -> fresh consumer; failed handle released once.
func TestMqConsume_RunSetupErrorIsRetried(t *testing.T) {
	fleet := useFakeMq(t, nil)
	fleet.configure = func(n int, c *fakeConsumer) {
		if n < 2 {
			c.runErr = errors.New("declare queue failed: Exception (504)")
		}
	}
	require.NoError(t, MqConsume("ex", "rk", "q-setup", noopProcessor))

	failed1, failed2 := waitConsumer(t, fleet), waitConsumer(t, fleet)
	ok := waitConsumer(t, fleet)
	st := waitState(t, "q-setup", MqConsumerRunning)
	assert.Equal(t, 2, st.Restarts)
	assert.Equal(t, int32(1), failed1.closeCalls.Load())
	assert.Equal(t, int32(1), failed2.closeCalls.Load())
	assert.Equal(t, rabbitmq.Ack, deliver(t, ok))
}

// Invariant 4: shutdown stops supervisors for good; nothing is rebuilt.
func TestMqClose_StopsConsumersWithoutRearm(t *testing.T) {
	fleet := useFakeMq(t, nil)
	require.NoError(t, MqConsume("ex", "rk", "q-close", noopProcessor))
	c := waitConsumer(t, fleet)
	waitState(t, "q-close", MqConsumerRunning)

	rbmqMux.Lock()
	sup := rbmqSupervisors["q-close"]
	rbmqMux.Unlock()

	MqClose()

	select {
	case <-sup.done:
	case <-time.After(time.Second):
		t.Fatal("supervisor goroutine did not exit after MqClose")
	}
	assert.Equal(t, int32(1), c.closeCalls.Load())
	assert.Empty(t, MqConsumerStatuses())
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, fleet.count(), "no consumer may be built after shutdown")

	// Registration works again after shutdown (used by tests and restarts).
	require.NoError(t, MqConsume("ex", "rk", "q-close", noopProcessor))
	waitConsumer(t, fleet)
}

// Invariant 4: shutdown is bounded with a never-returning handler & doesn't deadlock from inside one.
func TestMqClose_BoundedWithStuckHandler(t *testing.T) {
	fleet := useFakeMq(t, nil)
	release := make(chan struct{})
	entered := make(chan struct{})
	require.NoError(t, MqConsume("ex", "rk", "q-stuck", func(context.Context, []byte) error {
		close(entered)
		<-release
		return nil
	}))
	c := waitConsumer(t, fleet)
	waitState(t, "q-stuck", MqConsumerRunning)
	go func() { c.deliveries <- rabbitmq.Delivery{} }()
	<-entered

	start := time.Now()
	MqClose()
	assert.Less(t, time.Since(start), 2*mqShutdownTimeout+500*time.Millisecond, "MqClose must be bounded")
	close(release)

	// MqClose from inside a handler returns instead of waiting on itself.
	fleet2 := useFakeMq(t, nil)
	closed := make(chan struct{})
	require.NoError(t, MqConsume("ex", "rk", "q-self", func(context.Context, []byte) error {
		MqClose()
		close(closed)
		return nil
	}))
	c2 := waitConsumer(t, fleet2)
	waitState(t, "q-self", MqConsumerRunning)
	go func() { c2.deliveries <- rabbitmq.Delivery{} }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("MqClose called from a handler deadlocked")
	}
}

// Invariant 5: a queue has one supervisor.
func TestMqConsume_DuplicateQueueRejected(t *testing.T) {
	fleet := useFakeMq(t, nil)
	require.NoError(t, MqConsume("ex", "rk", "q-dup", noopProcessor))
	waitConsumer(t, fleet)
	err := MqConsume("ex", "rk", "q-dup", noopProcessor)
	assert.ErrorIs(t, err, ErrMqConsumerAlreadyRegistered)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, fleet.count())
}

// Delivery semantics unchanged: processor error -> NackRequeue.
func TestMqConsume_AckSemanticsUnchanged(t *testing.T) {
	fleet := useFakeMq(t, nil)
	var fail atomic.Bool
	require.NoError(t, MqConsume("ex", "rk", "q-ack", func(context.Context, []byte) error {
		if fail.Load() {
			return errors.New("processing failed")
		}
		return nil
	}))
	c := waitConsumer(t, fleet)
	waitState(t, "q-ack", MqConsumerRunning)
	assert.Equal(t, rabbitmq.Ack, deliver(t, c))
	fail.Store(true)
	assert.Equal(t, rabbitmq.NackRequeue, deliver(t, c))
}

// In-flight work is visible while a handler runs, and cleared after.
func TestMqConsumerStatus_ReportsInFlight(t *testing.T) {
	fleet := useFakeMq(t, nil)
	release := make(chan struct{})
	entered := make(chan struct{})
	require.NoError(t, MqConsume("ex", "rk", "q-inflight", func(context.Context, []byte) error {
		close(entered)
		<-release
		return nil
	}))
	c := waitConsumer(t, fleet)
	waitState(t, "q-inflight", MqConsumerRunning)
	go func() { c.deliveries <- rabbitmq.Delivery{} }()
	<-entered

	st := waitState(t, "q-inflight", MqConsumerRunning)
	assert.Equal(t, 1, st.InFlight)
	assert.NotNil(t, st.OldestInFlightSince)
	close(release)
	<-c.actions
	st = waitState(t, "q-inflight", MqConsumerRunning)
	assert.Zero(t, st.InFlight)
}

func TestMqRearmDelay_IsBounded(t *testing.T) {
	base, maxDelay := 5*time.Second, time.Minute
	assert.Equal(t, 5*time.Second, mqRearmDelay(1, base, maxDelay))
	assert.Equal(t, 10*time.Second, mqRearmDelay(2, base, maxDelay))
	assert.Equal(t, 40*time.Second, mqRearmDelay(4, base, maxDelay))
	assert.Equal(t, time.Minute, mqRearmDelay(5, base, maxDelay))
	assert.Equal(t, time.Minute, mqRearmDelay(50, base, maxDelay))
}

// Review #39163: "since" was reset on every retry, so an outage never outlasted the grace and
// /health/mq stayed 200. A consumer detached for 3× grace must be unhealthy.
func TestMqConsumersHealthy_OutageLongerThanGraceIsUnhealthy(t *testing.T) {
	useFakeMq(t, func(string) (*rabbitmq.Conn, error) { return nil, errors.New("dial tcp: connection refused") })
	require.NoError(t, MqConsume("ex", "rk", "q-outage", noopProcessor))

	grace := 150 * time.Millisecond
	time.Sleep(3 * grace)
	healthy, statuses := MqConsumersHealthy(grace)
	require.Len(t, statuses, 1)
	assert.False(t, healthy, "detached for 3× grace must be unhealthy (since=%s ago, restarts=%d)",
		time.Since(statuses[0].Since).Round(time.Millisecond), statuses[0].Restarts)
	assert.Greater(t, statuses[0].Restarts, 3, "retries must keep running while reported unhealthy")
	assert.Greater(t, time.Since(statuses[0].Since), 3*grace-50*time.Millisecond, "since = outage start, not last retry")
}

func TestMqConsumersHealthy_NoConsumersIsUnhealthy(t *testing.T) {
	useFakeMq(t, nil)
	healthy, statuses := MqConsumersHealthy(time.Hour)
	assert.False(t, healthy, "zero registered consumers must not report ok")
	assert.Empty(t, statuses)
}

// Review #39163: a dial hung on the network must not block other callers or shutdown.
func TestMqConnection_HungDialBlocksNeitherCallersNorShutdown(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	useFakeMq(t, func(string) (*rabbitmq.Conn, error) {
		once.Do(func() { close(started) })
		<-release
		return &rabbitmq.Conn{}, nil
	})
	var discarded atomic.Int32
	mqCloseConn = func(*rabbitmq.Conn) error { discarded.Add(1); return nil }

	dialer := make(chan *rabbitmq.Conn, 1)
	go func() { dialer <- getConnection() }()
	<-started

	within := func(what string, f func()) {
		t.Helper()
		done := make(chan struct{})
		go func() { f(); close(done) }()
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("%s waited on the hung dial", what)
		}
	}
	within("second caller", func() { assert.Nil(t, getConnection()) })
	within("publish", func() { assert.ErrorIs(t, MqPublish("ex", "rk", "m"), ErrRbmqNoConn) })
	within("MqClose", MqClose)

	close(release)
	assert.Nil(t, <-dialer, "a dial that finishes after MqClose must be discarded")
	assert.Equal(t, int32(1), discarded.Load(), "the discarded connection must be closed")
	rbmqConnMu.Lock()
	assert.Nil(t, rbmqConn)
	rbmqConnMu.Unlock()
}

// Supervisors wait for an in-flight dial instead of recording a spurious failure.
func TestMqConsume_WaitsForInFlightDial(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	fleet := useFakeMq(t, func(string) (*rabbitmq.Conn, error) {
		once.Do(func() { close(started) })
		<-release
		return &rabbitmq.Conn{}, nil
	})
	// Wait for this dial before the test ends: connect() reads the dial
	// settings useFakeMq restores in its cleanup, so returning first races.
	dialed := make(chan struct{})
	go func() {
		defer close(dialed)
		getConnection()
	}()
	<-started
	require.NoError(t, MqConsume("ex", "rk", "q-wait", noopProcessor))
	time.Sleep(50 * time.Millisecond)
	close(release)

	waitConsumer(t, fleet)
	st := waitState(t, "q-wait", MqConsumerRunning)
	assert.Zero(t, st.Restarts, "waiting for another caller's dial is not a failure")
	<-dialed
}

// The real dialer is bounded: a broker that accepts TCP but never speaks AMQP (black-holed
// handshake) must fail within mqDialTimeout instead of amqp's 30s default.
func TestMqDial_StalledHandshakeTimesOut(t *testing.T) {
	savedTimeout := mqDialTimeout
	t.Cleanup(func() { mqDialTimeout = savedTimeout })
	mqDialTimeout = 300 * time.Millisecond

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	var held []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c) // accept, never respond
			mu.Unlock()
		}
	}()
	defer func() {
		mu.Lock()
		for _, c := range held {
			_ = c.Close()
		}
		mu.Unlock()
	}()

	start := time.Now()
	conn, err := mqDial("amqp://u:p@" + ln.Addr().String())
	assert.Nil(t, conn)
	assert.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second, "dial must be bounded by mqDialTimeout")
}

// Review #39163: a publish that fails on every attempt returns an error after maxAttempts,
// and consumers are untouched. Callers own what happens next (completion publishes are
// best-effort and logged; see event_analyzer_mq.go).
func TestMqPublish_PermanentFailureReturnsErrorAndLeavesConsumers(t *testing.T) {
	fleet := useFakeMq(t, nil)
	require.NoError(t, MqConsume("ex", "rk", "q-perm", noopProcessor))
	c := waitConsumer(t, fleet)
	waitState(t, "q-perm", MqConsumerRunning)

	var pubMu sync.Mutex
	var publishers []*fakePublisher
	mqNewPublisher = func(*rabbitmq.Conn, ...func(*rabbitmq.PublisherOptions)) (mqPublisher, error) {
		pubMu.Lock()
		defer pubMu.Unlock()
		p := &fakePublisher{publishErr: amqp.ErrClosed}
		publishers = append(publishers, p)
		return p, nil
	}

	err := MqPublish("completed_ex", "completed", "m")
	require.Error(t, err)
	assert.ErrorIs(t, err, amqp.ErrClosed)
	pubMu.Lock()
	assert.Len(t, publishers, maxAttempts, "one replacement publisher per attempt")
	for _, p := range publishers {
		assert.Equal(t, int32(1), p.closeCalls.Load(), "every failed publisher is closed exactly once")
	}
	pubMu.Unlock()

	// A non-channel error is not retried.
	mqNewPublisher = func(*rabbitmq.Conn, ...func(*rabbitmq.PublisherOptions)) (mqPublisher, error) {
		return &fakePublisher{publishErr: errors.New("PRECONDITION_FAILED")}, nil
	}
	rbmqMux.Lock()
	rbmqPublishers = make(map[string]mqPublisher)
	rbmqMux.Unlock()
	start := time.Now()
	require.Error(t, MqPublish("completed_ex", "completed", "m"))
	assert.Less(t, time.Since(start), reconnectTimeDelay, "non-channel errors return without retrying")

	assert.Zero(t, c.closeCalls.Load(), "publish failures never close consumers")
	assert.Equal(t, rabbitmq.Ack, deliver(t, c))
}

// Review #39163: a consumer close that ignores its ctx (AMQP channel close hung on the broker) and a
// hung connection close must not stretch MqClose past its bound.
func TestMqClose_BoundedWhenAmqpCloseHangs(t *testing.T) {
	fleet := useFakeMq(t, nil)
	hang := make(chan struct{})
	t.Cleanup(func() { close(hang) })
	fleet.configure = func(_ int, c *fakeConsumer) { c.closeHang = hang }
	require.NoError(t, MqConsume("ex", "rk", "q-hang", noopProcessor))
	waitConsumer(t, fleet)
	waitState(t, "q-hang", MqConsumerRunning)
	getConnection()
	mqCloseConn = func(*rabbitmq.Conn) error { <-hang; return nil }

	closed := make(chan struct{})
	go func() { MqClose(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2*mqShutdownTimeout + 500*time.Millisecond):
		t.Fatal("MqClose waited on a hung AMQP close")
	}
}

// H scenario (real broker, #39163): during a broker outage the consumer sits inside go-rabbitmq's own
// reconnect and never returns to its supervisor, so /health/mq stayed 200. The conn watcher must
// surface it as "reconnecting" -> unhealthy after grace, then healthy again on recovery.
func TestMqConsumersHealthy_BrokerOutageWhileRunningIsUnhealthy(t *testing.T) {
	fleet := useFakeMq(t, nil)
	require.NoError(t, MqConsume("ex", "rk", "q-conn", noopProcessor))
	waitConsumer(t, fleet)
	waitState(t, "q-conn", MqConsumerRunning)

	grace := 150 * time.Millisecond
	outageStart := time.Now()
	fleet.connDown.Store(true)
	st := waitState(t, "q-conn", MqConsumerReconnecting)
	assert.Contains(t, st.LastError, "broker connection lost")
	healthy, _ := MqConsumersHealthy(grace)
	assert.True(t, healthy, "within grace")

	time.Sleep(3 * grace)
	healthy, statuses := MqConsumersHealthy(grace)
	require.Len(t, statuses, 1)
	assert.False(t, healthy, "an outage longer than grace must be unhealthy")
	assert.WithinDuration(t, outageStart, statuses[0].Since, 100*time.Millisecond, "since = outage start")

	fleet.connDown.Store(false)
	waitState(t, "q-conn", MqConsumerRunning)
	healthy, _ = MqConsumersHealthy(grace)
	assert.True(t, healthy, "healthy again once the connection is back")
}

// The watcher belongs to one connection: MqClose (new generation) stops it.
func TestMqConnWatcher_StopsAfterMqClose(t *testing.T) {
	useFakeMq(t, nil)
	var polls atomic.Int32
	mqConnIsClosed = func(*rabbitmq.Conn) bool { polls.Add(1); return false }
	require.NotNil(t, getConnection())
	require.Eventually(t, func() bool { return polls.Load() > 2 }, time.Second, 5*time.Millisecond)

	MqClose()
	time.Sleep(50 * time.Millisecond)
	after := polls.Load()
	time.Sleep(100 * time.Millisecond)
	assert.LessOrEqual(t, polls.Load(), after+1, "watcher must stop once its connection generation is closed")
}

// Invariant 6: after a reconnect go-rabbitmq restarts a consumer in place, and when
// that restart fails its Run never returns — the fake's Run likewise blocks until closed.
// The supervisor must rebuild the consumer instead of waiting on it.
func TestMqConsume_RebuiltAfterBrokerReconnect(t *testing.T) {
	fleet := useFakeMq(t, nil)
	require.NoError(t, MqConsume("ex", "rk", "q_reconnect", noopProcessor))
	first := waitConsumer(t, fleet)
	waitState(t, "q_reconnect", MqConsumerRunning)

	fleet.connDown.Store(true)
	waitState(t, "q_reconnect", MqConsumerReconnecting)
	fleet.connDown.Store(false)

	second := waitConsumer(t, fleet)
	assert.EqualValues(t, 1, first.closeCalls.Load(), "the consumer from before the outage is closed")
	waitState(t, "q_reconnect", MqConsumerRunning)
	assert.Equal(t, rabbitmq.Ack, deliver(t, second))
	assert.EqualValues(t, 0, second.closeCalls.Load(), "a consumer built after the outage is left alone")
	assert.EqualValues(t, 2, fleet.count())
	assert.LessOrEqual(t, fleet.maxActive.Load(), int32(1), "never two live consumers for one queue")
}

// A connection closed for mqConnResetAfter is closed (stopping its own redial loop, so it
// cannot bring back a second consumer) and a new one is dialed.
func TestMqConnWatcher_ReDialsAConnectionClosedTooLong(t *testing.T) {
	var dials atomic.Int32
	fleet := useFakeMq(t, func(string) (*rabbitmq.Conn, error) {
		dials.Add(1)
		return &rabbitmq.Conn{}, nil
	})
	var closedConns atomic.Int32
	mqCloseConn = func(*rabbitmq.Conn) error { closedConns.Add(1); return nil }
	savedResetAfter := mqConnResetAfter
	mqConnResetAfter = 50 * time.Millisecond
	t.Cleanup(func() { mqConnResetAfter = savedResetAfter })

	require.NoError(t, MqConsume("ex", "rk", "q_reset", noopProcessor))
	first := waitConsumer(t, fleet)
	waitState(t, "q_reset", MqConsumerRunning)

	fleet.connDown.Store(true)
	require.Eventually(t, func() bool { return closedConns.Load() >= 1 }, 2*time.Second, 5*time.Millisecond,
		"the connection stuck closed is closed")
	fleet.connDown.Store(false)

	waitConsumer(t, fleet)
	waitState(t, "q_reset", MqConsumerRunning)
	assert.GreaterOrEqual(t, dials.Load(), int32(2), "a new connection is dialed")
	assert.EqualValues(t, 1, first.closeCalls.Load())
	assert.LessOrEqual(t, fleet.maxActive.Load(), int32(1), "never two live consumers for one queue")
}
