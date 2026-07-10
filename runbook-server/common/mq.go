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
	rbmqConnOnce sync.Once
	rbmqConn     *rabbitmq.Conn
	// rbmqMux guards rbmqConsumers / rbmqPublishers. Both are mutated from
	// multiple goroutines — the reconnect loop of each consumer and
	// concurrent MqPublish callers — so unsynchronized map access can trip
	// Go's "concurrent map writes" fatal panic. All access must hold rbmqMux.
	rbmqMux            sync.Mutex
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
	rbmqMux.Lock()
	consumers := rbmqConsumers
	publishers := rbmqPublishers
	rbmqConsumers = make(map[string]*rabbitmq.Consumer)
	rbmqPublishers = make(map[string]*rabbitmq.Publisher)
	rbmqMux.Unlock()

	// Close handles outside the lock — Close() can block briefly and we
	// don't want to serialize teardown with healthy publishes/consumes.
	for _, consumer := range consumers {
		consumer.Close()
	}
	for _, publisher := range publishers {
		publisher.Close()
	}

	rbmqConnOnce = sync.Once{}
	rbmqConn = nil
}

func MqConsume(exchangeName string, routingKey string, queue string, processor func(data []byte) error) error {
	conn := getConnection()
	if conn == nil {
		slog.Error("rbmq: error connecting to rabbitmq")
		return ErrRbmqNoConn
	}

	// newConsumer builds a consumer on the current shared connection. Used
	// for both the initial creation and every reconnect below so the two can
	// never drift; getConnection() is re-fetched each call so a reconnect
	// picks up the live connection after a broker restart.
	newConsumer := func() (*rabbitmq.Consumer, error) {
		conn := getConnection()
		if conn == nil {
			return nil, ErrRbmqNoConn
		}
		return rabbitmq.NewConsumer(
			conn,
			queue,
			rabbitmq.WithConsumerOptionsRoutingKey(routingKey),
			rabbitmq.WithConsumerOptionsExchangeName(exchangeName),
			rabbitmq.WithConsumerOptionsQOSPrefetch(1),
			rabbitmq.WithConsumerOptionsExchangeDeclare,
			rabbitmq.WithConsumerOptionsExchangeDurable,
			rabbitmq.WithConsumerOptionsConsumerName(config.Config.OtelServiceName+"/"+routingKey+"/"+config.Config.ServerName),
		)
	}

	consumer, err := newConsumer()
	if err != nil {
		slog.Error("rbmq: error creating consumer", "error", err)
		return err
	}

	rbmqMux.Lock()
	rbmqConsumers[queue] = consumer
	rbmqMux.Unlock()

	go func() {
		// Infinite reconnect: a bounded (maxAttempts) reconnect loop gives up
		// forever once the budget is exhausted — after a broker flap the
		// consumer goroutine exits and the queue is left with no consumer, so
		// the event-investigation completion callback is never delivered and
		// the suspended workflow activity only unblocks via its
		// StartToCloseTimeout. Loop forever with backoff instead so the
		// consumer recovers from any duration of broker unavailability. Each
		// reconnect rebuilds a fresh consumer (rather than recursing) so this
		// stays a single long-lived goroutine and never calls Run on a closed
		// handle.
		for {
			err := consumer.Run(
				func(d rabbitmq.Delivery) rabbitmq.Action {
					if perr := processor(d.Body); perr != nil {
						log.Printf("rbmq: error processing message on %s: %s", queue, perr)
						return rabbitmq.NackRequeue
					}
					return rabbitmq.Ack
				})
			if err == nil {
				// consumer.Run returns nil only on an intentional Close();
				// clean shutdown, don't re-arm.
				return
			}
			slog.Error("rbmq: consumer.run failed; reconnecting", "queue", queue, "error", err)
			consumer.Close()
			rbmqMux.Lock()
			if rbmqConsumers[queue] == consumer {
				delete(rbmqConsumers, queue)
			}
			rbmqMux.Unlock()

			// Rebuild with backoff until NewConsumer succeeds.
			for {
				time.Sleep(reconnectTimeDelay)
				c, nerr := newConsumer()
				if nerr != nil {
					slog.Error("rbmq: error recreating consumer; will retry", "queue", queue, "error", nerr)
					continue
				}
				consumer = c
				rbmqMux.Lock()
				rbmqConsumers[queue] = consumer
				rbmqMux.Unlock()
				break
			}
		}
	}()

	return nil
}

func MqPublish(exchangeName string, routingKey string, message ...any) error {
	var err error
	for range maxAttempts {
		err = nil
		key := fmt.Sprintf("%s:%s", exchangeName, routingKey)

		// Map lookup + create-if-missing happens under rbmqMux so concurrent
		// publishers can't fatally race the map header.
		rbmqMux.Lock()
		publisher, ok := rbmqPublishers[key]
		rbmqMux.Unlock()
		if !ok {
			conn := getConnection()
			if conn == nil {
				slog.Error("rbmq: error connecting to rabbitmq")
				return ErrRbmqNoConn
			}

			newPublisher, perr := rabbitmq.NewPublisher(
				conn,
				rabbitmq.WithPublisherOptionsLogging,
				rabbitmq.WithPublisherOptionsExchangeName(exchangeName),
				rabbitmq.WithPublisherOptionsExchangeDeclare,
				rabbitmq.WithPublisherOptionsExchangeDurable,
			)
			if perr != nil {
				slog.Error("rbmq: error creating publisher", "error", perr)
				return perr
			}
			// Re-check under lock — another goroutine may have raced ahead.
			rbmqMux.Lock()
			if existing, dup := rbmqPublishers[key]; dup && existing != nil {
				newPublisher.Close()
				publisher = existing
			} else {
				rbmqPublishers[key] = newPublisher
				publisher = newPublisher
			}
			rbmqMux.Unlock()
		}

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
				rbmqMux.Lock()
				delete(rbmqPublishers, key)
				rbmqMux.Unlock()
				time.Sleep(reconnectTimeDelay)
				MqClose()
				continue
			}
		}
		return err
	}

	return err
}
