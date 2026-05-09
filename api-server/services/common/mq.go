package common

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/services/config"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/wagslane/go-rabbitmq"
)

var (
	rbmqConnMux        sync.Mutex
	rbmqConn           *rabbitmq.Conn
	rbmqConsumers      = make(map[string]*rabbitmq.Consumer)
	rbmqConsumersMux   sync.Mutex
	rbmqPublishers     = make(map[string]*rabbitmq.Publisher)
	rbmqPublishersMux  sync.Mutex
	maxAttempts        = 3
	reconnectTimeDelay = 5 * time.Second
)

// MaxMqPayloadSize is the maximum allowed RabbitMQ message payload size (10MB).
// RabbitMQ's default frame_max is 128MB, but large payloads cause memory pressure
// and connection drops. This limit keeps queue sizes and consumer memory reasonable.
const MaxMqPayloadSize = 10 * 1024 * 1024 // 10MB

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
	rbmqConnMux.Lock()
	defer rbmqConnMux.Unlock()
	if rbmqConn != nil {
		return rbmqConn
	}
	rbmqConn1, err := rabbitmq.NewConn(
		fmt.Sprintf("amqp://%s:%s@%s:%d", config.Config.RabbitMqUsername, config.Config.RabbitMqPassword, config.Config.RabbitMqHost, config.Config.RabbitMqPort),
		rabbitmq.WithConnectionOptionsLogging,
		rabbitmq.WithConnectionOptionsReconnectInterval(reconnectTimeDelay),
	)
	if err != nil {
		slog.Default().Error("Error connecting to RabbitMQ", "error", err)
		return nil
	}
	rbmqConn = rbmqConn1
	slog.Info("rbmq: RabbitMQ connection established successfully")

	return rbmqConn
}

// isRabbitMQConnectionError checks if the given error is a common RabbitMQ connection or channel error.
func isRabbitMQConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "channel/connection is not open") ||
		strings.Contains(errStr, "connection is not open") ||
		strings.Contains(errStr, "channel is not open") ||
		strings.Contains(errStr, "eof") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection reset by peer") ||
		strings.Contains(errStr, "no such connection") ||
		strings.Contains(errStr, "use of closed network connection") ||
		(strings.HasPrefix(errStr, "amqp:") && (strings.Contains(errStr, "channel error") || strings.Contains(errStr, "connection error") || strings.Contains(errStr, "command invalid")))
}

func MqClose() {
	rbmqConsumersMux.Lock()
	for _, consumer := range rbmqConsumers {
		consumer.Close()
	}
	rbmqConsumers = make(map[string]*rabbitmq.Consumer)
	rbmqConsumersMux.Unlock()

	rbmqPublishersMux.Lock()
	for _, publisher := range rbmqPublishers {
		publisher.Close()
	}
	rbmqPublishers = make(map[string]*rabbitmq.Publisher)
	rbmqPublishersMux.Unlock()

	rbmqConnMux.Lock()
	if rbmqConn != nil {
		func() {
			err := rbmqConn.Close()
			if err != nil {
				slog.Error("rbmq: error closing connection", "error", err)
			}
		}()
		rbmqConn = nil
	}
	rbmqConnMux.Unlock()
	slog.Info("rbmq: all connections, consumers, and publishers closed")
}

type MqHealthInfo struct {
	Err        error
	Consumers  int
	Publishers int
}

func MqHealthCheck(ctx context.Context) MqHealthInfo {
	rbmqConnMux.Lock()
	conn := rbmqConn
	rbmqConnMux.Unlock()

	rbmqConsumersMux.Lock()
	consumers := len(rbmqConsumers)
	rbmqConsumersMux.Unlock()

	rbmqPublishersMux.Lock()
	publishers := len(rbmqPublishers)
	rbmqPublishersMux.Unlock()

	info := MqHealthInfo{
		Consumers:  consumers,
		Publishers: publishers,
	}
	if conn == nil {
		info.Err = fmt.Errorf("rabbitmq connection not established")
		return info
	}

	// Verify the connection is actually alive by opening and immediately
	// closing a temporary publisher. This creates an AMQP channel under
	// the hood — if the connection is dead, it fails immediately.
	// Use a channel to handle timeout for the blocking NewPublisher call
	type result struct {
		p   *rabbitmq.Publisher
		err error
	}
	resChan := make(chan result, 1)
	go func() {
		p, err := rabbitmq.NewPublisher(conn)
		resChan <- result{p, err}
	}()

	select {
	case res := <-resChan:
		if res.err != nil {
			info.Err = fmt.Errorf("rabbitmq connection lost: %w", res.err)
			return info
		}
		res.p.Close()
	case <-ctx.Done():
		info.Err = fmt.Errorf("rabbitmq health check timed out: %w", ctx.Err())
		return info
	}

	return info
}

func MqConsume(exchangeName string, routingKey string, queue string, concurrency int, processor func(data []byte) error) error {
	conn := getConnection()
	if conn == nil {
		slog.Error("rbmq: initial connection to rabbitmq failed for consumer setup", "queue", queue, "exchange", exchangeName)
		return ErrRbmqNoConn
	}

	go func() {
		var currentConsumer *rabbitmq.Consumer
		consumerKey := queue

		// Ensure consumer is closed and removed from map when this goroutine exits
		defer func() {
			rbmqConsumersMux.Lock()
			// only delete if it's the one we managed
			if c, ok := rbmqConsumers[consumerKey]; ok && c == currentConsumer {
				delete(rbmqConsumers, consumerKey)
			}
			rbmqConsumersMux.Unlock()
			if currentConsumer != nil {
				currentConsumer.Close()
			}
			slog.Info("rbmq: consumer goroutine shut down", "queue", queue, "exchange", exchangeName)
		}()

		// Loop indefinitely to manage consumer lifecycle
		for attempt := 0; ; attempt++ {
			if attempt > 0 {
				slog.Info("rbmq: delaying consumer reconnect attempt", "queue", queue, "delay", reconnectTimeDelay)
				time.Sleep(reconnectTimeDelay)
			}

			conn := getConnection()
			if conn == nil {
				slog.Error("rbmq: failed to get rabbitmq connection for consumer", "queue", queue, "attempt", attempt+1)
				continue
			}

			var err error
			currentConsumer, err = rabbitmq.NewConsumer(
				conn,
				queue,
				rabbitmq.WithConsumerOptionsRoutingKey(routingKey),
				rabbitmq.WithConsumerOptionsExchangeName(exchangeName),
				rabbitmq.WithConsumerOptionsQOSPrefetch(concurrency),
				rabbitmq.WithConsumerOptionsExchangeDeclare,
				rabbitmq.WithConsumerOptionsConcurrency(concurrency),
				rabbitmq.WithConsumerOptionsExchangeDurable,
				rabbitmq.WithConsumerOptionsLogging,
				rabbitmq.WithConsumerOptionsConsumerName(config.Config.OtelServiceName+"/"+routingKey+"/"+config.Config.ServerName),
			)
			if err != nil {
				slog.Error("rbmq: error creating consumer", "error", err, "queue", queue, "attempt", attempt+1)
				continue
			}

			rbmqConsumersMux.Lock()
			rbmqConsumers[consumerKey] = currentConsumer
			rbmqConsumersMux.Unlock()
			slog.Info("rbmq: consumer created and started", "queue", queue, "exchange", exchangeName, "concurrency", concurrency)

			runErr := func() (runPanicErr error) {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("rbmq: consumer.Run panicked", "panic", r, "queue", queue, "exchange", exchangeName, "stack", string(debug.Stack()))
						runPanicErr = fmt.Errorf("panic in consumer.Run: %v", r)
					}
				}()

				handlerFunc := func(d rabbitmq.Delivery) rabbitmq.Action {
					return processMessageAndDetermineAction(d, processor, queue, exchangeName)
				}
				return currentConsumer.Run(handlerFunc)
			}()

			// Handle consumer.Run exit (error or panic)
			if runErr != nil {
				slog.Warn("rbmq: consumer.Run exited", "error", runErr, "queue", queue, "exchange", exchangeName, "attempt", attempt+1)
			} else {
				// consumer.Run exited cleanly, possibly due to connection closure not handled by library's auto-reconnect
				slog.Info("rbmq: consumer.Run exited cleanly, will attempt to restart", "queue", queue, "exchange", exchangeName, "attempt", attempt+1)
			}

			// Clean up current consumer before retrying
			rbmqConsumersMux.Lock()
			if c, ok := rbmqConsumers[consumerKey]; ok && c == currentConsumer {
				delete(rbmqConsumers, consumerKey)
			}
			rbmqConsumersMux.Unlock()
			currentConsumer.Close()
			// Ensure it's nil for the next iteration
			currentConsumer = nil
		}
	}()

	return nil
}

type mqPublishOptions struct {
	Expiration   time.Duration
	ExchangeType string
}

type MqPublishOption func(o *mqPublishOptions)

func MqPublishWithExpiration(expiration time.Duration) MqPublishOption {
	return func(o *mqPublishOptions) {
		o.Expiration = expiration
	}
}

func MqPublishWithExchangeType(exchangeType string) MqPublishOption {
	return func(o *mqPublishOptions) {
		o.ExchangeType = exchangeType
	}
}

// processMessageAndDetermineAction handles the core message processing and decides the RabbitMQ action.
// This function is called by the handler in consumer.Run.
func processMessageAndDetermineAction(d rabbitmq.Delivery, processorFunc func(data []byte) error, queueName string, exchangeName string) rabbitmq.Action {
	slog.Debug("rbmq: processing message", "queue", queueName, "exchange", exchangeName, "delivery_tag", d.DeliveryTag)
	if err := processorFunc(d.Body); err != nil {
		slog.Error("mq: error processing message", "error", err, "queue", queueName, "exchange", exchangeName, "delivery_tag", d.DeliveryTag)
		// Nack and requeue the message for another attempt
		return rabbitmq.NackRequeue
	}
	slog.Debug("mq: message processed successfully, acking", "queue", queueName, "exchange", exchangeName, "delivery_tag", d.DeliveryTag)
	// Acknowledge the message
	return rabbitmq.Ack
}

func MqPublish(exchangeName string, routingKey string, message any, opts ...MqPublishOption) error {
	options := mqPublishOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	var marshaledData []byte
	var marshalErr error
	switch msgData := message.(type) {
	case string:
		marshaledData = []byte(msgData)
	case []byte:
		marshaledData = msgData
	default:
		marshaledData, marshalErr = MarshalJson(msgData)
		if marshalErr != nil {
			// This is a non-retryable error related to message content.
			return fmt.Errorf("rbmq: failed to marshal message to json: %w", marshalErr)
		}
	}

	// Reject payloads exceeding MaxMqPayloadSize to prevent RabbitMQ connection drops
	// and OOM kills. The default RabbitMQ frame_max is 128MB; we enforce a 10MB limit
	// to keep memory usage and queue sizes reasonable.
	if len(marshaledData) > MaxMqPayloadSize {
		slog.Warn("rbmq: rejecting oversized payload", "exchange", exchangeName, "routing_key", routingKey, "payload_size", len(marshaledData), "max_size", MaxMqPayloadSize)
		return fmt.Errorf("rbmq: payload size %d bytes exceeds maximum allowed %d bytes for exchange=%s routingKey=%s",
			len(marshaledData), MaxMqPayloadSize, exchangeName, routingKey)
	}

	publishOptsList := []func(*rabbitmq.PublishOptions){
		rabbitmq.WithPublishOptionsContentType("application/json"),
		rabbitmq.WithPublishOptionsExchange(exchangeName),
		rabbitmq.WithPublishOptionsHeaders(map[string]any{"x-nb-source": config.Config.OtelServiceName}),
	}
	if options.Expiration > 0 {
		publishOptsList = append(publishOptsList, rabbitmq.WithPublishOptionsExpiration(fmt.Sprintf("%d", options.Expiration.Milliseconds())))
	}

	var lastErr error
	publisherKey := fmt.Sprintf("%s:%s", exchangeName, routingKey)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		var currentPublisher *rabbitmq.Publisher

		rbmqPublishersMux.Lock()
		p, ok := rbmqPublishers[publisherKey]
		if ok && p != nil {
			currentPublisher = p
			rbmqPublishersMux.Unlock()
		} else {
			rbmqPublishersMux.Unlock() // Unlock before potentially long operation (NewPublisher)

			conn := getConnection()
			if conn == nil {
				lastErr = ErrRbmqNoConn
				slog.Error("rbmq: unable to get rabbitmq connection for publisher", "attempt", attempt+1, "key", publisherKey, "error", lastErr)
				if attempt < maxAttempts-1 {
					time.Sleep(reconnectTimeDelay)
					continue
				}
				break // Max attempts reached for getting connection
			}

			publisherOptions := []func(*rabbitmq.PublisherOptions){
				rabbitmq.WithPublisherOptionsLogging,
				rabbitmq.WithPublisherOptionsExchangeName(exchangeName),
				rabbitmq.WithPublisherOptionsExchangeDeclare,
				rabbitmq.WithPublisherOptionsExchangeDurable,
			}
			if options.ExchangeType != "" {
				publisherOptions = append(publisherOptions, rabbitmq.WithPublisherOptionsExchangeKind(options.ExchangeType))
			}

			newP, pubErr := rabbitmq.NewPublisher(
				conn,
				publisherOptions...,
			)
			if pubErr != nil {
				lastErr = fmt.Errorf("rbmq: error creating publisher on attempt %d: %w", attempt+1, pubErr)
				slog.Error("rbmq: error creating publisher", "attempt", attempt+1, "key", publisherKey, "error", pubErr)
				if attempt < maxAttempts-1 {
					time.Sleep(reconnectTimeDelay)
					continue
				}
				break // Max attempts reached for creating publisher
			}

			rbmqPublishersMux.Lock()
			// Check if another goroutine created it while we were unlocked
			if PInMap, PExists := rbmqPublishers[publisherKey]; PExists && PInMap != nil {
				newP.Close() // Close the one we just made, use existing
				currentPublisher = PInMap
			} else {
				rbmqPublishers[publisherKey] = newP
				currentPublisher = newP
				slog.Info("rbmq: new publisher created and cached", "key", publisherKey)
			}
			rbmqPublishersMux.Unlock()
		}

		if currentPublisher == nil {
			lastErr = fmt.Errorf("rbmq: publisher instance is nil before publish on attempt %d for key %s", attempt+1, publisherKey)
			slog.Error("rbmq: publisher is nil before publish", "attempt", attempt+1, "key", publisherKey)
			if attempt < maxAttempts-1 {
				time.Sleep(reconnectTimeDelay)
				continue
			}
			break
		}

		err := currentPublisher.Publish(
			marshaledData,
			[]string{routingKey},
			publishOptsList...,
		)

		if err == nil {
			return nil // Success
		}

		lastErr = err // Store the error from this attempt
		slog.Warn("rbmq: failed to publish message", "attempt", attempt+1, "of", maxAttempts, "key", publisherKey, "error", err)

		if isRabbitMQConnectionError(err) {
			slog.Info("rbmq: connection/channel issue detected with publisher, will attempt to recycle", "key", publisherKey, "error", err)

			rbmqPublishersMux.Lock()
			// Only remove if it's still the same instance in the map
			if p, ok := rbmqPublishers[publisherKey]; ok && p == currentPublisher {
				delete(rbmqPublishers, publisherKey)
				slog.Debug("rbmq: removed faulty publisher from cache", "key", publisherKey)
			}
			rbmqPublishersMux.Unlock()
			currentPublisher.Close() // Close the faulty publisher instance

			if attempt < maxAttempts-1 {
				time.Sleep(reconnectTimeDelay)
				continue // Continue to the next attempt to recreate the publisher
			}
		} else {
			// For non-connection related errors, or if it's the last attempt for a connection error
			slog.Error("rbmq: non-recoverable error during publish or max attempts reached for connection error", "key", publisherKey, "error", lastErr)
			return lastErr // Return the error immediately
		}
	}

	slog.Error("rbmq: failed to publish message after max attempts", "key", publisherKey, "error", lastErr)
	return lastErr
}
