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

// RPCClient defines a simple RabbitMQ-backed RPC interface.
type RPCClient interface {
	Call(ctx context.Context, exchange, routingKey string, payload []byte, corrID string) ([]byte, error)
	// InstanceID identifies this client, and so this process, on the wire.
	InstanceID() string
	// DeliverLocal hands a reply straight to a caller blocked in Call within
	// this process, reporting whether one was waiting.
	DeliverLocal(corrID string, body []byte) bool
	Close()
}

// ClientImpl manages the AMQP connection, channels, and request/response dispatch.
type ClientImpl struct {
	connMgr    *ConnectionManager
	exchange   string
	topology   TenantEnsurer
	pubCh      *amqp.Channel
	consCh     *amqp.Channel
	replyQ     amqp.Queue
	pending    sync.Map // corrID → chan []byte
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
	if err := consCh.Qos(20, 0, false); err != nil {
		consCh.Close() // nolint:errcheck
		pubCh.Close()  // nolint:errcheck
		return fmt.Errorf("reply channel qos: %w", err)
	}

	// 5) start consuming replies
	msgs, err := consCh.Consume(replyQ.Name, "", true, true, false, false, nil)
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
	}
	c.logger.Warn("RPCClient: reply consumer closed")
}

// deliver hands body to the caller waiting on corrID, reporting whether there
// was one. The response channel is buffered, so this never blocks even if the
// caller has already given up.
func (c *ClientImpl) deliver(corrID string, body []byte) bool {
	chAny, ok := c.pending.LoadAndDelete(corrID)
	if !ok {
		return false
	}
	respCh := chAny.(chan []byte)
	respCh <- body
	close(respCh)
	return true
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
	respCh := make(chan []byte, 1)
	c.pending.Store(corrID, respCh)

	// Inject the active W3C trace context into the AMQP headers so the
	// in-cluster agent consuming this message can continue the same trace.
	headers := amqp.Table{}
	otel.GetTextMapPropagator().Inject(ctx, amqpHeaderCarrier(headers))

	// Name ourselves so the register session consuming this request knows
	// whether the caller it must reply to is in its own process.
	headers[HeaderRelayInstance] = c.instanceID

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
	case resp := <-respCh:
		return resp, nil
	case <-ctx.Done():
		c.pending.Delete(corrID)
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
