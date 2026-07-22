// Package egressfilter implements the outbound secret detector that runs in front of
// every LLM provider call. It is Phase 1 of the design described in
// llm/llm-server/docs/pii-secret-scrubbing.md.
//
// The egressfilter is a pure-regex + entropy detector. It does not call out to any
// other service and adds <5ms p50 to the LLM call path. It is wired by wrapping
// each constructed llms.Model with WrapModel at the GetLLMModel chokepoint.
//
// Two configured modes; the Action actually taken is resolved via
// ActionGate (see action_gate.go), so deployments can plug in their own
// post-detection policy without changing the wrapper:
//
//	ModeDetect  — record metrics/logs/events, do not block
//	ModeEnforce — request a block; only takes effect when an ActionGate is registered
//
// PII detection (Phase 2/3) and ingress prompt-injection sanitization (Phase 5)
// live in sibling files added later; they are out of scope here.
package egressfilter

import (
	"sort"
	"strings"
)

// Mode controls the action requested when a hit is detected. Whether that
// request is honoured is decided by ActionGate; when no gate is
// registered, every Mode collapses to ActionAudit.
type Mode string

const (
	// ModeDetect emits log + metric + FilterEvent on a hit but does NOT
	// block the LLM call. The safe rollout default before flipping to
	// ModeEnforce.
	ModeDetect Mode = "detect"
	// ModeEnforce requests that the LLM call be blocked on a hit. Only
	// honoured when an ActionGate is registered; otherwise the wrapper
	// falls back to ActionAudit and logs the gap once.
	ModeEnforce Mode = "enforce"
)

// ParseMode normalises a config string into a Mode. Accepts the canonical
// "detect" and the legacy "audit" synonym — old configs keep working
// without operator changes. Anything else falls back to ModeDetect —
// fail-safe for rollout.
func ParseMode(s string) Mode {
	norm := strings.ToLower(strings.TrimSpace(s))
	if Mode(norm) == ModeEnforce {
		return ModeEnforce
	}
	// Both "detect" (canonical) and "audit" (legacy) → ModeDetect.
	return ModeDetect
}

// Hit describes a single detection — what fired and where, nothing about
// what we did with it. Pure "what we found" record. Carried in Result by
// Scan and surfaced in FilterEvent.Hits.
//
// Action records ("what we did about a hit") live in a parallel Redaction
// slice on FilterEvent, populated only under redact / tokenize modes. This
// keeps detection and action concerns separate: Scan only ever produces
// Hits, never Redactions; only the future redact-mode wrapper produces
// Redactions, indexed against the Hits slice.
//
// Deliberate non-fields:
//   - the matched substring (it IS the secret; never leaves the process)
type Hit struct {
	// RuleID is the canonical identifier of the rule that matched.
	// Safe to expose in metrics, logs, and serialized metadata.
	RuleID string `json:"rule_id"`

	// Start, End are byte offsets into the serialized payload that Scan
	// inspected. Stable across the same call. Used by the future redact mode
	// to substitute placeholders right-to-left without invalidating earlier
	// offsets, and recorded so audits can reconstruct *where* matches were
	// without seeing *what*.
	Start int `json:"start"`
	End   int `json:"end"`
}

// Redaction describes the action taken on a single hit when the wrapper is
// operating in a payload-rewriting mode (future "redact" for secrets,
// "tokenize" for PII). One Redaction per Hit; the two slices align by
// index on FilterEvent.
//
// Deliberately a struct rather than `[]string` of placeholders so future
// tokenize modes can carry per-action context (e.g. an in-memory token id
// pointer for rehydration) without changing the FilterEvent shape again.
type Redaction struct {
	// Placeholder is the marker substituted in place of the matched range
	// (e.g. "[REDACTED:secret]" or "<NB_PII_PERSON_3>"). Safe to persist.
	Placeholder string `json:"placeholder"`
}

// Result is the outcome of a single Scan call.
type Result struct {
	Hits []Hit
}

// HasHits reports whether any rule matched.
func (r Result) HasHits() bool { return len(r.Hits) > 0 }

// RuleIDs returns the unique rule IDs that fired, sorted for deterministic
// metric labels and log lines.
func (r Result) RuleIDs() []string {
	if len(r.Hits) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(r.Hits))
	out := make([]string, 0, len(r.Hits))
	for _, h := range r.Hits {
		if _, dup := seen[h.RuleID]; dup {
			continue
		}
		seen[h.RuleID] = struct{}{}
		out = append(out, h.RuleID)
	}
	sort.Strings(out)
	return out
}

// Scan runs every registered Filter against text and returns a Result. It
// never returns an error; detector failures are silent and only reduce
// coverage, never break the call.
//
// The registered set comes from the registry (see registry.go) — the OSS
// baseline ships a handful of high-precision rules; extension packages
// register additional Filters via init().
//
// After collecting hits, Scan applies the allowlist (see allowlist.go):
// any Hit whose matched substring is in the global allowlist is dropped
// before the Result is returned. Allowlist application is a no-op (zero
// allocations) when the allowlist is empty.
func Scan(text string) Result {
	if text == "" {
		return Result{}
	}
	var hits []Hit
	for _, r := range active() {
		hits = append(hits, r.filter.Detect(text)...)
	}
	hits = applyAllowlist(text, hits)
	return Result{Hits: hits}
}
