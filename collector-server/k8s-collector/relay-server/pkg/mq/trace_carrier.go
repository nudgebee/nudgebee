package mq

import (
	amqp "github.com/rabbitmq/amqp091-go"
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
