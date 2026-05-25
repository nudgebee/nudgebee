package planners

import (
	"encoding/json"
	"fmt"
	"strings"
)

// LedgerCitation mirrors tools.Citation but lives in the planners package to
// avoid importing the heavyweight tools package for a 4-field struct. The
// shape MUST match tools.Citation's JSON so a ledger can be handed directly to
// submit_analysis without re-mapping.
type LedgerCitation struct {
	FilePath  string `json:"file_path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end,omitempty"`
	Snippet   string `json:"snippet"`
	Note      string `json:"note,omitempty"`
}

// LedgerFinding is one claim the agent has established, with pointers to the
// step numbers whose observations support it. Step IDs let reflection trace
// claims back to evidence cheaply (no need to re-read raw observations).
type LedgerFinding struct {
	Claim            string `json:"claim"`
	EvidenceStepIDs  []int  `json:"evidence_step_ids,omitempty"`
	SubQuestionIndex int    `json:"sub_question_index,omitempty"` // -1 if not tied to a specific sub-question
}

// Ledger is the LLM-maintained working memory threaded through the Plan loop.
// It replaces the sliding-window transcript as the canonical "what do I know"
// view — the planner appends recent tool observations in the prompt, but the
// agent reasons from the ledger.
//
// The ledger is updated by the reflection LLM call (not by parsing tool
// outputs heuristically). Each reflection reads the recent tool history plus
// the prior ledger and emits an updated ledger. This keeps memory consolidation
// inside the LLM rather than guessing in Go regex.
type Ledger struct {
	Findings         []LedgerFinding  `json:"findings,omitempty"`
	Citations        []LedgerCitation `json:"citations,omitempty"`
	OpenSubQuestions []string         `json:"open_sub_questions,omitempty"`

	// Answer is the headline 1–3 sentence response. Required in explore mode
	// before submit_analysis can be constructed. The reflection step fills
	// this in once the citations cover the open sub-questions.
	Answer string `json:"answer,omitempty"`

	// Confidence is reported by the reflection step (High|Medium|Low).
	Confidence string `json:"confidence,omitempty"`

	// ReadyToSubmit signals the planner to terminate. The reflection step
	// flips this once Answer is populated and every open_sub_question has at
	// least one supporting citation. The planner does not invent this flag —
	// it trusts reflection.
	ReadyToSubmit bool `json:"ready_to_submit,omitempty"`
}

// NewLedger returns a fresh ledger seeded with sub-questions if any are
// supplied. Sub-questions decompose the original query; reflection updates
// `OpenSubQuestions` as findings accumulate.
func NewLedger(initialSubQuestions []string) *Ledger {
	return &Ledger{
		OpenSubQuestions: append([]string(nil), initialSubQuestions...),
	}
}

// ToPromptBlock renders the ledger as a readable section for the next LLM
// turn. Returns "" when the ledger is empty so the planner can skip the
// section entirely on iteration 0.
func (l *Ledger) ToPromptBlock() string {
	if l == nil || l.IsEmpty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("## WORKING MEMORY (LEDGER)\n\n")
	b.WriteString("This is your consolidated understanding so far. Trust it — don't re-derive from scratch.\n\n")

	if len(l.Findings) > 0 {
		b.WriteString("### Findings established\n")
		for i, f := range l.Findings {
			fmt.Fprintf(&b, "%d. %s", i+1, f.Claim)
			if len(f.EvidenceStepIDs) > 0 {
				fmt.Fprintf(&b, " (from step %s)", formatStepIDs(f.EvidenceStepIDs))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(l.Citations) > 0 {
		b.WriteString("### Citations gathered\n")
		for i, c := range l.Citations {
			end := c.LineEnd
			if end == 0 || end == c.LineStart {
				fmt.Fprintf(&b, "%d. %s:%d", i+1, c.FilePath, c.LineStart)
			} else {
				fmt.Fprintf(&b, "%d. %s:%d-%d", i+1, c.FilePath, c.LineStart, end)
			}
			if c.Note != "" {
				fmt.Fprintf(&b, " — %s", c.Note)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(l.OpenSubQuestions) > 0 {
		b.WriteString("### Open sub-questions\n")
		for i, q := range l.OpenSubQuestions {
			fmt.Fprintf(&b, "%d. %s\n", i+1, q)
		}
		b.WriteString("\n")
	}

	if l.Answer != "" {
		fmt.Fprintf(&b, "### Draft answer (confidence=%s)\n%s\n\n", l.Confidence, l.Answer)
	}

	if l.ReadyToSubmit {
		b.WriteString("Status: READY TO SUBMIT — call submit_analysis with the contents of this ledger now.\n")
	} else {
		b.WriteString("Status: NOT READY — gaps remain. Pick the highest-leverage next tool call to close the open sub-questions.\n")
	}
	return b.String()
}

// IsEmpty reports whether the ledger has any meaningful content. Used to
// suppress the prompt section before the first reflection has run.
func (l *Ledger) IsEmpty() bool {
	if l == nil {
		return true
	}
	return len(l.Findings) == 0 &&
		len(l.Citations) == 0 &&
		len(l.OpenSubQuestions) == 0 &&
		l.Answer == "" &&
		!l.ReadyToSubmit
}

// ToExploreSubmitInput converts the ledger into the ActionInput map expected
// by submit_analysis in explore mode. Used when reflection says ready_to_submit
// and the planner needs to terminate cleanly — the ledger is already in the
// shape of the contract, so this is a struct-to-map projection, not a guess.
func (l *Ledger) ToExploreSubmitInput(query string) map[string]any {
	citations := make([]map[string]any, 0, len(l.Citations))
	for _, c := range l.Citations {
		entry := map[string]any{
			"file_path":  c.FilePath,
			"line_start": c.LineStart,
			"snippet":    c.Snippet,
		}
		if c.LineEnd != 0 {
			entry["line_end"] = c.LineEnd
		}
		if c.Note != "" {
			entry["note"] = c.Note
		}
		citations = append(citations, entry)
	}
	return map[string]any{
		"answer":           l.Answer,
		"citations":        citations,
		"confidence_score": l.Confidence,
	}
}

// MergeUpdate applies a reflection-produced ledger to this one. The reflection
// step is authoritative — it returns the new full ledger, not a delta — so we
// overwrite. Kept as a method (rather than direct field assignment) so future
// migrations (e.g. keeping a history of supplanted findings) have one place to
// land.
func (l *Ledger) MergeUpdate(next *Ledger) {
	if next == nil || l == nil {
		return
	}
	l.Findings = next.Findings
	l.Citations = next.Citations
	l.OpenSubQuestions = next.OpenSubQuestions
	l.Answer = next.Answer
	l.Confidence = next.Confidence
	l.ReadyToSubmit = next.ReadyToSubmit
}

// formatStepIDs renders a list of step IDs compactly: "3" or "3, 7, 12".
func formatStepIDs(ids []int) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("%d", id)
	}
	return strings.Join(parts, ", ")
}

// truncateRunes returns s clipped to at most maxRunes runes, appending "..."
// when truncation actually occurred. UTF-8-safe (never slices through a
// multi-byte rune) and allocation-free in the common case: walks the string
// once with `range`, recording the byte offset of the (maxRunes-3)-th rune
// without ever materialising a []rune.
//
// Used by ledger prompt rendering and reflection-step formatting. Replaces
// two earlier call sites that used either []rune conversion (allocates the
// whole slice up front) or raw byte slicing (could split a 2/3/4-byte rune
// and emit invalid UTF-8 into prompts/logs/JSON).
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || s == "" {
		return ""
	}
	// Budget too small to fit "..." — return a plain prefix at a rune
	// boundary.
	if maxRunes <= 3 {
		runeCount := 0
		for i := range s {
			if runeCount == maxRunes {
				return s[:i]
			}
			runeCount++
		}
		return s
	}
	// One walk: remember the byte index of the (maxRunes-3)-th rune
	// (where the ellipsis would start), and count total runes. Only
	// truncate if the count actually exceeds maxRunes.
	runeCount := 0
	cutByte := -1
	for i := range s {
		if runeCount == maxRunes-3 {
			cutByte = i
		}
		runeCount++
	}
	if runeCount <= maxRunes {
		return s
	}
	return s[:cutByte] + "..."
}

// ledgerJSONSchema is the JSON description the reflection LLM is asked to
// produce. Kept as a literal string so the schema doc lives next to the type
// rather than drifting in a prompt file. Exported via reflectionPromptSchema()
// for the reflection module.
const ledgerJSONSchema = `{
  "findings": [
    {"claim": "<one-sentence fact>", "evidence_step_ids": [<int>, ...], "sub_question_index": <0-based int or -1>}
  ],
  "citations": [
    {"file_path": "<repo-relative path>", "line_start": <int>, "line_end": <int|0>, "snippet": "<exact code lines>", "note": "<one-line why>"}
  ],
  "open_sub_questions": ["<remaining question>", ...],
  "answer": "<1-3 plain-prose sentences answering the original query, or empty if not ready>",
  "confidence": "High|Medium|Low",
  "ready_to_submit": <bool>
}`

// ParseLedgerJSON extracts a Ledger from a raw LLM-emitted JSON string. The
// reflection step calls this; tests poke it directly to verify schema
// roundtrip. Returns (ledger, error). On error, ledger is nil.
func ParseLedgerJSON(raw string) (*Ledger, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty ledger JSON")
	}
	var l Ledger
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		return nil, fmt.Errorf("parse ledger: %w", err)
	}
	return &l, nil
}
