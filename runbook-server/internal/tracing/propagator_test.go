package tracing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	commonpb "go.temporal.io/api/common/v1"
)

// fakeHeader implements workflow.HeaderWriter and workflow.HeaderReader over a
// plain map for roundtrip tests.
type fakeHeader map[string]*commonpb.Payload

func (h fakeHeader) Set(key string, value *commonpb.Payload) { h[key] = value }
func (h fakeHeader) Get(key string) (*commonpb.Payload, bool) {
	v, ok := h[key]
	return v, ok
}
func (h fakeHeader) ForEachKey(handler func(string, *commonpb.Payload) error) error {
	for k, v := range h {
		if err := handler(k, v); err != nil {
			return err
		}
	}
	return nil
}

func testSpanContext(t *testing.T) trace.SpanContext {
	t.Helper()
	traceID, err := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("0123456789abcdef")
	require.NoError(t, err)
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
}

func TestPropagatorInjectExtractRoundtrip(t *testing.T) {
	// The propagator relies on the global text-map propagator, which main()
	// installs via initOtel; install the same W3C one for the test.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	p := &Propagator{}

	sc := testSpanContext(t)
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	header := fakeHeader{}
	require.NoError(t, p.Inject(ctx, header))
	_, ok := header[headerKey]
	require.True(t, ok, "expected trace header to be written")

	out, err := p.Extract(context.Background(), header)
	require.NoError(t, err)
	got := trace.SpanContextFromContext(out)
	assert.True(t, got.IsValid())
	assert.Equal(t, sc.TraceID(), got.TraceID())
}

func TestPropagatorNoSpanIsNoop(t *testing.T) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	p := &Propagator{}

	header := fakeHeader{}
	require.NoError(t, p.Inject(context.Background(), header))
	assert.Empty(t, header, "no span in ctx must not write a header")

	// Extract from an empty header leaves the context span-less.
	out, err := p.Extract(context.Background(), header)
	require.NoError(t, err)
	assert.False(t, trace.SpanContextFromContext(out).IsValid())
}

func TestPropagatorMalformedHeaderIsIgnored(t *testing.T) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	p := &Propagator{}

	header := fakeHeader{headerKey: &commonpb.Payload{Data: []byte("not-json")}}
	out, err := p.Extract(context.Background(), header)
	require.NoError(t, err)
	assert.False(t, trace.SpanContextFromContext(out).IsValid())
}
