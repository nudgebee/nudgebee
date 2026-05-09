package security

import (
	"context"
	"io"
	"log/slog"
	"nudgebee/llm/common"
	"runtime"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type RequestContext struct {
	securityContext *SecurityContext
	logger          *slog.Logger
	tracer          trace.Tracer
	meter           metric.Meter
	context         context.Context
}

func (rc *RequestContext) GetSecurityContext() *SecurityContext {
	return rc.securityContext
}

func (rc *RequestContext) GetLogger() *slog.Logger {
	if rc.logger == nil {
		rc.logger = slog.Default()
	}
	_, file, line, _ := runtime.Caller(1)
	return rc.logger.With(
		slog.String("file", file),
		slog.Int("line", line),
	)
}

func (rc *RequestContext) GetTracer() trace.Tracer {
	return rc.tracer
}

func (rc *RequestContext) GetMeter() metric.Meter {
	return rc.meter
}

func (rc *RequestContext) GetContext() context.Context {
	return rc.context
}

func (rc *RequestContext) GetTraceId() string {
	span := trace.SpanFromContext(rc.context)
	return span.SpanContext().TraceID().String()
}

func NewRequestContext(context context.Context, securityContext *SecurityContext, logger *slog.Logger, tracer trace.Tracer, meter metric.Meter) *RequestContext {
	if logger == nil {
		logger = slog.Default()
	}
	if tracer == nil {
		tracer = otel.GetTracerProvider().Tracer("nudgebee-llm")
	}
	return &RequestContext{context: context, securityContext: securityContext, logger: logger, tracer: tracer, meter: meter}
}

func NewRequestContextForSuperAdmin() *RequestContext {
	t := otel.GetTracerProvider().Tracer("nudgebee-llm")
	return &RequestContext{context: context.Background(), securityContext: NewSecurityContextForSuperAdmin(), logger: slog.Default(), tracer: t, meter: nil}
}

func NewRequestContextForTenantAccountAdmin(tenant string, user string, accountId []string) *RequestContext {
	sc := NewSecurityContextForTenantAccountAdmin(tenant, user, accountId)
	t := otel.GetTracerProvider().Tracer("nudgebee-llm")
	return &RequestContext{context: context.Background(), securityContext: sc, logger: slog.Default(), tracer: t, meter: nil}
}

func NewRequestContextForTenantAdmin(tenantId string) *RequestContext {
	sc := NewSecurityContextForTenantAdmin(tenantId)
	t := otel.GetTracerProvider().Tracer("nudgebee-llm")
	return &RequestContext{context: context.Background(), securityContext: sc, logger: slog.Default(), tracer: t, meter: nil}
}

// CustomJSONHandler restructures log JSON output.
type CustomJSONHandler struct {
	h     slog.Handler
	attrs []slog.Attr
	w     io.Writer
	mu    sync.Mutex // protects writes to w
}

// NewCustomJSONHandler returns a new CustomJSONHandler writing to w.
func NewCustomJSONHandler(w io.Writer, opts *slog.HandlerOptions) slog.Handler {
	return &CustomJSONHandler{
		h: slog.NewJSONHandler(w, opts),
		w: w,
	}
}

func (h *CustomJSONHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.h.Enabled(ctx, level)
}

func (h *CustomJSONHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	allAttrs := h.collectAttrs(r)
	preMsg, postMsg := h.partitionAttrs(allAttrs)
	traceID, spanID, otherPreMsg := h.extractTraceAndSpan(preMsg)

	h.writeJSONLog(r, traceID, spanID, otherPreMsg, postMsg)
	return nil
}

// collectAttrs merges handler and record attrs, marshaling values to JSON.
func (h *CustomJSONHandler) collectAttrs(r slog.Record) [][2]string {
	allAttrs := make([][2]string, 0, len(h.attrs)+r.NumAttrs())
	for _, a := range h.attrs {
		val, err := common.MarshalJson(a.Value.Any())
		if err != nil {
			val = []byte(`"<marshal_error>"`)
		}
		allAttrs = append(allAttrs, [2]string{a.Key, string(val)})
	}
	if r.NumAttrs() > 0 {
		r.Attrs(func(a slog.Attr) bool {
			val, err := common.MarshalJson(a.Value.Any())
			if err != nil {
				val = []byte(`"<marshal_error>"`)
			}
			allAttrs = append(allAttrs, [2]string{a.Key, string(val)})
			return true
		})
	}
	return allAttrs
}

// partitionAttrs splits attributes into preMsg and postMsg.
func (h *CustomJSONHandler) partitionAttrs(allAttrs [][2]string) (preMsg, postMsg [][2]string) {
	preMsgKeys := map[string]bool{"file": true, "line": true, "trace_id": true, "span_id": true, "session_id": true, "user_id": true, "tenant_id": true, "account_id": true, "conversation_id": true, "agent_id": true, "message_id": true, "tool_call_id": true, "tool_name": true, "tool_id": true}
	for _, kv := range allAttrs {
		if preMsgKeys[kv[0]] {
			preMsg = append(preMsg, kv)
		} else {
			postMsg = append(postMsg, kv)
		}
	}
	return
}

// extractTraceAndSpan pulls trace_id and span_id from preMsg, returning them and the rest.
func (h *CustomJSONHandler) extractTraceAndSpan(preMsg [][2]string) (traceID, spanID string, otherPreMsg [][2]string) {
	for _, kv := range preMsg {
		switch kv[0] {
		case "trace_id":
			traceID = kv[1]
		case "span_id":
			spanID = kv[1]
		default:
			otherPreMsg = append(otherPreMsg, kv)
		}
	}
	return
}

// writeJSONLog writes the log record as JSON to the handler's writer.
func (h *CustomJSONHandler) writeJSONLog(r slog.Record, traceID, spanID string, otherPreMsg, postMsg [][2]string) {
	write := func(s string) {
		_, _ = io.WriteString(h.w, s)
	}
	write("{")
	write("\"time\":\"")
	write(r.Time.Local().Format("2006-01-02T15:04:05.000Z07:00"))
	write("\"")
	if traceID != "" {
		write(",\"trace_id\":")
		write(traceID)
	}
	if spanID != "" {
		write(",\"span_id\":")
		write(spanID)
	}
	write(",\"level\":\"")
	write(r.Level.String())
	for _, kv := range otherPreMsg {
		write(",\"")
		write(kv[0])
		write("\":")
		write(kv[1])
	}
	write(",\"msg\":")
	msg, err := common.MarshalJson(r.Message)
	if err != nil {
		msg = []byte(`"<marshal_error>"`)
	}
	_, _ = h.w.Write(msg)
	for _, kv := range postMsg {
		write(",\"")
		write(kv[0])
		write("\":")
		write(kv[1])
	}
	write("}\n")
}

func (h *CustomJSONHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := append([]slog.Attr{}, h.attrs...)
	newAttrs = append(newAttrs, attrs...)
	return &CustomJSONHandler{h: h.h.WithAttrs(attrs), attrs: newAttrs, w: h.w}
}

func (h *CustomJSONHandler) WithGroup(name string) slog.Handler {
	// Copy attrs and wrap the underlying handler with the group
	return &CustomJSONHandler{
		h:     h.h.WithGroup(name),
		attrs: h.attrs, // preserve parent attrs
		w:     h.w,
	}
}
