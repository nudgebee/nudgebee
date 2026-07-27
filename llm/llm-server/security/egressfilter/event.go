package egressfilter

import (
	"context"
	"strings"
	"sync"
)

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

	// Mode is the operator-configured mode for this call — "detect"
	// (forwarded with a log line), "enforce" (blocked, error returned), or
	// future "redact" / "tokenize". Note this is the *requested* mode; the
	// Action actually taken is decided by resolveAction (see action_gate.go).
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

	// HitSources is the distinct sorted set of Source values across Hits
	// (e.g. ["tool_result", "user"]). Lets dashboards filter "show me only
	// user-source hits" without walking every Hit. Empty when no hit was
	// tagged with a source — older clients producing untagged hits, or
	// hits that fell on a separator byte.
	HitSources []Source `json:"hit_sources,omitempty"`

	// AgentName carries the name of the running agent (e.g. "k8s_debug")
	// when the upstream wire-up has attached it to ctx via WithAgentName.
	// Empty when unknown. Enables agent-scoped dashboard queries and is
	// the structural prerequisite for a future per-agent suppression
	// policy phase.
	AgentName string `json:"agent_name,omitempty"`
}

// newFilterEvent constructs a FilterEvent from a Scan Result + per-call
// header. Centralizes the derive-the-aggregates logic so the wrapper and
// any future caller emit identically shaped events.
func newFilterEvent(auditID string, mode Mode, payloadBytes int, r Result, agentName string) FilterEvent {
	return FilterEvent{
		AuditID:      auditID,
		Mode:         mode,
		PayloadBytes: payloadBytes,
		Hits:         r.Hits,
		HitCount:     len(r.Hits),
		RuleIDs:      r.RuleIDs(),
		HitSources:   distinctSources(r.Hits),
		AgentName:    agentName,
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

// reportScopeKey is the unexported context key for the per-turn reporting
// scope. Unexported so only this package reads it; the public surface is
// WithReportBaseline + the wrapper's internal lookup.
type reportScopeKey struct{}

// reportScope decides, for one conversation turn, which detected secrets are
// NEW to the current message and therefore worth counting on its badge. A
// secret is NOT new when it either:
//
//   - already appears in the pre-turn conversation history (the planner folds
//     that history back into every outbound prompt, so a secret pasted on an
//     earlier turn is re-detected every turn — the cross-turn carry-over), or
//   - was already reported earlier in THIS turn (one turn drives several LLM
//     calls, each re-scanning the same user text — the per-call multiplication).
//
// The scope is stateful: it accumulates the secrets it has reported so later
// calls in the same turn suppress them, collapsing the per-message count to
// the number of DISTINCT new secrets. filter is atomic so parallel plan
// execution can't double-report the same secret.
//
// The matched secret bytes live only in this in-memory, per-turn scope for the
// lifetime of the turn; they are never logged or persisted (the scope is GC'd
// when the turn ends). Reporting only — block / redact still evaluate the full
// outbound payload, so a secret re-sent in history is still blocked/redacted.
type reportScope struct {
	mu      sync.Mutex
	history string
	seen    map[string]struct{}
}

func newReportScope(history string) *reportScope {
	return &reportScope{history: history, seen: make(map[string]struct{})}
}

// filter returns the subset of hits that are new to this message (see
// reportScope) and marks them seen, plus an index-aligned mask (mask[i] == true
// ⇔ hits[i] kept) so a parallel slice — e.g. the Redactor's redactions — can be
// filtered identically. Hits with malformed offsets are kept: a reporting fix
// must never hide a real detection, so it fails toward counting.
func (s *reportScope) filter(hits []Hit, payload string) ([]Hit, []bool) {
	mask := make([]bool, len(hits))
	kept := make([]Hit, 0, len(hits))
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, h := range hits {
		if h.Start < 0 || h.End > len(payload) || h.Start >= h.End {
			mask[i] = true
			kept = append(kept, h)
			continue
		}
		secret := payload[h.Start:h.End]
		if _, dup := s.seen[secret]; dup {
			continue
		}
		if s.history != "" && strings.Contains(s.history, secret) {
			continue
		}
		// Clone before retaining: payload[a:b] shares the (large) payload
		// backing array, and seen lives for the whole turn — storing the
		// slice header would pin the entire payload in memory until the turn
		// ends. The clone keeps only the secret's bytes.
		s.seen[strings.Clone(secret)] = struct{}{}
		mask[i] = true
		kept = append(kept, h)
	}
	return kept, mask
}

// WithReportBaseline attaches a per-turn reporting scope seeded with the
// conversation text that existed BEFORE this turn — prior user/assistant
// messages (and any distilled memory) that the planner folds back into every
// outbound prompt as history.
//
// It must be attached once per turn (a fresh scope per turn), before the
// turn's LLM calls. The seed suppresses cross-turn carry-over; the scope's
// accumulated state suppresses within-turn per-call duplicates. An empty seed
// is still attached so within-turn de-duplication works on the first turn.
func WithReportBaseline(ctx context.Context, baseline string) context.Context {
	return context.WithValue(ctx, reportScopeKey{}, newReportScope(baseline))
}

// reportScopeFromContext returns the attached per-turn scope, or nil if none
// (e.g. an LLM call outside the conversation path). Used by the wrapper; not
// exported.
func reportScopeFromContext(ctx context.Context) *reportScope {
	if ctx == nil {
		return nil
	}
	if v, ok := ctx.Value(reportScopeKey{}).(*reportScope); ok {
		return v
	}
	return nil
}
