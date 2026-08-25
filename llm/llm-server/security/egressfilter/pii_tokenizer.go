package egressfilter

import (
	"fmt"
	"strings"
)

// piiTokenizer is a per-call reversible-token allocator. Each call to
// ScrubPIIInProcess makes a fresh one — its lifetime is one batched
// scrub, matching the Python session in scrubber.py (fresh per HTTP
// request). Same-batch dedup: a value that appears in multiple pieces
// resolves to the SAME token so rehydration handles it correctly.
//
// Not concurrent-safe by design — one instance is used from one
// ScrubPIIInProcess call which iterates pieces sequentially. Concurrent
// batched scrubs allocate separate instances.
//
// Token format is `[<TYPE>_<n>]` with a per-type counter (EMAIL_1,
// EMAIL_2, PHONE_1, ...). Format is verbatim-compatible with the
// Python impl so response rehydration works identically regardless of
// which mode produced the mapping.
type piiTokenizer struct {
	counters map[string]int    // "EMAIL" → last-allocated N
	tokens   map[string]string // raw value → token (per-batch dedup)
	mapping  map[string]string // token → raw value (returned to caller)
}

func newPIITokenizer() *piiTokenizer {
	return &piiTokenizer{
		counters: make(map[string]int),
		tokens:   make(map[string]string),
		mapping:  make(map[string]string),
	}
}

// tokenFor returns the reversible token for value under kind. Dedups on
// value: a repeated (kind, value) pair always resolves to the same
// token, so `[EMAIL_1]` appearing in three pieces of the batch all
// rehydrate to the same original — matching Python session semantics.
func (t *piiTokenizer) tokenFor(value, kind string) string {
	if tok, ok := t.tokens[value]; ok {
		return tok
	}
	t.counters[kind]++
	tok := fmt.Sprintf("[%s_%d]", kind, t.counters[kind])
	t.tokens[value] = tok
	t.mapping[tok] = value
	return tok
}

// ScrubPIIInProcess is the in-process Go equivalent of
// scrubclient.Scrub: detect EMAIL + PHONE across a batch, substitute
// each match with a reversible token, and return the scrubbed texts
// (input-order-preserving) plus a UNIFIED {token → value} mapping
// covering every value seen in the batch.
//
// Shape MUST match scrubclient.Scrub exactly:
//   - len(scrubbed) == len(texts), same order.
//   - mapping is a single per-batch namespace (a value in pieces[0] and
//     pieces[3] collapses to the same token).
//   - Tokens are `[EMAIL_N]` and `[PHONE_N]`.
//
// The caller (ee/scrubbing/scrubllm.go) uses the same downstream
// rehydration and PIIValueAccumulator paths regardless of mode.
//
// Non-goals: NER (PERSON, LOCATION, etc). The in-process path is
// TIER-1 REGEX ONLY. Tenants who need NER coverage stay on http mode.
func ScrubPIIInProcess(texts []string) (scrubbed []string, mapping map[string]string) {
	tok := newPIITokenizer()
	scrubbed = make([]string, len(texts))
	for i, text := range texts {
		if text == "" {
			scrubbed[i] = text
			continue
		}
		scrubbed[i] = substituteBoth(text, tok)
	}
	return scrubbed, tok.mapping
}

// substituteBoth runs both EMAIL and PHONE passes on text in a
// deterministic order (EMAIL first) so a value like "call me at
// 5551234567 or alice@acme.co" always produces the same token indexing
// across runs. Each pass uses the shared tokenizer for cross-pass
// dedup: an email is still an email even if a later phone pass runs.
func substituteBoth(text string, tok *piiTokenizer) string {
	text = replaceRanges(text, FindPIIEmails(text), func(match string) string {
		return tok.tokenFor(match, "EMAIL")
	})
	text = replaceRanges(text, FindPIIPhones(text), func(match string) string {
		return tok.tokenFor(match, "PHONE")
	})
	return text
}

// replaceRanges applies a per-match replacement over sorted, non-
// overlapping [start,end) index pairs — the shape both regex.FindAll
// and FindPIIPhones return. Runs right-to-left so early indices stay
// valid as later ones are rewritten (RE2's FindAllStringIndex returns
// ascending, non-overlapping ranges by contract).
func replaceRanges(text string, ranges [][]int, fn func(string) string) string {
	if len(ranges) == 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	last := 0
	for _, r := range ranges {
		b.WriteString(text[last:r[0]])
		b.WriteString(fn(text[r[0]:r[1]]))
		last = r[1]
	}
	b.WriteString(text[last:])
	return b.String()
}
