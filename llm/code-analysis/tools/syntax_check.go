package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Post-edit syntax gate: a dependency-free, parse-only check run after each
// successful replace/write_file, so an edit that mechanically breaks a file is
// caught immediately (free, deterministic) instead of sailing into LLM review
// or CI.
//
// Design constraints (deliberate — see docs/token-cost-optimization-spec.md §4
// item 6): the workspace pod has NO dependency builder, and analyzed repos can
// use any language/version/toolchain. Therefore:
//   - ONLY pure parsers run (gofmt -e, python3 -m py_compile): they need no
//     go.mod resolution, no installed deps, no project config, so they cannot
//     fail for environment reasons.
//   - Tri-state result: passed | failed | not_checked. No parser for the file
//     or the binary is missing → not_checked, silently. Never a false failure.
//   - Advisory only: callers append the result to the tool observation; it
//     never blocks or gates the run.
//   - Never runs project builds, linters or tests.

// SyntaxCheckStatus is the tri-state outcome of a post-edit syntax check.
type SyntaxCheckStatus string

const (
	SyntaxCheckPassed     SyntaxCheckStatus = "passed"
	SyntaxCheckFailed     SyntaxCheckStatus = "failed"
	SyntaxCheckNotChecked SyntaxCheckStatus = "not_checked"
)

// SyntaxCheckResult carries the outcome plus a bounded human/LLM-readable detail.
type SyntaxCheckResult struct {
	Status  SyntaxCheckStatus
	Checker string // e.g. "gofmt -e", "python3 -m py_compile"
	Detail  string // bounded parser output on failure; "" otherwise
}

const (
	syntaxCheckTimeout   = 10 * time.Second
	syntaxCheckMaxDetail = 700
)

// syntaxCheckDisabled reports whether the gate is switched off via env
// (POST_EDIT_SYNTAX_CHECK=0). Default ON — safe because the check is advisory
// and cannot produce environment-shaped false failures by construction.
func syntaxCheckDisabled() bool {
	return strings.TrimSpace(os.Getenv("POST_EDIT_SYNTAX_CHECK")) == "0"
}

// CheckEditedFileSyntax runs the parse-only check appropriate for the file's
// language. Unknown extension or missing binary → SyntaxCheckNotChecked.
func CheckEditedFileSyntax(absPath string) SyntaxCheckResult {
	if syntaxCheckDisabled() {
		return SyntaxCheckResult{Status: SyntaxCheckNotChecked}
	}
	var bin string
	var args []string
	var checker string
	switch strings.ToLower(filepath.Ext(absPath)) {
	case ".go":
		bin, args, checker = "gofmt", []string{"-e", absPath}, "gofmt -e"
	case ".py":
		// In-memory compile: `-m py_compile` would write __pycache__/*.pyc into
		// the ANALYZED repo, polluting the customer workspace and confusing
		// later searches. compile() performs identical syntax validation with
		// zero writes.
		bin, args, checker = "python3", []string{"-c", "import sys; compile(open(sys.argv[1], 'rb').read(), sys.argv[1], 'exec')", absPath}, "python3 compile check"
	default:
		return SyntaxCheckResult{Status: SyntaxCheckNotChecked}
	}
	binPath, err := exec.LookPath(bin)
	if err != nil {
		// Binary absent in this environment — not the edit's fault.
		return SyntaxCheckResult{Status: SyntaxCheckNotChecked}
	}

	ctx, cancel := context.WithTimeout(context.Background(), syntaxCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath, args...)
	out, runErr := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		// A hung parser is an environment problem, not a syntax verdict.
		return SyntaxCheckResult{Status: SyntaxCheckNotChecked, Checker: checker}
	}
	if runErr == nil {
		// A syntactically valid .py file can still carry an undefined name
		// (use-before-definition) — compile() cannot see it, and it has
		// shipped broken fixes into PRs. Run the static undefined-name pass
		// as a second gate; absent checkers keep the compile verdict.
		if strings.ToLower(filepath.Ext(absPath)) == ".py" {
			if r := checkPythonUndefinedNames(absPath); r.Status == SyntaxCheckFailed {
				return r
			}
		}
		return SyntaxCheckResult{Status: SyntaxCheckPassed, Checker: checker}
	}
	// Kept whole. Truncation happens at presentation (AppendToObservation) so
	// that CheckEditedFileSyntaxDelta compares complete finding sets — cutting
	// here made two runs of the same file differ purely because their temp paths
	// were different lengths, and every finding past the cut looked new.
	detail := strings.TrimSpace(string(out))
	if detail == "" {
		// Non-zero exit with no diagnostics — can't attribute to the edit.
		return SyntaxCheckResult{Status: SyntaxCheckNotChecked, Checker: checker}
	}
	return SyntaxCheckResult{Status: SyntaxCheckFailed, Checker: checker, Detail: detail}
}

// checkPythonUndefinedNames statically detects undefined names in a single
// .py file — the defect class compile() cannot catch (e.g. a variable used
// before its definition). Prefers pyflakes (dependency-free single-file
// checker, no project resolution needed), falls back to ruff's F821/F823
// rules. Output is filtered to undefined-name findings only, so stylistic
// pyflakes warnings (unused imports etc.) on a legitimately edited file never
// surface as a failure. Both checkers absent → not_checked, per the gate's
// tri-state contract.
func checkPythonUndefinedNames(absPath string) SyntaxCheckResult {
	type candidate struct {
		bin     string
		args    []string
		checker string
	}
	candidates := []candidate{
		{"pyflakes", []string{absPath}, "pyflakes"},
		{"python3", []string{"-m", "pyflakes", absPath}, "python3 -m pyflakes"},
		{"ruff", []string{"check", "--select", "F821,F823", "--no-cache", "--quiet", absPath}, "ruff F821/F823"},
	}
	for _, c := range candidates {
		binPath, err := exec.LookPath(c.bin)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), syntaxCheckTimeout)
		cmd := exec.CommandContext(ctx, binPath, c.args...)
		out, runErr := cmd.CombinedOutput()
		expired := ctx.Err() == context.DeadlineExceeded
		cancel()
		if expired {
			return SyntaxCheckResult{Status: SyntaxCheckNotChecked, Checker: c.checker}
		}
		// `python3 -m pyflakes` exits 1 with "No module named pyflakes" when
		// the module is absent — try the next candidate, don't misread it as
		// a finding. Case-insensitive: interpreter wording varies.
		if runErr != nil && strings.Contains(strings.ToLower(string(out)), "no module named") {
			continue
		}
		if runErr == nil {
			return SyntaxCheckResult{Status: SyntaxCheckPassed, Checker: c.checker}
		}
		var findings []string
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			l := strings.ToLower(line)
			// "'from x import *' used; unable to detect undefined names" is a
			// CAPABILITY NOTICE, not a finding — pyflakes saying it cannot judge
			// this file. It contains the substring "undefined names", so a naive
			// match reports it as a defect. That is what fired on astropy's
			// builtin_frames/__init__.py and told the agent it had broken syntax
			// it never touched. Exclude it before matching anything else.
			if strings.Contains(l, "unable to detect undefined names") {
				continue
			}
			// Under a star import pyflakes hedges to "'x' may be undefined, or
			// defined from star imports" instead of "undefined name 'x'". Without
			// this the real finding is silently dropped in exactly the files where
			// the notice above is loudest — a false negative paired with a false
			// positive.
			if strings.Contains(l, "undefined name") || strings.Contains(l, "undefined local") ||
				strings.Contains(l, "may be undefined") ||
				strings.Contains(l, "referenced before assignment") || strings.Contains(l, "f821") || strings.Contains(l, "f823") {
				findings = append(findings, strings.TrimSpace(line))
			}
		}
		if len(findings) == 0 {
			// Checker complained about something other than undefined names
			// (or produced no parseable diagnostics) — not this gate's call.
			return SyntaxCheckResult{Status: SyntaxCheckPassed, Checker: c.checker}
		}
		// Whole, for the same reason — see the note at the compile-based site.
		return SyntaxCheckResult{Status: SyntaxCheckFailed, Checker: c.checker, Detail: strings.Join(findings, "\n")}
	}
	return SyntaxCheckResult{Status: SyntaxCheckNotChecked}
}

// AppendToObservation renders the check outcome onto a tool observation.
// Failure gets a bounded, actionable note; success a minimal confirmation;
// not_checked adds nothing (silence — no parser is not a finding).
func (r SyntaxCheckResult) AppendToObservation(obs string) string {
	switch r.Status {
	case SyntaxCheckFailed:
		return fmt.Sprintf("%s\n\n⚠ POST-EDIT SYNTAX CHECK FAILED (%s) — your edit likely broke the file's syntax. Fix this before proceeding:\n%s", obs, r.Checker, truncateDetail(r.Detail))
	case SyntaxCheckPassed:
		return fmt.Sprintf("%s\n[syntax OK: %s]", obs, r.Checker)
	default:
		return obs
	}
}

// --- before/after attribution -------------------------------------------------
//
// CheckEditedFileSyntax answers "does this file have undefined-name findings?"
// and the observation then tells the agent "your edit likely broke the file's
// syntax". Those are different questions whenever the file already had findings.
//
// astropy's `builtin_frames/__init__.py` carries a long-standing
// `'from .ecliptic import *' unable to detect undefined names`. Editing one
// unrelated line there produced the full "YOUR EDIT LIKELY BROKE THE FILE'S
// SYNTAX. Fix this before proceeding" banner attached to a successful edit.
// With no runtime in the workspace, that is the strongest signal the agent
// gets, so it edits again — SWE-bench astropy-13398 burned 6 edits and 12
// minutes that way and ended with a genuinely broken import.
//
// Comparing against the pre-edit content answers the question actually being
// asked. Findings are matched on message text, not path:line:col, because an
// edit shifts line numbers and would otherwise make every pre-existing finding
// look new.

// findingMessageRe strips the "path:line:col: " prefix from a checker finding,
// leaving the message — the part that is stable across an edit.
var findingMessageRe = regexp.MustCompile(`^[^:]+:\d+:(?:\d+:)?\s*`)

// tracebackLineRe matches the line number inside a Python traceback frame:
//
//	File "/path/to/mod.py", line 12
//
// A compile error's detail is a traceback, not a "path:line:col:" finding, so
// findingMessageRe does not touch it and the raw text is compared. An edit ABOVE
// a pre-existing SyntaxError shifts that number — baseline says line 10, the
// edited file says line 12 — and the identical, pre-existing error would be
// reported as introduced. Masked for comparison only.
var tracebackLineRe = regexp.MustCompile(`(, line )\d+`)

// newFindings returns the findings in after whose message does not already
// appear in before, preserving order and multiplicity: a second copy of an
// existing message is genuinely new.
func newFindings(after, before string) []string {
	// Comparison key: message text with traceback line numbers masked. Both are
	// things an unrelated edit can move without changing what the finding says.
	key := func(line string) string {
		k := strings.TrimSpace(findingMessageRe.ReplaceAllString(strings.TrimSpace(line), ""))
		return tracebackLineRe.ReplaceAllString(k, "${1}N")
	}
	baseline := map[string]int{}
	for _, l := range strings.Split(before, "\n") {
		if m := key(l); m != "" {
			baseline[m]++
		}
	}
	var out []string
	for _, l := range strings.Split(after, "\n") {
		line := strings.TrimSpace(l)
		if line == "" {
			continue
		}
		msg := key(line)
		if msg != "" && baseline[msg] > 0 {
			baseline[msg]--
			continue
		}
		out = append(out, line)
	}
	return out
}

// CheckEditedFileSyntaxDelta reports only what the edit introduced.
//
// before is the file's content prior to the edit; nil means no baseline could
// be captured (a newly created file, or an unreadable one), in which case this
// degrades to the plain post-edit check — every finding is attributable,
// because there was nothing there before.
func CheckEditedFileSyntaxDelta(absPath string, before []byte) SyntaxCheckResult {
	after := CheckEditedFileSyntax(absPath)
	if after.Status != SyntaxCheckFailed || before == nil {
		return after
	}

	baseline, baselinePath, ok := checkContentAsFile(absPath, before)
	if !ok || baseline.Status != SyntaxCheckFailed {
		// Either the baseline could not be established, or the file was clean
		// before this edit. Both mean the finding is the edit's to answer for.
		return after
	}

	// Compare with paths neutralised. A compile error's detail is a Python
	// traceback that embeds the file path — `File "/tmp/.../baseline.py", line 3`
	// versus `File "/work/.../cds.py", line 3` — so an identical pre-existing
	// SyntaxError would never match and would be reported as introduced. Only the
	// comparison is normalised; the findings handed back keep their real paths.
	introduced := newFindings(
		normalizeDetailPaths(after.Detail, absPath),
		normalizeDetailPaths(baseline.Detail, baselinePath),
	)
	introduced = denormalizeDetailPaths(introduced, absPath)
	if len(introduced) == 0 {
		// Every finding predates the edit. Saying nothing is the whole point:
		// an advisory that fires on someone else's code is worse than silence.
		return SyntaxCheckResult{Status: SyntaxCheckPassed, Checker: after.Checker}
	}
	return SyntaxCheckResult{Status: SyntaxCheckFailed, Checker: after.Checker, Detail: strings.Join(introduced, "\n")}
}

// checkContentAsFile runs the same check over content written to a throwaway
// file that keeps origPath's extension, so checker selection is unchanged.
func checkContentAsFile(origPath string, content []byte) (SyntaxCheckResult, string, bool) {
	dir, err := os.MkdirTemp("", "syntax-baseline-")
	if err != nil {
		return SyntaxCheckResult{}, "", false
	}
	defer func() { _ = os.RemoveAll(dir) }()

	tmp := filepath.Join(dir, "baseline"+filepath.Ext(origPath))
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		return SyntaxCheckResult{}, "", false
	}
	return CheckEditedFileSyntax(tmp), tmp, true
}

// detailPathPlaceholder stands in for a file path while two check results are
// compared, so that the same finding on the same content matches regardless of
// where the file happened to live.
const detailPathPlaceholder = "\x00FILE\x00"

// normalizeDetailPaths replaces a checker's references to path — full path first,
// then bare base name, since pyflakes prints what it was given while a traceback
// may print either.
func normalizeDetailPaths(detail, path string) string {
	if path == "" || detail == "" {
		return detail
	}
	out := strings.ReplaceAll(detail, path, detailPathPlaceholder)
	if base := filepath.Base(path); base != "" && base != "." && base != string(filepath.Separator) {
		out = strings.ReplaceAll(out, base, detailPathPlaceholder)
	}
	return out
}

// denormalizeDetailPaths restores the real path in findings that will be shown.
func denormalizeDetailPaths(lines []string, path string) []string {
	if path == "" {
		return lines
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, strings.ReplaceAll(l, detailPathPlaceholder, path))
	}
	return out
}

// truncateDetail bounds a finding list for display. Applied only when rendering
// an observation: SyntaxCheckResult.Detail itself stays whole so before/after
// comparison is exact.
func truncateDetail(detail string) string {
	if len(detail) <= syntaxCheckMaxDetail {
		return detail
	}
	cut := syntaxCheckMaxDetail
	for cut > 0 && !utf8.RuneStart(detail[cut]) {
		cut--
	}
	return detail[:cut] + "\n[... truncated]"
}
