package agents

import (
	"strings"
	"testing"
)

// The findings below are verbatim from SWE-bench astropy-13398 and -14369, where
// the agent changed one line, pyflakes reported astropy's own long-standing
// issues in the same file, and the agent spent 6 edits / 12 minutes chasing them
// before breaking a test import.
func TestScopeLintOutputToChangedLines(t *testing.T) {
	changed := changedLineSet{
		"astropy/units/format/cds.py":                    {310: true, 311: true},
		"astropy/coordinates/builtin_frames/__init__.py": {120: true},
	}

	cases := []struct {
		name        string
		output      string
		moduleDir   string
		wantKept    []string
		wantDropped []string
	}{
		{
			name:        "pre-existing finding on an untouched line is dropped",
			output:      "astropy/units/format/cds.py:78:9: local variable 'tokens' is assigned to but never used",
			wantDropped: []string{"tokens"},
		},
		{
			name:     "finding on a changed line is kept",
			output:   "astropy/units/format/cds.py:310:5: undefined name 'foo'",
			wantKept: []string{"undefined name 'foo'"},
		},
		{
			name: "mixed output keeps only the agent's own finding",
			output: strings.Join([]string{
				"astropy/units/format/cds.py:78:9: local variable 'tokens' is assigned to but never used",
				"astropy/units/format/cds.py:311:1: undefined name 'bar'",
				"astropy/coordinates/builtin_frames/__init__.py:41:1: 'from .ecliptic import *' unable to detect undefined names",
			}, "\n"),
			wantKept:    []string{"undefined name 'bar'"},
			wantDropped: []string{"tokens", "ecliptic"},
		},
		{
			name:        "module-relative path resolves against the module dir",
			output:      "format/cds.py:78:9: local variable 'tokens' is assigned to but never used",
			moduleDir:   "astropy/units",
			wantDropped: []string{"tokens"},
		},
		{
			name:     "a file we have no diff for is kept — never guess",
			output:   "some/other/file.py:5:1: undefined name 'zap'",
			wantKept: []string{"zap"},
		},
		{
			name:     "non-finding lines are always kept",
			output:   strings.Join([]string{"Traceback (most recent call last):", "  File \"x.py\", line 3", "SyntaxError: bad"}, "\n"),
			wantKept: []string{"Traceback", "SyntaxError"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := scopeLintOutputToChangedLines(c.output, changed, c.moduleDir)
			for _, w := range c.wantKept {
				if !strings.Contains(got, w) {
					t.Errorf("expected %q to survive scoping; got:\n%s", w, got)
				}
			}
			for _, w := range c.wantDropped {
				if strings.Contains(got, w) {
					t.Errorf("expected %q to be dropped; got:\n%s", w, got)
				}
			}
		})
	}
}

// An empty changed-line set means we could not determine scope. Showing a stale
// finding is recoverable; hiding a real one is not, so ambiguity keeps output.
func TestScopeLintOutput_NoScopeInfoLeavesOutputIntact(t *testing.T) {
	out := "astropy/units/format/cds.py:78:9: local variable 'tokens' is assigned to but never used"
	got, dropped := scopeLintOutputToChangedLines(out, changedLineSet{}, "")
	if got != out || dropped != 0 {
		t.Fatalf("output must be untouched without scope info; got %q (dropped=%d)", got, dropped)
	}
}

func TestDiffLineRe_ParsesHunkHeaders(t *testing.T) {
	cases := map[string][2]string{
		"@@ -12,3 +40,7 @@ def f():": {"40", "7"},
		"@@ -1 +1 @@":                {"1", ""},
		"@@ -0,0 +1,25 @@":           {"1", "25"},
	}
	for in, want := range cases {
		m := diffLineRe.FindStringSubmatch(in)
		if m == nil {
			t.Fatalf("failed to parse hunk header %q", in)
		}
		if m[1] != want[0] || m[2] != want[1] {
			t.Errorf("%q -> start=%q len=%q, want %q/%q", in, m[1], m[2], want[0], want[1])
		}
	}
}

// Two identical consecutive findings, both outside the changed lines, must both
// be counted as dropped. The earlier implementation inferred "was this kept?"
// from the tail of the kept slice, which silently miscounts a repeat.
func TestScopeLintOutput_CountsDuplicateConsecutiveDrops(t *testing.T) {
	changed := changedLineSet{"mod.py": {100: true}}
	dup := "mod.py:5:1: undefined name 'x'"
	got, dropped := scopeLintOutputToChangedLines(dup+"\n"+dup, changed, "")
	if strings.TrimSpace(got) != "" {
		t.Errorf("both findings are outside the changed lines and should be dropped; got:\n%s", got)
	}
	if dropped != 2 {
		t.Errorf("dropped = %d, want 2 — identical consecutive findings must count separately", dropped)
	}
}

// A content line beginning with "++ " is rendered as "+++ " in a unified diff.
// Treating any "+++ " as a file header would reattribute every following hunk to
// a bogus path, so the b/ prefix is required.
func TestChangedLines_ContentLineLookingLikeAHeaderIsNotOne(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/real.py b/real.py",
		"--- a/real.py",
		"+++ b/real.py",
		"@@ -1,0 +1,2 @@",
		`++ this line begins with plus-plus and renders as "+++ "`,
		"+ordinary added line",
		"@@ -10,0 +20,1 @@",
		"+another",
	}, "\n")

	got := parseChangedLinesDiff(diff)
	if _, bogus := got[`this line begins with plus-plus and renders as "+++ "`]; bogus {
		t.Fatalf("a content line was parsed as a file header: %v", got)
	}
	lines, ok := got["real.py"]
	if !ok {
		t.Fatalf("real.py missing from parsed diff: %v", got)
	}
	// Both hunks must be attributed to real.py, including the one after the
	// deceptive content line.
	if !lines[1] || !lines[2] || !lines[20] {
		t.Errorf("expected lines 1,2,20 on real.py; got %v", lines)
	}
}

// git quotes paths containing spaces or non-ASCII.
func TestChangedLines_QuotedPathIsUnquoted(t *testing.T) {
	diff := strings.Join([]string{
		`+++ "b/dir with spaces/mod.py"`,
		"@@ -0,0 +5,1 @@",
		"+x = 1",
	}, "\n")
	got := parseChangedLinesDiff(diff)
	if _, ok := got["dir with spaces/mod.py"]; !ok {
		t.Fatalf("quoted path was not unquoted: %v", got)
	}
}

// pyflakes echoes the path it was given, so "./mod.py" must still match a diff
// entry recorded as "mod.py".
func TestScopeLintOutput_DotSlashPathMatches(t *testing.T) {
	changed := changedLineSet{"mod.py": {7: true}}
	kept, dropped := scopeLintOutputToChangedLines("./mod.py:7:1: undefined name 'x'", changed, "")
	if !strings.Contains(kept, "undefined name 'x'") {
		t.Errorf("./-prefixed path should match and be kept; got %q (dropped=%d)", kept, dropped)
	}
	_, d2 := scopeLintOutputToChangedLines("./mod.py:99:1: undefined name 'y'", changed, "")
	if d2 != 1 {
		t.Errorf("./-prefixed path outside the changed lines should drop; dropped=%d", d2)
	}
}
