package common

// RabbitMQ lifecycle contract (enforced by mq_lifecycle_test.go), ported from llm-server (#39004):
//  1. One supervisor per queue: runs a consumer or retries w/ backoff until MqClose, so a broker
//     that is down at boot is picked up once it answers.
//  2. Failed dials are never cached (retry after mqDialCooldown); dials are bounded by mqDialTimeout and
//     never hold a lock, so callers & MqClose don't wait on the network.
//  3. A publish error replaces only that publisher; it never closes or waits on consumers.
//  4. Only MqClose stops a supervisor (cancel -> close, bounded ~2×mqShutdownTimeout); any other stop -> re-arm.
//  5. ≤1 live library consumer per supervisor; the old one is closed before the next is built.
//  6. After a broker reconnect every consumer built before the outage is closed and rebuilt by its
//     supervisor, re-declaring its exchange, queue and binding: go-rabbitmq restarts it in place, and
//     if that fails Run never returns. A connection that stays closed for mqConnResetAfter is closed
//     and re-dialed.
//
// Delivery (unchanged): success -> Ack, error -> NackRequeue. Queues are non-durable & messages transient
// -> lost on broker restart; channel loss before ack -> redelivery -> possible duplicates.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"nudgebee/runbook/config"
	"sort"
	"strings"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/wagslane/go-rabbitmq"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// mqConsumer and mqPublisher are the parts of the go-rabbitmq handles this file
// uses; they exist so the lifecycle can be tested without a broker.
type mqConsumer interface {
	Run(handler rabbitmq.Handler) error
	CloseWithContext(ctx context.Context)
}

type mqPublisher interface {
	Publish(data []byte, routingKeys []string, optionFuncs ...func(*rabbitmq.PublishOptions)) error
	Close()
}

var (
	// rbmqConnMu guards the fields below; never held across network I/O.
	rbmqConnMu       sync.Mutex
	rbmqConn         *rabbitmq.Conn
	rbmqLastDialFail time.Time
	rbmqDialDone     chan struct{} // non-nil while a dial is in flight
	rbmqConnGen      uint64        // bumped by MqClose -> a dial finishing later is discarded
	rbmqConnDownAt   time.Time     // set by watchConn while the shared conn is closed (library redialing)

	// rbmqMux guards rbmqPublishers and rbmqSupervisors. Both are mutated from
	// multiple goroutines — the consumer supervisors and concurrent MqPublish
	// callers — so unsynchronized map access can trip Go's "concurrent map
	// writes" fatal panic. All access must hold rbmqMux.
	rbmqMux         sync.Mutex
	rbmqPublishers  = make(map[string]mqPublisher)
	rbmqSupervisors = make(map[string]*mqConsumerSupervisor)

	maxAttempts        = 3
	reconnectTimeDelay = 5 * time.Second
	mqDialCooldown     = time.Second
	mqDialTimeout      = 5 * time.Second // TCP connect + AMQP handshake, per dial and per library redial
	mqRearmMaxDelay    = time.Minute
	mqRearmResetAfter  = time.Minute
	mqShutdownTimeout  = 10 * time.Second
	// go-rabbitmq stops redialing for good if a new connection closes before it re-arms its
	// close listener, so a connection closed this long is dropped and re-dialed.
	mqConnResetAfter = 5 * time.Minute

	mqDial = func(url string) (*rabbitmq.Conn, error) {
		return rabbitmq.NewConn(url,
			rabbitmq.WithConnectionOptionsLogging,
			rabbitmq.WithConnectionOptionsBaseReconnectInterval(reconnectTimeDelay),
			rabbitmq.WithConnectionOptionsConfig(rabbitmq.Config{Dial: amqp.DefaultDial(mqDialTimeout)}),
		)
	}
	mqCloseConn = func(c *rabbitmq.Conn) error { return c.Close() }
	// A consumer inside go-rabbitmq's own reconnect never returns to its supervisor, so conn state
	// is the only signal of a mid-life broker outage.
	mqConnIsClosed     = func(c *rabbitmq.Conn) bool { return c.IsClosed() }
	mqConnPollInterval = time.Second
	mqNewConsumer      = func(conn *rabbitmq.Conn, queue string, opts ...func(*rabbitmq.ConsumerOptions)) (mqConsumer, error) {
		return rabbitmq.NewConsumer(conn, queue, opts...)
	}
	mqNewPublisher = func(conn *rabbitmq.Conn, opts ...func(*rabbitmq.PublisherOptions)) (mqPublisher, error) {
		return rabbitmq.NewPublisher(conn, opts...)
	}
)

var (
	ErrRbmqNoConn                  = fmt.Errorf("rbmq: unable to connect to rabbitmq")
	ErrMqConsumerAlreadyRegistered = errors.New("rbmq: consumer already registered for queue")
	errMqConsumerStoppedUnexpected = errors.New("rbmq: consumer stopped without shutdown")
	errMqConsumerRestarted         = errors.New("rbmq: consumer restarted after the broker connection came back")
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

// getConnection returns the shared conn, or nil without waiting if none is available: a dial is
// in flight, the last one failed < mqDialCooldown ago, or this call's own dial fails.
func getConnection() *rabbitmq.Conn { return connect(false) }

// connect dials if needed. At most one dial runs at a time; waitForDial callers (supervisors)
// wait for an in-flight dial, bounded by mqDialTimeout. Failures aren't cached past the cooldown.
// Once dialed, go-rabbitmq reconnects the conn itself, so only MqClose (or watchConn) drops it.
func connect(waitForDial bool) *rabbitmq.Conn {
	rbmqConnMu.Lock()
	if rbmqConn != nil {
		defer rbmqConnMu.Unlock()
		return rbmqConn
	}
	if done := rbmqDialDone; done != nil {
		rbmqConnMu.Unlock()
		if !waitForDial {
			return nil
		}
		<-done
		rbmqConnMu.Lock()
		defer rbmqConnMu.Unlock()
		return rbmqConn
	}
	if !rbmqLastDialFail.IsZero() && time.Since(rbmqLastDialFail) < mqDialCooldown {
		rbmqConnMu.Unlock()
		return nil
	}
	done := make(chan struct{})
	rbmqDialDone = done
	gen := rbmqConnGen
	rbmqConnMu.Unlock()

	slog.Default().Info("rbmq: dialing rabbitmq",
		"host", config.Config.RabbitMqHost, "port", config.Config.RabbitMqPort)
	conn, err := mqDial(fmt.Sprintf("amqp://%s:%s@%s:%d", config.Config.RabbitMqUsername, config.Config.RabbitMqPassword, config.Config.RabbitMqHost, config.Config.RabbitMqPort))

	rbmqConnMu.Lock()
	rbmqDialDone = nil
	close(done)
	switch {
	case gen != rbmqConnGen: // MqClose ran during the dial
		rbmqConnMu.Unlock()
		if conn != nil {
			_ = mqCloseConn(conn)
		}
		return nil
	case err != nil:
		rbmqLastDialFail = time.Now()
		rbmqConnMu.Unlock()
		slog.Default().Error("rbmq: error connecting to rabbitmq",
			"host", config.Config.RabbitMqHost, "port", config.Config.RabbitMqPort, "error", err)
		return nil
	}
	rbmqLastDialFail = time.Time{}
	rbmqConn = conn
	rbmqConnDownAt = time.Time{}
	rbmqConnMu.Unlock()
	go watchConn(conn, gen, mqConnIsClosed, mqConnPollInterval, mqConnResetAfter)
	slog.Default().Info("rbmq: connected to rabbitmq",
		"host", config.Config.RabbitMqHost, "port", config.Config.RabbitMqPort)
	return conn
}

// watchConn records when the shared conn goes down & clears it on recovery, until MqClose
// replaces the generation. isClosed/interval are captured so it never reads globals late.
// On recovery it restarts every consumer built before the outage (invariant 6); a conn closed
// for resetAfter is dropped, so supervisors dial a new one.
func watchConn(conn *rabbitmq.Conn, gen uint64, isClosed func(*rabbitmq.Conn) bool, interval, resetAfter time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		closed := isClosed(conn) // may block while the library redials (≤ mqDialTimeout)
		var recoveredFrom time.Time
		reset := false
		rbmqConnMu.Lock()
		if gen != rbmqConnGen {
			rbmqConnMu.Unlock()
			return
		}
		switch {
		case !closed:
			recoveredFrom = rbmqConnDownAt
			rbmqConnDownAt = time.Time{}
		case rbmqConnDownAt.IsZero():
			rbmqConnDownAt = time.Now()
		case time.Since(rbmqConnDownAt) >= resetAfter:
			slog.Warn("rbmq: connection closed too long; dropping it and re-dialing", "down_for", time.Since(rbmqConnDownAt).Round(time.Second).String())
			rbmqConn = nil
			rbmqConnGen++ // stops this watcher
			rbmqConnDownAt = time.Time{}
			reset = true
		}
		rbmqConnMu.Unlock()

		switch {
		case !recoveredFrom.IsZero():
			slog.Info("rbmq: connection re-established; restarting consumers", "down_for", time.Since(recoveredFrom).Round(time.Second).String())
			restartConsumersBuiltBefore(recoveredFrom)
		case reset:
			closeConn := mqCloseConn
			go func() { _ = closeConn(conn) }() // stops the old conn's own redial loop
			restartConsumersBuiltBefore(time.Now())
			return
		}
	}
}

// restartConsumersBuiltBefore closes every running consumer built before cutoff; its
// supervisor then rebuilds it, re-declaring its exchange, queue and binding.
func restartConsumersBuiltBefore(cutoff time.Time) {
	rbmqMux.Lock()
	supervisors := make([]*mqConsumerSupervisor, 0, len(rbmqSupervisors))
	for _, s := range rbmqSupervisors {
		supervisors = append(supervisors, s)
	}
	rbmqMux.Unlock()
	for _, s := range supervisors {
		go s.restartIfBuiltBefore(cutoff)
	}
}

// MqClose is process shutdown, never error recovery: stops supervisors (no re-arm), closes publishers & conn.
// Bounded ~2×mqShutdownTimeout even with a stuck handler; later MqConsume/MqPublish start clean.
func MqClose() {
	rbmqMux.Lock()
	supervisors := rbmqSupervisors
	publishers := rbmqPublishers
	rbmqSupervisors = make(map[string]*mqConsumerSupervisor)
	rbmqPublishers = make(map[string]mqPublisher)
	rbmqMux.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), mqShutdownTimeout)
	defer cancel()

	// Cancel all first so no supervisor re-arms while its siblings close.
	for _, s := range supervisors {
		s.cancel()
	}
	// Close in parallel so one slow handler can't spend its siblings' grace period.
	waitBounded(ctx, "close consumers", func() {
		var wg sync.WaitGroup
		for _, s := range supervisors {
			wg.Go(func() { s.closeCurrent(ctx) })
		}
		wg.Wait()
	})
	// A stuck handler can use up ctx during close; Run still returns once the
	// close finishes, so give supervisors their own window to exit.
	exitCtx, exitCancel := context.WithTimeout(context.Background(), mqShutdownTimeout)
	defer exitCancel()
	for _, s := range supervisors {
		select {
		case <-s.done:
		case <-exitCtx.Done():
			slog.Warn("rbmq: consumer supervisor did not stop before shutdown timeout", "queue", s.queue)
		}
	}

	rbmqConnMu.Lock()
	conn := rbmqConn
	rbmqConn = nil
	rbmqLastDialFail = time.Time{}
	rbmqConnGen++ // never waits on an in-flight dial; its result is discarded; stops watchConn
	rbmqConnDownAt = time.Time{}
	rbmqConnMu.Unlock()
	closeConn := mqCloseConn // captured: an abandoned step must not read globals late
	waitBounded(exitCtx, "close publishers and connection", func() {
		for _, publisher := range publishers {
			publisher.Close()
		}
		if conn != nil {
			if err := closeConn(conn); err != nil {
				slog.Warn("rbmq: error closing connection", "error", err)
			}
		}
	})
}

// waitBounded runs f and returns when it finishes or ctx ends. AMQP channel/connection closes wait on
// the broker with no timeout; at shutdown an unfinished f is left to the process exit.
func waitBounded(ctx context.Context, step string, f func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-ctx.Done():
		slog.Warn("rbmq: shutdown step timed out; continuing", "step", step)
	}
}

// MqConsume registers a supervised consumer for queue and returns at once; connecting, and
// rebuilding after any broker restart, happens in the background until MqClose. A broker that
// is down at boot is therefore picked up once it answers. Errors only if queue already has a
// consumer.
func MqConsume(exchangeName string, routingKey string, queue string, processor func(ctx context.Context, data []byte) error) error {
	rbmqMux.Lock()
	defer rbmqMux.Unlock()
	if _, exists := rbmqSupervisors[queue]; exists {
		return fmt.Errorf("%w: %s", ErrMqConsumerAlreadyRegistered, queue)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &mqConsumerSupervisor{
		queue:     queue,
		processor: processor,
		// Every build declares the exchange, queue and binding before consuming, so a
		// rebuilt consumer never assumes topology survived a broker restart.
		build: func(conn *rabbitmq.Conn) (mqConsumer, error) {
			return mqNewConsumer(
				conn,
				queue,
				rabbitmq.WithConsumerOptionsRoutingKey(routingKey),
				rabbitmq.WithConsumerOptionsExchangeName(exchangeName),
				rabbitmq.WithConsumerOptionsQOSPrefetch(1),
				rabbitmq.WithConsumerOptionsExchangeDeclare,
				rabbitmq.WithConsumerOptionsExchangeDurable,
				rabbitmq.WithConsumerOptionsConsumerName(config.Config.OtelServiceName+"/"+routingKey+"/"+config.Config.ServerName),
			)
		},
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		state:  mqConsumerConnecting,
		since:  time.Now(),
		timing: mqSupervisorTiming{
			rearmBase:    reconnectTimeDelay,
			rearmMax:     mqRearmMaxDelay,
			resetAfter:   mqRearmResetAfter,
			closeTimeout: mqShutdownTimeout,
		},
	}
	rbmqSupervisors[queue] = s
	go s.run()
	return nil
}

// mqConsumerState is a supervised consumer's state.
type mqConsumerState string

const (
	// mqConsumerConnecting: no library consumer is attached; the supervisor is
	// retrying (connection unavailable, declare failed, or re-arming).
	mqConsumerConnecting mqConsumerState = "connecting"
	// mqConsumerRunning: a library consumer is attached and running.
	mqConsumerRunning mqConsumerState = "running"
)

// mqConsumerStatus is a snapshot of one supervised consumer.
type mqConsumerStatus struct {
	Queue     string
	State     mqConsumerState
	Since     time.Time
	LastError string
	Restarts  int
}

// mqSupervisorTiming is captured when a supervisor starts, so a supervisor
// never reads package settings that may change while it runs.
type mqSupervisorTiming struct {
	rearmBase    time.Duration
	rearmMax     time.Duration
	resetAfter   time.Duration
	closeTimeout time.Duration
}

type mqConsumerSupervisor struct {
	queue     string
	processor func(ctx context.Context, data []byte) error
	build     func(*rabbitmq.Conn) (mqConsumer, error)
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	timing    mqSupervisorTiming

	mu        sync.Mutex
	current   mqConsumer
	state     mqConsumerState
	since     time.Time
	lastError string
	restarts  int
	restarted bool // current was closed by restartIfBuiltBefore
}

func (s *mqConsumerSupervisor) run() {
	defer close(s.done)
	failures := 0
	for {
		if s.ctx.Err() != nil {
			return
		}
		err := s.runOnce()
		if s.ctx.Err() != nil {
			return
		}
		if errors.Is(err, errMqConsumerRanLongEnough) {
			failures = 0
		}
		failures++
		s.mu.Lock()
		if s.state != mqConsumerConnecting { // since = start of the outage, not of this retry
			s.state = mqConsumerConnecting
			s.since = time.Now()
		}
		s.lastError = err.Error()
		s.restarts++
		s.mu.Unlock()
		delay := mqRearmDelay(failures, s.timing.rearmBase, s.timing.rearmMax)
		if errors.Is(err, errMqConsumerRestarted) {
			slog.Info("rbmq: rebuilding consumer", "queue", s.queue, "reason", err, "retry_in", delay)
		} else {
			slog.Error("rbmq: consumer not running; retrying", "queue", s.queue, "error", err, "retry_in", delay)
		}
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

var errMqConsumerRanLongEnough = errors.New("rbmq: consumer ran long enough to reset backoff")

// runOnce builds & runs one consumer until it stops, returning why; wraps errMqConsumerRanLongEnough
// if it had been up for timing.resetAfter.
func (s *mqConsumerSupervisor) runOnce() error {
	conn := connect(true)
	if conn == nil {
		return ErrRbmqNoConn
	}
	built, err := s.build(conn)
	if err != nil {
		return fmt.Errorf("rbmq: create consumer: %w", err)
	}
	consumer := &onceClosedConsumer{mqConsumer: built}
	// Always release this handle before building another, so a supervisor
	// never owns two. A handle MqClose already closed makes this a no-op.
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), s.timing.closeTimeout)
		defer cancel()
		consumer.CloseWithContext(closeCtx)
	}()

	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return s.ctx.Err()
	}
	s.current = consumer
	s.state = mqConsumerRunning
	s.since = time.Now()
	s.mu.Unlock()
	slog.Info("rbmq: consumer running", "queue", s.queue)

	started := time.Now()
	runErr := consumer.Run(s.handle)

	s.mu.Lock()
	s.current = nil
	restarted := s.restarted
	s.restarted = false
	s.mu.Unlock()
	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}

	if runErr != nil {
		runErr = fmt.Errorf("rbmq: consumer run: %w", runErr)
	}
	switch {
	case restarted && runErr != nil:
		runErr = fmt.Errorf("%w: %w", errMqConsumerRestarted, runErr)
	case restarted:
		runErr = errMqConsumerRestarted
	case runErr == nil:
		// Run returns nil only after Close, and only MqClose and restartIfBuiltBefore close.
		runErr = errMqConsumerStoppedUnexpected
	}
	if time.Since(started) >= s.timing.resetAfter {
		return fmt.Errorf("%w: %w", errMqConsumerRanLongEnough, runErr)
	}
	return runErr
}

// restartIfBuiltBefore closes the running consumer if it was built before cutoff, so run()
// rebuilds it (invariant 6). It waits for a handler no longer than timing.closeTimeout.
func (s *mqConsumerSupervisor) restartIfBuiltBefore(cutoff time.Time) {
	s.mu.Lock()
	current := s.current
	if current == nil || s.state != mqConsumerRunning || !s.since.Before(cutoff) {
		s.mu.Unlock()
		return
	}
	s.restarted = true
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), s.timing.closeTimeout)
	defer cancel()
	current.CloseWithContext(ctx)
}

// closeCurrent closes the running consumer, if any, waiting for its handler
// no longer than ctx allows. The caller must have cancelled s first.
func (s *mqConsumerSupervisor) closeCurrent(ctx context.Context) {
	s.mu.Lock()
	current := s.current
	s.mu.Unlock()
	if current != nil {
		current.CloseWithContext(ctx)
	}
}

func (s *mqConsumerSupervisor) handle(d rabbitmq.Delivery) rabbitmq.Action {
	// Continue the distributed trace: extract the W3C context the
	// publisher injected into the message headers and start a
	// consumer span, so the processor's logs / outbound calls share
	// the same trace_id.
	ctx := extractTraceContext(d.Headers)
	ctx, span := otel.Tracer("mq").Start(ctx, "rabbitmq.consume", trace.WithSpanKind(trace.SpanKindConsumer))
	perr := s.processor(ctx, d.Body)
	if perr != nil {
		// No-op under the default noop tracer; marks the span
		// failed when a real exporter is configured.
		span.RecordError(perr)
		span.SetStatus(codes.Error, perr.Error())
	}
	span.End()
	if perr != nil {
		log.Printf("rbmq: error processing message on %s: %s", s.queue, perr)
		return rabbitmq.NackRequeue
	}
	return rabbitmq.Ack
}

func (s *mqConsumerSupervisor) status() mqConsumerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return mqConsumerStatus{
		Queue:     s.queue,
		State:     s.state,
		Since:     s.since,
		LastError: s.lastError,
		Restarts:  s.restarts,
	}
}

// onceClosedConsumer makes CloseWithContext idempotent: go-rabbitmq's Close
// must only be called once, and both the supervisor and MqClose may close.
type onceClosedConsumer struct {
	mqConsumer
	once sync.Once
}

func (c *onceClosedConsumer) CloseWithContext(ctx context.Context) {
	c.once.Do(func() { c.mqConsumer.CloseWithContext(ctx) })
}

// mqRearmDelay doubles base per consecutive failure, capped at maxDelay.
func mqRearmDelay(failures int, base, maxDelay time.Duration) time.Duration {
	delay := base
	for i := 1; i < failures && delay < maxDelay; i++ {
		delay *= 2
	}
	return min(delay, maxDelay)
}

// mqConsumerStatuses returns every registered consumer's status, by queue.
func mqConsumerStatuses() []mqConsumerStatus {
	rbmqMux.Lock()
	supervisors := make([]*mqConsumerSupervisor, 0, len(rbmqSupervisors))
	for _, s := range rbmqSupervisors {
		supervisors = append(supervisors, s)
	}
	rbmqMux.Unlock()

	statuses := make([]mqConsumerStatus, 0, len(supervisors))
	for _, s := range supervisors {
		statuses = append(statuses, s.status())
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Queue < statuses[j].Queue })
	return statuses
}

// extractTraceContext restores the W3C trace context (traceparent /
// tracestate) from AMQP message headers. Header values arrive as string or,
// depending on the publisher's client library, []byte — both are handled so
// extraction doesn't silently fail.
func extractTraceContext(headers map[string]any) context.Context {
	carrier := propagation.MapCarrier{}
	for k, v := range headers {
		switch val := v.(type) {
		case string:
			carrier[k] = val
		case []byte:
			carrier[k] = string(val)
		}
	}
	return otel.GetTextMapPropagator().Extract(context.Background(), carrier)
}

// MqPublish publishes without a trace context (background/cron callers).
// Callers holding a request or activity context should use MqPublishCtx so
// the consumer continues the same distributed trace.
func MqPublish(exchangeName string, routingKey string, message ...any) error {
	return MqPublishCtx(context.Background(), exchangeName, routingKey, message...)
}

// MqPublishCtx retries up to maxAttempts on a closed channel or failed publisher create, replacing
// only its own publisher (invariant 3). Not mandatory, not confirmed -> unroutable messages drop silently.
func MqPublishCtx(ctx context.Context, exchangeName string, routingKey string, message ...any) error {
	headers := map[string]any{"x-nb-source": config.Config.OtelServiceName}
	// Inject the active W3C trace context into the message headers so
	// the consuming service can continue the same distributed trace.
	// No-op when ctx carries no span.
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	for k, v := range carrier {
		headers[k] = v
	}

	payloads := make([][]byte, 0, len(message))
	for _, msg1 := range message {
		switch msg := msg1.(type) {
		case string:
			payloads = append(payloads, []byte(msg))
		case []byte:
			payloads = append(payloads, msg)
		default:
			data, err := MarshalJson(msg)
			if err != nil {
				return err
			}
			payloads = append(payloads, data)
		}
	}

	key := fmt.Sprintf("%s:%s", exchangeName, routingKey)
	var lastErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("rbmq: publish to %s abandoned: %w (last error: %w)", key, ctx.Err(), lastErr)
			case <-time.After(reconnectTimeDelay):
			}
		}

		publisher, err := getPublisher(key, exchangeName)
		if err != nil {
			if errors.Is(err, ErrRbmqNoConn) {
				return err
			}
			lastErr = err
			continue
		}

		for i := range payloads {
			err = publisher.Publish(
				payloads[i],
				[]string{routingKey},
				rabbitmq.WithPublishOptionsContentType("application/json"),
				rabbitmq.WithPublishOptionsExchange(exchangeName),
				rabbitmq.WithPublishOptionsHeaders(headers),
			)
			if err != nil {
				break
			}
		}
		if err == nil {
			return nil
		}
		if !strings.Contains(err.Error(), "channel/connection is not open") {
			return fmt.Errorf("rbmq: publish to %s: %w", key, err)
		}
		slog.Info("rbmq: publisher channel closed; replacing publisher", "key", key, "attempt", attempt+1)
		dropPublisher(key, publisher)
		lastErr = err
	}
	return fmt.Errorf("rbmq: publish to %s failed after %d attempts: %w", key, maxAttempts, lastErr)
}

func getPublisher(key string, exchangeName string) (mqPublisher, error) {
	rbmqMux.Lock()
	publisher, ok := rbmqPublishers[key]
	rbmqMux.Unlock()
	if ok {
		return publisher, nil
	}

	conn := getConnection()
	if conn == nil {
		slog.Error("rbmq: error connecting to rabbitmq")
		return nil, ErrRbmqNoConn
	}
	newPublisher, err := mqNewPublisher(
		conn,
		rabbitmq.WithPublisherOptionsLogging,
		rabbitmq.WithPublisherOptionsExchangeName(exchangeName),
		rabbitmq.WithPublisherOptionsExchangeDeclare,
		rabbitmq.WithPublisherOptionsExchangeDurable,
	)
	if err != nil {
		slog.Error("rbmq: error creating publisher", "key", key, "error", err)
		return nil, fmt.Errorf("rbmq: create publisher %s: %w", key, err)
	}
	// Re-check under lock — another goroutine may have raced ahead.
	rbmqMux.Lock()
	if existing, dup := rbmqPublishers[key]; dup && existing != nil {
		rbmqMux.Unlock()
		newPublisher.Close() // channel close is network I/O -> never under rbmqMux
		return existing, nil
	}
	rbmqPublishers[key] = newPublisher
	rbmqMux.Unlock()
	return newPublisher, nil
}

// dropPublisher removes publisher from the cache and closes it, but only if it
// is still the cached one, so concurrent publishers never close it twice.
func dropPublisher(key string, publisher mqPublisher) {
	rbmqMux.Lock()
	current, ok := rbmqPublishers[key]
	owned := ok && current == publisher
	if owned {
		delete(rbmqPublishers, key)
	}
	rbmqMux.Unlock()
	if owned {
		publisher.Close()
	}
}
