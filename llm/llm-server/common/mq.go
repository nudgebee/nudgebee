package common

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"nudgebee/llm/config"
	"strings"
	"sync"
	"time"

	"github.com/wagslane/go-rabbitmq"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// mqExtractContext continues the distributed trace: it extracts the W3C context
// the publisher injected into the AMQP headers and starts a consumer span, so
// the processor's logs / outbound calls share the same trace_id as the
// publisher. Mirrors the consumer path in api-server / cloud-collector mq.go.
func mqExtractContext(headers map[string]interface{}) (context.Context, trace.Span) {
	carrier := propagation.MapCarrier{}
	for k, v := range headers {
		// AMQP header values arrive as string or, depending on the publisher's
		// client library / broker encoding, []byte. Handle both so trace
		// context extraction doesn't silently fail.
		switch val := v.(type) {
		case string:
			carrier[k] = val
		case []byte:
			carrier[k] = string(val)
		}
	}
	ctx := otel.GetTextMapPropagator().Extract(context.Background(), carrier)
	return otel.Tracer("mq").Start(ctx, "rabbitmq.consume", trace.WithSpanKind(trace.SpanKindConsumer))
}

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
			// Diagnostic: this dial has no client-side timeout. A blocked
			// SYN/handshake can stall the entire startup path silently
			// (rabbit.NewConn doesn't log until it errors). The "dialing"
			// line below + the "connected" / error line is what lets us
			// tell from logs alone whether we were stuck here.
			slog.Default().Info("rbmq: dialing rabbitmq",
				"host", config.Config.RabbitMqHost, "port", config.Config.RabbitMqPort)
			rbmqConn1, err := rabbitmq.NewConn(
				fmt.Sprintf("amqp://%s:%s@%s:%d", config.Config.RabbitMqUsername, config.Config.RabbitMqPassword, config.Config.RabbitMqHost, config.Config.RabbitMqPort),
				rabbitmq.WithConnectionOptionsLogging,
				rabbitmq.WithConnectionOptionsReconnectInterval(reconnectTimeDelay),
			)
			if err != nil {
				slog.Default().Error("rbmq: error connecting to rabbitmq",
					"host", config.Config.RabbitMqHost, "port", config.Config.RabbitMqPort, "error", err)
			} else {
				slog.Default().Info("rbmq: connected to rabbitmq",
					"host", config.Config.RabbitMqHost, "port", config.Config.RabbitMqPort)
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

func MqConsume(exchangeName string, routingKey string, queue string, processor func(ctx context.Context, data []byte) error) error {
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
		// published messages pile up (or, for a non-durable queue, are
		// dropped) until the pod is manually restarted. That silent wedge is
		// exactly what took down the event-investigation pipeline. Loop
		// forever with backoff instead so the consumer recovers from any
		// duration of broker unavailability. Each reconnect rebuilds a fresh
		// consumer (rather than recursing) so this stays a single long-lived
		// goroutine and never calls Run on a closed handle.
		for {
			err := consumer.Run(
				func(d rabbitmq.Delivery) rabbitmq.Action {
					ctx, span := mqExtractContext(d.Headers)
					defer span.End()
					if perr := processor(ctx, d.Body); perr != nil {
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
func MqFanoutSubscribe(exchangeName string, processor func(ctx context.Context, data []byte) error) error {
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
					ctx, span := mqExtractContext(d.Headers)
					defer span.End()
					if perr := processor(ctx, d.Body); perr != nil {
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
	return mqPublish(context.Background(), exchangeName, routingKey, message...)
}

// MqPublishWithContext threads a request context into the publish so the active
// W3C trace context (traceparent) is injected into the message headers. The
// consuming service can then extract it and continue the same distributed trace.
func MqPublishWithContext(ctx context.Context, exchangeName string, routingKey string, message ...any) error {
	return mqPublish(ctx, exchangeName, routingKey, message...)
}

func mqPublish(ctx context.Context, exchangeName string, routingKey string, message ...any) error {
	// Base header carried on every publish, plus the active W3C trace context
	// (traceparent) injected from ctx so the consumer can continue the trace.
	// A background ctx (the MqPublish path) simply has no span to inject.
	headers := map[string]any{"x-nb-source": config.Config.OtelServiceName}
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	for k, v := range carrier {
		headers[k] = v
	}

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
				rabbitmq.WithPublishOptionsHeaders(headers),
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
