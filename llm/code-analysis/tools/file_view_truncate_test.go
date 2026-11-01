package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLines writes a file of n short lines ("line 1", "line 2", …) and returns
// a read tool rooted at its directory.
func writeLines(t *testing.T, name string, n int) (*FileViewTool, string) {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewFileViewTool(dir), dir
}

func viewFile(t *testing.T, tool *FileViewTool, dir, name string, extra map[string]any) string {
	t.Helper()
	input := map[string]any{"file_path": name, "working_directory": dir}
	for k, v := range extra {
		input[k] = v
	}
	resp := tool.Execute(context.Background(), input)
	if resp.Status == "error" {
		t.Fatalf("expected a read, got error: %s", resp.Error)
	}
	return resp.Observation
}

// A file larger than the cap must be bounded (clamped + paged), not rejected with an
// error, and the response must tell the agent how to read the next chunk.
func TestFileView_ClampsLargeFileInsteadOfErroring(t *testing.T) {
	tool, dir := writeLines(t, "big.txt", 700) // > maxLinesDefault
	obs := viewFile(t, tool, dir, "big.txt", nil)

	if !strings.Contains(obs, "TRUNCATED") {
		t.Fatalf("expected a TRUNCATED marker guiding the agent to page; observation:\n%s", obs)
	}
	// Must include the last in-window line and exclude the first out-of-window line.
	if !strings.Contains(obs, fmt.Sprintf("%d: ", maxLinesDefault)) {
		t.Errorf("expected line %d in the bounded window", maxLinesDefault)
	}
	if strings.Contains(obs, fmt.Sprintf("%d: ", maxLinesDefault+1)) {
		t.Errorf("line %d must not appear (it is beyond the %d-line cap)", maxLinesDefault+1, maxLinesDefault)
	}
	if !strings.Contains(obs, fmt.Sprintf("start_line=%d", maxLinesDefault+1)) {
		t.Errorf("the note must say where the next chunk starts:\n%s", obs[len(obs)-200:])
	}
}

// Every read says which lines it returned and how long the file is. Without
// that the agent cannot tell a whole file from a slice, and reads again to find
// out — one in five tool calls was such a re-read.
func TestFileView_StatesLinesReturnedAndFileLength(t *testing.T) {
	tests := []struct {
		name  string
		lines int
		extra map[string]any
		want  string
	}{
		{"whole small file", 87, nil, "[lines 1-87 of 87: the whole file]"},
		{"a range", 300, map[string]any{"start_line": float64(120), "end_line": float64(180)}, "[lines 120-180 of 300]"},
		{"start only reads to the end", 300, map[string]any{"start_line": float64(250)}, "[lines 250-300 of 300]"},
		{"range past the end is cut to the file", 300, map[string]any{"start_line": float64(280), "end_line": float64(900)}, "[lines 280-300 of 300]"},
		{"long file, no range", 700, nil, fmt.Sprintf("[lines 1-%d of 700]", maxLinesDefault)},
		{"start only on a long file is bounded", 2000, map[string]any{"start_line": float64(100)}, fmt.Sprintf("[lines 100-%d of 2000]", 100+maxLinesDefault-1)},
		{"start past the end", 50, map[string]any{"start_line": float64(80)}, "[no lines returned: the file has 50 lines]"},
		{"empty file", 0, nil, "[empty file]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tool, dir := writeLines(t, "f.txt", tc.lines)
			obs := viewFile(t, tool, dir, "f.txt", tc.extra)
			if !strings.HasPrefix(obs, tc.want+"\n") {
				t.Fatalf("observation must start with %q, got:\n%.200s", tc.want, obs)
			}
		})
	}
}

// A read that stops short of what was asked for must say so. Before, a
// start-only read on a long file was returned whole and then cut in the middle
// by the planner, after this tool had finished describing it.
func TestFileView_NeverStopsShortSilently(t *testing.T) {
	t.Run("more lines asked for than one call returns", func(t *testing.T) {
		tool, dir := writeLines(t, "f.txt", 2000)
		obs := viewFile(t, tool, dir, "f.txt", map[string]any{"start_line": float64(100), "end_line": float64(1500)})
		last := 100 + maxLinesDefault - 1
		if !strings.Contains(obs, "TRUNCATED") || !strings.Contains(obs, fmt.Sprintf("start_line=%d", last+1)) {
			t.Fatalf("expected a note pointing at line %d:\n%s", last+1, obs[len(obs)-260:])
		}
	})

	t.Run("long lines stop on a line boundary under the character limit", func(t *testing.T) {
		dir := t.TempDir()
		var b strings.Builder
		for i := 1; i <= 200; i++ {
			fmt.Fprintf(&b, "%s\n", strings.Repeat("x", 400)) // 200 lines of 400 chars = 80,000 chars
		}
		if err := os.WriteFile(filepath.Join(dir, "wide.txt"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		obs := viewFile(t, NewFileViewTool(dir), dir, "wide.txt", nil)

		if len(obs) > maxCharsDefault+400 {
			t.Fatalf("returned %d characters, limit is %d", len(obs), maxCharsDefault)
		}
		var first, last, total int
		if _, err := fmt.Sscanf(obs, "[lines %d-%d of %d]", &first, &last, &total); err != nil {
			t.Fatalf("no header: %.80s", obs)
		}
		if total != 200 || last >= 200 || last < 1 {
			t.Fatalf("header says lines %d-%d of %d", first, last, total)
		}
		// The header must describe exactly what follows it: the last line
		// returned is whole, and the next one is absent.
		if !strings.Contains(obs, fmt.Sprintf("\n%d: %s\n", last, strings.Repeat("x", 400))) {
			t.Fatalf("line %d is not returned whole", last)
		}
		if strings.Contains(obs, fmt.Sprintf("\n%d: ", last+1)) {
			t.Fatalf("line %d is present but the header says the read ends at %d", last+1, last)
		}
		if !strings.Contains(obs, fmt.Sprintf("start_line=%d", last+1)) {
			t.Fatalf("no note pointing at line %d", last+1)
		}
	})

	// An end before the start is read as "to the end of the file", so on a long
	// file it stops at the per-call limit and has to say so.
	t.Run("an end before the start on a long file", func(t *testing.T) {
		tool, dir := writeLines(t, "f.txt", 2000)
		obs := viewFile(t, tool, dir, "f.txt", map[string]any{"start_line": float64(100), "end_line": float64(50)})
		last := 100 + maxLinesDefault - 1
		if !strings.HasPrefix(obs, fmt.Sprintf("[lines 100-%d of 2000]\n", last)) {
			t.Fatalf("header: %.60s", obs)
		}
		if !strings.Contains(obs, "TRUNCATED") || !strings.Contains(obs, fmt.Sprintf("start_line=%d", last+1)) {
			t.Fatalf("expected a note pointing at line %d:\n%s", last+1, obs[len(obs)-200:])
		}
	})

	// A line longer than the scanner's default 64KB must not make the file
	// unreadable: the line count and the read use the same limit.
	t.Run("a very long line", func(t *testing.T) {
		dir := t.TempDir()
		body := "first\n" + strings.Repeat("m", 100*1024) + "\nlast\n"
		if err := os.WriteFile(filepath.Join(dir, "min.js"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		obs := viewFile(t, NewFileViewTool(dir), dir, "min.js", nil)
		if !strings.HasPrefix(obs, "[lines 1-1 of 3]\n1: first\n") {
			t.Fatalf("header: %.60s", obs)
		}
		if !strings.Contains(obs, "start_line=2") {
			t.Fatalf("no note pointing at the long line:\n%s", obs[len(obs)-200:])
		}
		// Asked for directly, the long line is returned whole: the first line of
		// a read is never withheld.
		obs = viewFile(t, NewFileViewTool(dir), dir, "min.js", map[string]any{"start_line": float64(2), "end_line": float64(2)})
		if !strings.HasPrefix(obs, "[lines 2-2 of 3]\n2: mmmm") || len(obs) < 100*1024 {
			t.Fatalf("the long line was not returned: %d chars, %.40s", len(obs), obs)
		}
	})

	t.Run("a read that got everything carries no note", func(t *testing.T) {
		tool, dir := writeLines(t, "f.txt", 300)
		obs := viewFile(t, tool, dir, "f.txt", map[string]any{"start_line": float64(120), "end_line": float64(180)})
		if strings.Contains(obs, "TRUNCATED") {
			t.Fatalf("a complete read must not claim it was truncated:\n%s", obs[len(obs)-200:])
		}
	})
}

// A file found under a different path than the one asked for used to lose its
// truncation note.
func TestFileView_KeepsTheNoteWhenThePathWasCorrected(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 1; i <= 700; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "deep", "only_here.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	obs := viewFile(t, NewFileViewTool(dir), dir, "only_here.txt", nil)
	if !strings.Contains(obs, "File found at: pkg/deep/only_here.txt") {
		t.Fatalf("path correction did not trigger, so this test checks nothing:\n%.160s", obs)
	}
	if !strings.Contains(obs, "TRUNCATED") || !strings.Contains(obs, "of 700]") {
		t.Fatalf("the corrected-path reply lost its header or note:\n%.200s", obs)
	}
}

// The description promises the line header on reads, not on in-file searches,
// which return matches with context instead. The two have to agree.
func TestFileView_HeaderIsOnReadsNotOnSearches(t *testing.T) {
	tool, dir := writeLines(t, "f.txt", 60)
	if obs := viewFile(t, tool, dir, "f.txt", map[string]any{"search_pattern": "line 30"}); strings.HasPrefix(obs, "[lines ") {
		t.Fatalf("an in-file search now carries the read header; update the description:\n%.120s", obs)
	}
	if desc := tool.Description(); !strings.Contains(desc, `A read (with or without start_line/end_line) starts with "[lines X-Y of N]"`) {
		t.Fatal("the description no longer says which replies carry the line header")
	}
}
