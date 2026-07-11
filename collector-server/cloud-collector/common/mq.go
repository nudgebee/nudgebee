package common

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"nudgebee/collector/cloud/config"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
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

// Channel-leak recovery. go-rabbitmq v0.15.0's ChannelManager.reconnectLoop has no
// stop signal: once a channel closes with a non-nil error the manager goroutine keeps
// opening channels on the shared connection and cannot be reaped by Consumer.Close().
// Repeatedly recreating a consumer (the MqConsume outer loop) therefore orphans channel
// managers that leak channels until the connection's channel-id space is exhausted
// (504 "channel id space exhausted"), permanently killing every consumer and publisher.
// To bound this, on a connection-level failure we tear down and rebuild the whole shared
// connection — closing it force-closes every channel at once and yields a fresh
// channel-id space.
const (
	rbmqMaxConsecutiveFailures = 3
	rbmqHealthyRunThreshold    = 30 * time.Second
	rbmqResetDebounce          = 30 * time.Second
	rbmqConsumerWedgeTimeout   = 5 * time.Minute
)

// rbmqLastResetNanos is the unix-nano timestamp of the last shared-connection rebuild,
// used to debounce concurrent resets from the handful of consumer goroutines that all
// observe the same failure.
var rbmqLastResetNanos atomic.Int64

// consumerHealth tracks whether a given MqConsume goroutine currently has a live,
// running consumer. MqHealthy (and thus /livez) goes unhealthy when any consumer has
// been failing to (re)establish for longer than rbmqConsumerWedgeTimeout — closing the
// gap where the connection-level heartbeat stays green (its own long-lived channel
// survives) while individual sync consumers are dead.
type consumerHealth struct {
	healthy      bool
	failingSince time.Time
	lastError    string
	// session identifies the current consumer run (its startedAt). markConsumerHealthy
	// only takes effect for the active session, so a healthy-timer left over from an
	// already-exited run cannot resurrect a dead consumer.
	session time.Time
}

var (
	rbmqConsumerHealth    = make(map[string]*consumerHealth)
	rbmqConsumerHealthMux sync.Mutex
)

// MQ heartbeat: a message is round-tripped through the shared RabbitMQ connection
// on a fixed interval. If the round-trip stops (e.g. the go-rabbitmq consumer
// wedges after a broker restart — it can silently stop reconnecting on a nil-error
// channel close), MqHealthy() starts returning false and the Kubernetes liveness
// probe restarts the pod, which rebuilds every consumer/publisher from scratch.
const (
	mqHeartbeatInterval = 30 * time.Second
	mqHeartbeatTimeout  = 120 * time.Second
	mqHeartbeatExchange = "cloud_collector_mq_heartbeat_exchange"
	mqHeartbeatQueue    = "cloud_collector_mq_heartbeat"
	mqHeartbeatKey      = "heartbeat"
)

// mqLastHeartbeatNanos holds the unix-nano timestamp of the last heartbeat that
// completed the publish -> broker -> consume round-trip. Zero means the heartbeat
// has not been started yet (boot grace period).
var mqLastHeartbeatNanos atomic.Int64

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
	return strings.Contains(errStr, "channel id space exhausted") ||
		strings.Contains(errStr, "channel/connection is not open") ||
		strings.Contains(errStr, "connection is not open") ||
		strings.Contains(errStr, "channel is not open") ||
		strings.Contains(errStr, "eof") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection reset by peer") ||
		strings.Contains(errStr, "no such connection") ||
		strings.Contains(errStr, "use of closed network connection") ||
		(strings.HasPrefix(errStr, "amqp:") && (strings.Contains(errStr, "channel error") || strings.Contains(errStr, "connection error") || strings.Contains(errStr, "command invalid")))
}

// isQueueDurabilityMismatch reports whether err is a RabbitMQ PRECONDITION_FAILED
// raised because an existing queue's durable flag differs from what we declared.
func isQueueDurabilityMismatch(err error) bool {
	if err == nil {
		return false
	}
	e := strings.ToLower(err.Error())
	return strings.Contains(e, "precondition_failed") && strings.Contains(e, "durable")
}

// deleteQueueForMigration deletes a queue using a short-lived raw AMQP connection.
// Used only to migrate a legacy non-durable queue so it can be recreated as durable.
// A separate connection is used so the shared connection / other consumers are
// unaffected by the channel-level exception a delete can raise.
func deleteQueueForMigration(queue string) error {
	// Build the URL via net/url so credentials with special characters
	// (@, :, /, ?) are escaped correctly.
	u := &url.URL{
		Scheme: "amqp",
		User:   url.UserPassword(config.Config.RabbitMqUsername, config.Config.RabbitMqPassword),
		Host:   fmt.Sprintf("%s:%d", config.Config.RabbitMqHost, config.Config.RabbitMqPort),
	}
	conn, err := amqp.Dial(u.String())
	if err != nil {
		return fmt.Errorf("migration dial: %w", err)
	}
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("migration channel: %w", err)
	}
	defer func() { _ = ch.Close() }()
	_, err = ch.QueueDelete(queue, false /* ifUnused */, false /* ifEmpty */, false /* noWait */)
	return err
}

// migrateQueueDurabilityIfMismatch deletes a legacy non-durable queue when a durable
// re-declare was rejected with PRECONDITION_FAILED, so the next attempt recreates it as
// durable. The queue declaration happens inside Consumer.Run() (startGoroutines), so
// this error surfaces from Run(), not from NewConsumer — it must be handled in both
// places. Returns true if a mismatch was detected and a migration was attempted.
func migrateQueueDurabilityIfMismatch(queue string, err error) bool {
	if !isQueueDurabilityMismatch(err) {
		return false
	}
	// The queue is non-durable, so it (and any messages in it) would be lost on the next
	// broker restart anyway; deleting it to recreate as durable is safe.
	slog.Warn("rbmq: queue durability mismatch, deleting legacy non-durable queue for migration", "queue", queue, "error", err)
	if delErr := deleteQueueForMigration(queue); delErr != nil {
		// Deletion failed, so the mismatch still exists. Report not-migrated so the caller
		// treats it as a real failure (marks the consumer failing and counts it toward the
		// budget) rather than resetting and retrying forever in a silent green loop.
		slog.Error("rbmq: failed to delete legacy queue during durability migration", "queue", queue, "error", delErr)
		return false
	}
	slog.Info("rbmq: deleted legacy non-durable queue, will recreate as durable", "queue", queue)
	return true
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

// resetSharedConnection tears down the shared RabbitMQ connection (and cached
// publishers) so the next getConnection() builds a brand-new connection with a fresh
// channel-id space. Closing the connection force-closes every channel on it at once,
// reclaiming channels leaked by orphaned go-rabbitmq channel-manager reconnect
// goroutines. Debounced (rbmqResetDebounce) so the handful of consumer goroutines that
// observe the same failure don't thrash the connection.
func resetSharedConnection(reason string) {
	rbmqConnMux.Lock()
	last := rbmqLastResetNanos.Load()
	if last != 0 && time.Since(time.Unix(0, last)) < rbmqResetDebounce {
		rbmqConnMux.Unlock()
		return
	}
	rbmqLastResetNanos.Store(time.Now().UnixNano())
	old := rbmqConn
	rbmqConn = nil
	rbmqConnMux.Unlock()

	if old != nil {
		slog.Warn("rbmq: rebuilding shared connection to reclaim leaked channels", "reason", reason)
		if err := old.Close(); err != nil {
			slog.Error("rbmq: error closing shared connection during reset", "error", err)
		}
	}

	// Cached publishers are bound to the now-closed connection; drop them so the next
	// publish builds a fresh publisher on the rebuilt connection. Swap the map out under
	// the lock and close the old publishers outside it — publisher.Close() can block on
	// network flushes, so holding rbmqPublishersMux across it would stall publishers.
	rbmqPublishersMux.Lock()
	oldPublishers := rbmqPublishers
	rbmqPublishers = make(map[string]*rabbitmq.Publisher)
	rbmqPublishersMux.Unlock()
	for _, publisher := range oldPublishers {
		publisher.Close()
	}
}

// markConsumerRunning records the start of a new consumer run as the active session.
// markConsumerHealthy only takes effect for this session, so a healthy-timer from an
// already-exited run can't resurrect a dead consumer.
func markConsumerRunning(queue string, startedAt time.Time) {
	rbmqConsumerHealthMux.Lock()
	defer rbmqConsumerHealthMux.Unlock()
	h, ok := rbmqConsumerHealth[queue]
	if !ok {
		h = &consumerHealth{}
		rbmqConsumerHealth[queue] = h
	}
	h.session = startedAt
}

// markConsumerHealthy records that a consumer is up and running (survived the healthy
// run threshold). It is a no-op unless startedAt is still the active session — the run
// that armed the timer may already have exited.
func markConsumerHealthy(queue string, startedAt time.Time) {
	rbmqConsumerHealthMux.Lock()
	defer rbmqConsumerHealthMux.Unlock()
	h, ok := rbmqConsumerHealth[queue]
	if !ok || !h.session.Equal(startedAt) {
		return
	}
	h.healthy = true
	h.failingSince = time.Time{}
	h.lastError = ""
}

// markConsumerFailing records that a consumer failed to (re)establish. failingSince is
// stamped on the first failing transition and preserved until the consumer recovers, so
// MqHealthy can measure how long a consumer has been wedged. It also clears the active
// session so an in-flight healthy-timer for the exited run cannot re-mark it healthy.
func markConsumerFailing(queue string, err error) {
	rbmqConsumerHealthMux.Lock()
	defer rbmqConsumerHealthMux.Unlock()
	h, ok := rbmqConsumerHealth[queue]
	if !ok {
		h = &consumerHealth{}
		rbmqConsumerHealth[queue] = h
	}
	if h.healthy || h.failingSince.IsZero() {
		h.failingSince = time.Now()
	}
	h.healthy = false
	h.session = time.Time{}
	if err != nil {
		h.lastError = err.Error()
	}
}

// anyConsumerWedged reports the first consumer that has been failing to (re)establish
// for longer than rbmqConsumerWedgeTimeout, if any.
func anyConsumerWedged() (string, bool) {
	rbmqConsumerHealthMux.Lock()
	defer rbmqConsumerHealthMux.Unlock()
	for queue, h := range rbmqConsumerHealth {
		if !h.healthy && !h.failingSince.IsZero() && time.Since(h.failingSince) >= rbmqConsumerWedgeTimeout {
			return queue, true
		}
	}
	return "", false
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

		// Loop indefinitely to manage consumer lifecycle. consecutiveFailures counts
		// create/run failures that did not stay up past rbmqHealthyRunThreshold; once it
		// crosses the budget we rebuild the shared connection to reclaim leaked channels.
		consecutiveFailures := 0
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
				// Durable queue: survives a broker restart so jobs are not silently
				// dropped while the consumer is briefly disconnected.
				rabbitmq.WithConsumerOptionsQueueDurable,
				rabbitmq.WithConsumerOptionsLogger(rbmqLogger),
				rabbitmq.WithConsumerOptionsConsumerName(config.Config.OtelServiceName+"/"+routingKey+"/"+config.SERVICE_NAME),
			)
			if err != nil {
				// A pre-existing non-durable queue makes the durable re-declare fail with
				// PRECONDITION_FAILED. Migrate it (delete so it recreates durable) and retry
				// without counting it as a failure.
				if migrateQueueDurabilityIfMismatch(queue, err) {
					consecutiveFailures = 0
					continue
				}
				slog.Error("rbmq: error creating consumer", "error", err, "queue", queue, "attempt", attempt+1)
				markConsumerFailing(queue, err)
				consecutiveFailures++
				if isRabbitMQConnectionError(err) || consecutiveFailures >= rbmqMaxConsecutiveFailures {
					resetSharedConnection(fmt.Sprintf("consumer %q create failed: %v", queue, err))
					consecutiveFailures = 0
				}
				continue
			}

			rbmqConsumersMux.Lock()
			rbmqConsumers[consumerKey] = currentConsumer
			rbmqConsumersMux.Unlock()
			slog.Info("rbmq: consumer created and started", "queue", queue, "exchange", exchangeName, "concurrency", concurrency)

			// Mark the consumer healthy once it has stayed up past the threshold. Run()
			// blocks for the consumer's whole lifetime, so this is reported from a timer
			// that is stopped if Run() exits early (a sub-threshold run is not healthy).
			startedAt := time.Now()
			markConsumerRunning(queue, startedAt)
			healthyTimer := time.AfterFunc(rbmqHealthyRunThreshold, func() {
				markConsumerHealthy(queue, startedAt)
			})

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
			healthyTimer.Stop()

			// Handle consumer.Run exit (error or panic)
			if runErr != nil {
				slog.Warn("rbmq: consumer.Run exited", "error", runErr, "queue", queue, "exchange", exchangeName, "attempt", attempt+1)
			} else {
				// consumer.Run exited cleanly, possibly due to connection closure not handled by library's auto-reconnect
				slog.Info("rbmq: consumer.Run exited cleanly, will attempt to restart", "queue", queue, "exchange", exchangeName, "attempt", attempt+1)
			}

			// The durable-queue declaration happens inside Run() (startGoroutines), so a
			// PRECONDITION_FAILED from a pre-existing non-durable queue surfaces here, not
			// from NewConsumer. Migrate it and retry without counting toward the
			// connection-rebuild budget — a rebuild would not fix a durability mismatch and
			// the churn would disrupt the heartbeat that backs /livez.
			migratedDurability := migrateQueueDurabilityIfMismatch(queue, runErr)

			// Always mark the consumer failing when Run() exits — it is no longer running.
			// A healthy reconnect re-marks it healthy after rbmqHealthyRunThreshold; until
			// then a persistent post-exit failure (e.g. getConnection() returning nil) stays
			// visible to the liveness probe. The threshold only governs the failure budget:
			// a consumer that stayed up past it is a transient blip and resets the budget;
			// one that exits almost immediately counts toward the connection rebuild.
			markConsumerFailing(queue, runErr)
			if migratedDurability || time.Since(startedAt) >= rbmqHealthyRunThreshold {
				consecutiveFailures = 0
			} else {
				consecutiveFailures++
			}
			if !migratedDurability && ((runErr != nil && isRabbitMQConnectionError(runErr)) || consecutiveFailures >= rbmqMaxConsecutiveFailures) {
				resetSharedConnection(fmt.Sprintf("consumer %q run exited: %v", queue, runErr))
				consecutiveFailures = 0
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
// newManagedPublisher creates a publisher in confirm mode and registers handlers
// that surface two otherwise-silent failure modes:
//   - NotifyReturn: a message published with the mandatory flag that no queue is
//     bound to is returned by the broker and dropped (this is exactly what happened
//     when the cost-report queue/consumer was dead — publishes reported success
//     while every message was discarded).
//   - NotifyPublish: a publish the broker negatively acknowledges (not persisted).
func newManagedPublisher(conn *rabbitmq.Conn, exchangeName string) (*rabbitmq.Publisher, error) {
	publisher, err := rabbitmq.NewPublisher(
		conn,
		rabbitmq.WithPublisherOptionsLogger(rbmqLogger),
		rabbitmq.WithPublisherOptionsExchangeName(exchangeName),
		rabbitmq.WithPublisherOptionsExchangeDeclare,
		rabbitmq.WithPublisherOptionsExchangeDurable,
		rabbitmq.WithPublisherOptionsConfirm,
	)
	if err != nil {
		return nil, err
	}
	publisher.NotifyReturn(func(r rabbitmq.Return) {
		slog.Error("rbmq: message returned as unroutable and dropped (no bound queue?)",
			"exchange", r.Exchange, "routing_key", r.RoutingKey,
			"reply_code", r.ReplyCode, "reply_text", r.ReplyText)
	})
	publisher.NotifyPublish(func(c rabbitmq.Confirmation) {
		if !c.Ack {
			slog.Error("rbmq: publish was nacked by broker (not persisted)",
				"exchange", exchangeName, "delivery_tag", c.DeliveryTag)
		}
	})
	return publisher, nil
}

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

	publisher, err := newManagedPublisher(conn, exchangeName)
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
		rabbitmq.WithPublishOptionsPersistentDelivery,
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

	publisher, err := newManagedPublisher(conn, exchangeName)
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
		rabbitmq.WithPublishOptionsPersistentDelivery,
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

// MqDeclareDLQ declares a durable dead-letter exchange and queue and binds them
// together (routing key == queue name). DLQ targets are published to via MqPublish
// (see sendToDLQWithConfig / publishToPoisonDLQ), which declares only the exchange.
// RabbitMQ silently discards messages published to an exchange with no bound queue,
// so without this declaration DLQ messages are lost. Call once at consumer startup.
// The declaration is idempotent: re-declaring with the same parameters is a no-op.
func MqDeclareDLQ(exchangeName string, queueName string) error {
	if exchangeName == "" || queueName == "" {
		slog.Warn("rbmq: DLQ not configured, skipping DLQ declaration", "dlq_exchange", exchangeName, "dlq_queue", queueName)
		return nil
	}

	url := fmt.Sprintf("amqp://%s:%s@%s:%d", config.Config.RabbitMqUsername, config.Config.RabbitMqPassword, config.Config.RabbitMqHost, config.Config.RabbitMqPort)
	// Use a bounded dial timeout so a slow/unreachable broker can't hang consumer startup.
	conn, err := amqp.DialConfig(url, amqp.Config{Dial: amqp.DefaultDial(10 * time.Second)})
	if err != nil {
		return fmt.Errorf("rbmq: dlq dial failed: %w", err)
	}
	defer func() { _ = conn.Close() }()

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("rbmq: dlq channel open failed: %w", err)
	}
	defer func() { _ = ch.Close() }()

	// Durable direct exchange to match how MqPublish declares exchanges.
	if err := ch.ExchangeDeclare(exchangeName, amqp.ExchangeDirect, true, false, false, false, nil); err != nil {
		return fmt.Errorf("rbmq: dlq exchange declare failed: %w", err)
	}
	if _, err := ch.QueueDeclare(queueName, true, false, false, false, nil); err != nil {
		return fmt.Errorf("rbmq: dlq queue declare failed: %w", err)
	}
	// Bind with routing key == queue name, matching the routing key used by
	// sendToDLQWithConfig / publishToPoisonDLQ when publishing to the DLQ.
	if err := ch.QueueBind(queueName, queueName, exchangeName, false, nil); err != nil {
		return fmt.Errorf("rbmq: dlq queue bind failed: %w", err)
	}

	slog.Info("rbmq: dead-letter queue declared and bound", "dlq_exchange", exchangeName, "dlq_queue", queueName)
	return nil
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
		// Persist messages to disk so they survive a broker restart, and mark
		// mandatory so an unroutable publish is returned (and logged) instead of
		// being silently discarded by the broker.
		rabbitmq.WithPublishOptionsPersistentDelivery,
		rabbitmq.WithPublishOptionsMandatory,
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

			newP, pubErr := newManagedPublisher(conn, exchangeName)
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

// MqHealthy reports whether the MQ heartbeat has completed a publish -> broker ->
// consume round-trip recently. It returns true during the boot grace period
// (before the first heartbeat) so a slow startup does not trip the liveness probe.
// Once heartbeats have started, a stale heartbeat (older than mqHeartbeatTimeout)
// means a consumer or the connection has wedged and the pod should be restarted.
func MqHealthy() bool {
	// Connection-level check: the heartbeat round-trip must be recent once it has
	// started. last == 0 is the boot grace period (heartbeat not started yet).
	last := mqLastHeartbeatNanos.Load()
	if last != 0 && time.Since(time.Unix(0, last)) >= mqHeartbeatTimeout {
		return false
	}
	// Consumer-level check: the heartbeat rides the shared connection's own long-lived
	// channel, which can survive while individual sync consumers are wedged. Report
	// unhealthy if any consumer has been unable to (re)establish past the wedge timeout
	// so the liveness probe restarts the pod.
	if queue, wedged := anyConsumerWedged(); wedged {
		slog.Warn("rbmq: consumer wedged beyond timeout, reporting unhealthy for liveness probe", "queue", queue)
		return false
	}
	return true
}

// StartMqHeartbeat starts the heartbeat consumer and publisher. It is safe to call
// once at startup. The consumer records the time of each received heartbeat; the
// publisher emits one on mqHeartbeatInterval. If the round-trip stops, MqHealthy()
// goes false and the Kubernetes liveness probe restarts the pod.
func StartMqHeartbeat() {
	// Seed with the current time so the boot grace period is exactly one timeout
	// window, after which a missing round-trip is treated as unhealthy.
	mqLastHeartbeatNanos.Store(time.Now().UnixNano())

	err := MqConsume(mqHeartbeatExchange, mqHeartbeatKey, mqHeartbeatQueue, 1, func(_ []byte) error {
		mqLastHeartbeatNanos.Store(time.Now().UnixNano())
		return nil
	})
	if err != nil {
		slog.Error("rbmq: failed to start heartbeat consumer", "error", err)
	}

	go func() {
		ticker := time.NewTicker(mqHeartbeatInterval)
		defer ticker.Stop()
		for range ticker.C {
			// Expire heartbeats so they never accumulate if the consumer is wedged.
			if pubErr := MqPublish(mqHeartbeatExchange, mqHeartbeatKey, "ping", MqPublishWithExpiration(mqHeartbeatTimeout)); pubErr != nil {
				slog.Warn("rbmq: heartbeat publish failed", "error", pubErr)
			}
		}
	}()

	slog.Info("rbmq: MQ heartbeat started", "interval", mqHeartbeatInterval.String(), "timeout", mqHeartbeatTimeout.String())
}
