package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func numberedFile(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "row %d\n", i)
	}
	return b.String()
}

// An edit's result has to show what the edit left behind. A bare "replaced
// successfully" sent the agent back to file_view after every edit, one model
// round trip each time.
func TestReplace_ResultShowsTheChangedLines(t *testing.T) {
	dir := t.TempDir()
	path := createTempFile(t, dir, "f.txt", numberedFile(40))

	resp := NewReplaceToolWithWorkspace(dir).Execute(context.Background(), map[string]any{
		"file_path": "f.txt", "working_directory": dir,
		"old_string": "row 20\n", "new_string": "row twenty\nrow twenty and a half\n",
	})

	require.Equal(t, "success", resp.Status, resp.Error)
	assert.Contains(t, readFile(t, path), "row twenty and a half")
	// Line numbers are the ones the file has now, and the file is one line longer.
	assert.Contains(t, resp.Observation, "Changed lines as they now stand in the file (lines 17-24 of 41):")
	for _, want := range []string{"17: row 17", "20: row twenty", "21: row twenty and a half", "22: row 21", "24: row 23"} {
		assert.Contains(t, resp.Observation, want)
	}
	assert.NotContains(t, resp.Observation, "16: row 16", "context is three lines on each side")
	assert.NotContains(t, resp.Observation, "25: row 24")
}

func TestChangedRegion(t *testing.T) {
	base := numberedFile(30)

	t.Run("nothing changed", func(t *testing.T) {
		assert.Empty(t, changedRegion(base, base))
	})

	t.Run("a deletion shows the lines around the gap", func(t *testing.T) {
		got := changedRegion(base, strings.Replace(base, "row 10\nrow 11\n", "", 1))
		assert.Contains(t, got, "(lines 7-12 of 28)")
		assert.Contains(t, got, "9: row 9\n10: row 12")
	})

	t.Run("a change at the top", func(t *testing.T) {
		got := changedRegion(base, strings.Replace(base, "row 1\n", "first row\n", 1))
		assert.Contains(t, got, "(lines 1-4 of 30)")
		assert.Contains(t, got, "1: first row\n2: row 2")
	})

	t.Run("a change at the bottom", func(t *testing.T) {
		got := changedRegion(base, strings.Replace(base, "row 30\n", "last row\n", 1))
		assert.Contains(t, got, "(lines 27-30 of 30)")
		assert.True(t, strings.HasSuffix(got, "30: last row"), got)
	})

	t.Run("a file with no trailing newline", func(t *testing.T) {
		got := changedRegion("a\nb\nc", "a\nB\nc")
		assert.Contains(t, got, "(lines 1-3 of 3)")
		assert.Contains(t, got, "2: B")
	})

	t.Run("the file was emptied", func(t *testing.T) {
		assert.Contains(t, changedRegion("a\n", ""), "the file now has 0 lines")
	})

	// replace_all can touch the first and last lines of a long file. The region
	// between them is then cut in the middle, and the cut is stated with the
	// line numbers left out.
	t.Run("a long region is cut with a stated gap", func(t *testing.T) {
		long := numberedFile(400)
		after := strings.Replace(strings.Replace(long, "row 5\n", "ROW 5\n", 1), "row 395\n", "ROW 395\n", 1)
		got := changedRegion(long, after)

		assert.Contains(t, got, "(lines 2-398 of 400)")
		assert.Contains(t, got, "5: ROW 5")
		assert.Contains(t, got, "395: ROW 395")
		assert.Contains(t, got, "[... lines 62-378 not shown ...]")
		assert.NotContains(t, got, "200: row 200")
		assert.LessOrEqual(t, strings.Count(got, "\n"), changedRegionMaxLines+3)
	})
}

// The tool's text and the tool's behaviour have to agree: a description that
// still tells the agent to re-read after an edit would be followed over a
// result that shows the edit.
func TestReplace_DescriptionDoesNotAskForARereadAfterEdits(t *testing.T) {
	desc := NewReplaceTool().Description()
	assert.NotContains(t, desc, "Not re-reading the file after previous edits")
	assert.Contains(t, desc, "do not read the file again to check an edit")
}
