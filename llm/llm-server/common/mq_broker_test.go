package common

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/llm/config"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"
	"github.com/wagslane/go-rabbitmq"
)

// Broker-backed lifecycle regressions; need RabbitMQ on 127.0.0.1:5672 (guest/guest), skip otherwise.

func waitStateWithin(t *testing.T, queue string, want MqConsumerState, within time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		for _, st := range MqConsumerStatuses() {
			if st.Queue == queue {
				return st.State == want
			}
		}
		return false
	}, within, 50*time.Millisecond, "queue %s never reached state %s", queue, want)
}

// waitConsuming waits until the broker itself reports a consumer on queue. MqConsumerRunning is
// set before the library declares and binds the queue, so a publish right after it can still be
// dropped as unroutable; a consumer on the queue means the binding is in place.
func waitConsuming(t *testing.T, queue string, within time.Duration) {
	t.Helper()
	conn, err := amqp.Dial(brokerURL())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	require.Eventually(t, func() bool {
		// A passive declare of a queue that does not exist yet closes the channel, so use a
		// fresh one per attempt.
		ch, err := conn.Channel()
		if err != nil {
			return false
		}
		defer func() { _ = ch.Close() }()
		q, err := ch.QueueDeclarePassive(queue, false, false, false, false, nil)
		return err == nil && q.Consumers > 0
	}, within, 50*time.Millisecond, "broker never reported a consumer on queue %s", queue)
}

func brokerURL() string {
	return fmt.Sprintf("amqp://%s:%s@%s:%d", config.Config.RabbitMqUsername, config.Config.RabbitMqPassword, config.Config.RabbitMqHost, config.Config.RabbitMqPort)
}

// OSS incident on a real broker: handler's completion publish hits a broker-closed channel.
// Old code deadlocked in MqClose here -> queue no longer consumed.
func TestBroker_ClosedPublisherChannelInHandler_ConsumerKeepsConsuming(t *testing.T) {
	requireRabbitMQ(t)
	MqClose()
	t.Cleanup(MqClose)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	inExchange, inQueue, outExchange := "nbtest_in_"+suffix, "nbtest_in_"+suffix, "nbtest_out_"+suffix

	handled := make(chan error, 4)
	require.NoError(t, MqConsume(inExchange, inQueue, inQueue, 1, func(ctx context.Context, _ []byte) error {
		handled <- MqPublishWithContext(ctx, outExchange, "rk", "completion")
		return nil
	}))
	waitStateWithin(t, inQueue, MqConsumerRunning, 15*time.Second)
	waitConsuming(t, inQueue, 15*time.Second)

	require.NoError(t, MqPublish(outExchange, "rk", "warm"))
	outKey := outExchange + ":rk"
	rbmqMux.Lock()
	warm := rbmqPublishers[outKey]
	rbmqMux.Unlock()
	require.NotNil(t, warm)

	// Delete exchange + publish once -> broker 404-closes the channel; go-rabbitmq waits seconds
	// before reopening, the window the incident hit.
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
		require.NoError(t, err, "completion publish must succeed on a replacement publisher")
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

// On-prem fresh install on a real broker: dials fail at registration, then succeed -> must attach
// without a restart. The old sync.Once never dialed again.
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
	got := make(chan struct{}, 1)
	require.NoError(t, MqConsume(exchange, queue, queue, 1, func(context.Context, []byte) error {
		got <- struct{}{}
		return nil
	}))
	time.Sleep(200 * time.Millisecond)
	healthy, _ := MqConsumersHealthy(0)
	require.False(t, healthy)

	reachable.Store(true)
	waitStateWithin(t, queue, MqConsumerRunning, 30*time.Second)
	waitConsuming(t, queue, 30*time.Second)
	require.NoError(t, MqPublish(exchange, queue, "hello"))
	select {
	case <-got:
	case <-time.After(20 * time.Second):
		t.Fatal("consumer attached late never received a message")
	}
}
