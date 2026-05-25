package metrics

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	// Core HTTP/RPC metrics
	RequestLatency metric.Float64Histogram
	AgentRTT       metric.Float64Histogram
	InFlight       metric.Int64UpDownCounter
	Fallbacks      metric.Int64Counter

	// WebSocket session metrics
	WS_Sessions        metric.Int64UpDownCounter
	WS_SessionDuration metric.Float64Histogram
	WS_Messages        metric.Int64Counter
	WS_MessageErrors   metric.Int64Counter

	// Per-request WebSocket metrics
	WS_InFlightRequests metric.Int64UpDownCounter
	WS_RequestDuration  metric.Float64Histogram
	WS_RequestTimeouts  metric.Int64Counter
	WS_RequestErrors    metric.Int64Counter
)

// Init initializes all OTel instruments using the given meter.
// Call this once after your MeterProvider is set up.
func Init(meter metric.Meter) error {
	var err error

	RequestLatency, err = meter.Float64Histogram(
		"nb_relay_request_duration_seconds",
		metric.WithDescription("Total end-to-end HTTP handler latency"),
	)
	if err != nil {
		return err
	}

	AgentRTT, err = meter.Float64Histogram(
		"nb_relay_agent_rtt_seconds",
		metric.WithDescription("Latency from publishing RPC to agent until reply"),
	)
	if err != nil {
		return err
	}

	InFlight, err = meter.Int64UpDownCounter(
		"nb_relay_inflight_requests",
		metric.WithDescription("Number of in-flight RPC calls"),
	)
	if err != nil {
		return err
	}

	Fallbacks, err = meter.Int64Counter(
		"nb_relay_request_fallbacks_total",
		metric.WithDescription("Count of HTTP fallbacks to direct HTTP"),
	)
	if err != nil {
		return err
	}

	WS_Sessions, err = meter.Int64UpDownCounter(
		"nb_relay_ws_sessions",
		metric.WithDescription("Number of active WebSocket /register sessions"),
	)
	if err != nil {
		return err
	}

	WS_SessionDuration, err = meter.Float64Histogram(
		"nb_relay_ws_session_duration_seconds",
		metric.WithDescription("Duration of WebSocket /register sessions"),
	)
	if err != nil {
		return err
	}

	WS_Messages, err = meter.Int64Counter(
		"nb_relay_ws_messages_total",
		metric.WithDescription("Total messages forwarded over WebSocket"),
	)
	if err != nil {
		return err
	}

	WS_MessageErrors, err = meter.Int64Counter(
		"nb_relay_ws_message_errors_total",
		metric.WithDescription("Total errors encountered forwarding WebSocket messages"),
	)
	if err != nil {
		return err
	}

	// Initialize per-request WebSocket metrics
	WS_InFlightRequests, err = meter.Int64UpDownCounter(
		"nb_relay_ws_requests_in_flight",
		metric.WithDescription("Number of in-flight WebSocket requests"),
	)
	if err != nil {
		return err
	}

	WS_RequestDuration, err = meter.Float64Histogram(
		"nb_relay_ws_request_duration_seconds",
		metric.WithDescription("Duration of WebSocket request round-trips"),
	)
	if err != nil {
		return err
	}

	WS_RequestTimeouts, err = meter.Int64Counter(
		"nb_relay_ws_request_timeouts_total",
		metric.WithDescription("Count of WebSocket request timeouts"),
	)
	if err != nil {
		return err
	}

	WS_RequestErrors, err = meter.Int64Counter(
		"nb_relay_ws_request_errors_total",
		metric.WithDescription("Count of WebSocket request errors"),
	)
	if err != nil {
		return err
	}

	return nil
}

// AttrAccount returns a KeyValue for tagging by account ID.
func AttrAccount(accountID string) attribute.KeyValue {
	return attribute.String("account_id", accountID)
}
