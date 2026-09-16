// pkg/mq/rpc_client.go
package mq

import (
	"context"
	"fmt"
	"sync"
	"time"

	"log/slog"
	"nudgebee/relay-server/pkg/config"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
)

// HeaderRelayInstance names the publisher's RPCClient instance on each request,
// so the register session can tell whether the caller awaiting this correlation
// ID lives in its own process. See DeliverLocal.
const HeaderRelayInstance = "x-relay-instance"

// HeaderRelayDeadline carries the publishing caller's deadline as Unix
// nanoseconds, so the register session waits out the remainder of the caller's
// budget rather than starting a fresh full-length timer when the message is
// delivered. Without it a message that queued behind a saturated prefetch
// window was granted its whole timeout again on top of the time it had already
// spent waiting.
const HeaderRelayDeadline = "x-relay-deadline-unix-nano"

// DeadlineFromHeaders reads the publisher's deadline back off a delivery,
// reporting false when the header is absent — which is both the pre-upgrade
// wire format and any caller whose context carried no deadline. Callers fall
// back to their configured timeout in that case.
//
// AMQP field tables normalise integers to whatever width fits, so accept the
// signed widths amqp091 can hand back rather than asserting int64.
func DeadlineFromHeaders(headers amqp.Table) (time.Time, bool) {
	var nanos int64
	switch v := headers[HeaderRelayDeadline].(type) {
	case int64:
		nanos = v
	case int32:
		nanos = int64(v)
	case int:
		nanos = int64(v)
	default:
		return time.Time{}, false
	}
	return time.Unix(0, nanos), true
}

// RPCClient defines a simple RabbitMQ-backed RPC interface.
type RPCClient interface {
	Call(ctx context.Context, exchange, routingKey string, payload []byte, corrID string) ([]byte, error)
	// InstanceID identifies this client, and so this process, on the wire.
	InstanceID() string
	// DeliverLocal hands a reply straight to a caller blocked in Call within
	// this process, reporting whether one was waiting.
	DeliverLocal(corrID string, body []byte) bool
	// AbandonCh returns a channel that closes when the caller which published
	// corrID from this process stops waiting for it. Only meaningful for
	// correlation IDs this process published — see the doc comment on the
	// method for why an unknown ID yields an already-closed channel.
	AbandonCh(corrID string) <-chan struct{}
	Close()
}

// replyPrefetchCount bounds how many agent replies the broker may have in
// flight to this process at once. Replies are the large payloads in this
// system — tens of megabytes for a wide prometheus query — so this, not CPU,
// is what sets the relay's peak memory.
const replyPrefetchCount = 20

// alreadyAbandoned is the channel AbandonCh hands back for a correlation ID it
// no longer holds. It is closed once at init and shared: every such caller is
// abandoned by definition, so they can all read the same closed channel.
var alreadyAbandoned = func() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}()

// pendingCall is the state a blocked Call leaves behind for the reply path.
//
// resp carries the reply. abandon is closed when the caller gives up, which is
// the only signal the register session has that the work it is holding a
// RabbitMQ prefetch slot for is no longer wanted: a caller's HTTP client
// disconnecting cancels its context long before the relay's own timeout
// expires, and nothing on the delivery side could previously observe that.
type pendingCall struct {
	resp    chan []byte
	abandon chan struct{}
}

// ClientImpl manages the AMQP connection, channels, and request/response dispatch.
type ClientImpl struct {
	connMgr    *ConnectionManager
	exchange   string
	topology   TenantEnsurer
	pubCh      *amqp.Channel
	consCh     *amqp.Channel
	replyQ     amqp.Queue
	pending    sync.Map // corrID → *pendingCall
	closeErrCh chan *amqp.Error
	initOnce   sync.Once
	logger     *slog.Logger
	cfg        *config.Config
	instanceID string
}

// NewRPCClient constructs and connects an RPCClient, wiring in auto-reconnect.
func NewRPCClient(cm *ConnectionManager, logger *slog.Logger, cfg *config.Config) (RPCClient, error) {
	client := &ClientImpl{
		connMgr:    cm,
		exchange:   cfg.RabbitMQ.ExchangeName,
		closeErrCh: make(chan *amqp.Error, 1),
		logger:     logger,
		cfg:        cfg,
		instanceID: uuid.NewString(),
	}
	// initial setup
	if err := client.reconnect(); err != nil {
		return nil, err
	}
	return client, nil
}

// reconnect sets up topology, publisher and consumer channels, and reply queue.
func (c *ClientImpl) reconnect() error {
	c.logger.Info("RPCClient: setting up channels and topology")

	// cleanup old channels
	if c.pubCh != nil {
		_ = c.pubCh.Close()
	}
	if c.consCh != nil {
		_ = c.consCh.Close()
	}

	// 1) declare exchanges & per-tenant setup via Topology
	topo, err := NewTopology(c.connMgr, c.cfg)
	if err != nil {
		return fmt.Errorf("topology init: %w", err)
	}

	// 2) open publisher channel
	pubCh, err := c.connMgr.GetChannel(context.Background())
	if err != nil {
		return fmt.Errorf("open pub channel: %w", err)
	}
	pubClose := pubCh.NotifyClose(make(chan *amqp.Error, 1))

	// 3) open consumer channel for replies
	consCh, err := c.connMgr.GetChannel(context.Background())
	if err != nil {
		pubCh.Close() // nolint:errcheck
		return fmt.Errorf("open cons channel: %w", err)
	}
	consClose := consCh.NotifyClose(make(chan *amqp.Error, 1))

	// 4) declare auto-deleted reply queue
	replyQ, err := consCh.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		consCh.Close() // nolint:errcheck
		pubCh.Close()  // nolint:errcheck
		return fmt.Errorf("declare reply queue: %w", err)
	}

	// 4.5) apply QoS to reply consumer to avoid unbounded prefetch
	if err := consCh.Qos(replyPrefetchCount, 0, false); err != nil {
		consCh.Close() // nolint:errcheck
		pubCh.Close()  // nolint:errcheck
		return fmt.Errorf("reply channel qos: %w", err)
	}

	// 5) start consuming replies.
	//
	// autoAck must stay false for the Qos above to mean anything: AMQP ignores
	// prefetch entirely for no-ack consumers, so this channel previously had no
	// bound at all on how many multi-megabyte agent replies the broker could
	// push at once — the relay's OOM exposure. dispatchLoop acks every delivery
	// unconditionally, including ones with no waiting caller, so the prefetch
	// window can never stall.
	msgs, err := consCh.Consume(replyQ.Name, "", false, true, false, false, nil)
	if err != nil {
		consCh.Close() // nolint:errcheck
		pubCh.Close()  // nolint:errcheck
		return fmt.Errorf("consume replies: %w", err)
	}

	// commit new state
	c.pubCh = pubCh
	c.consCh = consCh
	c.replyQ = replyQ
	c.topology = topo

	// watch for channel closes
	go func() {
		if e := <-pubClose; e != nil {
			c.closeErrCh <- e
		}
	}()
	go func() {
		if e := <-consClose; e != nil {
			c.closeErrCh <- e
		}
	}()

	// start dispatching replies
	go c.dispatchLoop(msgs)

	// on first connect, start watching
	c.initOnce.Do(func() {
		go c.watchDisconnect()
	})

	c.logger.Info("RPCClient: connected and dispatch loop running")
	return nil
}

// watchDisconnect triggers reconnect on any channel or connection closure.
func (c *ClientImpl) watchDisconnect() {
	c.logger.Info("RPCClient: watching for disconnects")
	for err := range c.closeErrCh {
		c.logger.Warn("RPCClient: detected close, reconnecting", "error", err)
		backoff := time.Second
		for {
			if err := c.reconnect(); err != nil {
				c.logger.Error("RPCClient: reconnect failed", "error", err)
				time.Sleep(backoff)
				if backoff < time.Minute {
					backoff *= 2
				}
				continue
			}
			break
		}
	}
}

// dispatchLoop sends incoming reply messages to the right pending caller.
func (c *ClientImpl) dispatchLoop(msgs <-chan amqp.Delivery) {
	c.logger.Info("RPCClient: dispatch loop started")
	for d := range msgs {
		// LoadAndDelete so this and DeliverLocal retire a pending entry
		// atomically — whichever path reaches the caller first wins, and the
		// other cannot send on an already-closed channel.
		if !c.deliver(d.CorrelationId, d.Body) {
			c.logger.Warn("RPCClient: no pending channel for corrID", "corrID", d.CorrelationId)
		}
		// Ack regardless: an undelivered reply has nowhere else to go, and
		// leaving it unacked would consume a prefetch slot permanently.
		if err := d.Ack(false); err != nil {
			c.logger.Warn("RPCClient: ack reply failed", "corrID", d.CorrelationId, "err", err)
		}
	}
	c.logger.Warn("RPCClient: reply consumer closed")
}

// deliver hands body to the caller waiting on corrID, reporting whether there
// was one. The response channel is buffered, so this never blocks even if the
// caller has already given up.
func (c *ClientImpl) deliver(corrID string, body []byte) bool {
	callAny, ok := c.pending.LoadAndDelete(corrID)
	if !ok {
		return false
	}
	call := callAny.(*pendingCall)
	call.resp <- body
	close(call.resp)
	return true
}

// AbandonCh returns a channel that closes once the caller which published
// corrID from this process has stopped waiting.
//
// An unknown correlation ID yields an already-closed channel rather than nil.
// Callers only ask about IDs stamped with this process's instance header, so
// "unknown" means the Call has already retired the entry — the caller is gone,
// which is exactly what a closed channel signals. Returning nil would instead
// block the caller's select forever, which is the bug this exists to fix.
func (c *ClientImpl) AbandonCh(corrID string) <-chan struct{} {
	if callAny, ok := c.pending.Load(corrID); ok {
		return callAny.(*pendingCall).abandon
	}
	return alreadyAbandoned
}

// InstanceID identifies this client, and so this process, on the wire.
func (c *ClientImpl) InstanceID() string { return c.instanceID }

// DeliverLocal hands a reply straight to a caller blocked in Call within this
// process, bypassing the broker.
//
// The relay is both the publisher of a request and — via the register session
// holding the agent's WebSocket — the receiver of its reply. When both sit in
// the same process, which is every request at the chart's default
// replicaCount: 1, publishing the reply serialises a payload out to RabbitMQ
// only for this client's own consumer to reassemble it. Callers must fall back
// to publishing when this returns false: the waiter is then on another replica
// (or has already timed out).
func (c *ClientImpl) DeliverLocal(corrID string, body []byte) bool {
	return c.deliver(corrID, body)
}

// Call sends an RPC request and blocks until a reply or context cancellation.
func (c *ClientImpl) Call(
	ctx context.Context,
	exchange, routingKey string,
	payload []byte,
	corrID string,
) ([]byte, error) {
	// topology should already exist from register call

	// prepare a response channel
	call := &pendingCall{
		resp:    make(chan []byte, 1),
		abandon: make(chan struct{}),
	}
	c.pending.Store(corrID, call)

	// Inject the active W3C trace context into the AMQP headers so the
	// in-cluster agent consuming this message can continue the same trace.
	headers := amqp.Table{}
	otel.GetTextMapPropagator().Inject(ctx, amqpHeaderCarrier(headers))

	// Name ourselves so the register session consuming this request knows
	// whether the caller it must reply to is in its own process.
	headers[HeaderRelayInstance] = c.instanceID

	// Publish our deadline so the delivery side can spend what is left of it
	// rather than restarting the clock. See HeaderRelayDeadline.
	if deadline, ok := ctx.Deadline(); ok {
		headers[HeaderRelayDeadline] = deadline.UnixNano()
	}

	// publish the RPC request
	err := c.pubCh.PublishWithContext(
		ctx,
		exchange,
		routingKey,
		false, false,
		amqp.Publishing{
			ContentType:   "application/json",
			Body:          payload,
			CorrelationId: corrID,
			ReplyTo:       c.replyQ.Name,
			Headers:       headers,
		},
	)
	if err != nil {
		c.pending.Delete(corrID)
		return nil, fmt.Errorf("publish rpc request: %w", err)
	}

	// wait for either the reply or context cancellation
	select {
	case resp := <-call.resp:
		return resp, nil
	case <-ctx.Done():
		// LoadAndDelete so this races cleanly with a reply arriving at the
		// same instant: whichever path retires the entry wins, and abandon is
		// only closed when we are the one who retired it.
		if _, ours := c.pending.LoadAndDelete(corrID); ours {
			close(call.abandon)
		}
		return nil, ctx.Err()
	}
}

// Close gracefully tears down channels and the underlying connection.
func (c *ClientImpl) Close() {
	c.logger.Info("RPCClient: closing")
	if c.pubCh != nil {
		_ = c.pubCh.Close()
	}
	if c.consCh != nil {
		_ = c.consCh.Close()
	}
	if c.connMgr != nil {
		c.connMgr.Close()
	}
}
