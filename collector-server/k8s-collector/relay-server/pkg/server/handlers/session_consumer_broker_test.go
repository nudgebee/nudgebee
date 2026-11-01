package handlers

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"

	"nudgebee/relay-server/pkg/config"
	"nudgebee/relay-server/pkg/mq"
	"nudgebee/relay-server/pkg/server/health"
)

// Broker-backed tests for consumeSessionQueue. They need RabbitMQ on
// 127.0.0.1:5672 (guest/guest) and skip without it, unless NB_REQUIRE_RABBITMQ
// is set (CI), where a missing broker is a failure.
//
// The session under test reaches the broker through brokerProxy, which can
// refuse new connections and sever open ones — what a broker restart looks like
// to a client. The tests talk to the broker directly (rawAmqp) to change its
// topology and to publish requests the way the relay's publish path does:
// through the relay exchange, so a queue that comes back unbound fails too.

const testBrokerAddr = "127.0.0.1:5672"

func requireRabbitMQ(t *testing.T) *brokerProxy {
	t.Helper()
	conn, err := net.DialTimeout("tcp", testBrokerAddr, time.Second)
	if err != nil {
		if os.Getenv("NB_REQUIRE_RABBITMQ") != "" {
			t.Fatalf("RabbitMQ required but not reachable on %s: %v", testBrokerAddr, err)
		}
		t.Skipf("RabbitMQ not reachable on %s; skipping (%v)", testBrokerAddr, err)
	}
	_ = conn.Close()
	return startBrokerProxy(t, testBrokerAddr)
}

type brokerProxy struct {
	ln     net.Listener
	target string
	down   atomic.Bool
	mu     sync.Mutex
	open   map[net.Conn]struct{}
}

// startBrokerProxy's listener is never closed. The session's ConnectionManager
// cannot be stopped and redials whatever closes under it, so a closed proxy
// would leave it retrying, and logging an error, every 2s for the rest of the
// test binary; an open one leaves a single idle connection.
func startBrokerProxy(t *testing.T, target string) *brokerProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "broker proxy")
	p := &brokerProxy{ln: ln, target: target, open: map[net.Conn]struct{}{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			if p.down.Load() {
				_ = c.Close()
				continue
			}
			go p.forward(c)
		}
	}()
	return p
}

func (p *brokerProxy) url() string {
	return fmt.Sprintf("amqp://guest:guest@127.0.0.1:%d/", p.ln.Addr().(*net.TCPAddr).Port)
}

func (p *brokerProxy) forward(client net.Conn) {
	upstream, err := net.Dial("tcp", p.target)
	if err != nil {
		_ = client.Close()
		return
	}
	p.mu.Lock()
	p.open[client], p.open[upstream] = struct{}{}, struct{}{}
	p.mu.Unlock()
	go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close() }()
	_, _ = io.Copy(client, upstream)
	_ = client.Close()
	p.mu.Lock()
	delete(p.open, client)
	delete(p.open, upstream)
	p.mu.Unlock()
}

// setDown(true) refuses new connections and severs every open one.
func (p *brokerProxy) setDown(down bool) {
	p.down.Store(down)
	if !down {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for c := range p.open {
		_ = c.Close()
	}
}

func rawAmqp(t *testing.T, fn func(ch *amqp.Channel) error) {
	t.Helper()
	conn, err := amqp.DialConfig("amqp://guest:guest@"+testBrokerAddr+"/", amqp.Config{Dial: amqp.DefaultDial(2 * time.Second)})
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	require.NoError(t, err)
	require.NoError(t, fn(ch))
}

// consumerCount is the broker's live consumer count for queue; -1 when the
// queue does not exist or the broker cannot be asked. It runs inside
// require.Eventually's goroutine, so it must not fail the test itself.
func consumerCount(queue string) int {
	conn, err := amqp.DialConfig("amqp://guest:guest@"+testBrokerAddr+"/", amqp.Config{Dial: amqp.DefaultDial(2 * time.Second)})
	if err != nil {
		return -1
	}
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	if err != nil {
		return -1
	}
	q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
	if err != nil {
		return -1
	}
	return q.Consumers
}

type testSession struct {
	queue    string
	exchange string
	received chan string
}

// startSession declares a relay request queue the way a register session does
// and runs consumeSessionQueue on it until the test ends.
func startSession(t *testing.T, proxy *brokerProxy) *testSession {
	t.Helper()
	cfg := &config.Config{}
	cfg.RabbitMQ.ExchangeName = "relay-test-" + uuid.NewString()
	cfg.RabbitMQ.MessageTTL = time.Minute

	// Left open on purpose: its reconnect goroutine redials anything that
	// closes it, so Close would only trade one idle connection for another.
	connMgr, err := mq.NewConnectionManager(proxy.url())
	require.NoError(t, err)
	topo, err := mq.NewTopology(connMgr, cfg)
	require.NoError(t, err)

	s := &testSession{
		queue:    mq.RelayQueueName(uuid.NewString(), "k8s"),
		exchange: cfg.RabbitMQ.ExchangeName,
		received: make(chan string, 16),
	}
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, topo.EnsureQueue(ctx, s.queue))

	done := make(chan error, 1)
	go func() {
		done <- consumeSessionQueue(ctx, connMgr, topo, health.NewTracker(connMgr), topo.EnsureQueue,
			s.queue, "test-"+s.queue, 10, slog.New(slog.NewTextHandler(io.Discard, nil)),
			func(_ *amqp.Channel, d amqp.Delivery) {
				_ = d.Ack(false)
				s.received <- string(d.Body)
			})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		rawAmqp(t, func(ch *amqp.Channel) error {
			_, _ = ch.QueueDelete(s.queue, false, false, false)
			_, _ = ch.QueueDelete(s.queue+".dlq", false, false, false)
			_ = ch.ExchangeDelete(s.exchange, false, false)
			_ = ch.ExchangeDelete(s.exchange+".dlx", false, false)
			return nil
		})
	})
	return s
}

// requireServed waits for the session to be consuming again, then proves it by
// publishing a request through the relay exchange and receiving it.
func (s *testSession) requireServed(t *testing.T, body string, within time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool { return consumerCount(s.queue) == 1 }, within, 200*time.Millisecond,
		"queue %s never regained exactly one consumer", s.queue)
	rawAmqp(t, func(ch *amqp.Channel) error {
		return ch.PublishWithContext(context.Background(), s.exchange, s.queue, false, false,
			amqp.Publishing{Body: []byte(body)})
	})
	select {
	case got := <-s.received:
		require.Equal(t, body, got)
	case <-time.After(5 * time.Second):
		t.Fatalf("request %q published to %s was never delivered to the session", body, s.queue)
	}
}

// The broker drops the queue out from under a live consumer — what an operator
// delete or a definitions-less broker looks like mid-session. The session must
// re-declare the queue (and its binding) itself, not retry NOT_FOUND forever.
func TestBroker_SessionRecreatesQueueDeletedUnderIt(t *testing.T) {
	s := startSession(t, requireRabbitMQ(t))
	s.requireServed(t, "before", 10*time.Second)

	rawAmqp(t, func(ch *amqp.Channel) error {
		_, err := ch.QueueDelete(s.queue, false, false, false)
		return err
	})

	s.requireServed(t, "after", 15*time.Second)
}

// A broker restart that loses the queue: the connection drops, the broker comes
// back without the session's queue, and the session must reconnect, re-declare
// and resume on its own, with one consumer rather than an accumulation.
func TestBroker_SessionResumesAfterBrokerComesBackWithoutQueue(t *testing.T) {
	proxy := requireRabbitMQ(t)
	s := startSession(t, proxy)
	s.requireServed(t, "before", 10*time.Second)

	proxy.setDown(true)
	rawAmqp(t, func(ch *amqp.Channel) error {
		_, err := ch.QueueDelete(s.queue, false, false, false)
		return err
	})
	time.Sleep(3 * time.Second)
	proxy.setDown(false)

	s.requireServed(t, "after", 30*time.Second)
}
