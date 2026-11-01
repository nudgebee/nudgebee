package common

import (
	"context"
	"errors"
	"fmt"
	"net"
	"nudgebee/runbook/config"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"
	"github.com/wagslane/go-rabbitmq"
)

// Broker-backed lifecycle regressions (ported from llm-server, #39004); need RabbitMQ on
// 127.0.0.1:5672 (guest/guest), skip otherwise, fail if NB_REQUIRE_RABBITMQ is set.

func requireRabbitMQ(t *testing.T) {
	t.Helper()
	savedHost, savedPort, savedUser, savedPass := config.Config.RabbitMqHost, config.Config.RabbitMqPort, config.Config.RabbitMqUsername, config.Config.RabbitMqPassword
	t.Cleanup(func() {
		config.Config.RabbitMqHost, config.Config.RabbitMqPort, config.Config.RabbitMqUsername, config.Config.RabbitMqPassword = savedHost, savedPort, savedUser, savedPass
	})
	config.Config.RabbitMqHost = "127.0.0.1"
	config.Config.RabbitMqPort = 5672
	config.Config.RabbitMqUsername = "guest"
	config.Config.RabbitMqPassword = "guest"

	addr := net.JoinHostPort(config.Config.RabbitMqHost, strconv.Itoa(config.Config.RabbitMqPort))
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		if os.Getenv("NB_REQUIRE_RABBITMQ") != "" {
			t.Fatalf("RabbitMQ required (NB_REQUIRE_RABBITMQ) but not reachable at %s: %v", addr, err)
		}
		t.Skipf("RabbitMQ not reachable at %s; skipping (%v)", addr, err)
	}
	_ = conn.Close()
}

func brokerURL() string {
	return fmt.Sprintf("amqp://%s:%s@%s:%d", config.Config.RabbitMqUsername, config.Config.RabbitMqPassword, config.Config.RabbitMqHost, config.Config.RabbitMqPort)
}

// publishUntilDelivered republishes until got receives a message. "running" means Run was called,
// and go-rabbitmq declares the queue & binding inside Run, so a single publish right after can
// still be unroutable (and silently dropped: publishes are not mandatory).
func publishUntilDelivered(t *testing.T, exchange, routingKey string, got <-chan struct{}, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if err := MqPublish(exchange, routingKey, "hello"); err != nil {
			t.Logf("publish while attaching: %v", err)
		}
		select {
		case <-got:
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
	t.Fatalf("consumer on %s never received a message within %s", routingKey, within)
}

// Failure mode 1 on a real broker: a handler's publish hits a broker-closed channel. Old code
// called MqClose here, closing every consumer for good.
func TestBroker_ClosedPublisherChannelInHandler_ConsumerKeepsConsuming(t *testing.T) {
	requireRabbitMQ(t)
	MqClose()
	t.Cleanup(MqClose)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	inExchange, inQueue, outExchange := "nbtest_in_"+suffix, "nbtest_in_"+suffix, "nbtest_out_"+suffix

	handled := make(chan error, 4)
	require.NoError(t, MqConsume(inExchange, inQueue, inQueue, func(ctx context.Context, _ []byte) error {
		handled <- MqPublishCtx(ctx, outExchange, "rk", "completion")
		return nil
	}))
	waitStateWithin(t, inQueue, mqConsumerRunning, 15*time.Second)

	require.NoError(t, MqPublish(outExchange, "rk", "warm"))
	outKey := outExchange + ":rk"
	rbmqMux.Lock()
	warm := rbmqPublishers[outKey]
	rbmqMux.Unlock()
	require.NotNil(t, warm)

	// Delete exchange + publish once -> broker 404-closes the channel; go-rabbitmq waits seconds
	// before reopening, the window the publish error path is hit in.
	raw, err := amqp.Dial(brokerURL())
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	rawCh, err := raw.Channel()
	require.NoError(t, err)
	require.NoError(t, rawCh.ExchangeDelete(outExchange, false, false))
	_ = warm.Publish([]byte("trigger-404"), []string{"rk"}, rabbitmq.WithPublishOptionsExchange(outExchange))
	time.Sleep(300 * time.Millisecond)

	require.NoError(t, MqPublish(inExchange, inQueue, "go"))
	select {
	case err := <-handled:
		require.NoError(t, err, "the publish must succeed on a replacement publisher")
	case <-time.After(20 * time.Second):
		t.Fatal("handler did not finish: publish error path deadlocked")
	}
	rbmqMux.Lock()
	replaced := rbmqPublishers[outKey] != warm
	rbmqMux.Unlock()
	require.True(t, replaced, "the closed publisher should have been replaced (closed-channel path not exercised)")

	require.NoError(t, MqPublish(inExchange, inQueue, "again"))
	select {
	case err := <-handled:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("consumer stopped consuming after a publish failure")
	}
}

// Failure mode 2 on a real broker: dials fail at registration, then succeed -> the consumer must
// attach without a restart. The old sync.Once never dialed again.
func TestBroker_ConsumerAttachesAfterBrokerBecomesReachable(t *testing.T) {
	requireRabbitMQ(t)
	MqClose()
	realDial, savedCooldown := mqDial, mqDialCooldown
	t.Cleanup(func() {
		MqClose()
		mqDial, mqDialCooldown = realDial, savedCooldown
	})

	var reachable atomic.Bool
	mqDialCooldown = 0
	mqDial = func(url string) (*rabbitmq.Conn, error) {
		if !reachable.Load() {
			return nil, errors.New("dial tcp: connection refused")
		}
		return realDial(url)
	}

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	exchange, queue := "nbtest_late_"+suffix, "nbtest_late_"+suffix
	got := make(chan struct{}, 64)
	require.NoError(t, MqConsume(exchange, queue, queue, func(context.Context, []byte) error {
		got <- struct{}{}
		return nil
	}))
	time.Sleep(200 * time.Millisecond)
	waitStateWithin(t, queue, mqConsumerConnecting, time.Second)

	reachable.Store(true)
	waitStateWithin(t, queue, mqConsumerRunning, 30*time.Second)
	publishUntilDelivered(t, exchange, queue, got, 20*time.Second)
}
