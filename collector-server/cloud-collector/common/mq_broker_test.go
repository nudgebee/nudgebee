package common

import (
	"context"
	"io"
	"net"
	"nudgebee/collector/cloud/config"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Broker-backed recovery tests. They need RabbitMQ on 127.0.0.1:5672
// (guest/guest) and skip without it, unless NB_REQUIRE_RABBITMQ is set (CI), where a
// missing broker is a failure.
//
// The service under test reaches the broker through brokerProxy, which can refuse new
// connections and sever open ones — what a broker restart looks like to a client. The
// tests themselves talk to the broker directly (rawAmqp) to change its topology.

const testBrokerAddr = "127.0.0.1:5672"

var (
	testBrokerOnce  sync.Once
	testBrokerProxy *brokerProxy
)

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
	// config is written once, before any consumer goroutine reads it.
	testBrokerOnce.Do(func() {
		testBrokerProxy = startBrokerProxy(t, testBrokerAddr)
		config.Config.RabbitMqHost = "127.0.0.1"
		config.Config.RabbitMqPort = testBrokerProxy.port()
		config.Config.RabbitMqUsername = "guest"
		config.Config.RabbitMqPassword = "guest"
	})
	testBrokerProxy.setDown(false)
	return testBrokerProxy
}

type brokerProxy struct {
	ln     net.Listener
	target string
	down   atomic.Bool
	mu     sync.Mutex
	open   map[net.Conn]struct{}
}

func startBrokerProxy(t *testing.T, target string) *brokerProxy {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("broker proxy: %v", err)
	}
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

func (p *brokerProxy) port() int { return p.ln.Addr().(*net.TCPAddr).Port }

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

// outage takes the broker away for d, running during() while it is gone.
func (p *brokerProxy) outage(d time.Duration, during func()) {
	p.setDown(true)
	during()
	time.Sleep(d)
	p.setDown(false)
}

func rawAmqp(t *testing.T, fn func(ch *amqp.Channel) error) error {
	t.Helper()
	conn, err := amqp.DialConfig("amqp://guest:guest@"+testBrokerAddr+"/", amqp.Config{Dial: amqp.DefaultDial(2 * time.Second)})
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	return fn(ch)
}

func mustRawAmqp(t *testing.T, what string, fn func(ch *amqp.Channel) error) {
	t.Helper()
	if err := rawAmqp(t, fn); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// consumeTestQueue starts MqConsume on a fresh queue and returns the queue name and a
// counter of processed messages.
func consumeTestQueue(t *testing.T, name string) (string, *atomic.Int64) {
	t.Helper()
	queue := name + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	var received atomic.Int64
	if err := MqConsume(queue+"_exchange", queue, queue, 1, func(context.Context, []byte) error {
		received.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("MqConsume: %v", err)
	}
	return queue, &received
}

// requireConsuming publishes to queue's exchange until the consumer processes a message.
func requireConsuming(t *testing.T, queue string, received *atomic.Int64, within time.Duration) {
	t.Helper()
	start := received.Load()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(time.Second) {
		_ = rawAmqp(t, func(ch *amqp.Channel) error {
			return ch.Publish(queue+"_exchange", queue, false, false, amqp.Publishing{Body: []byte("ping")})
		})
		if received.Load() > start {
			return
		}
	}
	t.Fatalf("queue %s: no message consumed within %s", queue, within)
}

// requireOneConsumer checks the broker reports exactly one consumer on queue: back, and
// not duplicated by the recovery.
func requireOneConsumer(t *testing.T, queue string) {
	t.Helper()
	var consumers int
	mustRawAmqp(t, "inspect "+queue, func(ch *amqp.Channel) error {
		q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
		consumers = q.Consumers
		return err
	})
	if consumers != 1 {
		t.Fatalf("queue %s: want exactly 1 consumer, got %d", queue, consumers)
	}
}

// A pod that starts while the broker is down must begin consuming once the broker is
// reachable, without a restart. MqConsume used to give up on the first failed dial.
func TestBroker_ConsumerStartsWhenBrokerComesUpLater(t *testing.T) {
	broker := requireRabbitMQ(t)
	MqClose() // drop any cached connection so the first dial really fails

	broker.setDown(true)
	queue, received := consumeTestQueue(t, "nbtest_late_broker")
	time.Sleep(2 * time.Second)
	broker.setDown(false)

	requireConsuming(t, queue, received, 45*time.Second)
	requireOneConsumer(t, queue)
}

// After a reconnect, go-rabbitmq restarts a consumer in place, and when that restart
// fails it never tries again on a healthy connection — the consumer stays attached to
// nothing. Here the restart fails deterministically: the broker comes back with
// the queue re-declared non-durable. The consumer must be rebuilt by MqConsume, whose
// durability migration repairs the queue.
func TestBroker_ConsumerRebuiltAfterReconnect(t *testing.T) {
	broker := requireRabbitMQ(t)
	queue, received := consumeTestQueue(t, "nbtest_reconnect")
	requireConsuming(t, queue, received, 30*time.Second)

	broker.outage(3*time.Second, func() {
		mustRawAmqp(t, "re-declare "+queue+" non-durable", func(ch *amqp.Channel) error {
			if _, err := ch.QueueDelete(queue, false, false, false); err != nil {
				return err
			}
			_, err := ch.QueueDeclare(queue, false, false, false, false, nil)
			return err
		})
	})

	requireConsuming(t, queue, received, 60*time.Second)
	requireOneConsumer(t, queue)
	mustRawAmqp(t, queue+" is durable again", func(ch *amqp.Channel) error {
		_, err := ch.QueueDeclare(queue, true, false, false, false, nil)
		return err
	})
}

// A broker that returns without its data (volume loss) gets every exchange, queue,
// binding and dead-letter queue back, with exactly one consumer per queue.
func TestBroker_TopologyRestoredAfterWipe(t *testing.T) {
	broker := requireRabbitMQ(t)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	dlqExchange, dlqQueue := "nbtest_dlq_exchange_"+suffix, "nbtest_dlq_"+suffix
	if err := MqDeclareDLQ(dlqExchange, dlqQueue); err != nil {
		t.Fatalf("MqDeclareDLQ: %v", err)
	}
	queue, received := consumeTestQueue(t, "nbtest_wipe")
	requireConsuming(t, queue, received, 30*time.Second)

	broker.outage(3*time.Second, func() {
		mustRawAmqp(t, "wipe topology", func(ch *amqp.Channel) error {
			for _, q := range []string{queue, dlqQueue} {
				if _, err := ch.QueueDelete(q, false, false, false); err != nil {
					return err
				}
			}
			for _, ex := range []string{queue + "_exchange", dlqExchange} {
				if err := ch.ExchangeDelete(ex, false, false); err != nil {
					return err
				}
			}
			return nil
		})
	})

	requireConsuming(t, queue, received, 60*time.Second)
	requireOneConsumer(t, queue)

	// The DLQ is back and bound: a message routed to it is kept.
	var kept int
	for deadline := time.Now().Add(15 * time.Second); kept == 0 && time.Now().Before(deadline); time.Sleep(time.Second) {
		_ = rawAmqp(t, func(ch *amqp.Channel) error {
			if err := ch.Publish(dlqExchange, dlqQueue, false, false, amqp.Publishing{Body: []byte("dead")}); err != nil {
				return err
			}
			time.Sleep(200 * time.Millisecond)
			q, err := ch.QueueDeclarePassive(dlqQueue, true, false, false, false, nil)
			kept = q.Messages
			return err
		})
	}
	if kept == 0 {
		t.Fatalf("DLQ %s -> %s not re-declared after the wipe", dlqExchange, dlqQueue)
	}
}
