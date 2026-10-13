package common

import (
	"context"
	"log/slog"
)

// dedupeHandler wraps a slog.Handler so each key appears once per line.
// Request contexts are built from a parent's GetLogger(), and each level adds
// trace_id, span_id, file and line again with logger.With. The stdlib JSON
// handler writes every copy, so a nested line carried the same keys ~7 times.
// Attrs are held here instead of passed to the inner handler, then collapsed
// on Handle: the last value wins, kept at the key's first position.
type dedupeHandler struct {
	inner slog.Handler
	attrs []slog.Attr
}

// NewDedupeHandler returns a handler that drops repeated attribute keys before
// passing each record to inner.
func NewDedupeHandler(inner slog.Handler) slog.Handler {
	return &dedupeHandler{inner: inner}
}

func (h *dedupeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *dedupeHandler) Handle(ctx context.Context, r slog.Record) error {
	if len(h.attrs) == 0 && r.NumAttrs() == 0 {
		return h.inner.Handle(ctx, r)
	}
	all := make([]slog.Attr, 0, len(h.attrs)+r.NumAttrs())
	all = append(all, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		all = append(all, a)
		return true
	})
	nr := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	nr.AddAttrs(dedupeSlogAttrs(all)...)
	return h.inner.Handle(ctx, nr)
}

func (h *dedupeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)
	return &dedupeHandler{inner: h.inner, attrs: dedupeSlogAttrs(merged)}
}

// WithGroup hands the held attrs to the inner handler first, so they stay
// outside the group; keys are then deduped separately inside the group.
func (h *dedupeHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	inner := h.inner
	if len(h.attrs) > 0 {
		inner = inner.WithAttrs(h.attrs)
	}
	return &dedupeHandler{inner: inner.WithGroup(name)}
}

// dedupeSlogAttrs keeps one attr per key: the last value, at the first
// position. Empty-key attrs (inline groups) pass through untouched. A linear
// scan, since a line carries only a handful of attrs.
func dedupeSlogAttrs(attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		if a.Key == "" {
			out = append(out, a)
			continue
		}
		found := false
		for i := range out {
			if out[i].Key == a.Key {
				out[i] = a
				found = true
				break
			}
		}
		if !found {
			out = append(out, a)
		}
	}
	return out
}
