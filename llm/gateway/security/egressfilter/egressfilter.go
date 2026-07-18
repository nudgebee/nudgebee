// Package egressfilter is the gateway's outbound secret detector: a pure-regex
// scanner run over an outbound request body before it leaves to the provider, so an
// org's own credentials (API keys, cloud creds, tokens) can't leak into a prompt.
//
// It is lifted from llm-server's security/egressfilter (same rule corpus and design),
// adapted to operate on the raw request TEXT rather than langchaingo messages — the
// gateway sees provider-native bytes, not an llms.Model. No external calls; adds a few
// hundred µs to low-ms per request. The comprehensive rule set lives behind the EE
// seam (ee/egressfilter); the OSS baseline here is a handful of unambiguous rules.
//
// Three modes (resolved by the caller, see the filter stage):
//
//	detect  — record the hit, do not modify or block (safe rollout default)
//	enforce — block the request (a secret must not reach the provider)
//	redact  — replace the secret span with a placeholder and forward
package egressfilter

import (
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Mode controls what happens on a hit. Whether enforce/redact take effect is the
// filter stage's call; detect is the fail-safe default.
type Mode string

const (
	ModeOff     Mode = ""        // filtering disabled (no scan)
	ModeDetect  Mode = "detect"  // record only
	ModeEnforce Mode = "enforce" // block the request
	ModeRedact  Mode = "redact"  // replace the secret + forward
)

// ParseMode normalizes a config string; anything unrecognized (but non-empty) is
// detect — fail-safe for rollout. Empty means off.
func ParseMode(s string) Mode {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case "":
		return ModeOff
	case ModeEnforce:
		return ModeEnforce
	case ModeRedact:
		return ModeRedact
	default:
		return ModeDetect
	}
}

// Hit is one detected secret — the rule that fired and the byte range in the scanned
// text (so redaction can substitute placeholders right-to-left).
type Hit struct {
	RuleID string `json:"rule_id"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
}

// Result is the outcome of a Scan.
type Result struct{ Hits []Hit }

// HasHits reports whether any rule matched.
func (r Result) HasHits() bool { return len(r.Hits) > 0 }

// RuleIDs returns the unique rule ids that fired, sorted for stable labels.
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

// Filter is the contract every detector implements; Detect must be concurrency-safe.
type Filter interface {
	Detect(payload string) []Hit
}

// FilterFunc adapts a plain function to Filter (e.g. an entropy scanner).
type FilterFunc func(string) []Hit

// Detect satisfies Filter.
func (f FilterFunc) Detect(payload string) []Hit { return f(payload) }

// RegexFilter wraps a compiled regex as a Filter.
func RegexFilter(id string, re *regexp.Regexp) Filter { return &regexFilter{id: id, regex: re} }

type regexFilter struct {
	id    string
	regex *regexp.Regexp
}

func (f *regexFilter) Detect(payload string) []Hit {
	locs := f.regex.FindAllStringIndex(payload, -1)
	hits := make([]Hit, 0, len(locs))
	for _, l := range locs {
		hits = append(hits, Hit{RuleID: f.id, Start: l[0], End: l[1]})
	}
	return hits
}

// --- registry (atomic copy-on-write; writes at init only) ---

type registered struct {
	name   string
	filter Filter
}

var registryValue atomic.Value // []registered
var registryMu sync.Mutex

func init() {
	base := baselineRules()
	seed := make([]registered, 0, len(base))
	for _, r := range base {
		seed = append(seed, registered{name: r.id, filter: RegexFilter(r.id, r.regex)})
	}
	registryValue.Store(seed)
}

// Register adds a Filter to the active set. Extension packages (ee/egressfilter) call
// this from init() to add the comprehensive corpus.
func Register(name string, f Filter) {
	registryMu.Lock()
	defer registryMu.Unlock()
	cur, _ := registryValue.Load().([]registered)
	next := make([]registered, len(cur), len(cur)+1)
	copy(next, cur)
	registryValue.Store(append(next, registered{name: name, filter: f}))
}

func active() []registered {
	cur, _ := registryValue.Load().([]registered)
	return cur
}

// Scan runs every registered Filter over text and returns the hits, minus any whose
// matched substring is allowlisted. Never errors — a bad detector reduces coverage,
// it doesn't break the request.
func Scan(text string) Result {
	if text == "" {
		return Result{}
	}
	var hits []Hit
	for _, r := range active() {
		hits = append(hits, r.filter.Detect(text)...)
	}
	return Result{Hits: applyAllowlist(text, hits)}
}

// Redact replaces each secret span in text with a "[REDACTED:<rule>]" placeholder.
// Overlapping or adjacent hits (e.g. a PEM header caught by two rules) are merged into
// one span first, so no byte is rewritten twice and no part of a secret can slip
// through a partial overlap. Merged spans are applied right-to-left so earlier offsets
// stay valid. Irreversible — the provider never sees the raw secret.
func Redact(text string, hits []Hit) string {
	if len(hits) == 0 {
		return text
	}
	type span struct {
		start, end int
		rule       string
	}
	// Sort by start asc (then widest first on ties) and merge overlapping/adjacent runs.
	sorted := make([]Hit, len(hits))
	copy(sorted, hits)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start != sorted[j].Start {
			return sorted[i].Start < sorted[j].Start
		}
		return sorted[i].End > sorted[j].End
	})
	var merged []span
	for _, h := range sorted {
		if h.Start < 0 || h.End > len(text) || h.Start >= h.End {
			continue // fail-closed on a bad offset: skip rather than corrupt
		}
		if n := len(merged); n > 0 && h.Start <= merged[n-1].end {
			if h.End > merged[n-1].end {
				merged[n-1].end = h.End // extend the current run
			}
			continue
		}
		merged = append(merged, span{start: h.Start, end: h.End, rule: h.RuleID})
	}
	// Apply right-to-left so each replacement leaves the remaining (lower) offsets valid.
	b := []byte(text)
	for i := len(merged) - 1; i >= 0; i-- {
		s := merged[i]
		ph := []byte("[REDACTED:" + s.rule + "]")
		b = append(b[:s.start], append(ph, b[s.end:]...)...)
	}
	return string(b)
}

// --- allowlist (values that trip a rule but are known-safe, e.g. vendor docs samples) ---

var allowlistValue atomic.Value // map[string]struct{}
var allowlistMu sync.Mutex

// RegisterAllowedValues adds exact-match values to the global allowlist (idempotent).
func RegisterAllowedValues(values []string) {
	allowlistMu.Lock()
	defer allowlistMu.Unlock()
	cur, _ := allowlistValue.Load().(map[string]struct{})
	next := make(map[string]struct{}, len(cur)+len(values))
	for k := range cur {
		next[k] = struct{}{}
	}
	for _, v := range values {
		if v != "" {
			next[v] = struct{}{}
		}
	}
	allowlistValue.Store(next)
}

// applyAllowlist drops hits whose exact matched substring is allowlisted. No-op (no
// allocation) when the allowlist is empty.
func applyAllowlist(text string, hits []Hit) []Hit {
	al, _ := allowlistValue.Load().(map[string]struct{})
	if len(al) == 0 || len(hits) == 0 {
		return hits
	}
	out := hits[:0]
	for _, h := range hits {
		if h.Start < 0 || h.End > len(text) || h.Start >= h.End {
			continue // fail-closed: drop a malformed offset
		}
		if _, ok := al[text[h.Start:h.End]]; ok {
			continue
		}
		out = append(out, h)
	}
	return out
}

// rule is one baseline secret-detection rule.
type rule struct {
	id    string
	regex *regexp.Regexp
}

// baselineRules is the OSS default set — a handful of unambiguous, high-impact
// patterns. The EE bundle (ee/egressfilter) extends this via Register.
func baselineRules() []rule {
	return []rule{
		{"aws-access-key-id", regexp.MustCompile(`\b((?:AKIA|ASIA|ABIA|ACCA)[0-9A-Z]{16})\b`)},
		{"private-key-header", regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA |ENCRYPTED |PGP )?PRIVATE KEY-----`)},
		{"github-pat", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,251}\b`)},
		{"openai-api-key", regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_\-]{32,}\b`)},
		{"anthropic-api-key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_\-]{32,}\b`)},
	}
}
