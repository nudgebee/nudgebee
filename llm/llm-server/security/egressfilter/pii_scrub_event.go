package egressfilter

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// DetectorPII is the value written into PIIScrubEvent.Detector. Kept as a
// named constant so consumers reading the merged metadata.egressfilter[]
// array can discriminate against it without re-declaring the string literal.
// Sibling of DetectorSecrets.
const DetectorPII = "pii"

// PIIScrubEvent is the structured audit record emitted whenever the EE PII
// scrubber tokenizes at least one value on an outbound LLM call. One event
// per scrubbed call (no event when the scrub finds nothing).
//
// Sibling of FilterEvent — both marshal into the SAME `metadata.egressfilter[]`
// JSONB array on llm_conversation_messages. Consumers switch on the `detector`
// field ("secrets" vs "pii") to pick per-detector rendering / policy. One
// array, N detector types — same shape any polymorphic JSON collection uses.
//
// This type lives in OSS `security/egressfilter` alongside FilterEvent so
// the persistence site (conversation.go) — which already imports egressfilter
// for FilterEvent — can hold both without adding a dependency. The mechanism
// itself (the /scrub HTTP client + LLM wrapper) is EE and lives in
// `ee/scrubbing`; only the metadata contract is OSS.
//
// Deliberate non-field: the matched values. They ARE the PII; recording them
// would re-leak exactly what the scrubber removed. Only counts, categories,
// and correlation metadata are kept.
type PIIScrubEvent struct {
	// AuditID correlates the event with logs. Format: "scrub-<12 lowercase hex>".
	// Distinct prefix from FilterEvent's "egress-" so log-tailers can tell
	// secrets vs PII at a glance without parsing the JSON body.
	AuditID string `json:"audit_id"`

	// Detector is always "pii" — see DetectorPII. Sibling of
	// FilterEvent.Detector ("secrets"). Consumers iterating the merged
	// metadata.egressfilter[] array switch on this field.
	Detector string `json:"detector"`

	// HitCount is the number of distinct values tokenized on this call.
	HitCount int `json:"hit_count"`

	// Categories is the distinct, sorted set of soft-PII types tokenized
	// (e.g. ["EMAIL", "PERSON"]). Derived from the token namespace so
	// dashboards can slice by category without seeing values.
	Categories []string `json:"categories"`

	// Reversible is true when the scrub used reversible tokenization (the
	// wrapper always does — it must rehydrate the response). Recorded so the
	// shape is forward-compatible with a future detect-only/irreversible mode.
	Reversible bool `json:"reversible"`

	// PayloadBytes is the total size of the text sent to /scrub, for
	// correlating unusually large calls with scrub activity.
	PayloadBytes int `json:"payload_bytes"`

	// AgentName carries the running agent (e.g. "k8s_debug") when known.
	// When produced by the per-message consolidator, this is the sorted
	// comma-joined list of every contributing agent — see AgentCounts
	// below for the machine-friendlier per-agent breakdown.
	AgentName string `json:"agent_name,omitempty"`

	// CategoryCounts breaks HitCount down per category ({"EMAIL": 3,
	// "PERSON": 20, "LOCATION": 12}). Present on consolidated events
	// (PIIValueAccumulator.Consolidated) so the UI can render "3 emails,
	// 20 names, 12 locations" instead of a flat number and a category
	// set. Omitted from per-call events (NewPIIScrubEvent) — those still
	// live for backward compat but aren't the write path any more.
	CategoryCounts map[string]int `json:"category_counts,omitempty"`

	// AgentCounts breaks the distinct-value count down per contributing
	// agent — number of NEW distinct values each agent's wrapper call
	// contributed to the accumulator this turn. Answers "which agent
	// generated the most PII" for the diagnostic view without exposing
	// values. Present only on consolidated events.
	AgentCounts map[string]int `json:"agent_counts,omitempty"`
}

// NewPIIScrubEvent builds an event from a scrub result. mapping is the
// {token: original} map the scrubber returns; only its keys (tokens) are read
// here — original values are never copied into the event. piiTokenCategory
// extracts the TYPE from a "[TYPE_n]" token.
func NewPIIScrubEvent(mapping map[string]string, reversible bool, payloadBytes int, agentName string) PIIScrubEvent {
	catSet := make(map[string]struct{}, len(mapping))
	for token := range mapping {
		if c := piiTokenCategory(token); c != "" {
			catSet[c] = struct{}{}
		}
	}
	cats := make([]string, 0, len(catSet))
	for c := range catSet {
		cats = append(cats, c)
	}
	slices.Sort(cats)
	return PIIScrubEvent{
		AuditID:      newPIIAuditID(),
		Detector:     DetectorPII,
		HitCount:     len(mapping),
		Categories:   cats,
		Reversible:   reversible,
		PayloadBytes: payloadBytes,
		AgentName:    agentName,
	}
}

// FilterPIIMappingByCategory splits a /scrub mapping into two subsets keyed by
// whether the token's category is in disabledCategories:
//   - kept    : tokens whose category is NOT disabled — proceed to scrub.
//   - unscrub : tokens whose category IS disabled — the wrapper uses these
//     to Rehydrate() the disabled-category tokens back to real
//     values in the scrubbed pieces BEFORE sending to the LLM.
//
// Empty disabledCategories → (mapping, nil), zero allocation. Case-insensitive
// comparison; whitespace is trimmed on category names.
//
// The audit trail (PIIScrubEvent) should be built from `kept` — the count
// and categories reflect what was actually protected, not what /scrub tried
// to tokenize before the tenant filter ran.
func FilterPIIMappingByCategory(mapping map[string]string, disabledCategories []string) (kept, unscrub map[string]string) {
	if len(disabledCategories) == 0 {
		return mapping, nil
	}
	disabledSet := make(map[string]struct{}, len(disabledCategories))
	for _, c := range disabledCategories {
		disabledSet[strings.ToUpper(strings.TrimSpace(c))] = struct{}{}
	}
	kept = make(map[string]string, len(mapping))
	unscrub = make(map[string]string)
	for token, value := range mapping {
		if _, dis := disabledSet[piiTokenCategory(token)]; dis {
			unscrub[token] = value
		} else {
			kept[token] = value
		}
	}
	return
}

// piiTokenCategory extracts "EMAIL" from "[EMAIL_1]". Returns "" for anything
// not shaped like a "[TYPE_n]" reversible token (e.g. a fixed "[REDACTED_*]"
// placeholder, which never appears in the reversible mapping anyway).
func piiTokenCategory(token string) string {
	if len(token) < 2 || token[0] != '[' || token[len(token)-1] != ']' {
		return ""
	}
	inner := token[1 : len(token)-1] // TYPE_n
	i := strings.LastIndexByte(inner, '_')
	if i <= 0 {
		return ""
	}
	return inner[:i]
}

// newPIIAuditID mirrors newAuditID (12 hex of a UUIDv4) with a "scrub-"
// prefix so PII audit IDs are visually distinct from "egress-" ones in
// tailed logs.
func newPIIAuditID() string {
	id := strings.ReplaceAll(uuid.New().String(), "-", "")
	return "scrub-" + id[:12]
}

// PIIValueAccumulator collects distinct PII values scrubbed across all
// wrapper calls in a single conversation turn, so the message-end
// consolidator can emit ONE PIIScrubEvent with a true-distinct hit_count.
// Multiple react-loop calls that reference the same values otherwise
// inflate the sum-of-hit_count chip label — a react turn with 9 wrapper
// calls could easily produce 95 "hits" for what is really 1-few distinct
// values, per the 2026-07-30 UI review.
//
// Values live in memory only for the duration of one turn — the
// accumulator is ctx-scoped and never serialized. Same risk profile as
// the per-call mapping the wrapper already holds for rehydration. The
// accumulator is safe for concurrent use (parallel plan execution can
// invoke the wrapper from multiple goroutines under one turn).
type PIIValueAccumulator struct {
	mu           sync.Mutex
	values       map[string]string // raw value -> category (first-seen wins)
	payloadBytes int
	// agentContributions tracks how many NEW distinct values each agent's
	// wrapper call contributed to `values` (i.e., values the agent was
	// first to introduce this turn). Answers the diagnostic question
	// "which agent generated the most PII" without exposing raw values.
	// A value seen by agent A and then again by agent B counts only for
	// A — B's call added zero net-new distinct values.
	agentContributions map[string]int
}

// NewPIIValueAccumulator returns an empty accumulator ready to be attached
// to ctx via WithPIIValueAccumulator.
func NewPIIValueAccumulator() *PIIValueAccumulator {
	return &PIIValueAccumulator{
		values:             make(map[string]string),
		agentContributions: make(map[string]int),
	}
}

// Add contributes a per-call scrub mapping to the accumulator. Called by
// the EE wrapper after each successful /scrub call. Deduplicates by raw
// value across all prior calls in the turn.
//
// A value's category is stable in practice (an email is always EMAIL);
// first-seen wins if a value ever changes category, which should not
// happen but is defended against so the accumulator degrades gracefully.
func (a *PIIValueAccumulator) Add(mapping map[string]string, agentName string, payloadBytes int) {
	if a == nil || len(mapping) == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// Lazy-init so a caller using &PIIValueAccumulator{} (bypassing
	// NewPIIValueAccumulator) still works — nil-map assignment would
	// otherwise panic. Cheap; hit on first Add per accumulator.
	if a.values == nil {
		a.values = make(map[string]string)
	}
	if a.agentContributions == nil {
		a.agentContributions = make(map[string]int)
	}
	newFromThisCall := 0
	for token, value := range mapping {
		if _, seen := a.values[value]; !seen {
			a.values[value] = piiTokenCategory(token)
			newFromThisCall++
		}
	}
	a.payloadBytes += payloadBytes
	if agentName != "" && newFromThisCall > 0 {
		// Attribute the new-value count to this agent. An agent that only
		// re-touched already-seen values contributes 0 — matches the
		// diagnostic intent "which agent introduced PII we hadn't seen".
		a.agentContributions[agentName] += newFromThisCall
	}
}

// Consolidated returns a single PIIScrubEvent summarising every distinct
// value the wrapper saw this turn. HitCount is the true-distinct count
// across all wrapper calls. Categories is the sorted union. AgentName
// carries the sorted comma-joined list of contributing agents so the
// chip tooltip can surface which agents touched PII. Returns nil for an
// empty accumulator (nothing to report).
func (a *PIIValueAccumulator) Consolidated() *PIIScrubEvent {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.values) == 0 {
		return nil
	}
	catSet := make(map[string]struct{}, 4)
	for _, cat := range a.values {
		if cat != "" {
			catSet[cat] = struct{}{}
		}
	}
	cats := make([]string, 0, len(catSet))
	for c := range catSet {
		cats = append(cats, c)
	}
	slices.Sort(cats)

	agents := make([]string, 0, len(a.agentContributions))
	for name := range a.agentContributions {
		agents = append(agents, name)
	}
	slices.Sort(agents)

	// Per-category counts derived from the deduped values map — cheap
	// (bounded by the ~4 known categories × distinct-value count) and
	// gives the UI a breakdown without a schema round-trip.
	categoryCounts := make(map[string]int, len(catSet))
	for _, cat := range a.values {
		if cat != "" {
			categoryCounts[cat]++
		}
	}

	// Copy agentContributions so callers can't mutate the accumulator's
	// internal state via the returned event.
	agentCounts := make(map[string]int, len(a.agentContributions))
	maps.Copy(agentCounts, a.agentContributions)

	return &PIIScrubEvent{
		AuditID:        newPIIAuditID(),
		Detector:       DetectorPII,
		HitCount:       len(a.values),
		Categories:     cats,
		Reversible:     true,
		PayloadBytes:   a.payloadBytes,
		AgentName:      strings.Join(agents, ","),
		CategoryCounts: categoryCounts,
		AgentCounts:    agentCounts,
	}
}

// piiValueAccumulatorKey attaches an accumulator to ctx for the wrapper
// to find on each call.
type piiValueAccumulatorKey struct{}

// WithPIIValueAccumulator attaches acc to ctx. conversation.go creates
// the accumulator at turn start and reads it back at message-end to
// build the consolidated event.
func WithPIIValueAccumulator(ctx context.Context, acc *PIIValueAccumulator) context.Context {
	if acc == nil {
		return ctx
	}
	return context.WithValue(ctx, piiValueAccumulatorKey{}, acc)
}

// PIIValueAccumulatorFromContext returns the accumulator attached via
// WithPIIValueAccumulator, or nil when none is attached (background jobs,
// non-conversation callers). Wrapper calls Add on a nil accumulator as a
// no-op, so the "no accumulator" path is safe.
func PIIValueAccumulatorFromContext(ctx context.Context) *PIIValueAccumulator {
	if v, ok := ctx.Value(piiValueAccumulatorKey{}).(*PIIValueAccumulator); ok {
		return v
	}
	return nil
}

// piiScrubEventReporterKey is the unexported context key for the per-request
// reporter callback.
//
// DEPRECATED (2026-07-31): superseded by PIIValueAccumulator, which
// produces one consolidated event per message instead of one per call.
// The reporter type + Report/With helpers are kept for backward compat
// (a few tests still use them) but the wrapper no longer calls
// ReportPIIScrubEvent — see ee/scrubbing/scrubllm.go.
type piiScrubEventReporterKey struct{}

// WithPIIScrubEventReporter attaches a callback the EE scrub wrapper invokes
// once per outbound LLM call that tokenizes at least one value. The caller
// (conversation.go) accumulates the events for the turn and persists them at
// message-end into metadata.egressfilter[] alongside FilterEvent rows. A nil
// callback is ignored.
//
// The callback runs in the wrapper goroutine and must not block. Because the
// wrapper instance is cached and shared across goroutines (parallel plan
// execution), the callback must be safe to invoke concurrently.
func WithPIIScrubEventReporter(ctx context.Context, fn func(PIIScrubEvent)) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, piiScrubEventReporterKey{}, fn)
}

// ReportPIIScrubEvent looks up the reporter on ctx and invokes it. No-op when
// none is attached (OSS callers, or turns that don't wire one). Exported so
// the EE wrapper in ee/scrubbing can call it.
func ReportPIIScrubEvent(ctx context.Context, e PIIScrubEvent) {
	if fn, ok := ctx.Value(piiScrubEventReporterKey{}).(func(PIIScrubEvent)); ok && fn != nil {
		fn(e)
	}
}
