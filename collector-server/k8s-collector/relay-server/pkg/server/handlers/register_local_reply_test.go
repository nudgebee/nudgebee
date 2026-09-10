package handlers

import (
	"context"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"

	"nudgebee/relay-server/pkg/mq"
)

// localReplyRPC records whether DeliverLocal was reached, so the tests can tell
// "refused the handover" from "never attempted it".
type localReplyRPC struct {
	instanceID string
	deliver    bool
	attempted  bool
	corrID     string
}

func (f *localReplyRPC) Call(ctx context.Context, exchange, routingKey string, payload []byte, corrID string) ([]byte, error) {
	return nil, nil
}
func (f *localReplyRPC) InstanceID() string { return f.instanceID }
func (f *localReplyRPC) DeliverLocal(corrID string, body []byte) bool {
	f.attempted = true
	f.corrID = corrID
	return f.deliver
}
func (f *localReplyRPC) Close() {}

func delivery(headers amqp.Table) amqp.Delivery {
	return amqp.Delivery{CorrelationId: "corr-1", Headers: headers}
}

func TestDeliverReplyLocally(t *testing.T) {
	tests := []struct {
		name string
		rpc  *localReplyRPC
		// headers on the delivery being replied to
		headers amqp.Table
		// a caller in this process took the reply
		wantDelivered bool
		// this process published the request, so the reply is owed to nobody
		// else and must never be republished
		wantLocal bool
		// the handover was attempted at all
		wantAttempt bool
	}{
		{
			name:          "request published by this process is handed over",
			rpc:           &localReplyRPC{instanceID: "me", deliver: true},
			headers:       amqp.Table{mq.HeaderRelayInstance: "me"},
			wantDelivered: true,
			wantLocal:     true,
			wantAttempt:   true,
		},
		{
			// Owed to nobody: the caller is gone and no other replica can be
			// waiting, so the caller drops the reply instead of republishing.
			name:          "caller in this process already gave up",
			rpc:           &localReplyRPC{instanceID: "me", deliver: false},
			headers:       amqp.Table{mq.HeaderRelayInstance: "me"},
			wantDelivered: false,
			wantLocal:     true,
			wantAttempt:   true,
		},
		{
			// The guard that stops a correlation ID raised on another replica
			// waking an unrelated local request.
			name:          "request published by another replica is never matched",
			rpc:           &localReplyRPC{instanceID: "me", deliver: true},
			headers:       amqp.Table{mq.HeaderRelayInstance: "other-replica"},
			wantDelivered: false,
			wantLocal:     false,
			wantAttempt:   false,
		},
		{
			name:          "request from an older relay carries no instance header",
			rpc:           &localReplyRPC{instanceID: "me", deliver: true},
			headers:       amqp.Table{},
			wantDelivered: false,
			wantLocal:     false,
			wantAttempt:   false,
		},
		{
			name:          "header present but not a string",
			rpc:           &localReplyRPC{instanceID: "me", deliver: true},
			headers:       amqp.Table{mq.HeaderRelayInstance: 42},
			wantDelivered: false,
			wantLocal:     false,
			wantAttempt:   false,
		},
		{
			name:          "nil headers",
			rpc:           &localReplyRPC{instanceID: "me", deliver: true},
			headers:       nil,
			wantDelivered: false,
			wantLocal:     false,
			wantAttempt:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delivered, isLocal := deliverReplyLocally(tt.rpc, delivery(tt.headers), []byte(`{"ok":true}`))
			assert.Equal(t, tt.wantDelivered, delivered, "a caller took the reply")
			assert.Equal(t, tt.wantLocal, isLocal, "this process published the request")
			assert.Equal(t, tt.wantAttempt, tt.rpc.attempted,
				"whether the handover was attempted at all")
			if tt.wantAttempt {
				assert.Equal(t, "corr-1", tt.rpc.corrID)
			}
		})
	}
}

func TestDeliverReplyLocallyWithoutRPCClient(t *testing.T) {
	delivered, isLocal := deliverReplyLocally(nil, delivery(amqp.Table{mq.HeaderRelayInstance: "me"}), nil)
	assert.False(t, delivered)
	assert.False(t, isLocal, "a nil client must fall back to publishing rather than panic or drop")
}
