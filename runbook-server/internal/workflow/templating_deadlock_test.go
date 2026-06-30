package workflow

import (
	"strings"
	"testing"
)

// TestGenerateMatrixCombinations_UnderCap verifies the full cartesian product is
// produced when it stays within the configured cap.
func TestGenerateMatrixCombinations_UnderCap(t *testing.T) {
	matrix := map[string]any{
		"fruit": []any{"apple", "banana"},
		"color": []any{"red", "green", "blue"},
	}

	combos, err := generateMatrixCombinations(matrix, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(combos) != 6 {
		t.Fatalf("expected 6 combinations, got %d", len(combos))
	}
}

// TestGenerateMatrixCombinations_ExceedsCap verifies a combinatorial blow-up is
// rejected with an actionable error instead of being expanded inline (which is
// what trips Temporal's deadlock detector).
func TestGenerateMatrixCombinations_ExceedsCap(t *testing.T) {
	big := make([]any, 40)
	for i := range big {
		big[i] = i
	}
	matrix := map[string]any{
		"a": big, // 40 * 40 = 1600 > cap
		"b": big,
	}

	combos, err := generateMatrixCombinations(matrix, 1000)
	if err == nil {
		t.Fatalf("expected error for matrix exceeding cap, got %d combinations", len(combos))
	}
	if !strings.Contains(err.Error(), "runbook_server_matrix_max_combinations") {
		t.Fatalf("error should reference the config knob, got: %v", err)
	}
}

// TestGenerateMatrixCombinations_CapDisabled verifies maxCombinations <= 0 keeps
// the previous (uncapped) behaviour so existing callers/tests are unaffected.
func TestGenerateMatrixCombinations_CapDisabled(t *testing.T) {
	big := make([]any, 40)
	for i := range big {
		big[i] = i
	}
	matrix := map[string]any{"a": big, "b": big}

	combos, err := generateMatrixCombinations(matrix, 0)
	if err != nil {
		t.Fatalf("unexpected error with cap disabled: %v", err)
	}
	if len(combos) != 1600 {
		t.Fatalf("expected 1600 combinations, got %d", len(combos))
	}
}

// TestGenerateMatrixCombinations_EmptyDimension preserves the zero-combination
// behaviour when a matrix dimension is empty (must not be flagged by the cap).
func TestGenerateMatrixCombinations_EmptyDimension(t *testing.T) {
	matrix := map[string]any{
		"a": []any{1, 2},
		"b": []any{},
	}
	combos, err := generateMatrixCombinations(matrix, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(combos) != 0 {
		t.Fatalf("expected 0 combinations for empty dimension, got %d", len(combos))
	}
}

// TestCompileGonjaTemplate_CachesAndRenders verifies the memoised compile path
// returns a usable template and is consistent across repeated calls (same source
// string), and that parse errors are returned consistently from the cache.
func TestCompileGonjaTemplate_CachesAndRenders(t *testing.T) {
	const tpl = "hello {{ Vars.name }}"

	t1, err := compileGonjaTemplate(tpl)
	if err != nil {
		t.Fatalf("unexpected compile error: %v", err)
	}
	t2, err := compileGonjaTemplate(tpl)
	if err != nil {
		t.Fatalf("unexpected compile error on second call: %v", err)
	}
	if t1 != t2 {
		t.Fatalf("expected the same cached template instance on repeat compile")
	}

	ctx := NewTemplateContext(nil, map[string]any{"name": "world"})
	out, err := ctx.renderGonja(tpl)
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}
	if out != "hello world" {
		t.Fatalf("expected 'hello world', got %q", out)
	}

	// A malformed template must surface (and cache) its parse error consistently.
	const bad = "{{ unterminated"
	if _, err1 := compileGonjaTemplate(bad); err1 == nil {
		t.Fatalf("expected parse error for malformed template")
	}
	if _, err2 := compileGonjaTemplate(bad); err2 == nil {
		t.Fatalf("expected cached parse error for malformed template")
	}
}
