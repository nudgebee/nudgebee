package tracing

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/workflow"
)

// stubWorkflowContext is a minimal workflow.Context for exercising
// ExtractToWorkflow / TraceLoggingAttrs outside a Temporal test environment.
// workflow.WithValue works on any workflow.Context implementation.
type stubWorkflowContext struct{}

func (stubWorkflowContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (stubWorkflowContext) Done() workflow.Channel      { return nil }
func (stubWorkflowContext) Err() error                  { return nil }
func (stubWorkflowContext) Value(any) any               { return nil }

func newTestWorkflowContext() workflow.Context { return stubWorkflowContext{} }

func TestTraceLoggingAttrsFromExtractedHeader(t *testing.T) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	p := &Propagator{}

	sc := testSpanContext(t)
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	header := fakeHeader{}
	require.NoError(t, p.Inject(ctx, header))

	wfCtx, err := p.ExtractToWorkflow(newTestWorkflowContext(), header)
	require.NoError(t, err)

	attrs := TraceLoggingAttrs(wfCtx)
	assert.Equal(t, []any{"trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String()}, attrs)
}

func TestTraceLoggingAttrsNoHeaderIsNil(t *testing.T) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	p := &Propagator{}

	wfCtx, err := p.ExtractToWorkflow(newTestWorkflowContext(), fakeHeader{})
	require.NoError(t, err)
	assert.Nil(t, TraceLoggingAttrs(wfCtx))
}
