package common

import (
	"log/slog"
	"net"
	"nudgebee/llm/config"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// requireRabbitMQ skips MQ tests when no broker is reachable. Without it the
// tests not only fail but panic (publishing fails, leaving a nil publisher that
// is then Close()d), aborting the whole package's test binary.
func requireRabbitMQ(t *testing.T) {
	t.Helper()
	config.Config.RabbitMqHost = "127.0.0.1"
	config.Config.RabbitMqPort = 5672
	config.Config.RabbitMqUsername = "guest"
	config.Config.RabbitMqPassword = "guest"

	addr := net.JoinHostPort(config.Config.RabbitMqHost, strconv.Itoa(config.Config.RabbitMqPort))
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		t.Skipf("RabbitMQ not reachable at %s; skipping (%v)", addr, err)
	}
	_ = conn.Close()
}

func TestPublishConnectionClose(t *testing.T) {
	requireRabbitMQ(t)

	err := MqPublish("test", "test", "test")
	assert.Nil(t, err)

	MqClose()
	// auto connect
	err = MqPublish("test", "test", "test")
	assert.Nil(t, err)
}

func TestPublishClose(t *testing.T) {
	requireRabbitMQ(t)

	err := MqPublish("test", "test", "test")
	assert.Nil(t, err)

	pub := rbmqPublishers["test:test"]
	assert.NotNil(t, pub)

	pub.Close()

	// auto connect
	err = MqPublish("test", "test", "test")
	assert.Nil(t, err)
}

func TestConsumerClose(t *testing.T) {
	requireRabbitMQ(t)

	err := MqConsume("test", "test", "test", func(data []byte) error {
		slog.Info("consumed message", "data", string(data))
		return nil
	})
	assert.Nil(t, err)
	time.Sleep(5 * time.Second)

	// auto connect
	err = MqPublish("test", "test", "test")
	assert.Nil(t, err)

	MqClose()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	i := 0
	for range ticker.C {
		if i == 3 {
			ticker.Stop()
			break
		}
		err = MqPublish("test", "test", "test")
		assert.Nil(t, err)
		i++
	}

	// auto connect
	assert.Nil(t, err)
}
