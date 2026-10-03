package egressfilter

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"nudgebee/llm/common"
)

// maxShapeLen caps a rendered shape. Long secrets (PEM bodies, base64 blobs)
// would otherwise put a multi-KB string in every audit row for no diagnostic
// gain — the first 64 character classes already tell you what you are looking
// at. Length is reported separately and is never truncated.
const maxShapeLen = 64

// structuralRunes is the CLOSED set of characters that survive into a shape.
// Everything outside it collapses to `#`. The set is deliberately small: it is
// only what makes a shape readable as a shape ("is this an email? a URL? a
// dotted host?"), not a general punctuation pass-through.
//
// An open default branch is a content leak. Before this was closed,
// ValueShape("!@#$%^&*()_+") returned itself and ValueShape("P@$$!") returned
// "A@$$!" — 4 of 5 characters preserved. Punctuation-dense passwords are
// exactly what generic-kv-secret and high-entropy-blob match, so the field an
// operator reaches for was reproducing the credential.
const structuralRunes = "@.-_/:+ "

// ValueShape renders a value as a character-class mask: letters collapse to
// `a`/`A`, digits to `9`, the structural characters in structuralRunes survive
// verbatim, and every other rune collapses to `#`.
//
//	zaphod.b@galaxy.test  ->  aaaaaa.a@aaaaaa.aaaa
//	+1 415-555-0142       ->  +9 999-999-9999
//	AKIAIOSFODNN7EXAMPLE  ->  AAAAAAAAAAAA9AAAAAAA
//
// This is what the audit trail carries when value reveal is off. It answers
// the question the raw value is usually wanted for — "is this really an email
// / did the detector grab the right span?" — while carrying no content: every
// value of a given shape renders identically, so nothing about the specific
// person, host, or credential survives.
func ValueShape(v string) string {
	if v == "" {
		return ""
	}
	var b strings.Builder
	// Cap the pre-allocation: output is bounded to maxShapeLen runes plus the
	// ellipsis, so a multi-MB match (PEM body, base64 blob) must not reserve
	// its own length for a 64-char result. 4 bytes/rune covers the widest
	// verbatim structural rune. (Gemini review.)
	if alloc := len(v); alloc > maxShapeLen*4 {
		b.Grow(maxShapeLen*4 + len("…"))
	} else {
		b.Grow(alloc)
	}
	n := 0
	for _, r := range v {
		if n >= maxShapeLen {
			b.WriteString("…")
			break
		}
		// Unicode-aware, NOT an ASCII range check. An ASCII-only mask writes
		// every other rune through verbatim, so "Владимир Петров" masked to
		// itself and a Japanese name survived in full — raw PII persisted in
		// the shape field with reveal OFF, which is the exact at-rest exposure
		// this function exists to prevent. (Gemini review on PR #35859.)
		switch {
		case unicode.IsLower(r):
			b.WriteByte('a')
		case unicode.IsUpper(r):
			b.WriteByte('A')
		case unicode.IsLetter(r):
			// Caseless scripts (CJK, Arabic, Hebrew, Devanagari).
			b.WriteByte('a')
		case unicode.IsDigit(r):
			b.WriteByte('9')
		case strings.ContainsRune(structuralRunes, r):
			b.WriteRune(r)
		default:
			// Closed set: anything else is content, not structure.
			b.WriteByte('#')
		}
		n++
	}
	return b.String()
}

// RevealValues reports whether audit events may carry raw matched values.
//
// DANGEROUS, TESTING ONLY, DEFAULT OFF. When on, every detected secret and
// every piece of PII is written verbatim into
// `llm_conversation_messages.metadata` — a persistent, queryable table. That
// is precisely the at-rest exposure the scrubber exists to prevent: API keys,
// database passwords and personal data all land in Postgres and stay there.
// A tenant running `pii_mode=enforce` for HIPAA/GDPR reasons is not compliant
// with this on.
//
// Use it to debug a specific false positive on a non-production environment,
// then turn it off.
//
// Scoped PER TENANT via the feature_flag table, so enabling it for one tenant
// under investigation does not expose every other tenant on the same instance.
// Enrol with:
//
//	INSERT INTO public.feature_flag (feature_id, tenant_id, status)
//	VALUES ('EGRESSFILTER_REVEAL_VALUES', '<tenant-uuid>', 'enabled');
//
// Reads are cached (see common.IsFeatureEnabled), so a UI flip takes effect
// within the cache TTL rather than instantly.
func RevealValues(ctx context.Context) bool {
	tenantID, ok := tenantIDFromContext(ctx)
	if !ok {
		return false
	}
	// Fail CLOSED on any lookup error: an unreachable DB or a cache miss must
	// never be the reason raw secrets start being persisted.
	on, err := revealLookup(tenantID.String())
	if err != nil {
		return false
	}
	return on
}

// revealLookup is the tenant feature-flag read, indirected so tests can
// substitute it without a database — same seam style as tenantConfigLoader.
var revealLookup = func(tenantID string) (bool, error) {
	return common.IsFeatureEnabled(FeatureRevealValues, tenantID)
}

// FeatureRevealValues is the feature.value registered by migration V905.
const FeatureRevealValues = "EGRESSFILTER_REVEAL_VALUES"

// revealed returns v when reveal is on and "" otherwise. Takes the resolved
// bool rather than ctx so the per-tenant lookup happens ONCE per event, not
// once per value — and so every value in one event agrees on the decision.
func revealed(reveal bool, v string) string {
	if reveal {
		return v
	}
	return ""
}

// annotateHits fills the descriptive fields (Length, Shape, optional Value)
// and KB attribution on a COPY of hits, using the payload the offsets index
// into. Returns a new slice so the caller's security decision keeps operating
// on the untouched Result.
//
// Offsets are bounds-checked: a custom Filter implementation can return
// nonsense ranges, and a panic in the audit path must not take down an LLM
// call that has otherwise already been decided.
func annotateHits(ctx context.Context, hits []Hit, payload string) []Hit {
	if len(hits) == 0 {
		return hits
	}
	reveal := RevealValues(ctx)
	// Locate the KB block ONCE per payload rather than re-scanning inside
	// kbOriginAt for every hit — that was O(hits x payload) on payloads that
	// carry a retrieved-knowledge block.
	kbOpen := strings.Index(payload, kbBlockOpen)
	out := make([]Hit, len(hits))
	copy(out, hits)
	for i := range out {
		s, e := out[i].Start, out[i].End
		if s < 0 || e > len(payload) || e < s {
			continue
		}
		v := payload[s:e]
		out[i].Length = utf8.RuneCountInString(v)
		out[i].Shape = ValueShape(v)
		out[i].Value = revealed(reveal, v)
		if kbOpen >= 0 && s > kbOpen {
			if origin, docURL, ok := kbOriginAt(payload, s); ok {
				out[i].Origin = origin
				out[i].DocURL = docURL
			}
		}
	}
	return out
}
