package egressfilter

import "context"

// FilterEvent is the structured record the wrapper emits whenever a Scan
// returns hits. One FilterEvent per outbound LLM call (no event when the
// Scan is clean). Callers attach a reporter via WithFilterEventReporter
// before invoking GenerateContent and recover the FilterEvent(s) after the
// call to persist or surface them.
//
// Name is intentionally prefixed (vs. just "Event") because the broader
// codebase already has an "event" domain (anomaly events, agent events,
// etc.); FilterEvent keeps grep, IDE navigation, and reading code free of
// ambiguity.
//
// The shape is forward-compatible. Audit and enforce modes populate only
// Hits ("what we found, where"). Future redact / tokenize modes additionally
// populate a parallel Redactions slice ("what we did about each hit") that
// aligns with Hits by index. Detection and action concerns stay in separate
// slices so callers that only need "what fired" don't have to walk an
// extra field, and the future tokenize mode can grow per-action context
// (e.g. rehydration token pointer) without changing the Hit contract.
//
// Deliberate non-fields:
//
//   - Matched values. They ARE the secret; we will not pass them through.
//   - The serialized payload. Too large; verify via logs or local replay.
//
// What IS recorded enables:
//
//   - Block / audit telemetry — AuditID, Mode, RuleIDs.
//   - Per-hit positional detail — Hits[].Start/End for redaction or audit.
//   - Future redact / tokenize — Redactions[].Placeholder for "what we put
//     back in" without ever recording "what we took out".
type FilterEvent struct {
	// AuditID correlates the event with the structured log line emitted at
	// detection time. Format: "egress-<12 lowercase hex>".
	AuditID string `json:"audit_id"`

	// Mode is the action taken on this call — "audit" (forwarded with a log
	// line), "enforce" (blocked, error returned), or future "redact" /
	// "tokenize".
	Mode Mode `json:"mode"`

	// PayloadBytes is the length of the scanned payload after serialization.
	// Useful for spotting unusually large LLM calls correlated with hits.
	PayloadBytes int `json:"payload_bytes"`

	// Hits is the per-detection slice — "what we found, where". Same shape
	// Scan returns. Always populated when the event fires (one entry per
	// match across all registered Filters).
	Hits []Hit `json:"hits"`

	// Redactions is the per-action slice — "what we did about each hit".
	// Populated only when Mode is "redact" or future "tokenize"; nil under
	// audit/enforce. Same length as Hits when populated; entries align by
	// index (Redactions[i] is the action taken on Hits[i]).
	Redactions []Redaction `json:"redactions,omitempty"`

	// HitCount and RuleIDs are derived from Hits, cached on the struct so
	// downstream consumers (UI, dashboards) don't have to recompute.
	HitCount int      `json:"hit_count"`
	RuleIDs  []string `json:"rule_ids"`
}

// newFilterEvent constructs a FilterEvent from a Scan Result + per-call
// header. Centralizes the derive-the-aggregates logic so the wrapper and
// any future caller emit identically shaped events.
func newFilterEvent(auditID string, mode Mode, payloadBytes int, r Result) FilterEvent {
	return FilterEvent{
		AuditID:      auditID,
		Mode:         mode,
		PayloadBytes: payloadBytes,
		Hits:         r.Hits,
		HitCount:     len(r.Hits),
		RuleIDs:      r.RuleIDs(),
	}
}

// filterEventReporterKey is the unexported context key for the per-request
// reporter callback. Unexported so only this package can read/write it; the
// public surface is WithFilterEventReporter + the wrapper's internal lookup.
type filterEventReporterKey struct{}

// WithFilterEventReporter attaches a callback to ctx that the wrapper
// invokes once per outbound LLM call that produces hits. The callback
// receives the fully-populated FilterEvent; the caller is responsible for
// collecting events (one message may make several LLM calls) and persisting
// them.
//
// The callback runs in the wrapper goroutine and must not block — accumulate
// quickly and process later. A nil callback is silently ignored.
//
// Calling WithFilterEventReporter does not attach any retry, persistence,
// or thread-safety guarantees. If the wrapper is shared across goroutines
// (it is, because GetLLMModel caches the wrapped instance), the caller's
// callback must be safe to invoke concurrently.
func WithFilterEventReporter(ctx context.Context, fn func(FilterEvent)) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, filterEventReporterKey{}, fn)
}

// reporterFromContext returns the registered callback, or nil if none. Used
// by the wrapper; not exported because no other caller needs it.
func reporterFromContext(ctx context.Context) func(FilterEvent) {
	if ctx == nil {
		return nil
	}
	v, ok := ctx.Value(filterEventReporterKey{}).(func(FilterEvent))
	if !ok {
		return nil
	}
	return v
}
