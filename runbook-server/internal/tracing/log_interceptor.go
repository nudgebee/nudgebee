package tracing

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

// NewLogInterceptor returns a worker interceptor that stamps trace_id/span_id
// onto every logger obtained via workflow.GetLogger / activity.GetLogger, so
// workflow and activity log lines correlate with the rest of the platform in
// Loki. The ids come from the trace context carried by Propagator: the stashed
// header carrier on the workflow side, the extracted span context on the
// activity side. Loggers are left untouched when no trace was propagated —
// no all-zero ids.
func NewLogInterceptor() interceptor.WorkerInterceptor {
	return &logInterceptor{}
}

type logInterceptor struct {
	interceptor.WorkerInterceptorBase
}

func (i *logInterceptor) InterceptWorkflow(ctx workflow.Context, next interceptor.WorkflowInboundInterceptor) interceptor.WorkflowInboundInterceptor {
	wi := &logWorkflowInbound{}
	wi.Next = next
	return wi
}

func (i *logInterceptor) InterceptActivity(ctx context.Context, next interceptor.ActivityInboundInterceptor) interceptor.ActivityInboundInterceptor {
	ai := &logActivityInbound{}
	ai.Next = next
	return ai
}

type logWorkflowInbound struct {
	interceptor.WorkflowInboundInterceptorBase
}

func (w *logWorkflowInbound) Init(outbound interceptor.WorkflowOutboundInterceptor) error {
	wo := &logWorkflowOutbound{}
	wo.Next = outbound
	return w.Next.Init(wo)
}

type logWorkflowOutbound struct {
	interceptor.WorkflowOutboundInterceptorBase
}

func (w *logWorkflowOutbound) GetLogger(ctx workflow.Context) log.Logger {
	logger := w.Next.GetLogger(ctx)
	if attrs := TraceLoggingAttrs(ctx); len(attrs) > 0 {
		logger = log.With(logger, attrs...)
	}
	return logger
}

type logActivityInbound struct {
	interceptor.ActivityInboundInterceptorBase
}

func (a *logActivityInbound) Init(outbound interceptor.ActivityOutboundInterceptor) error {
	ao := &logActivityOutbound{}
	ao.Next = outbound
	return a.Next.Init(ao)
}

type logActivityOutbound struct {
	interceptor.ActivityOutboundInterceptorBase
}

func (a *logActivityOutbound) GetLogger(ctx context.Context) log.Logger {
	logger := a.Next.GetLogger(ctx)
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		logger = log.With(logger, "trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
	}
	return logger
}
