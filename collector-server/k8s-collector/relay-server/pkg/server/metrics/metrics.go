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

	// WS_RepliesDelivered counts how every delivery the register session takes
	// off a tenant queue ends, exactly once, by delivery=:
	//   local     — reply handed to a caller blocked in this process
	//   amqp      — reply republished through the broker for another replica
	//   dropped   — reply arrived, but its in-process caller had already gone
	//   abandoned — caller gave up before the agent answered; slot released early
	//   timeout   — the budget expired with no reply from the agent
	//   expired   — the caller's deadline had already passed on arrival, so the
	//               request was never forwarded to the agent
	WS_RepliesDelivered metric.Int64Counter
)

// requestBuckets are the explicit bucket boundaries for every per-request
// duration histogram, in seconds.
//
// OTel's default boundaries — 0, 5, 10, 25 … 10000 — are chosen for values
// recorded in milliseconds. These histograms record seconds, so under the
// defaults the first real boundary is 5s and 98.9% of production requests fall
// into a single bucket: every quantile below p99 reported ~4.75s regardless of
// actual latency, which made the relay's latency unmeasurable. The boundaries
// below resolve the sub-second range where most requests live and still reach
// the 180s RELAY_HTTP_WRITE_TIMEOUT ceiling.
var requestBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 180,
}

// sessionBuckets are the boundaries for WebSocket session lifetimes, in
// seconds. Sessions live for hours, so they need a different scale entirely:
// the interesting signal is reconnect churn (minutes) versus healthy long-lived
// registrations (hours).
var sessionBuckets = []float64{
	10, 60, 300, 900, 1800, 3600, 7200, 21600, 43200, 86400,
}

// Init initializes all OTel instruments using the given meter.
// Call this once after your MeterProvider is set up.
func Init(meter metric.Meter) error {
	var err error

	RequestLatency, err = meter.Float64Histogram(
		"nb_relay_request_duration_seconds",
		metric.WithDescription("Total end-to-end HTTP handler latency"),
		metric.WithExplicitBucketBoundaries(requestBuckets...),
	)
	if err != nil {
		return err
	}

	AgentRTT, err = meter.Float64Histogram(
		"nb_relay_agent_rtt_seconds",
		metric.WithDescription("Latency from publishing RPC to agent until reply"),
		metric.WithExplicitBucketBoundaries(requestBuckets...),
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
		metric.WithExplicitBucketBoundaries(sessionBuckets...),
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

	WS_RepliesDelivered, err = meter.Int64Counter(
		"nb_relay_ws_replies_delivered_total",
		metric.WithDescription("Agent replies by delivery route: in-process or republished via AMQP"),
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
		metric.WithExplicitBucketBoundaries(requestBuckets...),
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
