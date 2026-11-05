package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"nudgebee/relay-server/pkg/models"
)

// sessionReplyChannel publishes a register session's replies on an AMQP
// channel of its own, never on the channel the session consumes requests from.
//
// The broker answers a bad publish (a message over its max_message_size, say)
// by closing the channel it arrived on. When that was the consume channel,
// every request still unacked on it went back to the queue and was delivered
// to the agent again; an agent reply that was always too big then closed the
// channel again on every delivery, so one oversized reply took down every
// request in flight for the cluster until the queue's TTL expired them. On a
// channel of its own, a refused publish costs only that reply.
//
// A broker-side refusal is asynchronous — Publish has already returned nil —
// so it surfaces as the channel being closed on the next publish, which then
// reopens it.
type sessionReplyChannel struct {
	open func(context.Context) (*amqp.Channel, error)

	mu sync.Mutex
	ch *amqp.Channel
}

// replyChannelOpenTimeout bounds opening the reply channel. A reply is often
// published after the session's context has ended (the "agent connection
// lost" reply), so the wait cannot hang off that context.
const replyChannelOpenTimeout = 5 * time.Second

func newSessionReplyChannel(open func(context.Context) (*amqp.Channel, error)) *sessionReplyChannel {
	return &sessionReplyChannel{open: open}
}

// publish sends msg to the reply queue replyTo on the default exchange.
func (p *sessionReplyChannel) publish(replyTo string, msg amqp.Publishing) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ch == nil || p.ch.IsClosed() {
		ctx, cancel := context.WithTimeout(context.Background(), replyChannelOpenTimeout)
		ch, err := p.open(ctx)
		cancel()
		if err != nil {
			return fmt.Errorf("open reply channel: %w", err)
		}
		p.ch = ch
	}
	if err := p.ch.Publish("", replyTo, false, false, msg); err != nil {
		_ = p.ch.Close()
		p.ch = nil
		return err
	}
	return nil
}

// close releases the channel. Call it once no delivery can publish any more.
func (p *sessionReplyChannel) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ch != nil {
		_ = p.ch.Close()
		p.ch = nil
	}
}

// fitReplyToBroker returns reply unchanged when the broker will take it, and
// otherwise a small 413 reply naming both sizes. The agent's websocket takes
// bigger messages than the broker's default max_message_size, so a reply can
// reach the relay and still be refused on publish; failing it here tells the
// caller why at once instead of after its full timeout.
func fitReplyToBroker(reply []byte, requestID string, limit int64) (out []byte, fitted bool) {
	if limit <= 0 || int64(len(reply)) <= limit {
		return reply, false
	}
	out, _ = json.Marshal(models.AgentResponse{
		StatusCode: http.StatusRequestEntityTooLarge,
		Action:     "error",
		RequestID:  requestID,
		Data: fmt.Sprintf("agent reply was %.1f MiB, over the message broker's %.1f MiB message limit, so it was discarded; ask for less data (a narrower query, a filter or a limit)",
			float64(len(reply))/(1<<20), float64(limit)/(1<<20)),
	})
	return out, true
}
