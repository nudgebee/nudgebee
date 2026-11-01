package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"nudgebee/relay-server/pkg/mq"
	"nudgebee/relay-server/pkg/server/health"
)

// consumeSessionQueue consumes one register-session queue until ctx ends,
// passing each delivery to handle together with the channel it arrived on
// (replies are published, and deliveries acked, on that channel).
//
// It has to outlive the broker: a closed channel or a consumer the broker
// cancelled opens a fresh channel and consumes again, and a Consume the broker
// refuses re-declares the queue before retrying. Only ctx ending, or the
// connection manager giving up on a channel, returns.
func consumeSessionQueue(
	ctx context.Context,
	connMgr *mq.ConnectionManager,
	topo *mq.Topology,
	healthTracker *health.Tracker,
	ensureQueue func(context.Context, string) error,
	queue, consumerTag string,
	prefetch int,
	logger *slog.Logger,
	handle func(*amqp.Channel, amqp.Delivery),
) error {
	for {
		// 1) open a fresh channel
		sessCh, err := connMgr.GetChannel(ctx)
		if err != nil {
			return fmt.Errorf("get channel: %w", err)
		}

		// 2) apply QoS.
		//
		// This bounds a single tenant's concurrent in-flight agent
		// requests. It was hardcoded to 10, and production tenants hit
		// that ceiling: with unacked pinned at 10 the broker stops
		// delivering, so further requests sit in the ready queue until
		// the 1m queue TTL dead-letters them — silently, with no error
		// to the caller. The messages held here are requests, which are
		// small; the large payloads are replies, bounded separately on
		// the RPC client's own channel.
		if err := sessCh.Qos(prefetch, 0, false); err != nil {
			sessCh.Close() // nolint:errcheck
			logger.Error("QoS setup failed, retrying", "err", err)
			time.Sleep(time.Second)
			continue
		}

		// 3) watch for channel-close
		closeErrCh := sessCh.NotifyClose(make(chan *amqp.Error, 1))

		// 4) start consuming
		msgs, err := sessCh.Consume(queue, consumerTag, false, false, false, false, nil)
		if err != nil {
			sessCh.Close() // nolint:errcheck
			logger.Error("Consume failed, re-declaring topology and retrying", "queue", queue, "err", err)

			// The broker disagrees with us about this queue, so the
			// "already declared" record is wrong. A RabbitMQ that came
			// back without its definitions is the case that matters:
			// Consume then fails with NOT_FOUND forever, nothing
			// re-creates the queue, and the session sits here retrying
			// a queue that does not exist while the agent's WebSocket
			// stays open — so the pod looks healthy and serves nothing
			// until someone deletes it. Re-declaring is a cheap no-op
			// when the queue is really there.
			healthTracker.ConsumeFailed(queue)
			topo.ForgetQueue(queue)
			if derr := ensureQueue(ctx, queue); derr != nil {
				logger.Error("re-declare after consume failure failed", "queue", queue, "err", derr)
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
			continue
		}

		healthTracker.ConsumerAttached(queue)

	ConsumeLoop:
		for {
			select {
			case <-ctx.Done():
				// clean shutdown
				sessCh.Cancel(consumerTag, false) // nolint
				sessCh.Close()                    // nolint:errcheck
				return ctx.Err()

			case amqpErr := <-closeErrCh:
				logger.Warn("AMQP channel closed; reconnecting", "err", amqpErr)
				sessCh.Close() // nolint:errcheck
				break ConsumeLoop

			case d, ok := <-msgs:
				if !ok {
					// broker closed the msgs channel—reconnect
					logger.Warn("msgs channel closed; reconnecting")
					sessCh.Close() // nolint:errcheck
					break ConsumeLoop
				}
				handle(sessCh, d)
			}
		}
	}
}
