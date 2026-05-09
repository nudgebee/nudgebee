// mq/topology.go
package mq

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/relay-server/pkg/config"
	"strings"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// TenantEnsurer is here for testability.
type TenantEnsurer interface {
	EnsureTenant(ctx context.Context, id string) error
	EnsureTenantForAgentType(ctx context.Context, id, agentType string) error
}

// Topology manages your primary exchange and per-tenant queues + DLQs.
type Topology struct {
	connMgr     *ConnectionManager
	exchange    string
	dlxExchange string
	declared    sync.Map // tenantID → struct{}
	ttlMs       int32    // x-message-ttl in milliseconds
}

// NewTopology declares the DLX and primary exchange exactly once.
// No context needed at startup; uses background internally.
func NewTopology(connMgr *ConnectionManager, cfg *config.Config) (*Topology, error) {
	// load TTL from env
	ttl := int32(cfg.RabbitMQ.MessageTTL.Milliseconds())
	exchange := cfg.RabbitMQ.ExchangeName

	dlx := exchange + ".dlx"

	// Create topology first to use retry mechanism
	topology := &Topology{
		connMgr:     connMgr,
		exchange:    exchange,
		dlxExchange: dlx,
		ttlMs:       ttl,
	}

	// one-time exchange declarations with retry
	err := topology.retryRabbitMQOperation(context.Background(), func(ch *amqp.Channel) error {
		if err := ch.ExchangeDeclare(dlx, "direct", true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare DLX %q: %w", dlx, err)
		}
		if err := ch.ExchangeDeclare(exchange, "direct", true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare exchange %q: %w", exchange, err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("topology init: %w", err)
	}

	return topology, nil
}

// EnsureTenant idempotently declares the per-tenant queue + its DLQ for k8s agents.
func (t *Topology) EnsureTenant(ctx context.Context, id string) error {
	return t.EnsureTenantForAgentType(ctx, id, "k8s")
}

// retryRabbitMQOperation retries RabbitMQ operations with exponential backoff
// when encountering connection/channel errors, with proper context cancellation support
func (t *Topology) retryRabbitMQOperation(ctx context.Context, operation func(*amqp.Channel) error) error {
	const maxRetries = 3
	const baseDelay = 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		ch, err := t.connMgr.GetChannel(ctx)
		if err != nil {
			// GetChannel already handles retries internally and only fails when context is cancelled
			// Return immediately as this is a terminal condition
			return fmt.Errorf("failed to get channel: %w", err)
		}
		defer ch.Close() // nolint:errcheck

		err = operation(ch)
		if err == nil {
			return nil
		}

		// Check if it's a connection/channel error that we should retry
		if isRetryableError(err) {
			if attempt == maxRetries-1 {
				return fmt.Errorf("operation failed after %d attempts: %w", maxRetries, err)
			}
			slog.Warn("RabbitMQ operation failed, retrying", "attempt", attempt+1, "err", err)
			if err := t.contextAwareWait(ctx, baseDelay*time.Duration(1<<attempt)); err != nil {
				return err // Context cancelled
			}
			continue
		}

		// Non-retryable error, return immediately
		return err
	}

	return fmt.Errorf("operation failed after %d attempts", maxRetries)
}

// contextAwareWait waits for the specified duration or until context is cancelled
func (t *Topology) contextAwareWait(ctx context.Context, duration time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(duration):
		return nil
	}
}

// isRetryableError checks if an error is retryable (connection/channel issues)
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for AMQP connection/channel errors
	if amqpErr, ok := err.(*amqp.Error); ok {
		switch amqpErr.Code {
		case 406: // PRECONDITION_FAILED
			// Check for message size errors - these are NOT retryable
			if strings.Contains(amqpErr.Reason, "message size") && strings.Contains(amqpErr.Reason, "larger than configured max size") {
				slog.Error("Message too large for RabbitMQ", "reason", amqpErr.Reason, "error_code", amqpErr.Code)
				return false
			}
			// Channel closure due to precondition failures are retryable
			if amqpErr.Reason == "channel/connection is not open" {
				return true
			}
			// TTL/parameter mismatches might be retryable in some cases
			if strings.Contains(amqpErr.Reason, "inequivalent arg") {
				return true
			}
			return false
		case 504: // CHANNEL_ERROR or CONNECTION_FORCED
			return true
		case 320: // CONNECTION_FORCED
			return true
		case 502: // COMMAND_INVALID (can happen during connection issues)
			return true
		}
	}

	// Check for common connection error strings
	errStr := err.Error()
	if errStr == "channel/connection is not open" ||
		errStr == "connection is not open" ||
		errStr == "channel is not open" {
		return true
	}

	// Handle the specific error message format from RabbitMQ
	if amqpErr, ok := err.(*amqp.Error); ok && amqpErr.Reason == "channel/connection is not open" {
		return true
	}

	return false
}

// EnsureTenantForAgentType idempotently declares the per-tenant queue for a specific agent type.
// For k8s agents, uses the standard queue name. For other types (e.g. proxy), appends the type suffix.
func (t *Topology) EnsureTenantForAgentType(ctx context.Context, id, agentType string) error {
	qName := RelayQueueName(id, agentType)
	// Use the queue name itself as the dedup key
	if _, loaded := t.declared.LoadOrStore(qName, struct{}{}); loaded {
		return nil
	}

	dlqName := qName + ".dlq"

	return t.retryRabbitMQOperation(ctx, func(ch *amqp.Channel) error {
		// 1) DLQ with TTL
		dlqArgs := amqp.Table{
			"x-message-ttl": t.ttlMs,
		}
		_, err := ch.QueueDeclare(dlqName, true, false, false, false, dlqArgs)
		if err != nil {
			slog.Warn("DLQ declare failed", "dlq_name", dlqName, "err", err)
			amqpErr, ok := err.(*amqp.Error)
			if !ok || amqpErr.Code != 406 {
				return fmt.Errorf("declare DLQ %q: %w", dlqName, err)
			}
			freshCh, err := t.connMgr.GetChannel(ctx)
			if err != nil {
				return fmt.Errorf("failed to get fresh channel for DLQ operations: %w", err)
			}
			defer freshCh.Close() // nolint:errcheck

			slog.Info("DLQ TTL mismatch detected, attempting delete and recreate", "dlq_name", dlqName, "error_code", amqpErr.Code)
			if _, err := freshCh.QueueDelete(dlqName, false, false, false); err != nil {
				return fmt.Errorf("delete DLQ %q: %w", dlqName, err)
			}
			_, err = freshCh.QueueDeclare(dlqName, true, false, false, false, dlqArgs)
			if err != nil {
				return fmt.Errorf("redeclare DLQ %q with TTL: %w", dlqName, err)
			}
		}

		if err := ch.QueueBind(dlqName, qName, t.dlxExchange, false, nil); err != nil {
			return fmt.Errorf("bind DLQ %q→%q: %w", dlqName, t.dlxExchange, err)
		}

		// 2) Main queue with per-queue TTL + DLX
		args := amqp.Table{
			"x-message-ttl":             t.ttlMs,
			"x-dead-letter-exchange":    t.dlxExchange,
			"x-dead-letter-routing-key": qName,
		}
		if _, err := ch.QueueDeclare(qName, true, false, false, false, args); err != nil {
			slog.Warn("Queue declare failed", "queue_name", qName, "err", err)
			amqpErr, ok := err.(*amqp.Error)
			if !ok || amqpErr.Code != 406 {
				return fmt.Errorf("declare queue %q: %w", qName, err)
			}
			freshCh, err := t.connMgr.GetChannel(ctx)
			if err != nil {
				return fmt.Errorf("failed to get fresh channel for queue operations: %w", err)
			}
			defer freshCh.Close() // nolint:errcheck

			if _, err := freshCh.QueueDelete(qName, false, false, false); err != nil {
				return fmt.Errorf("delete queue %q: %w", qName, err)
			}
			_, err = freshCh.QueueDeclare(qName, true, false, false, false, args)
			if err != nil {
				return fmt.Errorf("redeclare queue %q with TTL: %w", qName, err)
			}
		}
		if err := ch.QueueBind(qName, qName, t.exchange, false, nil); err != nil {
			return fmt.Errorf("bind queue %q→%q: %w", qName, t.exchange, err)
		}

		return nil
	})
}

// RelayQueueName builds the per-tenant queue name, with optional agent type suffix.
// k8s agents use "relay_requests_{id}", proxy agents use "relay_requests_{id}_proxy".
func RelayQueueName(id, agentType string) string {
	if agentType == "" || agentType == "k8s" {
		return "relay_requests_" + id
	}
	return "relay_requests_" + id + "_" + agentType
}

