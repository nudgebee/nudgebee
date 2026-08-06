package egressfilter

import "regexp"

// EMAIL and PHONE regexes for the in-process PII scrub path. These are the
// TIER-1 REVERSIBLE detectors — matched values get tokenized as
// `[EMAIL_N]` / `[PHONE_N]` in the outbound LLM payload, and rehydrated
// back to the real value in the response. Semantically identical to the
// Python impl in ml-k8s-server/server/ee/scrubbing/scrubber.py
// (_EMAIL_RE, _PHONE_RE) so tenants get identical output regardless of
// which mode (http vs inprocess) they run in.
//
// Kept in the `security/egressfilter` package (not `ee/`) because:
//   1. This is the always-available Tier-1 corpus, symmetric with the OSS
//      baseline secret rules (regex_filters.go) that already live here.
//   2. In-process PII detection is a lower-risk, higher-precision subset
//      of the full scrubber and belongs in the OSS surface. NER + the
//      infra-vocab allowlist stay behind the HTTP boundary (Python).
//
// Both patterns are pure regex — no Luhn or context checks — so we expose
// them as package-level *regexp.Regexp for reuse by tokenizer.go without
// double-compile.

// PIIEmailRegex matches an email address body. Same shape as Python's
// `\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`.
var PIIEmailRegex = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)

// FindPIIEmails returns the [start, end) byte ranges of email matches, minus
// any whose preceding byte is `@` — Go RE2 has no lookbehind, so the guard is
// a post-filter (same shape as FindPIIPhones).
//
// An address is never chained onto another `@`. Elasticsearch field paths are:
// `k8s@namespace@name.keyword` yields a bogus `namespace@name.keyword` match
// (TLD `keyword` passes `[A-Za-z]{2,}`). Every es_metrics_query /
// metrics_labels_list response carries these, so it fires on every such turn.
// Left unguarded it also corrupts what the model reads — the field name it
// must build a query from becomes `k8s@[EMAIL_1]`.
func FindPIIEmails(text string) [][]int {
	matches := PIIEmailRegex.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}
	kept := matches[:0]
	for _, m := range matches {
		if m[0] > 0 && text[m[0]-1] == '@' {
			continue
		}
		kept = append(kept, m)
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// piiPhoneCandidate is the pre-boundary phone regex — matches any of the
// three supported shapes without the surrounding "not-preceded/followed-by-
// digit" guards. The guards are re-applied by the PIIPhoneFilter helper
// below because Go RE2 has no lookbehind and post-filtering is the only
// way to enforce them without consuming boundary characters.
//
// Shapes matched:
//
//	(a) E.164-ish with '+':  +91 98765 43210, +1-555-123-4567
//	(b) US parens:           (555) 123-4567
//	(c) Hyphen/space 3-3-4:  555-123-4567, 555 123 4567
//
// Dot-separated bare digit runs like "100.500.1000" or version strings
// "v1.234.5678" are DELIBERATELY rejected — the 2026-08-01 Python
// rewrite (#35432) narrowed the shape after a metric-value false-fire on
// dev; we keep parity here. See the pattern doc in scrubber.py.
var piiPhoneCandidate = regexp.MustCompile(
	`\+\d{1,3}[ .\-]?\d{2,4}[ .\-]?\d{3,4}[ .\-]?\d{3,4}(?:[ .\-]?\d{2})?` +
		`|` +
		`\(\d{3}\)\s?\d{3}[ .\-]?\d{4}` +
		`|` +
		`\b\d{3}[ -]\d{3}[ -]\d{4}\b`,
)

// FindPIIPhones returns the [start, end) byte ranges of phone matches in
// text after applying the boundary guards Python's lookaround expresses
// as `(?<!\d)` + `(?!\d)` + `(?!\.\d)`. Go RE2 has no lookaround, so we
// post-filter: reject a match whose immediate left neighbor is a digit,
// or whose immediate right neighbor is a digit or `.<digit>`. This is
// the SAME safety net the Python impl uses to prevent a partial-match
// leak — a phone-shape prefix inside a longer digit run must NOT match
// and leave trailing digits unredacted.
func FindPIIPhones(text string) [][]int {
	matches := piiPhoneCandidate.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}
	kept := matches[:0]
	for _, m := range matches {
		if m[0] > 0 && isDigitByte(text[m[0]-1]) {
			continue // preceded by digit → not a phone boundary
		}
		if m[1] < len(text) {
			c := text[m[1]]
			if isDigitByte(c) {
				continue
			}
			if c == '.' && m[1]+1 < len(text) && isDigitByte(text[m[1]+1]) {
				continue
			}
		}
		kept = append(kept, m)
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

func isDigitByte(b byte) bool { return b >= '0' && b <= '9' }
