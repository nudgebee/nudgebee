package handlers

import (
	"context"
	"io"
	"log/slog"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type publishedReply struct {
	key string
	msg amqp.Publishing
}

type fakePublisher struct {
	published []publishedReply
	err       error
}

func (f *fakePublisher) Publish(exchange, key string, _, _ bool, msg amqp.Publishing) error {
	if f.err != nil {
		return f.err
	}
	f.published = append(f.published, publishedReply{key: key, msg: msg})
	return nil
}

// fakeAcker records how a delivery was settled. amqp.Delivery dispatches Ack and
// Nack through its exported Acknowledger, so no broker is needed.
type fakeAcker struct {
	acked    int
	nacked   int
	requeued bool
	rejected int
}

func (f *fakeAcker) Ack(tag uint64, multiple bool) error { f.acked++; return nil }
func (f *fakeAcker) Nack(tag uint64, multiple, requeue bool) error {
	f.nacked++
	f.requeued = requeue
	return nil
}
func (f *fakeAcker) Reject(tag uint64, requeue bool) error { f.rejected++; return nil }

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func expiredDelivery(acker amqp.Acknowledger, headers amqp.Table) amqp.Delivery {
	return amqp.Delivery{
		Acknowledger:  acker,
		Headers:       headers,
		CorrelationId: "corr-1",
		ReplyTo:       "amq.gen-replyq",
		Body:          []byte(`{"body":{"action_name":"prometheus_enricher"}}`),
	}
}

// The defect: a request dead-lettered by the queue TTL left its caller with no
// reply at all, hanging until its own much longer timeout fired.
func TestExpiredRequestGetsAFastFailReply(t *testing.T) {
	pub := &fakePublisher{}
	acker := &fakeAcker{}
	d := expiredDelivery(acker, amqp.Table{"x-first-death-reason": "expired"})

	handleExpiredRequest(context.Background(), pub, d, "acct-1", quietLogger())

	require.Len(t, pub.published, 1)
	got := pub.published[0]
	assert.Equal(t, "amq.gen-replyq", got.key, "reply must go to the caller's reply queue")
	assert.Equal(t, "corr-1", got.msg.CorrelationId, "reply must carry the correlation id the caller waits on")
	assert.JSONEq(t,
		`{"status_code":504,"action":"error","data":"request expired in queue before the agent could take it"}`,
		string(got.msg.Body))
	assert.Equal(t, 1, acker.acked)
}

// A message the delivery path rejected belongs to a caller that already gave up,
// so answering it would be pure noise on the reply queue.
func TestRejectedMessageIsNotAnswered(t *testing.T) {
	pub := &fakePublisher{}
	acker := &fakeAcker{}
	d := expiredDelivery(acker, amqp.Table{"x-first-death-reason": "rejected"})

	handleExpiredRequest(context.Background(), pub, d, "acct-1", quietLogger())

	assert.Empty(t, pub.published)
	assert.Equal(t, 1, acker.acked, "it must still be settled, or it occupies the DLQ forever")
}

func TestDeliveryWithoutDeathReasonIsNotAnswered(t *testing.T) {
	pub := &fakePublisher{}
	acker := &fakeAcker{}
	d := expiredDelivery(acker, amqp.Table{})

	handleExpiredRequest(context.Background(), pub, d, "acct-1", quietLogger())

	assert.Empty(t, pub.published)
	assert.Equal(t, 1, acker.acked)
}

func TestExpiredRequestWithNoReplyAddressIsDropped(t *testing.T) {
	pub := &fakePublisher{}
	acker := &fakeAcker{}
	d := expiredDelivery(acker, amqp.Table{"x-first-death-reason": "expired"})
	d.ReplyTo = ""

	handleExpiredRequest(context.Background(), pub, d, "acct-1", quietLogger())

	assert.Empty(t, pub.published, "nothing to answer without a reply address")
	assert.Equal(t, 1, acker.acked)
}

// If the reply cannot be published the message must not be acked, or the caller
// is left hanging with the evidence thrown away.
func TestFailedReplyRequeuesRatherThanAcking(t *testing.T) {
	pub := &fakePublisher{err: assert.AnError}
	acker := &fakeAcker{}
	d := expiredDelivery(acker, amqp.Table{"x-first-death-reason": "expired"})

	handleExpiredRequest(context.Background(), pub, d, "acct-1", quietLogger())

	assert.Equal(t, 0, acker.acked)
	assert.Equal(t, 1, acker.nacked)
	assert.True(t, acker.requeued, "requeue so a healthy channel can retry the reply")
}
