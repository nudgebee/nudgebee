package agents

import (
	"context"
	"strings"
	"testing"
)

func TestBuildEvidenceBlock_Empty(t *testing.T) {
	if got := buildEvidenceBlock(context.Background(), nil, nil, "X", ""); got != "" {
		t.Fatalf("empty logs must yield empty block, got %q", got)
	}
	if got := buildEvidenceBlock(context.Background(), nil, nil, "X", "   \n  "); got != "" {
		t.Fatalf("whitespace-only logs must yield empty block, got %q", got)
	}
}

func TestBuildEvidenceBlock_SmallInjectedVerbatim(t *testing.T) {
	logs := "panic: runtime error: invalid memory address"
	got := buildEvidenceBlock(context.Background(), nil, nil, "ERROR LOGS", logs)
	if !strings.Contains(got, "=== ERROR LOGS ===") || !strings.Contains(got, logs) {
		t.Fatalf("small evidence must be injected verbatim under its header; got:\n%s", got)
	}
}

func TestBuildEvidenceBlock_LargeFallsBackToBoundedExcerpt(t *testing.T) {
	// Larger than the inline threshold, with a nil llm client so summarization is
	// unavailable → must fall back to a bounded head+tail excerpt (never worse than a cap).
	logs := "FIRST_ERROR_LINE\n" + strings.Repeat("noise xyz\n", 6000) + "FINAL_FAILURE_LINE"
	if len(logs) <= evidenceInlineThreshold {
		t.Fatalf("test setup: logs not large enough")
	}
	got := buildEvidenceBlock(context.Background(), nil, nil, "ERROR LOGS", logs)

	if strings.Contains(got, "EVENT UNDERSTANDING") {
		t.Errorf("no llm client → must not claim a digest")
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("large evidence fallback must be marked truncated; got head:\n%s", got[:200])
	}
	if !strings.Contains(got, "FIRST_ERROR_LINE") || !strings.Contains(got, "FINAL_FAILURE_LINE") {
		t.Errorf("head and tail (first error + final state) must be preserved")
	}
	if len(got) >= len(logs) {
		t.Errorf("block (%d) must be much smaller than the raw evidence (%d)", len(got), len(logs))
	}
}

func TestEvidenceHeadTail(t *testing.T) {
	if got := evidenceHeadTail("tiny", 100); got != "tiny" {
		t.Errorf("input under cap must be unchanged, got %q", got)
	}
	s := "HEAD" + strings.Repeat("m", 2000) + "TAIL"
	got := evidenceHeadTail(s, 200)
	if !strings.HasPrefix(got, "HEAD") || !strings.HasSuffix(got, "TAIL") {
		t.Errorf("must keep both ends; got prefix=%q suffix=%q", got[:8], got[len(got)-8:])
	}
	if !strings.Contains(got, "omitted") {
		t.Errorf("must mark the dropped middle")
	}
	if len(got) > 200+64 {
		t.Errorf("result %d should be ~within cap (200) + marker", len(got))
	}
}
