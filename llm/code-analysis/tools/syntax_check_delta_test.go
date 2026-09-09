package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requirePyflakes(t *testing.T) {
	t.Helper()
	if err := exec.Command("python3", "-m", "pyflakes", "--version").Run(); err != nil {
		t.Skip("pyflakes not available")
	}
}

// The case that cost SWE-bench astropy-13398 six edits and twelve minutes:
// builtin_frames/__init__.py already contained `'from .ecliptic import *' unable
// to detect undefined names`. Editing an unrelated line there produced
// "YOUR EDIT LIKELY BROKE THE FILE'S SYNTAX", so the agent kept trying to fix
// what it had not broken, and finished with a genuinely broken import.
func TestCheckEditedFileSyntaxDelta_PreExistingFindingIsNotBlamedOnTheEdit(t *testing.T) {
	requirePyflakes(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "mod.py")

	before := "from os import *\n\ndef untouched():\n    return path\n\ndef edited():\n    return 'before'\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	// Confirm the fixture actually trips the plain check — otherwise the test
	// would pass for the wrong reason.
	if base := CheckEditedFileSyntax(path); base.Status != SyntaxCheckFailed {
		t.Skipf("fixture did not produce a pre-existing finding (status=%v)", base.Status)
	}

	after := strings.Replace(before, "return 'before'", "return 'after'", 1)
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		t.Fatal(err)
	}

	got := CheckEditedFileSyntaxDelta(path, []byte(before))
	if got.Status == SyntaxCheckFailed {
		t.Fatalf("edit touched an unrelated line but was blamed:\n%s", got.Detail)
	}
	obs := got.AppendToObservation("Successfully made a single replacement")
	if strings.Contains(obs, "BROKE THE FILE'S SYNTAX") {
		t.Errorf("observation still accuses the edit:\n%s", obs)
	}
}

// The gate must still fire when the edit genuinely introduces an undefined name,
// even in a file that already had one — otherwise the fix trades a false positive
// for a false negative.
func TestCheckEditedFileSyntaxDelta_NewFindingIsStillReported(t *testing.T) {
	requirePyflakes(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "mod.py")

	before := "from os import *\n\ndef f():\n    return path\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if base := CheckEditedFileSyntax(path); base.Status != SyntaxCheckFailed {
		t.Skipf("fixture did not produce a pre-existing finding (status=%v)", base.Status)
	}

	after := before + "\ndef g():\n    return totally_undefined_symbol\n"
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		t.Fatal(err)
	}

	got := CheckEditedFileSyntaxDelta(path, []byte(before))
	if got.Status != SyntaxCheckFailed {
		t.Fatalf("a newly introduced undefined name must be reported; got status=%v", got.Status)
	}
	if !strings.Contains(got.Detail, "totally_undefined_symbol") {
		t.Errorf("detail should name the new symbol; got:\n%s", got.Detail)
	}
	if strings.Contains(got.Detail, "unable to detect undefined names") {
		t.Errorf("pre-existing finding leaked into the detail:\n%s", got.Detail)
	}
}

// A new file has no baseline, so every finding is the write's responsibility.
func TestCheckEditedFileSyntaxDelta_NilBaselineBehavesLikePlainCheck(t *testing.T) {
	requirePyflakes(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "new.py")
	if err := os.WriteFile(path, []byte("def f():\n    return nope_undefined\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plain := CheckEditedFileSyntax(path)
	delta := CheckEditedFileSyntaxDelta(path, nil)
	if plain.Status != delta.Status {
		t.Fatalf("nil baseline should match the plain check: plain=%v delta=%v", plain.Status, delta.Status)
	}
}

// Message-level matching, because an edit shifts line numbers and a
// path:line:col comparison would report every pre-existing finding as new.
func TestNewFindings_MatchesOnMessageNotLineNumber(t *testing.T) {
	before := "mod.py:41:1: 'from .ecliptic import *' unable to detect undefined names"
	after := strings.Join([]string{
		"mod.py:57:1: 'from .ecliptic import *' unable to detect undefined names",
		"mod.py:88:5: undefined name 'brand_new'",
	}, "\n")

	got := newFindings(after, before)
	if len(got) != 1 {
		t.Fatalf("expected exactly the new finding, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "brand_new") {
		t.Errorf("wrong finding survived: %s", got[0])
	}
}

// A second copy of an existing message is genuinely new — multiplicity matters.
func TestNewFindings_SecondCopyOfExistingMessageIsNew(t *testing.T) {
	before := "mod.py:1:1: undefined name 'x'"
	after := "mod.py:1:1: undefined name 'x'\nmod.py:9:1: undefined name 'x'"
	if got := newFindings(after, before); len(got) != 1 {
		t.Fatalf("expected 1 new finding, got %d: %v", len(got), got)
	}
}

// The capability notice pyflakes emits for star imports must never be treated
// as a defect: it says the checker cannot judge the file, not that the file is
// wrong. It contains the substring "undefined names", which is how it slipped
// through and produced "YOUR EDIT LIKELY BROKE THE FILE'S SYNTAX" on untouched
// astropy code.
func TestCheckPythonUndefinedNames_StarImportNoticeIsNotAFinding(t *testing.T) {
	requirePyflakes(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "clean_star.py")
	// Star import present, but nothing actually undefined.
	if err := os.WriteFile(path, []byte("from os import *\n\ndef f():\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CheckEditedFileSyntax(path); got.Status == SyntaxCheckFailed {
		t.Fatalf("star-import notice was reported as a defect:\n%s", got.Detail)
	}
}

// The other half: under a star import pyflakes hedges to "may be undefined",
// which the original substring match missed entirely — so real problems went
// unreported in exactly the files that produced the loudest false positives.
func TestCheckPythonUndefinedNames_MayBeUndefinedIsAFinding(t *testing.T) {
	requirePyflakes(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "star_undef.py")
	if err := os.WriteFile(path, []byte("from os import *\n\ndef f():\n    return totally_undefined_symbol\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := CheckEditedFileSyntax(path)
	if got.Status != SyntaxCheckFailed {
		t.Fatalf("a real undefined name under a star import must be reported; got %v", got.Status)
	}
	if !strings.Contains(got.Detail, "totally_undefined_symbol") {
		t.Errorf("detail should name the symbol; got:\n%s", got.Detail)
	}
	if strings.Contains(got.Detail, "unable to detect undefined names") {
		t.Errorf("capability notice leaked into the detail:\n%s", got.Detail)
	}
}

// Detail used to be truncated at production. Two checks of the same content then
// differed purely because their file paths were different lengths — the cut
// landed on a different finding, and everything past it looked new. Every
// synthetic test passed anyway, because none produced more than 700 bytes of
// findings; the real astropy __init__.py (19 findings) did.
//
// So: enough pre-existing findings to cross the cut, and a baseline path of a
// deliberately different length from the edited file's.
func TestCheckEditedFileSyntaxDelta_SurvivesDetailTruncation(t *testing.T) {
	requirePyflakes(t)

	var b strings.Builder
	b.WriteString("from os import *\n\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "def pre_existing_%02d():\n    return undefined_symbol_number_%02d\n\n", i, i)
	}
	before := b.String()

	// A long directory name, so the edited file's path length differs markedly
	// from the baseline temp file's — the condition that exposed the bug.
	dir := filepath.Join(t.TempDir(), "a-deliberately-long-directory-name-to-shift-the-truncation-point")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mod.py")
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	base := CheckEditedFileSyntax(path)
	if base.Status != SyntaxCheckFailed {
		t.Skipf("fixture produced no findings (status=%v)", base.Status)
	}
	if len(base.Detail) <= syntaxCheckMaxDetail {
		t.Skipf("fixture detail (%d bytes) does not exceed the %d-byte cut; test would not exercise the bug",
			len(base.Detail), syntaxCheckMaxDetail)
	}

	// Append an unrelated, clean function.
	after := before + "\ndef added_and_harmless():\n    return 42\n"
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		t.Fatal(err)
	}

	got := CheckEditedFileSyntaxDelta(path, []byte(before))
	if got.Status == SyntaxCheckFailed {
		t.Fatalf("truncation made pre-existing findings look new:\n%s", got.Detail)
	}

	// The bound still applies where it belongs — on the rendered observation.
	rendered := SyntaxCheckResult{Status: SyntaxCheckFailed, Checker: "test", Detail: base.Detail}.
		AppendToObservation("edited")
	if !strings.Contains(rendered, "[... truncated]") {
		t.Error("presentation should still bound a long detail")
	}
}

// A compile error's detail is a Python traceback that embeds the file path:
//
//	File "/work/.../cds.py", line 3
//
// The baseline is checked from a temp file, so its traceback names a different
// path. Without normalisation an identical, pre-existing SyntaxError never
// matches and is reported as introduced by the edit — the exact false
// accusation this PR removes, in the one path that does not go through pyflakes.
func TestCheckEditedFileSyntaxDelta_PreExistingCompileErrorIsNotBlamed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.py")

	before := "def broken(\n    return 1\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if base := CheckEditedFileSyntax(path); base.Status != SyntaxCheckFailed {
		t.Skipf("fixture did not produce a compile error (status=%v)", base.Status)
	}

	// Append an unrelated line. The file is still broken — in the same way, for
	// the same pre-existing reason.
	after := before + "\n# a harmless trailing comment\n"
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		t.Fatal(err)
	}

	got := CheckEditedFileSyntaxDelta(path, []byte(before))
	if got.Status == SyntaxCheckFailed {
		t.Fatalf("pre-existing compile error was blamed on an unrelated edit:\n%s", got.Detail)
	}
}

// Normalisation is for comparison only — anything shown to the agent must still
// name the real file, or the message is not actionable.
func TestCheckEditedFileSyntaxDelta_ReportedDetailKeepsTheRealPath(t *testing.T) {
	requirePyflakes(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "mod.py")

	before := "def f():\n    return 1\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	after := before + "\ndef g():\n    return brand_new_undefined\n"
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		t.Fatal(err)
	}

	got := CheckEditedFileSyntaxDelta(path, []byte(before))
	if got.Status != SyntaxCheckFailed {
		t.Fatalf("a newly introduced undefined name must still be reported; got %v", got.Status)
	}
	if strings.Contains(got.Detail, detailPathPlaceholder) {
		t.Errorf("placeholder leaked into the reported detail:\n%s", got.Detail)
	}
	if !strings.Contains(got.Detail, "mod.py") {
		t.Errorf("detail should name the real file; got:\n%s", got.Detail)
	}
}

// An edit ABOVE a pre-existing SyntaxError shifts the line number inside the
// traceback — baseline reads `line 3`, the edited file reads `line 5`. The
// traceback is not a "path:line:col:" finding, so the raw text is compared and
// the identical, pre-existing error would look introduced. This is the same
// class as the path mismatch, one layer down.
func TestCheckEditedFileSyntaxDelta_TracebackLineShiftIsNotANewFinding(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.py")

	before := "x = 1\n\ndef broken(\n    return 1\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	base := CheckEditedFileSyntax(path)
	if base.Status != SyntaxCheckFailed {
		t.Skipf("fixture produced no compile error (status=%v)", base.Status)
	}

	// Insert two lines at the top: the error is untouched but moves down.
	after := "# inserted\n# inserted\n" + before
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		t.Fatal(err)
	}

	got := CheckEditedFileSyntaxDelta(path, []byte(before))
	if got.Status == SyntaxCheckFailed {
		t.Fatalf("a shifted pre-existing compile error was reported as introduced:\n%s", got.Detail)
	}
}

// Masking must not blind the comparison: a genuinely different traceback still
// has to surface.
func TestNewFindings_MaskedLineNumbersStillDistinguishDifferentErrors(t *testing.T) {
	before := "  File \"m.py\", line 3\nSyntaxError: '(' was never closed"
	after := "  File \"m.py\", line 9\nSyntaxError: invalid syntax"
	got := newFindings(after, before)
	if len(got) == 0 {
		t.Fatal("a different SyntaxError must still be reported")
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "invalid syntax") {
		t.Errorf("expected the new error text; got:\n%s", joined)
	}
}
