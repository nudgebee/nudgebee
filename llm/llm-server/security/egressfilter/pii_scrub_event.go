package egressfilter

import (
	"context"
	"strings"

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
	AgentName string `json:"agent_name,omitempty"`
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
	sortPIICategories(cats)
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

// sortPIICategories is a tiny insertion sort — categories are at most 4
// elements (PERSON, LOCATION, EMAIL, PHONE), so this avoids pulling in sort
// for a hot-ish path and keeps the output deterministic for tests/dashboards.
func sortPIICategories(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// newPIIAuditID mirrors newAuditID (12 hex of a UUIDv4) with a "scrub-"
// prefix so PII audit IDs are visually distinct from "egress-" ones in
// tailed logs.
func newPIIAuditID() string {
	id := strings.ReplaceAll(uuid.New().String(), "-", "")
	return "scrub-" + id[:12]
}

// piiScrubEventReporterKey is the unexported context key for the per-request
// reporter callback.
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
