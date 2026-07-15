package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
		return SyntaxCheckResult{Status: SyntaxCheckPassed, Checker: checker}
	}
	detail := strings.TrimSpace(string(out))
	if len(detail) > syntaxCheckMaxDetail {
		detail = detail[:syntaxCheckMaxDetail] + "\n[... truncated]"
	}
	if detail == "" {
		// Non-zero exit with no diagnostics — can't attribute to the edit.
		return SyntaxCheckResult{Status: SyntaxCheckNotChecked, Checker: checker}
	}
	return SyntaxCheckResult{Status: SyntaxCheckFailed, Checker: checker, Detail: detail}
}

// AppendToObservation renders the check outcome onto a tool observation.
// Failure gets a bounded, actionable note; success a minimal confirmation;
// not_checked adds nothing (silence — no parser is not a finding).
func (r SyntaxCheckResult) AppendToObservation(obs string) string {
	switch r.Status {
	case SyntaxCheckFailed:
		return fmt.Sprintf("%s\n\n⚠ POST-EDIT SYNTAX CHECK FAILED (%s) — your edit likely broke the file's syntax. Fix this before proceeding:\n%s", obs, r.Checker, r.Detail)
	case SyntaxCheckPassed:
		return fmt.Sprintf("%s\n[syntax OK: %s]", obs, r.Checker)
	default:
		return obs
	}
}
