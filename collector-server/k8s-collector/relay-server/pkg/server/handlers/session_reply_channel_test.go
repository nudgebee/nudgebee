package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/relay-server/pkg/models"
	"nudgebee/relay-server/pkg/mq"
)

func TestFitReplyToBroker(t *testing.T) {
	small := []byte(`{"status_code":200}`)
	out, fitted := fitReplyToBroker(small, "req-1", 1<<20)
	assert.False(t, fitted)
	assert.Equal(t, small, out)

	big := make([]byte, 17<<20)
	out, fitted = fitReplyToBroker(big, "req-1", 16<<20)
	require.True(t, fitted)
	assert.Less(t, len(out), 1<<10)
	var resp models.AgentResponse
	require.NoError(t, json.Unmarshal(out, &resp))
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	assert.Equal(t, "req-1", resp.RequestID)
	assert.Contains(t, resp.Data, "17.0 MiB")
	assert.Contains(t, resp.Data, "16.0 MiB")
}

// Regression: an agent reply over the broker's max_message_size was published
// on the session's consume channel. The broker closed that channel, the request
// (not yet acked) went back to the queue and was delivered again, and the same
// too-big reply closed the channel again — every few seconds until the queue
// TTL expired the request, taking every other in-flight request down with it.
// Published on the session's own reply channel, the refusal costs that reply
// only: the request is delivered once and the session keeps serving.
func TestBroker_OversizedReplyDoesNotRedeliverRequest(t *testing.T) {
	proxy := requireRabbitMQ(t)

	replyQueue := "relay-test-replies-" + strings.ReplaceAll(t.Name(), "/", "-")
	rawAmqp(t, func(ch *amqp.Channel) error {
		_, err := ch.QueueDeclare(replyQueue, true, false, false, false, nil)
		return err
	})
	t.Cleanup(func() {
		rawAmqp(t, func(ch *amqp.Channel) error {
			_, _ = ch.QueueDelete(replyQueue, false, false, false)
			return nil
		})
	})

	// Over RabbitMQ 4's default 16 MiB max_message_size.
	big := make([]byte, 17<<20)
	// Its own connection: what matters is that it is not the consume channel.
	connMgr, err := mq.NewConnectionManager(proxy.url())
	require.NoError(t, err)
	replyCh := newSessionReplyChannel(connMgr.GetChannel)
	t.Cleanup(replyCh.close)
	s := startSessionWith(t, proxy, func(_ *amqp.Channel, d amqp.Delivery) {
		if string(d.Body) == "big" {
			_ = replyCh.publish(replyQueue, amqp.Publishing{Body: big})
			// Let the broker refuse it before the ack goes out, as it does
			// when the agent's reply takes the whole budget to arrive.
			time.Sleep(500 * time.Millisecond)
		}
	})
	// Registered after the session, so it runs before the session's own
	// cleanup: a redelivery loop fills s.received, and a handler blocked on it
	// would keep the session from shutting down.
	t.Cleanup(func() {
		go func() {
			for range s.received { //nolint:revive // drain only
			}
		}()
	})
	s.requireServed(t, "warmup", 10*time.Second)

	rawAmqp(t, func(ch *amqp.Channel) error {
		return ch.PublishWithContext(context.Background(), s.exchange, s.queue, false, false,
			amqp.Publishing{Body: []byte("big")})
	})

	deliveries := 0
	deadline := time.After(5 * time.Second)
collect:
	for {
		select {
		case got := <-s.received:
			if got == "big" {
				deliveries++
			}
		case <-deadline:
			break collect
		}
	}
	require.Equal(t, 1, deliveries, "the request with the oversized reply was delivered again")

	s.requireServed(t, "after", 10*time.Second)
}
