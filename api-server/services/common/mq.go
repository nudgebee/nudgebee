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

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/wagslane/go-rabbitmq"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var (
	rbmqConnMux        sync.Mutex
	rbmqConn           *rabbitmq.Conn
	rbmqConsumers      = make(map[string]*managedConsumer)
	rbmqConsumersMux   sync.Mutex
	rbmqPublishers     = make(map[string]*rabbitmq.Publisher)
	rbmqPublishersMux  sync.Mutex
	maxAttempts        = 3
	reconnectTimeDelay = 5 * time.Second
)

// Unroutable-message detection for MqPublishWithMandatory.
//
// A publish to an exchange with no queue bound to the routing key is discarded
// by the broker and still confirmed, so the publisher cannot tell a delivered
// message from a dropped one. The mandatory flag makes the broker hand the
// message back over basic.return instead; go-rabbitmq surfaces those through a
// per-connection NotifyReturn handler, which is asynchronous, so the returned
// message id is correlated through this registry.
var (
	mqReturnedMux    sync.Mutex
	mqReturnedIDs    = make(map[string]time.Time)
	mqReturnedPruned time.Time
)

const (
	maxConsumerReconnectDelay = 60 * time.Second
	// How long a basic.return stays in the registry before it is pruned. Only
	// needs to outlive the grace window below; anything older is a leak.
	mqReturnRetention = 2 * time.Minute
	// How long to wait after the broker confirms a mandatory publish for a
	// basic.return to arrive. RabbitMQ sends the return before the ack on the
	// same channel, but the library dispatches it to another goroutine, so the
	// ordering guarantee alone is not enough. Costs this much latency on every
	// mandatory publish that is NOT returned — the routable, common case.
	mqReturnGrace = 150 * time.Millisecond
	mqReturnPoll  = 5 * time.Millisecond
	// Prune at most this often. A consumer outage returns every message, and
	// scanning the whole map under the lock on each one would make the publish
	// path quadratic exactly when it is busiest.
	mqReturnPruneInterval = 10 * time.Second
	// Upper bound on waiting for a publisher confirm before treating the
	// publish as failed.
	mqConfirmTimeout = 10 * time.Second
)

// ErrMqUnroutable means the broker accepted the message but had nowhere to route
// it — no queue is bound to the exchange for this routing key, usually because
// the consumer that declares that queue is not running.
var ErrMqUnroutable = fmt.Errorf("rbmq: message unroutable, no queue bound for routing key")

// recordReturnedMessage is called from the NotifyReturn handler for every
// message the broker hands back.
func recordReturnedMessage(messageID string) {
	if messageID == "" {
		return
	}
	mqReturnedMux.Lock()
	defer mqReturnedMux.Unlock()
	now := time.Now()
	if now.Sub(mqReturnedPruned) > mqReturnPruneInterval {
		for id, seen := range mqReturnedIDs {
			if now.Sub(seen) > mqReturnRetention {
				delete(mqReturnedIDs, id)
			}
		}
		mqReturnedPruned = now
	}
	mqReturnedIDs[messageID] = now
}

// takeReturnedMessage reports whether this message was returned as unroutable,
// removing it from the registry so each publish consumes its own return.
func takeReturnedMessage(messageID string) bool {
	mqReturnedMux.Lock()
	defer mqReturnedMux.Unlock()
	if _, ok := mqReturnedIDs[messageID]; !ok {
		return false
	}
	delete(mqReturnedIDs, messageID)
	return true
}

// waitForReturn polls the registry for the grace window, returning as soon as
// the message shows up rather than always sleeping the full window.
func waitForReturn(messageID string) bool {
	deadline := time.Now().Add(mqReturnGrace)
	for {
		if takeReturnedMessage(messageID) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(mqReturnPoll)
	}
}

// MaxMqPayloadSize is the maximum allowed RabbitMQ message payload size (10MB).
// RabbitMQ's default frame_max is 128MB, but large payloads cause memory pressure
// and connection drops. This limit keeps queue sizes and consumer memory reasonable.
const MaxMqPayloadSize = 10 * 1024 * 1024 // 10MB

var ErrRbmqNoConn = fmt.Errorf("rbmq: unable to connect to rabbitmq")

// Broker-restart recovery. go-rabbitmq restarts a consumer in place after it
// reconnects, and when that restart fails it can wait for a "next reconnect"
// that never comes on a healthy connection: Run stays blocked and the queue is
// never consumed again. So consumers are never trusted across a reconnect —
// pollConnection closes every consumer built before the connection went down,
// which makes Run return and the MqConsume loop rebuild it (re-declaring its
// exchange, queue and binding).
const (
	rbmqDialTimeout          = 5 * time.Second
	rbmqConnPollInterval     = time.Second
	rbmqConnDownResetAfter   = 5 * time.Minute
	rbmqConsumerCloseTimeout = 10 * time.Second
)

// Connection state maintained by pollConnection.
var (
	rbmqWatchOnce     sync.Once
	rbmqConnUp        atomic.Bool
	rbmqConnDownSince atomic.Int64 // unix nanos the connection was first seen down; 0 while up
)

// managedConsumer makes closing a consumer idempotent and bounded: go-rabbitmq's
// Close must be called only once and waits for in-flight handlers, while the
// MqConsume loop, pollConnection and MqClose all close consumers.
type managedConsumer struct {
	*rabbitmq.Consumer
	conn      *rabbitmq.Conn // the connection it was built on
	createdAt time.Time
	once      sync.Once
}

func (c *managedConsumer) close() {
	c.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), rbmqConsumerCloseTimeout)
		defer cancel()
		c.CloseWithContext(ctx)
	})
}

// startConnectionWatcher starts the pollConnection loop once per process.
func startConnectionWatcher() {
	rbmqWatchOnce.Do(func() {
		go func() {
			t := time.NewTicker(rbmqConnPollInterval)
			defer t.Stop()
			for now := range t.C {
				pollConnection(now)
			}
		}()
	})
}

// pollConnection records connection up/down transitions. On down->up it
// restarts every consumer built before the outage. A connection that stays
// closed for rbmqConnDownResetAfter is rebuilt: go-rabbitmq stops reconnecting
// for good if the new connection closes before it re-arms its close listener.
func pollConnection(now time.Time) {
	rbmqConnMux.Lock()
	conn := rbmqConn
	rbmqConnMux.Unlock()

	if conn != nil && !conn.IsClosed() {
		if rbmqConnUp.Swap(true) {
			return
		}
		if downSince := rbmqConnDownSince.Swap(0); downSince != 0 {
			slog.Info("rbmq: connection re-established", "down_for", now.Sub(time.Unix(0, downSince)).String())
			outage := time.Unix(0, downSince)
			restartConsumers("connection re-established", func(c *managedConsumer) bool { return c.createdAt.Before(outage) })
		}
		return
	}

	rbmqConnUp.Store(false)
	downSince := rbmqConnDownSince.Load()
	if downSince == 0 {
		rbmqConnDownSince.Store(now.UnixNano())
		return
	}
	if conn != nil && now.Sub(time.Unix(0, downSince)) >= rbmqConnDownResetAfter {
		resetSharedConnection(fmt.Sprintf("connection closed for %s", now.Sub(time.Unix(0, downSince)).Round(time.Second)))
		rbmqConnDownSince.Store(now.UnixNano())
	}
}

// restartConsumers closes every live consumer isStale picks, so its MqConsume
// loop rebuilds it. Closes run in the background: each waits up to
// rbmqConsumerCloseTimeout for in-flight handlers.
func restartConsumers(reason string, isStale func(*managedConsumer) bool) {
	rbmqConsumersMux.Lock()
	var stale []*managedConsumer
	for _, c := range rbmqConsumers {
		if isStale(c) {
			stale = append(stale, c)
		}
	}
	rbmqConsumersMux.Unlock()
	if len(stale) == 0 {
		return
	}
	slog.Warn("rbmq: restarting consumers", "count", len(stale), "reason", reason)
	for _, c := range stale {
		go c.close()
	}
}

// resetSharedConnection drops the shared connection (and the publishers cached
// on it) so the next getConnection dials a new one, then restarts the consumers
// that were built on it.
func resetSharedConnection(reason string) {
	rbmqConnMux.Lock()
	old := rbmqConn
	rbmqConn = nil
	rbmqConnMux.Unlock()
	if old == nil {
		return
	}
	slog.Warn("rbmq: rebuilding shared connection", "reason", reason)
	if err := old.Close(); err != nil {
		slog.Error("rbmq: error closing shared connection during reset", "error", err)
	}

	// Swap the map out under the lock and close outside it: publisher.Close()
	// can block on the network, and holding rbmqPublishersMux would stall publishes.
	rbmqPublishersMux.Lock()
	oldPublishers := rbmqPublishers
	rbmqPublishers = make(map[string]*rabbitmq.Publisher)
	rbmqPublishersMux.Unlock()
	for _, publisher := range oldPublishers {
		publisher.Close()
	}

	// Matched by connection, not by age: a consumer loop that fetched the old
	// connection just before the swap can finish building on it afterwards, and
	// nothing would ever reconnect it.
	restartConsumers(reason, func(c *managedConsumer) bool { return c.conn == old })
}

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
		rabbitmq.WithConnectionOptionsBaseReconnectInterval(reconnectTimeDelay),
		// Bounded so an unreachable broker can't hold rbmqConnMux (or the library's
		// own connection lock during a redial) for amqp's 30s default.
		rabbitmq.WithConnectionOptionsConfig(rabbitmq.Config{Dial: amqp.DefaultDial(rbmqDialTimeout)}),
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
	// In parallel: each close waits up to rbmqConsumerCloseTimeout for in-flight
	// handlers, and shutdown has to fit the pod's termination grace period.
	var wg sync.WaitGroup
	for _, consumer := range rbmqConsumers {
		wg.Add(1)
		go func(c *managedConsumer) {
			defer wg.Done()
			c.close()
		}(consumer)
	}
	wg.Wait()
	rbmqConsumers = make(map[string]*managedConsumer)
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

type mqConsumeOptions struct {
	QueueDurable bool
}

type MqConsumeOption func(o *mqConsumeOptions)

// MqConsumeWithDurableQueue declares the queue durable so its contents survive a
// broker restart. Pair it with MqPublishWithPersistentDelivery — a durable queue
// holding transient messages still loses everything on restart.
//
// Opt-in rather than the default because every queue this helper has created so
// far is non-durable, and RabbitMQ rejects a re-declare that changes durability
// with PRECONDITION_FAILED. Only new queue names can take this.
func MqConsumeWithDurableQueue() MqConsumeOption {
	return func(o *mqConsumeOptions) {
		o.QueueDurable = true
	}
}

// consumerReconnectDelay backs the reconnect loop off from reconnectTimeDelay to
// maxConsumerReconnectDelay. A broker that is down stays down for minutes, and
// eight consumers retrying every 5s for that long is pure log noise.
func consumerReconnectDelay(attempt int) time.Duration {
	delay := reconnectTimeDelay
	for i := 1; i < attempt && delay < maxConsumerReconnectDelay; i++ {
		delay *= 2
	}
	if delay > maxConsumerReconnectDelay {
		return maxConsumerReconnectDelay
	}
	return delay
}

// shouldLogConsumerRetry throttles the reconnect logs: every attempt for the
// first few, then one in ten. An outage still leaves a trail without flooding.
func shouldLogConsumerRetry(attempt int) bool {
	return attempt < 3 || attempt%10 == 0
}

func MqConsume(exchangeName string, routingKey string, queue string, concurrency int, processor func(ctx context.Context, data []byte) error, opts ...MqConsumeOption) error {
	consumeOpts := &mqConsumeOptions{}
	for _, opt := range opts {
		opt(consumeOpts)
	}

	// Single choke point for the API/worker deployment split: on a pod running
	// with SERVER_ROLE=api, no consumer is started, so every queue is drained
	// exclusively by the worker Deployment. Gating here rather than in each of
	// the eight consumer packages means a consumer added later is covered
	// without its author having to know the split exists.
	//
	// The connection is still dialled first, deliberately. getConnection is
	// lazy and MqConsume used to be the only eager caller, so returning before
	// it would leave API pods reporting nb_services_mq_status=0 until their
	// first publish — turning a real dependency alert into noise on two thirds
	// of the fleet. Dialling keeps the gauge meaning the same thing on every
	// pod; only the consumer count differs by role.
	//
	// A failed dial here is NOT fatal: the consumer goroutine below re-dials on
	// every attempt, so a pod that starts while the broker is down (or is being
	// recreated) still declares its queue once the broker is back. Returning
	// early used to leave the consumer permanently unregistered — the queue was
	// never re-declared and every message published to its exchange was dropped
	// by the broker with no error on the publish side.
	if conn := getConnection(); conn == nil {
		slog.Error("rbmq: initial connection to rabbitmq failed for consumer setup, retrying in background",
			"queue", queue, "exchange", exchangeName)
	}
	// Started on every pod: API pods have no consumers to restart, but they do
	// need a connection that has stopped reconnecting to be rebuilt.
	startConnectionWatcher()

	if !config.RunsBackgroundJobs() {
		slog.Info("rbmq: consumer not started on this pod (SERVER_ROLE=api); the worker deployment drains this queue",
			"queue", queue, "exchange", exchangeName)
		return nil
	}

	go func() {
		var currentConsumer *managedConsumer
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
				currentConsumer.close()
			}
			slog.Info("rbmq: consumer goroutine shut down", "queue", queue, "exchange", exchangeName)
		}()

		// Loop indefinitely to manage consumer lifecycle
		for attempt := 0; ; attempt++ {
			if attempt > 0 {
				delay := consumerReconnectDelay(attempt)
				if shouldLogConsumerRetry(attempt) {
					slog.Info("rbmq: delaying consumer reconnect attempt", "queue", queue, "delay", delay, "attempt", attempt+1)
				}
				time.Sleep(delay)
			}

			conn := getConnection()
			if conn == nil {
				if shouldLogConsumerRetry(attempt) {
					slog.Error("rbmq: failed to get rabbitmq connection for consumer", "queue", queue, "attempt", attempt+1)
				}
				continue
			}

			consumerOptions := []func(*rabbitmq.ConsumerOptions){
				rabbitmq.WithConsumerOptionsRoutingKey(routingKey),
				rabbitmq.WithConsumerOptionsExchangeName(exchangeName),
				rabbitmq.WithConsumerOptionsQOSPrefetch(concurrency),
				rabbitmq.WithConsumerOptionsExchangeDeclare,
				rabbitmq.WithConsumerOptionsConcurrency(concurrency),
				rabbitmq.WithConsumerOptionsExchangeDurable,
				rabbitmq.WithConsumerOptionsLogging,
				rabbitmq.WithConsumerOptionsConsumerName(config.Config.OtelServiceName + "/" + routingKey + "/" + config.Config.ServerName),
			}
			if consumeOpts.QueueDurable {
				consumerOptions = append(consumerOptions, rabbitmq.WithConsumerOptionsQueueDurable)
			}

			built, err := rabbitmq.NewConsumer(conn, queue, consumerOptions...)
			if err != nil {
				slog.Error("rbmq: error creating consumer", "error", err, "queue", queue, "attempt", attempt+1)
				continue
			}
			currentConsumer = &managedConsumer{Consumer: built, conn: conn, createdAt: time.Now()}

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
				// Run returns nil only after Close: restartConsumers (a reconnect
				// or connection rebuild) or MqClose.
				slog.Info("rbmq: consumer.Run exited cleanly, will attempt to restart", "queue", queue, "exchange", exchangeName, "attempt", attempt+1)
				// A deliberate restart is not a failure: rebuild after the base delay
				// rather than wherever the backoff had climbed to.
				attempt = 0
			}

			// Clean up current consumer before retrying
			rbmqConsumersMux.Lock()
			if c, ok := rbmqConsumers[consumerKey]; ok && c == currentConsumer {
				delete(rbmqConsumers, consumerKey)
			}
			rbmqConsumersMux.Unlock()
			currentConsumer.close()
			// Ensure it's nil for the next iteration
			currentConsumer = nil
		}
	}()

	return nil
}

type mqPublishOptions struct {
	Expiration         time.Duration
	ExchangeType       string
	BackgroundRetry    bool
	PersistentDelivery bool
	Mandatory          bool
	Ctx                context.Context
}

type MqPublishOption func(o *mqPublishOptions)

// MqPublishWithContext threads a request context into the publish so the active
// W3C trace context (traceparent) is injected into the message headers. The
// consuming service can then extract it and continue the same distributed trace.
func MqPublishWithContext(ctx context.Context) MqPublishOption {
	return func(o *mqPublishOptions) {
		o.Ctx = ctx
	}
}

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

// MqPublishWithPersistentDelivery marks the message persistent so the broker writes
// it to disk. Required for anything published to a durable queue — durability of the
// queue alone does not preserve transient messages across a broker restart.
func MqPublishWithPersistentDelivery() MqPublishOption {
	return func(o *mqPublishOptions) {
		o.PersistentDelivery = true
	}
}

// MqPublishWithMandatory turns a silent drop into an error: the message is
// published in confirm mode with the mandatory flag, and MqPublish returns
// ErrMqUnroutable if the broker hands it back because no queue is bound. Use it
// wherever the caller has a fallback — without it, a publish to an exchange
// whose queue was never declared (consumer down when the broker restarted)
// succeeds and the payload is discarded.
//
// Costs up to mqReturnGrace of extra latency per publish. Not compatible with
// MqPublishWithBackgroundRetry, which by design hides publish failures.
func MqPublishWithMandatory() MqPublishOption {
	return func(o *mqPublishOptions) {
		o.Mandatory = true
	}
}

// processMessageAndDetermineAction handles the core message processing and decides the RabbitMQ action.
// This function is called by the handler in consumer.Run.
func processMessageAndDetermineAction(d rabbitmq.Delivery, processorFunc func(ctx context.Context, data []byte) error, queueName string, exchangeName string) (action rabbitmq.Action) {
	// go-rabbitmq runs handlers on its own per-concurrency goroutines (consume.go
	// spawns options.Concurrency of them), so the recover() around consumer.Run in
	// MqConsume never sees a panic raised here — it would take down the whole
	// process. Ack rather than requeue: a panic is a bug in the processor, and
	// redelivering the same bytes would just panic again in a hot loop.
	defer func() {
		if r := recover(); r != nil {
			slog.Error("mq: PANIC in message processor, dropping message",
				"panic", r, "queue", queueName, "exchange", exchangeName,
				"delivery_tag", d.DeliveryTag, "stack", string(debug.Stack()))
			action = rabbitmq.Ack
		}
	}()

	slog.Debug("rbmq: processing message", "queue", queueName, "exchange", exchangeName, "delivery_tag", d.DeliveryTag)
	// Continue the distributed trace: extract the W3C context the publisher
	// injected into the AMQP headers and run the processor inside a consumer
	// span so its logs / outbound calls share the same trace_id.
	carrier := propagation.MapCarrier{}
	for k, v := range d.Headers {
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
	ctx, span := otel.Tracer("mq").Start(ctx, "rabbitmq.consume", trace.WithSpanKind(trace.SpanKindConsumer))
	defer span.End()
	if err := processorFunc(ctx, d.Body); err != nil {
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
func getOrCreatePublisher(exchangeName, exchangeType, publisherKey string, confirmMode bool) (*rabbitmq.Publisher, error) {
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
	if confirmMode {
		publisherOptions = append(publisherOptions, rabbitmq.WithPublisherOptionsConfirm)
	}

	newP, pubErr := rabbitmq.NewPublisher(conn, publisherOptions...)
	if pubErr != nil {
		return nil, fmt.Errorf("rbmq: error creating publisher: %w", pubErr)
	}
	if confirmMode {
		// Returns are delivered per connection, so the handler records every
		// returned message id and each publish picks its own out of the
		// registry.
		newP.NotifyReturn(func(r rabbitmq.Return) {
			slog.Warn("rbmq: broker returned an unroutable message",
				"exchange", r.Exchange, "routing_key", r.RoutingKey, "reply_text", r.ReplyText, "message_id", r.MessageId)
			recordReturnedMessage(r.MessageId)
		})
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
func publishOnce(ctx context.Context, exchangeName, routingKey, publisherKey, exchangeType string, payload []byte, publishOptsList []func(*rabbitmq.PublishOptions), mandatory bool) (bool, error) {
	pub, err := getOrCreatePublisher(exchangeName, exchangeType, publisherKey, mandatory)
	if err != nil {
		return true, err // no-conn / publisher creation: retry-eligible
	}
	if pub == nil {
		return true, fmt.Errorf("rbmq: publisher instance is nil for key %s", publisherKey)
	}
	if mandatory {
		return publishMandatoryOnce(ctx, pub, routingKey, publisherKey, payload, publishOptsList)
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

// publishMandatoryOnce publishes with the mandatory flag on a confirm-mode
// publisher, waits for the broker's confirm, then checks whether the broker
// returned the message as unroutable. Returns (recyclable, err) like
// publishOnce; ErrMqUnroutable is deliberately NOT recyclable — retrying on a
// fresh publisher cannot conjure a queue, and the caller's fallback should run
// immediately.
func publishMandatoryOnce(ctx context.Context, pub *rabbitmq.Publisher, routingKey, publisherKey string, payload []byte, publishOptsList []func(*rabbitmq.PublishOptions)) (bool, error) {
	messageID := uuid.NewString()
	opts := make([]func(*rabbitmq.PublishOptions), 0, len(publishOptsList)+2)
	opts = append(opts, publishOptsList...)
	opts = append(opts, rabbitmq.WithPublishOptionsMandatory, rabbitmq.WithPublishOptionsMessageID(messageID))

	ctx, cancel := context.WithTimeout(ctx, mqConfirmTimeout)
	defer cancel()

	confirmations, err := pub.PublishWithDeferredConfirmWithContext(ctx, payload, []string{routingKey}, opts...)
	if err != nil {
		if isRabbitMQConnectionError(err) {
			recyclePublisherIfMatches(publisherKey, pub)
			return true, err
		}
		return false, err
	}

	for _, confirmation := range confirmations {
		if confirmation == nil {
			continue
		}
		acked, waitErr := confirmation.WaitContext(ctx)
		if waitErr != nil {
			return true, fmt.Errorf("rbmq: waiting for publisher confirm failed: %w", waitErr)
		}
		if !acked {
			return true, fmt.Errorf("rbmq: broker nacked message for key %s", publisherKey)
		}
	}

	if waitForReturn(messageID) {
		return false, ErrMqUnroutable
	}
	return false, nil
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

	headers := map[string]any{"x-nb-source": config.Config.OtelServiceName}
	// Inject the active W3C trace context into the message headers so the
	// consuming service can continue the same distributed trace.
	if options.Ctx != nil {
		carrier := propagation.MapCarrier{}
		otel.GetTextMapPropagator().Inject(options.Ctx, carrier)
		for k, v := range carrier {
			headers[k] = v
		}
	}

	publishOptsList := []func(*rabbitmq.PublishOptions){
		rabbitmq.WithPublishOptionsContentType("application/json"),
		rabbitmq.WithPublishOptionsExchange(exchangeName),
		rabbitmq.WithPublishOptionsHeaders(headers),
	}
	if options.Expiration > 0 {
		publishOptsList = append(publishOptsList, rabbitmq.WithPublishOptionsExpiration(fmt.Sprintf("%d", options.Expiration.Milliseconds())))
	}

	if options.PersistentDelivery {
		publishOptsList = append(publishOptsList, rabbitmq.WithPublishOptionsPersistentDelivery)
	}

	// The caller's context bounds the confirm wait on a mandatory publish; every
	// other path ignores it, so publishing is never aborted just because the
	// request that triggered it ended.
	publishCtx := options.Ctx
	if publishCtx == nil {
		publishCtx = context.Background()
	}

	publisherKey := fmt.Sprintf("%s:%s", exchangeName, routingKey)
	if options.Mandatory {
		// A confirm-mode publisher behaves differently from a plain one, so it
		// gets its own cache entry rather than being shared with publishers
		// that never asked for confirms.
		publisherKey += ":confirm"
	}

	// Fire-and-forget: one sync attempt, then hand off to background worker on failure.
	// Callers that opt in have no useful way to react to a publish error (event already
	// inserted, webhook already 200'd), so blocking them past one attempt costs latency
	// without buying durability.
	if options.BackgroundRetry {
		_, err := publishOnce(publishCtx, exchangeName, routingKey, publisherKey, options.ExchangeType, marshaledData, publishOptsList, false)
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
		recyclable, err := publishOnce(publishCtx, exchangeName, routingKey, publisherKey, options.ExchangeType, marshaledData, publishOptsList, options.Mandatory)
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

		_, err := publishOnce(context.Background(), item.exchangeName, item.routingKey, item.publisherKey, item.exchangeType, item.payload, item.publishOpts, false)
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
