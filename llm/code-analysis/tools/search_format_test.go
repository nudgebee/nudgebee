package tools

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseMatchLines_SkipsContextAndSeparators(t *testing.T) {
	lines := []string{"a.go:10:foo", "--", "a.go-11-context", "b.go:5:bar"}
	matches, order, byFile := parseMatchLines(lines, "")
	if len(matches) != 2 {
		t.Fatalf("expected 2 match lines, got %d", len(matches))
	}
	if len(order) != 2 || len(byFile["a.go"]) != 1 {
		t.Fatalf("unexpected grouping: order=%v byFile=%v", order, byFile)
	}
}

func TestParseMatchLines_ContentWithColons_NoLineNumbers(t *testing.T) {
	// grep with line numbers disabled emits "file:content"; content may contain
	// colons. The middle field must not be misparsed as a line number.
	lines := []string{`a.go:fmt.Println("hello: world")`}
	matches, _, _ := parseMatchLines(lines, "")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].file != "a.go" {
		t.Fatalf("file = %q, want a.go", matches[0].file)
	}
	if matches[0].line != "" {
		t.Fatalf("line = %q, want empty (content is not a line number)", matches[0].line)
	}
	if matches[0].content != `fmt.Println("hello: world")` {
		t.Fatalf("content = %q, want full content preserved", matches[0].content)
	}
}

func TestRelToWorkspace_SiblingPrefixNotStripped(t *testing.T) {
	// "/w/clone-12" must not be treated as a prefix of "/w/clone-123/api/x.go".
	got := relToWorkspace("/w/clone-123/api/x.go", "/w/clone-12")
	if got != "/w/clone-123/api/x.go" {
		t.Fatalf("sibling-dir path was wrongly rewritten: %q", got)
	}
	if got := relToWorkspace("/w/clone-12/api/x.go", "/w/clone-12"); got != "api/x.go" {
		t.Fatalf("real workspace path not relativized: %q", got)
	}
}

func TestRenderContentMatches_DetailedForFocusedResult(t *testing.T) {
	lines := []string{"a.go:10:foo", "a.go:20:bar"}
	matches, order, byFile := parseMatchLines(lines, "")
	resp := renderContentMatches(matches, order, byFile, 100, nil)
	if resp.Status != "success" {
		t.Fatalf("status=%s", resp.Status)
	}
	if !strings.Contains(resp.Observation, "a.go:10") {
		t.Fatalf("expected detailed line view, got: %s", resp.Observation)
	}
}

func TestRenderContentMatches_ReferenceForBroadResult(t *testing.T) {
	var lines []string
	for i := 0; i < 25; i++ {
		lines = append(lines, fmt.Sprintf("f%d.go:%d:x", i, i))
	}
	matches, order, byFile := parseMatchLines(lines, "")
	resp := renderContentMatches(matches, order, byFile, 100, nil)
	if !strings.Contains(resp.Observation, "per-file summary") {
		t.Fatalf("expected reference-first summary, got: %s", resp.Observation)
	}
	data, _ := resp.Data.(map[string]any)
	if data["reference_mode"] != true {
		t.Fatalf("expected reference_mode=true in data")
	}
}

func TestRenderContentMatches_TruncationSignal(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, fmt.Sprintf("a.go:%d:x", i))
	}
	matches, order, byFile := parseMatchLines(lines, "")
	resp := renderContentMatches(matches, order, byFile, 100, nil)
	if !strings.Contains(resp.Observation, "IS_TRUNCATED") {
		t.Fatalf("expected honest truncation signal when capped, got: %s", resp.Observation)
	}
}

func TestNoMatchResponse_GuidesStrategyChange(t *testing.T) {
	resp := noMatchResponse(nil)
	if resp.Status != "success" {
		t.Fatalf("no-match should be a successful empty search, got %s", resp.Status)
	}
	if !strings.Contains(resp.Observation, "No matches found") {
		t.Fatalf("unexpected observation: %s", resp.Observation)
	}
}
