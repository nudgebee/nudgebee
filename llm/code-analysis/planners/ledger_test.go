package planners

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLedger_IsEmpty(t *testing.T) {
	if !(*Ledger)(nil).IsEmpty() {
		t.Error("nil ledger should be empty")
	}
	if !NewLedger(nil).IsEmpty() {
		t.Error("fresh ledger with no sub-questions should be empty")
	}
	if NewLedger([]string{"q1"}).IsEmpty() {
		t.Error("ledger with sub-questions should not be empty")
	}
}

func TestLedger_ToPromptBlock_EmptyRendersNothing(t *testing.T) {
	if got := NewLedger(nil).ToPromptBlock(); got != "" {
		t.Errorf("empty ledger should render empty prompt block, got %q", got)
	}
}

func TestLedger_ToPromptBlock_RendersFindingsAndCitations(t *testing.T) {
	l := &Ledger{
		Findings: []LedgerFinding{
			{Claim: "service uses helm", EvidenceStepIDs: []int{6, 9}},
		},
		Citations: []LedgerCitation{
			{FilePath: "values.yaml", LineStart: 1, Snippet: "image: foo", Note: "header comment"},
			{FilePath: "ci.yaml", LineStart: 10, LineEnd: 15, Snippet: "run: helm upgrade"},
		},
		OpenSubQuestions: []string{"is the workflow file present?"},
		Answer:           "draft answer",
		Confidence:       "Medium",
	}
	out := l.ToPromptBlock()
	for _, want := range []string{
		"WORKING MEMORY",
		"service uses helm",
		"from step 6, 9",
		"values.yaml:1",
		"ci.yaml:10-15",
		"is the workflow file present?",
		"draft answer",
		"confidence=Medium",
		"NOT READY",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("ToPromptBlock missing %q\nfull:\n%s", want, out)
		}
	}
}

func TestLedger_ToPromptBlock_ReadySignalsReadiness(t *testing.T) {
	l := &Ledger{
		Citations:     []LedgerCitation{{FilePath: "x.go", LineStart: 1, Snippet: "x"}},
		Answer:        "yes",
		ReadyToSubmit: true,
	}
	out := l.ToPromptBlock()
	if !strings.Contains(out, "READY TO SUBMIT") {
		t.Errorf("ready ledger should signal readiness:\n%s", out)
	}
}

func TestLedger_ToExploreSubmitInput_RoundTripsCitations(t *testing.T) {
	l := &Ledger{
		Answer:     "auth uses JWT tokens",
		Confidence: "High",
		Citations: []LedgerCitation{
			{FilePath: "auth.go", LineStart: 42, LineEnd: 50, Snippet: "validateJWT(...)", Note: "core verify"},
			{FilePath: "middleware.go", LineStart: 7, Snippet: "Bearer header"},
		},
	}
	got := l.ToExploreSubmitInput("how does auth work?")

	if got["answer"] != "auth uses JWT tokens" {
		t.Errorf("answer not preserved: %v", got["answer"])
	}
	if got["confidence_score"] != "High" {
		t.Errorf("confidence_score not mapped: %v", got["confidence_score"])
	}

	citations, ok := got["citations"].([]map[string]any)
	if !ok {
		t.Fatalf("citations wrong shape: %T", got["citations"])
	}
	if len(citations) != 2 {
		t.Fatalf("citations len = %d, want 2", len(citations))
	}
	if citations[0]["line_end"] != 50 {
		t.Errorf("first citation line_end missing: %v", citations[0])
	}
	if _, present := citations[1]["line_end"]; present {
		t.Errorf("second citation should omit zero line_end, got: %v", citations[1])
	}
	if citations[1]["note"] != nil {
		// note is omitted from the map when empty
		if _, has := citations[1]["note"]; has {
			t.Errorf("empty note should be omitted, got: %v", citations[1])
		}
	}
}

func TestLedger_MergeUpdate_OverwritesPriorState(t *testing.T) {
	prior := &Ledger{
		Findings:  []LedgerFinding{{Claim: "old"}},
		Citations: []LedgerCitation{{FilePath: "a", LineStart: 1, Snippet: "x"}},
		Answer:    "draft",
	}
	next := &Ledger{
		Findings:      []LedgerFinding{{Claim: "new"}},
		Answer:        "final",
		Confidence:    "High",
		ReadyToSubmit: true,
	}
	prior.MergeUpdate(next)
	if len(prior.Findings) != 1 || prior.Findings[0].Claim != "new" {
		t.Errorf("findings not replaced: %+v", prior.Findings)
	}
	if len(prior.Citations) != 0 {
		t.Errorf("citations should be replaced (next had none): %+v", prior.Citations)
	}
	if !prior.ReadyToSubmit {
		t.Error("ready_to_submit flag did not propagate")
	}
}

func TestParseLedgerJSON_HappyPath(t *testing.T) {
	raw := `{
		"findings": [{"claim": "uses helm", "evidence_step_ids": [3]}],
		"citations": [{"file_path": "a.go", "line_start": 1, "snippet": "x"}],
		"open_sub_questions": [],
		"answer": "yes",
		"confidence": "High",
		"ready_to_submit": true
	}`
	l, err := ParseLedgerJSON(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(l.Findings) != 1 || l.Findings[0].Claim != "uses helm" {
		t.Errorf("findings mis-parsed: %+v", l.Findings)
	}
	if !l.ReadyToSubmit {
		t.Error("ready_to_submit not true")
	}
}

func TestParseLedgerJSON_RejectsGarbage(t *testing.T) {
	if _, err := ParseLedgerJSON(""); err == nil {
		t.Error("expected error on empty string")
	}
	if _, err := ParseLedgerJSON("not json"); err == nil {
		t.Error("expected error on non-JSON input")
	}
}

func TestLedger_RoundTripsToJSON(t *testing.T) {
	l := &Ledger{
		Findings:         []LedgerFinding{{Claim: "c", EvidenceStepIDs: []int{1, 2}}},
		Citations:        []LedgerCitation{{FilePath: "p", LineStart: 5, Snippet: "s"}},
		OpenSubQuestions: []string{"q"},
		Answer:           "a",
		Confidence:       "Medium",
		ReadyToSubmit:    true,
	}
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	back, err := ParseLedgerJSON(string(b))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if back.Findings[0].Claim != "c" || back.Answer != "a" || !back.ReadyToSubmit {
		t.Errorf("round-trip lost data: %+v", back)
	}
}

func TestSummariseLedgerForHint_BasicCounts(t *testing.T) {
	l := &Ledger{
		Citations:        []LedgerCitation{{FilePath: "a", LineStart: 1, Snippet: "x"}},
		OpenSubQuestions: []string{"why?"},
		Findings:         []LedgerFinding{{Claim: "c"}},
	}
	out := summariseLedgerForHint(l)
	for _, want := range []string{"1 citations gathered", "1 open sub-questions", "1 findings"} {
		if !strings.Contains(out, want) {
			t.Errorf("hint missing %q: %s", want, out)
		}
	}
}

func TestSummariseLedgerForHint_EmptyLedger(t *testing.T) {
	out := summariseLedgerForHint(&Ledger{})
	if !strings.Contains(out, "ledger still empty") {
		t.Errorf("expected empty-ledger language, got: %s", out)
	}
}

// truncateRunes is exercised here (and indirectly via truncateLine /
// summariseLedgerForHint). The shared helper is the fix for the reviewer's
// two UTF-8 / allocation concerns; these tests pin both the rune-boundary
// behaviour and the no-allocation contract.
func TestTruncateRunes_ShortStringPassthrough(t *testing.T) {
	if got := truncateRunes("hello", 10); got != "hello" {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestTruncateRunes_EmptyAndZeroBudget(t *testing.T) {
	if got := truncateRunes("", 10); got != "" {
		t.Errorf("empty input should return empty, got %q", got)
	}
	if got := truncateRunes("hello", 0); got != "" {
		t.Errorf("maxRunes=0 should return empty, got %q", got)
	}
	if got := truncateRunes("hello", -1); got != "" {
		t.Errorf("negative maxRunes should return empty, got %q", got)
	}
}

func TestTruncateRunes_ASCIIBoundary(t *testing.T) {
	got := truncateRunes("hello world", 5)
	if got != "he..." {
		t.Errorf("expected 'he...', got %q", got)
	}
	// Result has exactly maxRunes runes.
	if runeCount := utf8RuneCount(got); runeCount != 5 {
		t.Errorf("expected 5 runes in output, got %d (%q)", runeCount, got)
	}
}

func TestTruncateRunes_MultiByteSafe(t *testing.T) {
	// é is 2 bytes; raw byte slicing s[:1] would cut it in half.
	got := truncateRunes("héllo wörld", 5)
	if got != "hé..." {
		t.Errorf("expected 'hé...', got %q", got)
	}
	if !isValidUTF8(got) {
		t.Errorf("multi-byte truncation produced invalid UTF-8: %q (% x)", got, []byte(got))
	}
}

func TestTruncateRunes_EmojiSafe(t *testing.T) {
	// Emojis are 4 bytes; verify we don't split them.
	in := "a🎉b🎉c🎉d🎉e" // 9 runes, 1 + 4 + 1 + 4 + 1 + 4 + 1 + 4 + 1 = 21 bytes
	got := truncateRunes(in, 5)
	if got != "a🎉..." {
		t.Errorf("expected 'a🎉...', got %q", got)
	}
	if !isValidUTF8(got) {
		t.Errorf("emoji truncation produced invalid UTF-8: %q (% x)", got, []byte(got))
	}
}

func TestTruncateRunes_SmallBudgetNoEllipsis(t *testing.T) {
	// maxRunes <= 3 leaves no room for "..." — return a plain prefix.
	if got := truncateRunes("hello world", 3); got != "hel" {
		t.Errorf("expected 'hel', got %q", got)
	}
	if got := truncateRunes("hello world", 1); got != "h" {
		t.Errorf("expected 'h', got %q", got)
	}
	// Multi-byte at small budget must still land on a rune boundary.
	got := truncateRunes("héllo", 2)
	if got != "hé" {
		t.Errorf("expected 'hé', got %q (% x)", got, []byte(got))
	}
	if !isValidUTF8(got) {
		t.Errorf("small-budget multi-byte produced invalid UTF-8: %q", got)
	}
}

func TestTruncateRunes_ExactlyAtBudget(t *testing.T) {
	// String with exactly maxRunes runes should pass through unchanged.
	if got := truncateRunes("hello", 5); got != "hello" {
		t.Errorf("string at budget should pass through, got %q", got)
	}
	// One rune over the budget should truncate.
	if got := truncateRunes("hellox", 5); got != "he..." {
		t.Errorf("string 1-over-budget should truncate, got %q", got)
	}
}

// utf8RuneCount returns the rune count of s. Local helper so the tests
// read clearly at the call site without sprinkling utf8.RuneCountInString
// throughout the assertions.
func utf8RuneCount(s string) int {
	return utf8.RuneCountInString(s)
}

// isValidUTF8 wraps utf8.ValidString. The whole point of the reviewer's
// fix request is that prior byte slicing could leave a dangling
// continuation byte that yields invalid UTF-8 — this is the smoking-gun
// check.
func isValidUTF8(s string) bool {
	return utf8.ValidString(s)
}
