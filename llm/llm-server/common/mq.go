package common

import (
	"fmt"
	"log"
	"log/slog"
	"nudgebee/llm/config"
	"strings"
	"sync"
	"time"

	"github.com/wagslane/go-rabbitmq"
)

var (
	rbmqConnOnce sync.Once
	rbmqConn     *rabbitmq.Conn
	// rbmqConsumers / rbmqPublishers are keyed by queue / exchange:routing-key
	// respectively. Both are mutated from multiple goroutines: the
	// reconnect path of each consumer/publisher modifies its own entry,
	// and concurrent reconnects across different exchanges (e.g. the
	// troubleshoot exchange and the cache-invalidation exchange) used to
	// race on the map header → fatal "concurrent map writes". All access
	// must hold rbmqMux.
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

	// Close handles outside the lock — Close() on wagslane handles can
	// block briefly and we don't want to serialize teardown with healthy
	// publishes/consumes that are racing for the same lock.
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
				rbmqMux.Lock()
				delete(rbmqConsumers, queue)
				rbmqMux.Unlock()
				err = MqConsume(exchangeName, routingKey, queue, processor)
				if err != nil {
					slog.Error("rbmq: error reconnecting consumer", "error", err)
					continue
				}
				return
			}
		}
	}()

	rbmqMux.Lock()
	rbmqConsumers[queue] = consumer
	rbmqMux.Unlock()
	return nil
}

// MqFanoutSubscribe subscribes to a fanout exchange so this pod receives
// every message regardless of routing key, in addition to every other pod
// bound to the same exchange. Used for cross-replica events like cache
// invalidation where each pod must process the message independently —
// NOT for work-distribution events where load-balancing is desired (use
// MqConsume for those).
//
// Each pod gets its own auto-delete + exclusive queue named
// "<exchangeName>_<ServerName>" so the queue is uniquely owned by this
// pod's connection and cleaned up by RabbitMQ when the pod disconnects.
// No leaked queues survive a pod restart.
func MqFanoutSubscribe(exchangeName string, processor func(data []byte) error) error {
	conn := getConnection()
	if conn == nil {
		slog.Error("rbmq: error connecting to rabbitmq for fanout subscribe")
		return ErrRbmqNoConn
	}

	queue := exchangeName + "_" + config.Config.ServerName

	consumer, err := rabbitmq.NewConsumer(
		conn,
		queue,
		rabbitmq.WithConsumerOptionsExchangeName(exchangeName),
		rabbitmq.WithConsumerOptionsExchangeKind("fanout"),
		rabbitmq.WithConsumerOptionsExchangeDeclare,
		rabbitmq.WithConsumerOptionsExchangeDurable,
		// Empty routing key is fine for fanout (the exchange ignores it on
		// publish), but we MUST call WithConsumerOptionsRoutingKey to make
		// wagslane append a Binding entry — without it the queue is created
		// but never bound to the exchange and every publish silently
		// returns "Message published but NOT routed".
		rabbitmq.WithConsumerOptionsRoutingKey(""),
		rabbitmq.WithConsumerOptionsQueueAutoDelete,
		rabbitmq.WithConsumerOptionsQueueExclusive,
		rabbitmq.WithConsumerOptionsQOSPrefetch(1),
		rabbitmq.WithConsumerOptionsConsumerName(config.Config.OtelServiceName+"/fanout/"+queue),
	)
	if err != nil {
		slog.Error("rbmq: error creating fanout consumer", "exchange", exchangeName, "queue", queue, "error", err)
		return err
	}

	go func() {
		// Infinite reconnect: a fanout consumer that gives up after N
		// attempts re-introduces the silent-staleness failure mode this
		// helper exists to prevent — published invalidations land on a
		// queue that no consumer is attached to (and because the queue is
		// auto-delete + exclusive, the queue itself is gone after the
		// consumer's connection drops). The pod would then run against
		// stale caches indefinitely with no signal until the next
		// restart. Looping forever with backoff matches the wagslane
		// connection-level reconnect interval and ensures recovery from
		// any duration of broker unavailability.
		for {
			err := consumer.Run(
				func(d rabbitmq.Delivery) rabbitmq.Action {
					if perr := processor(d.Body); perr != nil {
						log.Printf("rbmq fanout: error processing message on %s: %s", exchangeName, perr)
						return rabbitmq.NackRequeue
					}
					return rabbitmq.Ack
				})
			if err == nil {
				// consumer.Run returned without error — clean shutdown,
				// usually because someone called consumer.Close(). Don't
				// re-arm in that case.
				return
			}
			slog.Error("rbmq: fanout consumer.run failed; reconnecting",
				"exchange", exchangeName, "queue", queue, "error", err)
			time.Sleep(reconnectTimeDelay)
			consumer.Close()
			rbmqMux.Lock()
			delete(rbmqConsumers, queue)
			rbmqMux.Unlock()
			if rerr := MqFanoutSubscribe(exchangeName, processor); rerr != nil {
				slog.Error("rbmq: error reconnecting fanout consumer; will retry",
					"exchange", exchangeName, "queue", queue, "error", rerr)
				// Loop continues — sleep already happened above.
				continue
			}
			// Reconnect spawned its own goroutine; this one is done.
			return
		}
	}()

	rbmqMux.Lock()
	rbmqConsumers[queue] = consumer
	rbmqMux.Unlock()
	return nil
}

func MqPublish(exchangeName string, routingKey string, message ...any) error {
	var err error
	for range maxAttempts {
		err = nil
		key := fmt.Sprintf("%s:%s", exchangeName, routingKey)

		// Map lookup + create-if-missing happens under rbmqMux so two
		// concurrent publishers on different exchanges can't fatally race
		// the map header.
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
