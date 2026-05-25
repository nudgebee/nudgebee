package common

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"nudgebee/services/config"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
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
	Expiration      time.Duration
	ExchangeType    string
	BackgroundRetry bool
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

// MqPublishWithBackgroundRetry switches MqPublish to fire-and-forget semantics:
// one synchronous attempt, then on failure hand the payload to a background
// worker that retries with exponential backoff. The caller always sees nil.
// Permanent failures (retries exhausted or buffer full) are logged at ERROR
// and counted via mqBackgroundRetryDropped — they are NOT silently dropped.
// Intended for inserts (e.g. events) where the caller can't usefully react.
func MqPublishWithBackgroundRetry() MqPublishOption {
	return func(o *mqPublishOptions) {
		o.BackgroundRetry = true
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

// getOrCreatePublisher returns a cached publisher for (exchangeName, routingKey)
// or creates one. Returns ErrRbmqNoConn if the broker is unreachable.
func getOrCreatePublisher(exchangeName, exchangeType, publisherKey string) (*rabbitmq.Publisher, error) {
	rbmqPublishersMux.Lock()
	if p, ok := rbmqPublishers[publisherKey]; ok && p != nil {
		rbmqPublishersMux.Unlock()
		return p, nil
	}
	rbmqPublishersMux.Unlock() // unlock before potentially-long NewPublisher

	conn := getConnection()
	if conn == nil {
		return nil, ErrRbmqNoConn
	}

	publisherOptions := []func(*rabbitmq.PublisherOptions){
		rabbitmq.WithPublisherOptionsLogging,
		rabbitmq.WithPublisherOptionsExchangeName(exchangeName),
		rabbitmq.WithPublisherOptionsExchangeDeclare,
		rabbitmq.WithPublisherOptionsExchangeDurable,
	}
	if exchangeType != "" {
		publisherOptions = append(publisherOptions, rabbitmq.WithPublisherOptionsExchangeKind(exchangeType))
	}

	newP, pubErr := rabbitmq.NewPublisher(conn, publisherOptions...)
	if pubErr != nil {
		return nil, fmt.Errorf("rbmq: error creating publisher: %w", pubErr)
	}

	rbmqPublishersMux.Lock()
	defer rbmqPublishersMux.Unlock()
	// Another goroutine may have created it while we were unlocked.
	if existing, ok := rbmqPublishers[publisherKey]; ok && existing != nil {
		newP.Close()
		return existing, nil
	}
	rbmqPublishers[publisherKey] = newP
	slog.Info("rbmq: new publisher created and cached", "key", publisherKey)
	return newP, nil
}

// recyclePublisherIfMatches removes the publisher from the cache and closes it
// only if the cached entry is still the same instance we used. Idempotent under
// concurrent recycling.
func recyclePublisherIfMatches(publisherKey string, p *rabbitmq.Publisher) {
	rbmqPublishersMux.Lock()
	if cached, ok := rbmqPublishers[publisherKey]; ok && cached == p {
		delete(rbmqPublishers, publisherKey)
		slog.Debug("rbmq: removed faulty publisher from cache", "key", publisherKey)
	}
	rbmqPublishersMux.Unlock()
	p.Close()
}

// publishOnce does a single publish attempt: acquire-or-create publisher, send,
// recycle the publisher on connection errors. The caller decides whether to retry.
// Returns (recyclable, err) where recyclable is true for transient broker/channel
// errors that may succeed on a fresh publisher; false for terminal/non-conn errors.
func publishOnce(exchangeName, routingKey, publisherKey, exchangeType string, payload []byte, publishOptsList []func(*rabbitmq.PublishOptions)) (bool, error) {
	pub, err := getOrCreatePublisher(exchangeName, exchangeType, publisherKey)
	if err != nil {
		return true, err // no-conn / publisher creation: retry-eligible
	}
	if pub == nil {
		return true, fmt.Errorf("rbmq: publisher instance is nil for key %s", publisherKey)
	}
	err = pub.Publish(payload, []string{routingKey}, publishOptsList...)
	if err == nil {
		return false, nil
	}
	if isRabbitMQConnectionError(err) {
		recyclePublisherIfMatches(publisherKey, pub)
		return true, err
	}
	return false, err
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

	publisherKey := fmt.Sprintf("%s:%s", exchangeName, routingKey)

	// Fire-and-forget: one sync attempt, then hand off to background worker on failure.
	// Callers that opt in have no useful way to react to a publish error (event already
	// inserted, webhook already 200'd), so blocking them past one attempt costs latency
	// without buying durability.
	if options.BackgroundRetry {
		_, err := publishOnce(exchangeName, routingKey, publisherKey, options.ExchangeType, marshaledData, publishOptsList)
		if err == nil {
			return nil
		}
		slog.Warn("rbmq: sync attempt failed, handing off to background retry", "key", publisherKey, "error", err)
		enqueueBackgroundRetry(backgroundRetryItem{
			exchangeName: exchangeName,
			routingKey:   routingKey,
			publisherKey: publisherKey,
			exchangeType: options.ExchangeType,
			payload:      marshaledData,
			publishOpts:  publishOptsList,
			enqueuedAt:   time.Now(),
		})
		return nil
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		recyclable, err := publishOnce(exchangeName, routingKey, publisherKey, options.ExchangeType, marshaledData, publishOptsList)
		if err == nil {
			return nil // Success
		}
		lastErr = err
		slog.Warn("rbmq: failed to publish message", "attempt", attempt+1, "of", maxAttempts, "key", publisherKey, "error", err)

		if !recyclable {
			slog.Error("rbmq: non-recoverable error during publish", "key", publisherKey, "error", lastErr)
			return lastErr
		}
		if attempt < maxAttempts-1 {
			time.Sleep(reconnectTimeDelay)
		}
	}

	slog.Error("rbmq: failed to publish message after max attempts", "key", publisherKey, "error", lastErr)
	return lastErr
}

// Background-retry plumbing — for MqPublishWithBackgroundRetry.
//
// Bounded channel + bounded total retry budget. Permanent give-ups (channel
// full, retries exhausted, message aged out) are logged at ERROR and counted
// via mqBackgroundRetryDropped so a Rabbit-out hour can't lose 1000s of events
// without a signal.
const (
	backgroundRetryChannelSize = 1000
	backgroundRetryMaxAttempts = 10
	backgroundRetryMinDelay    = 5 * time.Second
	backgroundRetryMaxDelay    = 60 * time.Second
	backgroundRetryMaxAge      = 10 * time.Minute
)

type backgroundRetryItem struct {
	exchangeName string
	routingKey   string
	publisherKey string
	exchangeType string
	payload      []byte
	publishOpts  []func(*rabbitmq.PublishOptions)
	enqueuedAt   time.Time
	attempt      int
}

var (
	backgroundRetryCh         chan backgroundRetryItem
	backgroundRetryOnce       sync.Once
	mqBackgroundRetryDropped  atomic.Int64
	mqBackgroundRetrySucceed  atomic.Int64
	backgroundRetryRandSource = rand.New(rand.NewSource(time.Now().UnixNano()))
	backgroundRetryRandMux    sync.Mutex
)

// MqBackgroundRetryDroppedTotal returns the count of background-retry items
// that were dropped (channel full, retries exhausted, or aged out). Exposed
// for instrumentation by callers that want to alert on it.
func MqBackgroundRetryDroppedTotal() int64 { return mqBackgroundRetryDropped.Load() }

// MqBackgroundRetrySucceededTotal returns the count of items recovered via
// the background-retry path.
func MqBackgroundRetrySucceededTotal() int64 { return mqBackgroundRetrySucceed.Load() }

func ensureBackgroundRetryWorker() {
	backgroundRetryOnce.Do(func() {
		backgroundRetryCh = make(chan backgroundRetryItem, backgroundRetryChannelSize)
		go backgroundRetryWorker()
		slog.Info("rbmq: background-retry worker started", "channel_size", backgroundRetryChannelSize, "max_attempts", backgroundRetryMaxAttempts)
	})
}

func enqueueBackgroundRetry(item backgroundRetryItem) {
	ensureBackgroundRetryWorker()
	select {
	case backgroundRetryCh <- item:
	default:
		mqBackgroundRetryDropped.Add(1)
		slog.Error("rbmq: background-retry buffer full, dropping message",
			"key", item.publisherKey,
			"buffer_size", backgroundRetryChannelSize,
			"dropped_total", mqBackgroundRetryDropped.Load())
	}
}

// jitteredDelay returns the backoff for `attempt` (0-indexed) capped at
// backgroundRetryMaxDelay, with +/-25% uniform jitter.
func jitteredDelay(attempt int) time.Duration {
	base := backgroundRetryMinDelay << attempt
	if base > backgroundRetryMaxDelay || base < 0 {
		base = backgroundRetryMaxDelay
	}
	backgroundRetryRandMux.Lock()
	frac := 0.75 + 0.5*backgroundRetryRandSource.Float64()
	backgroundRetryRandMux.Unlock()
	return time.Duration(float64(base) * frac)
}

func backgroundRetryWorker() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("rbmq: background-retry worker panicked, restarting", "panic", r, "stack", string(debug.Stack()))
			// Self-heal: relaunch the worker so a single panic doesn't kill durability for the life of the pod.
			go backgroundRetryWorker()
		}
	}()
	for item := range backgroundRetryCh {
		go retryBackgroundItem(item)
	}
}

func retryBackgroundItem(item backgroundRetryItem) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("rbmq: background-retry item panicked", "key", item.publisherKey, "panic", r, "stack", string(debug.Stack()))
			mqBackgroundRetryDropped.Add(1)
		}
	}()
	for item.attempt < backgroundRetryMaxAttempts {
		time.Sleep(jitteredDelay(item.attempt))

		if time.Since(item.enqueuedAt) > backgroundRetryMaxAge {
			mqBackgroundRetryDropped.Add(1)
			slog.Error("rbmq: background-retry aged out, dropping",
				"key", item.publisherKey,
				"age", time.Since(item.enqueuedAt),
				"attempts_made", item.attempt,
				"dropped_total", mqBackgroundRetryDropped.Load())
			return
		}

		_, err := publishOnce(item.exchangeName, item.routingKey, item.publisherKey, item.exchangeType, item.payload, item.publishOpts)
		item.attempt++
		if err == nil {
			mqBackgroundRetrySucceed.Add(1)
			slog.Info("rbmq: background-retry succeeded",
				"key", item.publisherKey,
				"attempts_made", item.attempt,
				"age", time.Since(item.enqueuedAt))
			return
		}
		slog.Warn("rbmq: background-retry attempt failed", "key", item.publisherKey, "attempt", item.attempt, "of", backgroundRetryMaxAttempts, "error", err)
	}
	mqBackgroundRetryDropped.Add(1)
	slog.Error("rbmq: background-retry exhausted, dropping message",
		"key", item.publisherKey,
		"attempts_made", item.attempt,
		"age", time.Since(item.enqueuedAt),
		"dropped_total", mqBackgroundRetryDropped.Load())
}
