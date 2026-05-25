// pkg/mq/rpc_client.go
package mq

import (
	"context"
	"fmt"
	"sync"
	"time"

	"log/slog"
	"nudgebee/relay-server/pkg/config"

	amqp "github.com/rabbitmq/amqp091-go"
)

// RPCClient defines a simple RabbitMQ-backed RPC interface.
type RPCClient interface {
	Call(ctx context.Context, exchange, routingKey string, payload []byte, corrID string) ([]byte, error)
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
}

// NewRPCClient constructs and connects an RPCClient, wiring in auto-reconnect.
func NewRPCClient(cm *ConnectionManager, logger *slog.Logger, cfg *config.Config) (RPCClient, error) {
	client := &ClientImpl{
		connMgr:    cm,
		exchange:   cfg.RabbitMQ.ExchangeName,
		closeErrCh: make(chan *amqp.Error, 1),
		logger:     logger,
		cfg:        cfg,
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
		if chAny, ok := c.pending.Load(d.CorrelationId); ok {
			respCh := chAny.(chan []byte)
			respCh <- d.Body
			close(respCh)
			c.pending.Delete(d.CorrelationId)
		} else {
			c.logger.Warn("RPCClient: no pending channel for corrID", "corrID", d.CorrelationId)
		}
	}
	c.logger.Warn("RPCClient: reply consumer closed")
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
