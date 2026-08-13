package mq

import (
	"context"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
)

// amqpHeaderCarrier adapts an amqp.Table to the OpenTelemetry
// propagation.TextMapCarrier interface so the W3C trace context (traceparent /
// tracestate / baggage) can be injected into RabbitMQ message headers on
// publish and extracted from them on consume. This keeps a distributed trace
// intact across the relay-server -> in-cluster agent RabbitMQ hop.
type amqpHeaderCarrier amqp.Table

// Get returns the value for the given header key, or "" if absent. AMQP header
// values arrive as string or, depending on the publisher's client library /
// broker encoding, []byte — both are handled so extraction doesn't silently fail.
func (c amqpHeaderCarrier) Get(key string) string {
	if v, ok := c[key]; ok {
		switch val := v.(type) {
		case string:
			return val
		case []byte:
			return string(val)
		}
	}
	return ""
}

// Set stores the given header key/value.
func (c amqpHeaderCarrier) Set(key, value string) {
	c[key] = value
}

// Keys lists the header keys currently held by the carrier.
func (c amqpHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// ExtractTraceContext returns ctx enriched with the W3C trace context
// (traceparent / tracestate) carried in the AMQP message headers, as injected
// by the publish side (see rpc_client.go). When the headers carry no valid
// trace context, ctx is returned unchanged.
func ExtractTraceContext(ctx context.Context, headers amqp.Table) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, amqpHeaderCarrier(headers))
}
