package common

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/services/config"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"
)

// Live-broker recovery tests for a broker restart (#39004). Same switch as the
// scenario tests in mq_broker_scenario_test.go: skipped unless
// NB_TEST_RABBIT_HOST / NB_TEST_RABBIT_PORT point at a broker.
//
// MqConsume reaches the broker through brokerProxy, which can refuse new
// connections and sever open ones — what a broker restart looks like to a
// client. The tests talk to the broker directly (rawAmqp) to change its topology.

var (
	testBrokerProxyOnce sync.Once
	testBrokerProxy     *brokerProxy
	testBrokerAddr      string
)

func requireBrokerProxy(t *testing.T) *brokerProxy {
	t.Helper()
	host := os.Getenv("NB_TEST_RABBIT_HOST")
	if host == "" {
		t.Skip("NB_TEST_RABBIT_HOST not set; skipping live-broker scenario")
	}
	testBrokerProxyOnce.Do(func() {
		testBrokerAddr = net.JoinHostPort(host, os.Getenv("NB_TEST_RABBIT_PORT"))
		testBrokerProxy = startBrokerProxy(t, testBrokerAddr)
	})

	config.Config.RabbitMqHost = "127.0.0.1"
	config.Config.RabbitMqPort = testBrokerProxy.port()
	config.Config.RabbitMqUsername = envOrDefault("NB_TEST_RABBIT_USER", "guest")
	config.Config.RabbitMqPassword = envOrDefault("NB_TEST_RABBIT_PASSWORD", "guest")
	config.Config.ServerRole = config.ServerRoleWorker

	testBrokerProxy.setDown(false)
	MqClose()
	t.Cleanup(MqClose)
	require.NotNil(t, getConnection(), "could not reach the test broker through the proxy")
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

func rawAmqp(fn func(ch *amqp.Channel) error) error {
	url := fmt.Sprintf("amqp://%s:%s@%s/", config.Config.RabbitMqUsername, config.Config.RabbitMqPassword, testBrokerAddr)
	conn, err := amqp.DialConfig(url, amqp.Config{Dial: amqp.DefaultDial(2 * time.Second)})
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

// consumeTestQueue starts MqConsume on a fresh queue and returns the queue name
// and a counter of processed messages.
func consumeTestQueue(t *testing.T, name string) (string, *atomic.Int64) {
	t.Helper()
	queue := name + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	var received atomic.Int64
	require.NoError(t, MqConsume(queue+"_exchange", queue, queue, 1, func(context.Context, []byte) error {
		received.Add(1)
		return nil
	}))
	return queue, &received
}

// requireConsuming publishes to queue's exchange until the consumer processes a message.
func requireConsuming(t *testing.T, queue string, received *atomic.Int64, within time.Duration) {
	t.Helper()
	start := received.Load()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(time.Second) {
		_ = rawAmqp(func(ch *amqp.Channel) error {
			return ch.Publish(queue+"_exchange", queue, false, false, amqp.Publishing{Body: []byte(`"ping"`)})
		})
		if received.Load() > start {
			return
		}
	}
	t.Fatalf("queue %s: no message consumed within %s", queue, within)
}

// requireOneConsumer checks the broker reports exactly one consumer on queue:
// back, and not duplicated by the recovery.
func requireOneConsumer(t *testing.T, queue string) {
	t.Helper()
	var consumers int
	require.NoError(t, rawAmqp(func(ch *amqp.Channel) error {
		q, err := ch.QueueDeclarePassive(queue, false, false, false, false, nil)
		consumers = q.Consumers
		return err
	}), "inspect %s", queue)
	require.Equal(t, 1, consumers, "queue %s: want exactly one consumer", queue)
}

// registeredConsumer is the consumer MqConsume currently has registered for
// queue, or nil while it is between consumers.
func registeredConsumer(queue string) any {
	rbmqConsumersMux.Lock()
	defer rbmqConsumersMux.Unlock()
	if c, ok := rbmqConsumers[queue]; ok {
		return c
	}
	return nil
}

// TestMqScenarioConsumerRebuiltAfterReconnect: go-rabbitmq restarts a consumer
// in place after a reconnect, and when that restart fails it can stop trying on
// a healthy connection — Run stays blocked and the queue is never consumed
// again. That failure is a race inside the library, so this does not try to
// provoke it; it pins the contract that makes it harmless: after the connection
// comes back, the consumer that was running before the outage is replaced by a
// freshly built one.
func TestMqScenarioConsumerRebuiltAfterReconnect(t *testing.T) {
	broker := requireBrokerProxy(t)
	queue, received := consumeTestQueue(t, "scenario_reconnect")
	requireConsuming(t, queue, received, 30*time.Second)
	before := registeredConsumer(queue)
	require.NotNil(t, before)

	broker.outage(3*time.Second, func() {})

	require.Eventually(t, func() bool {
		c := registeredConsumer(queue)
		return c != nil && c != before
	}, 60*time.Second, 500*time.Millisecond, "consumer was not rebuilt after the broker came back")
	requireConsuming(t, queue, received, 30*time.Second)
	requireOneConsumer(t, queue)
}

// TestMqScenarioTopologyRestoredAfterWipe: a broker that returns without its
// data (volume loss) gets the exchange, queue and binding back, with exactly one
// consumer on the queue. The library's in-place restart re-declares them too, so
// this passes without the rebuild; it guards that rebuilding on top of that
// restart neither loses the topology nor leaves a second consumer.
func TestMqScenarioTopologyRestoredAfterWipe(t *testing.T) {
	broker := requireBrokerProxy(t)
	queue, received := consumeTestQueue(t, "scenario_wipe")
	requireConsuming(t, queue, received, 30*time.Second)

	broker.outage(3*time.Second, func() {
		require.NoError(t, rawAmqp(func(ch *amqp.Channel) error {
			if _, err := ch.QueueDelete(queue, false, false, false); err != nil {
				return err
			}
			return ch.ExchangeDelete(queue+"_exchange", false, false)
		}), "wipe topology")
	})

	requireConsuming(t, queue, received, 60*time.Second)
	requireOneConsumer(t, queue)
}
