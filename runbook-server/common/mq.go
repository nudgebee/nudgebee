package common

import (
	"fmt"
	"log"
	"log/slog"
	"nudgebee/runbook/config"
	"strings"
	"sync"
	"time"

	"github.com/wagslane/go-rabbitmq"
)

var (
	rbmqConnOnce       sync.Once
	rbmqConn           *rabbitmq.Conn
	rbmqConsumers      = make(map[string]*rabbitmq.Consumer)
	rbmqPublishers     = make(map[string]*rabbitmq.Publisher)
	maxAttempts        = 3
	reconnectTimeDelay = 5 * time.Second
)

var ErrRbmqNoConn = fmt.Errorf("rbmq: unable to connect to rabbitmq")

func init() {
	// todo close connections on exit gracefully
	// currently this is blocking testcase execution
	// c := make(chan os.Signal, 1)
	// signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	// for {
	// 	select {
	// 	case <-c:
	// 		closeConnection()
	// 		return
	// 	}
	// }
}

func getConnection() *rabbitmq.Conn {
	if rbmqConn == nil {
		rbmqConnOnce.Do(func() {
			rbmqConn1, err := rabbitmq.NewConn(
				fmt.Sprintf("amqp://%s:%s@%s:%d", config.Config.RabbitMqUsername, config.Config.RabbitMqPassword, config.Config.RabbitMqHost, config.Config.RabbitMqPort),
				rabbitmq.WithConnectionOptionsLogging,
				rabbitmq.WithConnectionOptionsReconnectInterval(reconnectTimeDelay),
			)
			if err != nil {
				slog.Default().Error("Error connecting to RabbitMQ", "error", err)
			}
			rbmqConn = rbmqConn1
		})
	}
	return rbmqConn
}

func MqClose() {
	for _, consumer := range rbmqConsumers {
		consumer.Close()
	}
	rbmqConsumers = make(map[string]*rabbitmq.Consumer)
	for _, publisher := range rbmqPublishers {
		publisher.Close()
	}
	rbmqPublishers = make(map[string]*rabbitmq.Publisher)

	rbmqConnOnce = sync.Once{}
	rbmqConn = nil
}

func MqConsume(exchangeName string, routingKey string, queue string, processor func(data []byte) error) error {
	conn := getConnection()
	if conn == nil {
		slog.Error("rbmq: error connecting to rabbitmq")
		return ErrRbmqNoConn
	}

	consumer, err := rabbitmq.NewConsumer(
		conn,
		queue,
		rabbitmq.WithConsumerOptionsRoutingKey(routingKey),
		rabbitmq.WithConsumerOptionsExchangeName(exchangeName),
		rabbitmq.WithConsumerOptionsQOSPrefetch(1),
		rabbitmq.WithConsumerOptionsExchangeDeclare,
		rabbitmq.WithConsumerOptionsExchangeDurable,
		rabbitmq.WithConsumerOptionsConsumerName(config.Config.OtelServiceName+"/"+routingKey+"/"+config.Config.ServerName),
	)
	if err != nil {
		slog.Error("rbmq: error creating consumer", "error", err)
		return err
	}

	go func() {
		for range maxAttempts {
			err := consumer.Run(
				func(d rabbitmq.Delivery) rabbitmq.Action {
					err := processor(d.Body)
					if err != nil {
						log.Printf("error processing message: %s", err)
						return rabbitmq.NackRequeue
					}
					return rabbitmq.Ack
				})
			if err != nil {
				slog.Error("rbmq: consumer.run failed", "error", err)
				time.Sleep(reconnectTimeDelay)
				slog.Info("rbmq: reconnecting consumer")
				consumer.Close()
				delete(rbmqConsumers, queue)
				err = MqConsume(exchangeName, routingKey, queue, processor)
				if err != nil {
					slog.Error("rbmq: error reconnecting consumer", "error", err)
					continue
				}
				return
			}
		}
	}()

	rbmqConsumers[queue] = consumer
	return nil
}

func MqPublish(exchangeName string, routingKey string, message ...any) error {
	var err error
	for range maxAttempts {
		err = nil
		key := fmt.Sprintf("%s:%s", exchangeName, routingKey)
		if _, ok := rbmqPublishers[key]; !ok {
			conn := getConnection()
			if conn == nil {
				slog.Error("rbmq: error connecting to rabbitmq")
				return ErrRbmqNoConn
			}

			publisher, err := rabbitmq.NewPublisher(
				conn,
				rabbitmq.WithPublisherOptionsLogging,
				rabbitmq.WithPublisherOptionsExchangeName(exchangeName),
				rabbitmq.WithPublisherOptionsExchangeDeclare,
				rabbitmq.WithPublisherOptionsExchangeDurable,
			)
			if err != nil {
				slog.Error("rbmq: error creating publisher", "error", err)
				return err
			}
			rbmqPublishers[key] = publisher
		}
		publisher := rbmqPublishers[key]

		for _, msg1 := range message {
			var data []byte
			switch msg := msg1.(type) {
			case string:
				data = []byte(msg)
			case []byte:
				data = msg
			default:
				data, err = MarshalJson(msg)
				if err != nil {
					return err
				}
			}

			err = publisher.Publish(
				data,
				[]string{routingKey},
				rabbitmq.WithPublishOptionsContentType("application/json"),
				rabbitmq.WithPublishOptionsExchange(exchangeName),
				rabbitmq.WithPublishOptionsHeaders(map[string]any{"x-nb-source": config.Config.OtelServiceName}),
			)
			if err != nil {
				break
			}
		}
		if err != nil {
			if strings.Contains(err.Error(), "channel/connection is not open") {
				slog.Info("rbmq: reconnecting publisher as connection is closed")
				publisher.Close()
				delete(rbmqPublishers, key)
				time.Sleep(reconnectTimeDelay)
				MqClose()
				continue
			}
		}
		return err
	}

	return err
}
