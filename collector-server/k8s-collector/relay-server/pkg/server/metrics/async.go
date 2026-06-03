package metrics

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// MetricEvent represents a single metric operation to be processed asynchronously
type MetricEvent struct {
	Type       MetricType
	Value      interface{}
	Attributes []attribute.KeyValue
}

type MetricType int

const (
	// Counter metrics
	FallbacksInc MetricType = iota
	WSMessagesInc
	WSMessageErrorsInc
	WSRequestTimeoutsInc
	WSRequestErrorsInc

	// UpDownCounter metrics
	InFlightInc
	InFlightDec
	WSSessionsInc
	WSSessionsDec
	WSInFlightRequestsInc
	WSInFlightRequestsDec

	// Histogram metrics
	RequestLatencyRecord
	AgentRTTRecord
	WSSessionDurationRecord
	WSRequestDurationRecord
)

// AsyncMetrics provides non-blocking metric operations
type AsyncMetrics struct {
	buffer    chan MetricEvent
	batchSize int
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewAsyncMetrics creates a new async metrics collector
func NewAsyncMetrics(bufferSize, batchSize int, flushInterval time.Duration) *AsyncMetrics {
	ctx, cancel := context.WithCancel(context.Background())

	am := &AsyncMetrics{
		buffer:    make(chan MetricEvent, bufferSize),
		batchSize: batchSize,
		ctx:       ctx,
		cancel:    cancel,
	}

	am.wg.Add(1)
	go am.processMetrics(flushInterval)

	return am
}

// processMetrics runs the background goroutine that processes metric events
func (am *AsyncMetrics) processMetrics(flushInterval time.Duration) {
	defer am.wg.Done()

	batch := make([]MetricEvent, 0, am.batchSize)
	flushTimer := time.NewTimer(flushInterval)

	for {
		select {
		case <-am.ctx.Done():
			// Flush remaining batch and exit
			am.flushBatch(batch)
			return

		case event := <-am.buffer:
			batch = append(batch, event)
			if len(batch) >= am.batchSize {
				am.flushBatch(batch)
				batch = batch[:0] // Reset slice but keep capacity
				flushTimer.Reset(flushInterval)
			}

		case <-flushTimer.C:
			if len(batch) > 0 {
				am.flushBatch(batch)
				batch = batch[:0]
			}
			flushTimer.Reset(flushInterval)
		}
	}
}

// flushBatch processes a batch of metric events
func (am *AsyncMetrics) flushBatch(batch []MetricEvent) {
	ctx := context.Background()

	for _, event := range batch {
		switch event.Type {
		// Counter increments
		case FallbacksInc:
			Fallbacks.Add(ctx, event.Value.(int64), metric.WithAttributes(event.Attributes...))
		case WSMessagesInc:
			WS_Messages.Add(ctx, event.Value.(int64), metric.WithAttributes(event.Attributes...))
		case WSMessageErrorsInc:
			WS_MessageErrors.Add(ctx, event.Value.(int64), metric.WithAttributes(event.Attributes...))
		case WSRequestTimeoutsInc:
			WS_RequestTimeouts.Add(ctx, event.Value.(int64), metric.WithAttributes(event.Attributes...))
		case WSRequestErrorsInc:
			WS_RequestErrors.Add(ctx, event.Value.(int64), metric.WithAttributes(event.Attributes...))

		// UpDownCounter operations
		case InFlightInc:
			InFlight.Add(ctx, event.Value.(int64), metric.WithAttributes(event.Attributes...))
		case InFlightDec:
			InFlight.Add(ctx, -event.Value.(int64), metric.WithAttributes(event.Attributes...))
		case WSSessionsInc:
			WS_Sessions.Add(ctx, event.Value.(int64), metric.WithAttributes(event.Attributes...))
		case WSSessionsDec:
			WS_Sessions.Add(ctx, -event.Value.(int64), metric.WithAttributes(event.Attributes...))
		case WSInFlightRequestsInc:
			WS_InFlightRequests.Add(ctx, event.Value.(int64), metric.WithAttributes(event.Attributes...))
		case WSInFlightRequestsDec:
			WS_InFlightRequests.Add(ctx, -event.Value.(int64), metric.WithAttributes(event.Attributes...))

		// Histogram records
		case RequestLatencyRecord:
			RequestLatency.Record(ctx, event.Value.(float64), metric.WithAttributes(event.Attributes...))
		case AgentRTTRecord:
			AgentRTT.Record(ctx, event.Value.(float64), metric.WithAttributes(event.Attributes...))
		case WSSessionDurationRecord:
			WS_SessionDuration.Record(ctx, event.Value.(float64), metric.WithAttributes(event.Attributes...))
		case WSRequestDurationRecord:
			WS_RequestDuration.Record(ctx, event.Value.(float64), metric.WithAttributes(event.Attributes...))
		}
	}
}

// Shutdown gracefully shuts down the async metrics collector
func (am *AsyncMetrics) Shutdown() {
	am.cancel()
	am.wg.Wait()
	close(am.buffer)
}

// Non-blocking metric operations

func (am *AsyncMetrics) RecordRequestLatency(value float64, attrs ...attribute.KeyValue) {
	select {
	case am.buffer <- MetricEvent{RequestLatencyRecord, value, attrs}:
	default:
		// Buffer full, drop metric (or could log warning)
	}
}

func (am *AsyncMetrics) RecordAgentRTT(value float64, attrs ...attribute.KeyValue) {
	select {
	case am.buffer <- MetricEvent{AgentRTTRecord, value, attrs}:
	default:
	}
}

func (am *AsyncMetrics) IncInFlight(attrs ...attribute.KeyValue) {
	select {
	case am.buffer <- MetricEvent{InFlightInc, int64(1), attrs}:
	default:
	}
}

func (am *AsyncMetrics) DecInFlight(attrs ...attribute.KeyValue) {
	select {
	case am.buffer <- MetricEvent{InFlightDec, int64(1), attrs}:
	default:
	}
}

func (am *AsyncMetrics) IncFallbacks(attrs ...attribute.KeyValue) {
	select {
	case am.buffer <- MetricEvent{FallbacksInc, int64(1), attrs}:
	default:
	}
}

func (am *AsyncMetrics) IncWSRequestTimeouts(attrs ...attribute.KeyValue) {
	select {
	case am.buffer <- MetricEvent{WSRequestTimeoutsInc, int64(1), attrs}:
	default:
	}
}

func (am *AsyncMetrics) IncWSRequestErrors(attrs ...attribute.KeyValue) {
	select {
	case am.buffer <- MetricEvent{WSRequestErrorsInc, int64(1), attrs}:
	default:
	}
}

func (am *AsyncMetrics) IncWSMessages(attrs ...attribute.KeyValue) {
	select {
	case am.buffer <- MetricEvent{WSMessagesInc, int64(1), attrs}:
	default:
	}
}

func (am *AsyncMetrics) IncWSMessageErrors(attrs ...attribute.KeyValue) {
	select {
	case am.buffer <- MetricEvent{WSMessageErrorsInc, int64(1), attrs}:
	default:
	}
}

func (am *AsyncMetrics) IncWSSessions(attrs ...attribute.KeyValue) {
	select {
	case am.buffer <- MetricEvent{WSSessionsInc, int64(1), attrs}:
	default:
	}
}

func (am *AsyncMetrics) DecWSSessions(attrs ...attribute.KeyValue) {
	select {
	case am.buffer <- MetricEvent{WSSessionsDec, int64(1), attrs}:
	default:
	}
}

func (am *AsyncMetrics) IncWSInFlightRequests(attrs ...attribute.KeyValue) {
	select {
	case am.buffer <- MetricEvent{WSInFlightRequestsInc, int64(1), attrs}:
	default:
	}
}

func (am *AsyncMetrics) DecWSInFlightRequests(attrs ...attribute.KeyValue) {
	select {
	case am.buffer <- MetricEvent{WSInFlightRequestsDec, int64(1), attrs}:
	default:
	}
}

func (am *AsyncMetrics) RecordWSSessionDuration(value float64, attrs ...attribute.KeyValue) {
	select {
	case am.buffer <- MetricEvent{WSSessionDurationRecord, value, attrs}:
	default:
	}
}

func (am *AsyncMetrics) RecordWSRequestDuration(value float64, attrs ...attribute.KeyValue) {
	select {
	case am.buffer <- MetricEvent{WSRequestDurationRecord, value, attrs}:
	default:
	}
}

// Global async metrics instance
var AsyncMetricsInstance *AsyncMetrics

// InitAsync initializes the global async metrics instance
func InitAsync(bufferSize, batchSize int, flushInterval time.Duration) {
	AsyncMetricsInstance = NewAsyncMetrics(bufferSize, batchSize, flushInterval)
}

// ShutdownAsync gracefully shuts down async metrics
func ShutdownAsync() {
	if AsyncMetricsInstance != nil {
		AsyncMetricsInstance.Shutdown()
	}
}
