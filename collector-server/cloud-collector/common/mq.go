package common

import (
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/config"
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

// slogRbmqLogger adapts slog to the go-rabbitmq Logger interface so that
// library-internal log lines are emitted as structured JSON instead of
// plain-text log.Printf output.
type slogRbmqLogger struct{}

func (l slogRbmqLogger) Fatalf(format string, v ...interface{}) {
	slog.Error(fmt.Sprintf(format, v...), "source", "go-rabbitmq")
}

func (l slogRbmqLogger) Errorf(format string, v ...interface{}) {
	slog.Error(fmt.Sprintf(format, v...), "source", "go-rabbitmq")
}

func (l slogRbmqLogger) Warnf(format string, v ...interface{}) {
	slog.Warn(fmt.Sprintf(format, v...), "source", "go-rabbitmq")
}

func (l slogRbmqLogger) Infof(format string, v ...interface{}) {
	slog.Info(fmt.Sprintf(format, v...), "source", "go-rabbitmq")
}

func (l slogRbmqLogger) Debugf(format string, v ...interface{}) {
	slog.Debug(fmt.Sprintf(format, v...), "source", "go-rabbitmq")
}

var rbmqLogger = slogRbmqLogger{}

var ErrRbmqNoConn = fmt.Errorf("rbmq: unable to connect to rabbitmq")

// PermanentError wraps an error to indicate it should not be retried.
// When a processor returns a PermanentError, the message will be NackDiscarded
// instead of NackRequeued.
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string {
	return e.Err.Error()
}

func (e *PermanentError) Unwrap() error {
	return e.Err
}

func NewPermanentError(err error) *PermanentError {
	return &PermanentError{Err: err}
}

const (
	retryCountHeader     = "x-nb-retry-count"
	maxRetryCount        = 3
	crashCountHeader     = "x-nb-crash-count"
	maxCrashRedeliveries = 3
)

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
		rabbitmq.WithConnectionOptionsLogger(rbmqLogger),
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
				rabbitmq.WithConsumerOptionsLogger(rbmqLogger),
				rabbitmq.WithConsumerOptionsConsumerName(config.Config.OtelServiceName+"/"+routingKey+"/"+config.SERVICE_NAME),
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
	Expiration time.Duration
	Headers    map[string]any
}

type MqPublishOption func(o *mqPublishOptions)

func MqPublishWithExpiration(expiration time.Duration) MqPublishOption {
	return func(o *mqPublishOptions) {
		o.Expiration = expiration
	}
}

func MqPublishWithHeaders(headers map[string]any) MqPublishOption {
	return func(o *mqPublishOptions) {
		o.Headers = headers
	}
}

// getRetryCount extracts the retry count from AMQP message headers.
func getRetryCount(headers map[string]any) int64 {
	if headers == nil {
		return 0
	}
	v, ok := headers[retryCountHeader]
	if !ok {
		return 0
	}
	switch count := v.(type) {
	case int64:
		return count
	case int32:
		return int64(count)
	case int:
		return int64(count)
	case float64:
		return int64(count)
	default:
		return 0
	}
}

// getCrashCount extracts the crash redelivery count from AMQP message headers.
func getCrashCount(headers map[string]any) int64 {
	if headers == nil {
		return 0
	}
	v, ok := headers[crashCountHeader]
	if !ok {
		return 0
	}
	switch count := v.(type) {
	case int64:
		return count
	case int32:
		return int64(count)
	case int:
		return int64(count)
	case float64:
		return int64(count)
	default:
		return 0
	}
}

// processMessageAndDetermineAction handles the core message processing and decides the RabbitMQ action.
// This function is called by the handler in consumer.Run.
func processMessageAndDetermineAction(d rabbitmq.Delivery, processorFunc func(data []byte) error, queueName string, exchangeName string) rabbitmq.Action {
	// Poison message detection: when the pod OOM-kills or crashes, RabbitMQ auto-requeues
	// unacked messages with Redelivered=true. Without this check, the same message causes
	// infinite crash loops. We track crash redeliveries and discard after maxCrashRedeliveries.
	if d.Redelivered {
		crashCount := getCrashCount(d.Headers)
		if crashCount >= int64(maxCrashRedeliveries) {
			slog.Error("rbmq: poison message detected - discarding after repeated crash redeliveries",
				"queue", queueName, "exchange", exchangeName, "delivery_tag", d.DeliveryTag,
				"crash_count", crashCount, "body_size", len(d.Body))
			publishToPoisonDLQ(exchangeName, queueName, d.Body, d.Headers, crashCount)
			return rabbitmq.NackDiscard
		}

		slog.Warn("rbmq: redelivered message detected (possible crash recovery), incrementing crash count",
			"queue", queueName, "exchange", exchangeName, "delivery_tag", d.DeliveryTag,
			"crash_count", crashCount+1)

		newHeaders := make(map[string]any)
		for k, v := range d.Headers {
			newHeaders[k] = v
		}
		newHeaders[crashCountHeader] = crashCount + 1

		if err := republishDirect(exchangeName, queueName, d.Body, newHeaders); err != nil {
			slog.Error("rbmq: failed to republish redelivered message, requeueing",
				"error", err, "queue", queueName, "exchange", exchangeName)
			return rabbitmq.NackRequeue
		}
		return rabbitmq.NackDiscard
	}

	retryCount := getRetryCount(d.Headers)
	slog.Debug("rbmq: processing message", "queue", queueName, "exchange", exchangeName, "delivery_tag", d.DeliveryTag, "retry_count", retryCount)

	if err := processorFunc(d.Body); err != nil {
		// Check if this is a permanent error that should not be retried
		var permErr *PermanentError
		if errors.As(err, &permErr) {
			slog.Warn("mq: permanent error processing message, discarding", "error", err, "queue", queueName, "exchange", exchangeName, "delivery_tag", d.DeliveryTag)
			return rabbitmq.NackDiscard
		}

		// Check if we've exceeded max retries
		if retryCount >= int64(maxRetryCount) {
			slog.Error("mq: max retries exceeded, discarding message", "error", err, "queue", queueName, "exchange", exchangeName, "delivery_tag", d.DeliveryTag, "retry_count", retryCount)
			return rabbitmq.NackDiscard
		}

		slog.Error("mq: error processing message, will retry", "error", err, "queue", queueName, "exchange", exchangeName, "delivery_tag", d.DeliveryTag, "retry_count", retryCount)

		// Republish with incremented retry count, then discard the original
		newHeaders := make(map[string]any)
		for k, v := range d.Headers {
			newHeaders[k] = v
		}
		newHeaders[retryCountHeader] = retryCount + 1

		err = republishWithDelay(exchangeName, queueName, d.Body, newHeaders)
		if err != nil {
			slog.Error("mq: failed to republish message for retry, requeueing", "error", err, "queue", queueName, "exchange", exchangeName)
			return rabbitmq.NackRequeue
		}
		return rabbitmq.NackDiscard
	}
	slog.Debug("mq: message processed successfully, acking", "queue", queueName, "exchange", exchangeName, "delivery_tag", d.DeliveryTag)
	// Acknowledge the message
	return rabbitmq.Ack
}

// republishWithDelay republishes a message with updated headers and a per-message TTL
// to create a delayed retry. The message is published with an expiration so RabbitMQ
// holds it before making it available for consumption again.
func republishWithDelay(exchangeName string, routingKey string, body []byte, headers map[string]any) error {
	retryCount := getRetryCount(headers)
	// Exponential backoff: 10s, 20s, 40s
	delaySec := 10 * (1 << (retryCount - 1))
	if delaySec > 60 {
		delaySec = 60
	}

	conn := getConnection()
	if conn == nil {
		return ErrRbmqNoConn
	}

	publisher, err := rabbitmq.NewPublisher(
		conn,
		rabbitmq.WithPublisherOptionsLogger(rbmqLogger),
		rabbitmq.WithPublisherOptionsExchangeName(exchangeName),
		rabbitmq.WithPublisherOptionsExchangeDeclare,
		rabbitmq.WithPublisherOptionsExchangeDurable,
	)
	if err != nil {
		return fmt.Errorf("republish: failed to create publisher: %w", err)
	}
	defer publisher.Close()

	return publisher.Publish(
		body,
		[]string{routingKey},
		rabbitmq.WithPublishOptionsContentType("application/json"),
		rabbitmq.WithPublishOptionsExchange(exchangeName),
		rabbitmq.WithPublishOptionsHeaders(headers),
		rabbitmq.WithPublishOptionsExpiration(fmt.Sprintf("%d", delaySec*1000)),
	)
}

// republishDirect republishes a message with updated headers immediately (no delay).
// Used for crash-redelivered messages to preserve the crash count header.
func republishDirect(exchangeName string, routingKey string, body []byte, headers map[string]any) error {
	conn := getConnection()
	if conn == nil {
		return ErrRbmqNoConn
	}

	publisher, err := rabbitmq.NewPublisher(
		conn,
		rabbitmq.WithPublisherOptionsLogger(rbmqLogger),
		rabbitmq.WithPublisherOptionsExchangeName(exchangeName),
		rabbitmq.WithPublisherOptionsExchangeDeclare,
		rabbitmq.WithPublisherOptionsExchangeDurable,
	)
	if err != nil {
		return fmt.Errorf("republishDirect: failed to create publisher: %w", err)
	}
	defer publisher.Close()

	return publisher.Publish(
		body,
		[]string{routingKey},
		rabbitmq.WithPublishOptionsContentType("application/json"),
		rabbitmq.WithPublishOptionsExchange(exchangeName),
		rabbitmq.WithPublishOptionsHeaders(headers),
	)
}

// publishToPoisonDLQ publishes a poison message to a dead letter queue for inspection.
// Uses convention-based naming: exchange → {exchange}_dlx, queue → {queue}.dlq
// This is best-effort; failures are logged but do not affect message handling.
// The message body is always logged (truncated) so operators can inspect it even if
// the DLQ queue is not set up in RabbitMQ.
func publishToPoisonDLQ(exchangeName string, queueName string, body []byte, originalHeaders map[string]any, crashCount int64) {
	// Always log the poison message body so it can be recovered even without a DLQ
	bodyPreview := string(body)
	if len(bodyPreview) > 1000 {
		bodyPreview = bodyPreview[:1000] + "...(truncated)"
	}
	slog.Error("rbmq: poison message body for recovery",
		"queue", queueName, "exchange", exchangeName, "crash_count", crashCount,
		"body", bodyPreview)

	dlqExchange := exchangeName + "_dlx"
	dlqQueue := queueName + ".dlq"

	dlqHeaders := make(map[string]any)
	for k, v := range originalHeaders {
		dlqHeaders[k] = v
	}
	dlqHeaders["x-nb-poison-reason"] = "max_crash_redeliveries_exceeded"
	dlqHeaders["x-nb-original-queue"] = queueName
	dlqHeaders["x-nb-original-exchange"] = exchangeName
	dlqHeaders[crashCountHeader] = crashCount

	if err := MqPublish(dlqExchange, dlqQueue, body, MqPublishWithHeaders(dlqHeaders)); err != nil {
		slog.Warn("rbmq: failed to publish poison message to DLQ (DLQ may not be set up)",
			"error", err, "queue", queueName, "dlq_exchange", dlqExchange, "dlq_queue", dlqQueue)
	} else {
		slog.Info("rbmq: poison message published to DLQ for inspection",
			"dlq_exchange", dlqExchange, "dlq_queue", dlqQueue, "crash_count", crashCount)
	}
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

	headers := map[string]any{"x-nb-source": config.Config.OtelServiceName}
	for k, v := range options.Headers {
		headers[k] = v
	}

	publishOptsList := []func(*rabbitmq.PublishOptions){
		rabbitmq.WithPublishOptionsContentType("application/json"),
		rabbitmq.WithPublishOptionsExchange(exchangeName),
		rabbitmq.WithPublishOptionsHeaders(headers),
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

			newP, pubErr := rabbitmq.NewPublisher(
				conn,
				rabbitmq.WithPublisherOptionsLogger(rbmqLogger),
				rabbitmq.WithPublisherOptionsExchangeName(exchangeName),
				rabbitmq.WithPublisherOptionsExchangeDeclare,
				rabbitmq.WithPublisherOptionsExchangeDurable,
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
