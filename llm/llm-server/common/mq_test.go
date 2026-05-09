package common

import (
	"log/slog"
	"nudgebee/llm/config"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPublishConnectionClose(t *testing.T) {
	config.Config.RabbitMqHost = "127.0.0.1"
	config.Config.RabbitMqPort = 5672
	config.Config.RabbitMqUsername = "guest"
	config.Config.RabbitMqPassword = "guest"

	err := MqPublish("test", "test", "test")
	assert.Nil(t, err)

	MqClose()
	// auto connect
	err = MqPublish("test", "test", "test")
	assert.Nil(t, err)
}

func TestPublishClose(t *testing.T) {
	config.Config.RabbitMqHost = "127.0.0.1"
	config.Config.RabbitMqPort = 5672
	config.Config.RabbitMqUsername = "guest"
	config.Config.RabbitMqPassword = "guest"

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
	config.Config.RabbitMqHost = "127.0.0.1"
	config.Config.RabbitMqPort = 5672
	config.Config.RabbitMqUsername = "guest"
	config.Config.RabbitMqPassword = "guest"

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
