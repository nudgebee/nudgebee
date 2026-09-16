package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/tidwall/gjson"
	"go.opentelemetry.io/otel/metric"

	"nudgebee/relay-server/pkg/mq"
	"nudgebee/relay-server/pkg/server/metrics"
)

// expiredRequestReply is the body handed back to a caller whose request died in
// the queue. It matches the AgentResponse shape the RPC caller already parses,
// the same way the "agent connection lost" reply in RegisterHandler does.
//
// 504 rather than 502: nothing is wrong with the agent connection, the request
// simply ran out of time before anyone could pick it up.
var expiredRequestReply = []byte(`{"status_code":504,"action":"error","data":"request expired in queue before the agent could take it"}`)

// consumeExpiredRequests answers callers whose requests died in the tenant queue
// instead of leaving them to hang.
//
// A tenant queue carries x-message-ttl (RABBITMQ_MESSAGE_TTL, 1m by default)
// while the caller waits RELAY_HTTP_WRITE_TIMEOUT (180s). The two clocks
// disagree, so when the session's prefetch window is full the requests that back
// up behind it are dead-lettered at 60s — and the DLQ has no consumer and no
// onward dead-letter exchange, so they were simply discarded. The caller learned
// nothing: no error, no reply, just silence until its own timeout fired two
// minutes later. Observed on production, where one tenant's DLQ held messages
// while its main queue sat at the prefetch ceiling.
//
// This does not stop requests expiring — raising the prefetch ceiling and making
// the expensive callers cheaper is what does that. It makes the failure fast and
// legible instead of silent, and gives it a metric to alert on.
//
// Only genuine expiries get a reply. A message dead-lettered because the
// delivery path rejected it (x-first-death-reason "rejected") belongs to a
// caller that has already stopped waiting, so answering it would be noise.
func consumeExpiredRequests(
	ctx context.Context,
	connMgr *mq.ConnectionManager,
	queue string,
	accountID string,
	logger *slog.Logger,
) error {
	// The DLQ is declared alongside the tenant queue in EnsureTenantForAgentType,
	// which runs before this goroutine starts.
	dlqName := queue + ".dlq"
	// Unique per session, matching the main consumer's tag convention, so a
	// reconnect that briefly overlaps the previous session cannot collide.
	consumerTag := fmt.Sprintf("dlq-%s-%d", accountID, time.Now().UnixNano())

	for {
		ch, err := connMgr.GetChannel(ctx)
		if err != nil {
			// GetChannel retries internally and only gives up when ctx is done,
			// so this is terminal and the error is always a context error.
			// Return it unwrapped: RegisterHandler reports anything that is not
			// exactly context.Canceled as a session failure.
			return err
		}

		closeErrCh := ch.NotifyClose(make(chan *amqp.Error, 1))

		msgs, err := ch.Consume(dlqName, consumerTag, false, false, false, false, nil)
		if err != nil {
			ch.Close() // nolint:errcheck
			logger.Error("DLQ consume failed, retrying", "dlq", dlqName, "err", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
			continue
		}

		logger.Info("watching for expired requests", "dlq", dlqName)

		if done := drainExpiredRequests(ctx, ch, msgs, closeErrCh, consumerTag, accountID, logger); done {
			return ctx.Err()
		}
	}
}

// drainExpiredRequests handles one channel's worth of deliveries, reporting
// whether the caller should stop entirely rather than reconnect.
func drainExpiredRequests(
	ctx context.Context,
	ch *amqp.Channel,
	msgs <-chan amqp.Delivery,
	closeErrCh chan *amqp.Error,
	consumerTag string,
	accountID string,
	logger *slog.Logger,
) bool {
	for {
		select {
		case <-ctx.Done():
			ch.Cancel(consumerTag, false) // nolint:errcheck
			ch.Close()                    // nolint:errcheck
			return true

		case amqpErr := <-closeErrCh:
			logger.Warn("DLQ channel closed; reconnecting", "err", amqpErr)
			ch.Close() // nolint:errcheck
			return false

		case d, ok := <-msgs:
			if !ok {
				logger.Warn("DLQ msgs channel closed; reconnecting")
				ch.Close() // nolint:errcheck
				return false
			}
			handleExpiredRequest(ctx, ch, d, accountID, logger)
		}
	}
}

// replyPublisher is the slice of *amqp.Channel that answering an expired
// request needs, so the reply path can be exercised without a broker.
type replyPublisher interface {
	Publish(exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error
}

// handleExpiredRequest replies to the caller of a single dead-lettered request.
func handleExpiredRequest(
	ctx context.Context,
	ch replyPublisher,
	d amqp.Delivery,
	accountID string,
	logger *slog.Logger,
) {
	reason, _ := d.Headers["x-first-death-reason"].(string)
	if reason != "expired" {
		// Rejected by the delivery path, whose caller is already gone.
		d.Ack(false) // nolint:errcheck
		return
	}

	// Dead-lettering republishes the original properties, so reply_to and
	// correlation_id still identify the waiting caller.
	if d.ReplyTo == "" || d.CorrelationId == "" {
		logger.Warn("expired request has no reply address, dropping",
			"account", accountID, "corr_id", d.CorrelationId)
		d.Ack(false) // nolint:errcheck
		return
	}

	action := gjson.GetBytes(d.Body, "body.action_name").String()
	logger.Warn("request expired in queue before the agent took it",
		"account", accountID,
		"corr_id", d.CorrelationId,
		"action", action,
	)

	metrics.RequestsExpiredInQueue.Add(ctx, 1, metric.WithAttributes(metrics.AttrAccount(accountID)))

	if err := ch.Publish(
		"", d.ReplyTo, false, false,
		amqp.Publishing{
			ContentType:   "application/json",
			Body:          expiredRequestReply,
			CorrelationId: d.CorrelationId,
			Timestamp:     time.Now(),
		},
	); err != nil {
		// Requeue rather than ack a reply we never sent. This cannot spin:
		// without publisher confirms, Publish only errors when the channel is
		// already closing, so the Nack fails too and closeErrCh takes the loop
		// into a reconnect. The DLQ's own TTL cleans up anything left behind.
		logger.Warn("failed to answer expired request", "corr_id", d.CorrelationId, "err", err)
		d.Nack(false, true) // nolint:errcheck
		return
	}
	d.Ack(false) // nolint:errcheck
}
