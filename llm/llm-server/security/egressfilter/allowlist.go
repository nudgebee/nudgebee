package egressfilter

import (
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
)

// The allowlist is a global set of values that Scan strips from its Result
// before returning: any Hit whose matched substring (text[Start:End]) is in
// the set is silently dropped. Typical use is canonical docs samples that
// trip a real rule — e.g. AWS's "AKIAIOSFODNN7EXAMPLE", a Google API key
// "AIzaSyExampleKey...", a Stripe `sk_test_...` example. Without an entry
// here, any prompt that quotes vendor docs will trip the rule and (in
// enforce mode) block legitimate traffic.
//
// Match semantics: strict, bytewise equality on the matched substring. The
// allowlist does NOT do prefix/regex/case-insensitive matching — the operator
// must paste the exact value as it appears in the prompt. This keeps the
// semantics predictable and forces the operator to think about each entry.
//
// Scope: package-global. Per-tenant allowlist is a future extension; see
// the egressfilter project memory for the deferred design.

var (
	allowlistMu    sync.Mutex   // serializes writers
	allowlistValue atomic.Value // stores map[string]struct{}; read lock-free
)

// RegisterAllowedValue adds value to the global allowlist. Returns silently
// on an empty string or an already-registered value (idempotent). Safe to
// call concurrently.
//
// Writes are expected at process startup; the slice is meant to grow during
// init, not churn at runtime. The API does not expose a deregister — once
// allowlisted, always allowlisted for the process lifetime.
func RegisterAllowedValue(value string) {
	if value == "" {
		return
	}
	allowlistMu.Lock()
	defer allowlistMu.Unlock()
	curr := loadAllowlist()
	if _, dup := curr[value]; dup {
		return
	}
	next := make(map[string]struct{}, len(curr)+1)
	for k := range curr {
		next[k] = struct{}{}
	}
	next[value] = struct{}{}
	allowlistValue.Store(next)
}

// RegisterAllowedValues is the bulk form. Empty strings are skipped.
func RegisterAllowedValues(values []string) {
	if len(values) == 0 {
		return
	}
	allowlistMu.Lock()
	defer allowlistMu.Unlock()
	curr := loadAllowlist()
	next := make(map[string]struct{}, len(curr)+len(values))
	for k := range curr {
		next[k] = struct{}{}
	}
	added := 0
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, dup := next[v]; dup {
			continue
		}
		next[v] = struct{}{}
		added++
	}
	if added == 0 {
		return
	}
	allowlistValue.Store(next)
}

// LoadAllowlistFromCSV parses a comma-separated string and registers each
// non-empty trimmed value. Convenience helper for env-var bootstrap from
// LLM_SERVER_EGRESSFILTER_ALLOWLIST=val1,val2,val3.
//
// Whitespace around each value is trimmed (so "  AKIA…EXAMPLE , AIza…" works);
// the value itself is NOT modified internally — the regex match is bytewise
// against the prompt's original characters.
func LoadAllowlistFromCSV(csv string) {
	if csv == "" {
		return
	}
	parts := strings.Split(csv, ",")
	values := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			values = append(values, v)
		}
	}
	RegisterAllowedValues(values)
}

// applyAllowlist filters hits whose matched substring is in the allowlist.
// Returns the input slice unchanged when the allowlist is empty (no
// allocations on the hot path — important because Scan runs on every
// outbound LLM call).
//
// A debug-level log line is emitted per dropped hit so operators verifying
// allowlist behaviour can correlate. INFO+ in prod means it costs nothing
// when not being tailed.
func applyAllowlist(text string, hits []Hit) []Hit {
	if len(hits) == 0 {
		return hits
	}
	allow := loadAllowlist()
	if len(allow) == 0 {
		return hits
	}
	kept := hits[:0]
	for _, h := range hits {
		// Bounds check is defensive: the built-in regexFilter is guaranteed
		// to return in-bounds offsets, but external packages can register
		// arbitrary Filter implementations and a buggy one would otherwise
		// panic on every outbound LLM call. Fail-closed — keep the hit when
		// offsets are invalid rather than silently dropping or panicking,
		// because the security control is allowlist=skip and the safer
		// default on a malformed input is "don't skip."
		if h.Start < 0 || h.End < h.Start || h.End > len(text) {
			slog.Warn("egressfilter: filter produced out-of-bounds hit offsets; keeping hit (fail-closed)",
				"rule_id", h.RuleID, "start", h.Start, "end", h.End, "text_len", len(text))
			kept = append(kept, h)
			continue
		}
		matched := text[h.Start:h.End]
		if _, ok := allow[matched]; ok {
			slog.Debug("egressfilter: hit dropped by allowlist", "rule_id", h.RuleID)
			continue
		}
		kept = append(kept, h)
	}
	return kept
}

// loadAllowlist returns the current allowlist map. Lock-free. The returned
// map must be treated as read-only — writers publish a fresh map via
// atomic.Value.
func loadAllowlist() map[string]struct{} {
	val := allowlistValue.Load()
	if val == nil {
		return nil
	}
	return val.(map[string]struct{})
}
