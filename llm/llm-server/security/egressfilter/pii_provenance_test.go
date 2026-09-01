package egressfilter

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildPIIProvenance_AttributesToPiece(t *testing.T) {
	pieces := []PIIPiece{
		{Text: "what is wrong with payments?", Source: SourceUser},
		{Text: `{"owner":"alice@acme.co"}`, Source: SourceToolResult, Tool: "kubectl_execute"},
		{Text: "call +1 415-555-0142 for escalation", Source: SourceAssistant},
	}
	mapping := map[string]string{
		"[EMAIL_1]": "alice@acme.co",
		"[PHONE_1]": "+1 415-555-0142",
	}

	got := BuildPIIProvenance(pieces, mapping)

	assert.Equal(t, SourceToolResult, got["alice@acme.co"].Source)
	assert.Equal(t, "kubectl_execute", got["alice@acme.co"].Tool)
	assert.Equal(t, SourceAssistant, got["+1 415-555-0142"].Source)
	assert.Empty(t, got["+1 415-555-0142"].Tool, "non-tool sources carry no tool name")
}

// The case this feature exists for: PII arriving via RAG retrieval, which is a
// knowledge-base curation problem rather than a cluster-data one.
func TestBuildPIIProvenance_KnowledgeBaseDoc(t *testing.T) {
	kb := "**TODAY's Date:** 2026-08-07\n" +
		"<retrieved_knowledge>\n" +
		"Runbook — CrashLoopBackOff\nSource: https://wiki/pages/97583130\n" +
		"---\n" +
		"INCIDENT REPORT INC-4821\nOn-call engineer Zaphod Beeblebrox (zaphod.b@galaxy.test) was paged.\n" +
		"Source: https://wiki/pages/109805569\n" +
		"</retrieved_knowledge>\n"
	pieces := []PIIPiece{{Text: kb, Source: SourceUser}}
	mapping := map[string]string{"[EMAIL_1]": "zaphod.b@galaxy.test"}

	got := BuildPIIProvenance(pieces, mapping)["zaphod.b@galaxy.test"]

	assert.Equal(t, OriginKnowledgeBase, got.Origin)
	assert.Equal(t, "https://wiki/pages/109805569", got.DocURL,
		"must attribute to the incident report, not the runbook above it")
}

func TestBuildPIIProvenance_EmptyInputs(t *testing.T) {
	assert.Nil(t, BuildPIIProvenance(nil, map[string]string{"[EMAIL_1]": "a@b.co"}))
	assert.Nil(t, BuildPIIProvenance([]PIIPiece{{Text: "x"}}, nil))
}

func TestAccumulator_RecordsShapeAndProvenance(t *testing.T) {
	ctx := withReveal(t, false)
	acc := NewPIIValueAccumulator()
	mapping := map[string]string{"[EMAIL_1]": "zaphod.b@galaxy.test"}
	prov := map[string]PIIValueDetail{
		"zaphod.b@galaxy.test": {Source: SourceUser, Origin: OriginKnowledgeBase, DocURL: "https://wiki/1"},
	}
	acc.AddWithProvenance(ctx, mapping, prov, "events", 28155)

	ev := acc.Consolidated()
	assert.Len(t, ev.Values, 1)
	v := ev.Values[0]
	assert.Equal(t, "[EMAIL_1]", v.Token)
	assert.Equal(t, "EMAIL", v.Category)
	assert.Equal(t, "events", v.Agent)
	assert.Equal(t, OriginKnowledgeBase, v.Origin)
	assert.Equal(t, "https://wiki/1", v.DocURL)
	assert.Equal(t, 20, v.Length)
	assert.Equal(t, "aaaaaa.a@aaaaaa.aaaa", v.Shape)
	assert.Empty(t, v.Value, "reveal off → no raw value in a persisted event")
}

func TestAccumulator_RevealOnCarriesValue(t *testing.T) {
	ctx := withReveal(t, true)
	acc := NewPIIValueAccumulator()
	acc.AddWithProvenance(ctx, map[string]string{"[EMAIL_1]": "zaphod.b@galaxy.test"}, nil, "events", 10)
	assert.Equal(t, "zaphod.b@galaxy.test", acc.Consolidated().Values[0].Value)
}

// Add() must keep working for callers that cannot attribute.
func TestAccumulator_AddWithoutProvenanceStillDescribes(t *testing.T) {
	ctx := withReveal(t, false)
	acc := NewPIIValueAccumulator()
	acc.Add(ctx, map[string]string{"[PHONE_1]": "+1 415-555-0142"}, "events", 10)
	v := acc.Consolidated().Values[0]
	assert.Equal(t, "PHONE", v.Category)
	assert.Equal(t, "+9 999-999-9999", v.Shape)
	assert.Empty(t, v.Source)
}

func TestAccumulator_FirstSeenWinsAndOrderStable(t *testing.T) {
	ctx := withReveal(t, false)
	acc := NewPIIValueAccumulator()
	acc.AddWithProvenance(ctx, map[string]string{"[EMAIL_1]": "a@x.co"},
		map[string]PIIValueDetail{"a@x.co": {Source: SourceUser}}, "first", 1)
	acc.AddWithProvenance(ctx, map[string]string{"[EMAIL_1]": "a@x.co", "[EMAIL_2]": "b@y.co"},
		map[string]PIIValueDetail{"a@x.co": {Source: SourceToolResult}}, "second", 1)

	ev := acc.Consolidated()
	assert.Equal(t, 2, ev.HitCount)
	assert.Equal(t, ValueShape("a@x.co"), ev.Values[0].Shape, "first-seen value stays first")
	assert.Equal(t, SourceUser, ev.Values[0].Source, "first attribution wins over the re-touch")
	assert.Equal(t, "first", ev.Values[0].Agent)
	assert.Equal(t, "second", ev.Values[1].Agent)
}

// Regression for a misattribution found on a real payload: kb_prestep emits
// "Source: <url>" only for documents whose metadata carries a url, so a doc
// without one is followed by the NEXT document's Source tag. Attribution must
// stop at the "---" document boundary rather than crediting the wrong page.
func TestBuildPIIProvenance_DocWithoutURLIsNotCreditedToTheNextDoc(t *testing.T) {
	kb := "<retrieved_knowledge>\n" +
		"Runbook one\nSource: https://wiki/runbook\n" +
		"\n---\n" +
		"INCIDENT REPORT INC-4821\nOn-call zaphod.b@galaxy.test was paged.\n[truncated]\n" +
		"\n---\n" +
		"Unrelated article\nSource: https://docs.example.com/rca/\n" +
		"</retrieved_knowledge>\n"
	got := BuildPIIProvenance(
		[]PIIPiece{{Text: kb, Source: SourceUser}},
		map[string]string{"[EMAIL_1]": "zaphod.b@galaxy.test"},
	)["zaphod.b@galaxy.test"]

	assert.Equal(t, OriginKnowledgeBase, got.Origin, "still known to come from RAG")
	assert.Empty(t, got.DocURL, "must not borrow the following document's url")
}

// The url must be bounded by the document, not just by the next newline: a
// Source line flush against the closing tag would otherwise swallow it.
// (Gemini review on PR #35859.)
func TestBuildPIIProvenance_URLNotFollowedByNewline(t *testing.T) {
	kb := "<retrieved_knowledge>\nIncident with alice@acme.co\nSource: https://wiki/1</retrieved_knowledge>"
	got := BuildPIIProvenance(
		[]PIIPiece{{Text: kb, Source: SourceUser}},
		map[string]string{"[EMAIL_1]": "alice@acme.co"},
	)["alice@acme.co"]
	assert.Equal(t, "https://wiki/1", got.DocURL, "must not absorb the closing tag")
}

// The cap bounds the persisted array without ever understating what was found:
// HitCount keeps the true distinct count and ValuesTruncated records the trim.
func TestAccumulator_ValuesCappedButCountIsTruthful(t *testing.T) {
	ctx := withReveal(t, false)
	acc := NewPIIValueAccumulator()
	m := map[string]string{}
	for i := 0; i < maxDetailValues+37; i++ {
		m[fmt.Sprintf("[EMAIL_%d]", i+1)] = fmt.Sprintf("person%d@example.com", i)
	}
	acc.AddWithProvenance(ctx, m, nil, "events", 1000)

	ev := acc.Consolidated()
	assert.Equal(t, maxDetailValues+37, ev.HitCount, "count must report every distinct value")
	assert.Len(t, ev.Values, maxDetailValues, "array is capped")
	assert.True(t, ev.ValuesTruncated, "trim must be visible to consumers")
	assert.Equal(t, maxDetailValues+37, ev.CategoryCounts["EMAIL"], "category counts stay complete")
}

// At or below the cap nothing is trimmed and the flag stays absent.
func TestAccumulator_UnderCapNotTruncated(t *testing.T) {
	ctx := withReveal(t, false)
	acc := NewPIIValueAccumulator()
	m := map[string]string{}
	for i := 0; i < maxDetailValues; i++ {
		m[fmt.Sprintf("[EMAIL_%d]", i+1)] = fmt.Sprintf("person%d@example.com", i)
	}
	acc.AddWithProvenance(ctx, m, nil, "events", 1000)

	ev := acc.Consolidated()
	assert.Len(t, ev.Values, maxDetailValues)
	assert.False(t, ev.ValuesTruncated)
}

// kb_prestep APPENDS the metadata url after the body, so the real tag is the
// LAST one in the document. Runbooks routinely cite their own sources inline;
// taking the first match returns that cited url instead of the KB document's.
// (Gemini review on PR #35859.)
func TestBuildPIIProvenance_InlineSourceInBodyIsNotTheDocURL(t *testing.T) {
	kb := "<retrieved_knowledge>\n" +
		"Runbook — CrashLoopBackOff\nEscalate to alice@acme.co.\n" +
		"Source: https://example.com/cited-inside-the-body\n" +
		"More body text after the citation.\n" +
		"Source: https://wiki/REAL-METADATA-URL\n" +
		"</retrieved_knowledge>\n"
	got := BuildPIIProvenance(
		[]PIIPiece{{Text: kb, Source: SourceUser}},
		map[string]string{"[EMAIL_1]": "alice@acme.co"},
	)["alice@acme.co"]

	assert.Equal(t, OriginKnowledgeBase, got.Origin)
	assert.Equal(t, "https://wiki/REAL-METADATA-URL", got.DocURL,
		"must take the appended metadata url, not one cited in the body")
}

// doc_url is arbitrary document text, not a validated field. Anything that is
// not an http(s) url of sane length must be dropped at capture — the frontend
// guard hides such a value but does not stop the write.
func TestBuildPIIProvenance_DocURLIsSanitized(t *testing.T) {
	cases := map[string]string{
		"10.0.1.5 db_password=hunter2":             "",
		"javascript:alert(1)":                      "",
		"ftp://host/file":                          "",
		"https://wiki/ok":                          "https://wiki/ok",
		"  https://wiki/trimmed  ":                 "https://wiki/trimmed",
		"https://wiki/ok see attached doc":         "https://wiki/ok",
		"https://wiki/" + strings.Repeat("x", 600): "",
		// Schemes are case-insensitive (RFC 3986 §3.1) -> a valid url must
		// survive any casing, and a dangerous one must still be dropped in
		// any casing.
		"HTTPS://wiki/upper":  "HTTPS://wiki/upper",
		"Http://wiki/mixed":   "Http://wiki/mixed",
		"JavaScript:alert(1)": "",
	}
	for src, want := range cases {
		kb := "<retrieved_knowledge>\nalice@acme.co\nSource: " + src + "\n</retrieved_knowledge>"
		got := BuildPIIProvenance([]PIIPiece{{Text: kb}}, map[string]string{"[EMAIL_1]": "alice@acme.co"})["alice@acme.co"]
		assert.Equal(t, want, got.DocURL, "Source: %q", src)
		assert.Equal(t, OriginKnowledgeBase, got.Origin, "origin survives even when the url is dropped")
	}
}

// Go randomises map iteration, so ranging the mapping directly made Values[]
// order (and therefore WHICH entries survive the cap) random per run.
func TestAccumulator_ValueOrderIsDeterministic(t *testing.T) {
	ctx := withReveal(t, false)
	orders := map[string]bool{}
	for run := 0; run < 10; run++ {
		acc := NewPIIValueAccumulator()
		m := map[string]string{}
		for i := 1; i <= 12; i++ {
			m[fmt.Sprintf("[EMAIL_%d]", i)] = fmt.Sprintf("p%d@x.co", i)
		}
		acc.AddWithProvenance(ctx, m, nil, "a", 10)
		var o []string
		for _, v := range acc.Consolidated().Values {
			o = append(o, v.Token)
		}
		orders[strings.Join(o, " ")] = true
	}
	assert.Len(t, orders, 1, "identical input must produce one ordering")
	// Numeric, not lexicographic: _10 follows _9.
	acc := NewPIIValueAccumulator()
	acc.AddWithProvenance(ctx, map[string]string{
		"[EMAIL_9]": "p9@x.co", "[EMAIL_10]": "p10@x.co", "[EMAIL_1]": "p1@x.co",
	}, nil, "a", 10)
	v := acc.Consolidated().Values
	assert.Equal(t, []string{"[EMAIL_1]", "[EMAIL_9]", "[EMAIL_10]"},
		[]string{v[0].Token, v[1].Token, v[2].Token})
}

// compareTokens must order by sign, not by difference. `na - nb` overflows
// when the operands span the int range, inverting the result. Tokens are not
// all self-generated — they also arrive from the ml-k8s /scrub response and
// from jsonb reads — and strconv.Atoi accepts a leading '-'.
func TestCompareTokens_NoOverflowInversion(t *testing.T) {
	// Ordinary case: numeric, not lexicographic.
	assert.Negative(t, compareTokens("[EMAIL_9]", "[EMAIL_10]"),
		"[EMAIL_9] must sort before [EMAIL_10]")

	min := fmt.Sprintf("[EMAIL_%d]", math.MinInt)
	one := "[EMAIL_1]"
	assert.Negative(t, compareTokens(min, one),
		"a smaller index must compare less regardless of the gap")
	assert.Positive(t, compareTokens(one, min), "and the reverse must hold")

	// The subtraction the fix replaced, shown inverting on the same input.
	na, nb := piiTokenIndex(min), piiTokenIndex(one)
	assert.Positive(t, na-nb, "na-nb overflows to a POSITIVE value here — the bug")
}
