package egressfilter

import (
	"log/slog"
	"regexp"
	"sync"
	"sync/atomic"
)

// The egressfilter package ships a small high-precision baseline (see
// regex_filters.go) and exposes a single extension hook below:
//
//   Register(name, Filter)
//
// Every detector — regex rule, entropy scanner, future ML-backed detector —
// implements the same Filter interface and is registered through the same
// function. Two helpers cover the common shapes:
//
//   RegexFilter(id, *regexp.Regexp)   — wraps a regex as a Filter
//   FilterFunc(func(string) []Hit)    — wraps a plain function as a Filter
//
// Self-hosted operators who want additional coverage without forking can call
// Register from their own init() in a blank-imported package.
//
// The registered slice is stored in atomic.Value with copy-on-write
// semantics: writes are serialized by a mutex but reads are lock-free and
// allocation-free on the hot path. Writes are expected to happen at process
// startup only — the API does not expose a deregister; the slice is meant
// to only grow during init, never shrink at runtime.

var (
	registryMu    sync.Mutex   // serializes Register writers
	registryValue atomic.Value // stores []registered; read lock-free in active()
)

// init runs at package load and seeds the registry with the OSS baseline.
// Kept at the top of the file (just below the package-level vars) so a
// reader of registry.go sees the boot-time state right where the vars are
// declared — what is loaded, in what order, with what content.
func init() {
	// Pre-populate with the baseline rule set. Each baseline rule wraps as a
	// RegexFilter; the registration name and the embedded rule id match.
	base := baselineRules()
	seed := make([]registered, 0, len(base))
	for _, r := range base {
		seed = append(seed, registered{name: r.id, filter: &regexFilter{id: r.id, regex: r.regex}})
	}
	registryValue.Store(seed)
}

// Filter is the contract every detector implements. Detect inspects a
// payload and returns zero or more Hits. Implementations must be safe to
// call concurrently from many goroutines: Scan invokes every registered
// Filter on every outbound LLM call, and the same Filter instance is shared
// across them.
//
// A Filter that returns Hits with an empty RuleID is silently dropped from
// the result — every Hit must carry a stable, cardinality-bounded rule id
// so it can become a metric label.
type Filter interface {
	Detect(payload string) []Hit
}

// FilterFunc adapts a plain function to the Filter interface. Useful for
// detectors that don't need any state — e.g. an entropy scanner whose
// thresholds are package-level constants.
type FilterFunc func(string) []Hit

// Detect satisfies Filter.
func (f FilterFunc) Detect(payload string) []Hit { return f(payload) }

// regexFilter is the canonical regex-rule implementation. The id is captured
// so every Hit carries it; the regex is shared across calls.
type regexFilter struct {
	id    string
	regex *regexp.Regexp
}

// Detect returns one Hit per non-overlapping match.
func (r *regexFilter) Detect(payload string) []Hit {
	matches := r.regex.FindAllStringIndex(payload, -1)
	if len(matches) == 0 {
		return nil
	}
	hits := make([]Hit, 0, len(matches))
	for _, m := range matches {
		hits = append(hits, Hit{RuleID: r.id, Start: m[0], End: m[1]})
	}
	return hits
}

// RegexFilter constructs a Filter from a (id, *regexp.Regexp) pair. The id
// becomes a metric label and a log field, so it must be cardinality-bounded
// (no values, no per-tenant data) and stable across deploys.
//
// Returns nil if id is empty or re is nil — callers can pass the result
// straight to Register without a guard; Register also no-ops on a nil
// filter.
func RegexFilter(id string, re *regexp.Regexp) Filter {
	if id == "" || re == nil {
		return nil
	}
	return &regexFilter{id: id, regex: re}
}

// registered carries a Filter plus the display name used in registration
// telemetry. The name is independent of any rule id the filter may emit —
// one filter can produce hits under multiple rule ids (e.g. an ML detector
// emitting PERSON / EMAIL / etc.).
type registered struct {
	name   string
	filter Filter
}

// Register appends a Filter to the active set under name. A nil filter or
// empty name is silently ignored — convenient for chaining with RegexFilter,
// which returns nil on invalid input.
//
// Duplicate names are tolerated but logged at WARN. Two registrations with
// the same name mean every Scan runs both filters and produces duplicate
// hits — Result.RuleIDs() dedups by id, but CPU is wasted on the redundant
// regex pass. The warning surfaces the duplicate at startup so operators
// notice it; we don't silently replace because idempotent re-registration
// (test setup, hot reload) is sometimes legitimate.
//
// Implementation note: the mutex serializes concurrent writers, then the
// new slice is published atomically. Readers that loaded the previous slice
// keep scanning it safely; subsequent reads see the new one.
func Register(name string, f Filter) {
	if f == nil || name == "" {
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	curr := active()
	for _, r := range curr {
		if r.name == name {
			slog.Warn("egressfilter: duplicate Filter name registered; both will run on every Scan, wasting CPU on the redundant pass",
				"name", name)
			break
		}
	}
	next := make([]registered, len(curr), len(curr)+1)
	copy(next, curr)
	next = append(next, registered{name: name, filter: f})
	registryValue.Store(next)
}

// active returns the currently registered filter slice for Scan. Lock-free
// and allocation-free; safe to call from any goroutine concurrently with
// Register.
//
// Callers must treat the returned slice as read-only — Register publishes a
// fresh slice via atomic.Value, so mutating the returned slice would race
// with concurrent writers.
func active() []registered {
	val := registryValue.Load()
	if val == nil {
		return nil
	}
	return val.([]registered)
}
