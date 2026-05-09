package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Helper functions ---

func createTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(content)
}

// --- Unit tests for helper functions ---

func TestNormalizeWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"single spaces", "UUID: lo.Ternary", "UUID: lo.Ternary"},
		{"double spaces", "UUID:  lo.Ternary", "UUID: lo.Ternary"},
		{"tabs", "\t\tUUID:\tlo.Ternary", "UUID: lo.Ternary"},
		{"mixed whitespace", "  UUID:  \t lo.Ternary  ", "UUID: lo.Ternary"},
		{"empty string", "", ""},
		{"only whitespace", "   \t  ", ""},
		{"no whitespace", "abc", "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeWhitespace(tt.input))
		})
	}
}

func TestLeadingWhitespaceWidth(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{"no whitespace", "abc", 0},
		{"4 spaces", "    abc", 4},
		{"8 spaces", "        abc", 8},
		{"1 tab", "\tabc", 4},
		{"2 tabs", "\t\tabc", 8},
		{"tab + 2 spaces", "\t  abc", 6},
		{"empty string", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, leadingWhitespaceWidth(tt.input))
		})
	}
}

func TestIndentString(t *testing.T) {
	tests := []struct {
		name     string
		width    int
		useTabs  bool
		expected string
	}{
		{"4 spaces", 4, false, "    "},
		{"8 spaces", 8, false, "        "},
		{"1 tab", 4, true, "\t"},
		{"2 tabs", 8, true, "\t\t"},
		{"tab + 2 spaces", 6, true, "\t  "},
		{"zero width", 0, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, indentString(tt.width, tt.useTabs))
		})
	}
}

// --- Integration tests for flexibleMatch ---

func TestFlexibleMatch_InternalWhitespaceDifference(t *testing.T) {
	// The real-world bug: old_string has "UUID:  lo.Ternary" (double space)
	// but file has "UUID: lo.Ternary" (single space).
	// flexibleMatch should normalize and match.
	tool := NewReplaceTool()

	fileContent := strings.Join([]string{
		"package main",
		"",
		"func buildStruct() MyStruct {",
		"    return MyStruct{",
		"        UUID: lo.Ternary(hasUUID, getUUID(), uuid.Nil),",
		"        Name: \"test\",",
		"    }",
		"}",
	}, "\n")

	oldString := "\t\tUUID:  lo.Ternary(hasUUID, getUUID(), uuid.Nil),"
	newString := "\t\tUUID: lo.Ternary(hasUUID, generateNewUUID(), uuid.Nil),"

	result, lineNum, ok := tool.flexibleMatch(fileContent, oldString, newString)
	require.True(t, ok, "flexibleMatch should succeed with internal whitespace differences")
	assert.Equal(t, 5, lineNum, "should match line 5")
	assert.Contains(t, result, "UUID: lo.Ternary(hasUUID, generateNewUUID(), uuid.Nil),")
	// Should use spaces (file's indentation), not tabs
	assert.Contains(t, result, "        UUID: lo.Ternary(hasUUID, generateNewUUID(), uuid.Nil),")
}

func TestFlexibleMatch_TabsVsSpaces(t *testing.T) {
	// File uses spaces, old_string uses tabs — should still match
	tool := NewReplaceTool()

	fileContent := strings.Join([]string{
		"func foo() {",
		"    bar := 1",
		"    baz := 2",
		"}",
	}, "\n")

	oldString := "\tbar := 1\n\tbaz := 2"
	newString := "\tbar := 10\n\tbaz := 20"

	result, lineNum, ok := tool.flexibleMatch(fileContent, oldString, newString)
	require.True(t, ok)
	assert.Equal(t, 2, lineNum)
	// Result should use the file's spaces, not the old_string's tabs
	lines := strings.Split(result, "\n")
	assert.Equal(t, "    bar := 10", lines[1])
	assert.Equal(t, "    baz := 20", lines[2])
}

func TestFlexibleMatch_RelativeIndentation(t *testing.T) {
	// Verify relative indentation is preserved across lines
	tool := NewReplaceTool()

	fileContent := strings.Join([]string{
		"func foo() {",
		"    if true {",
		"        doSomething()",
		"    }",
		"}",
	}, "\n")

	// old_string with tabs representing the nested structure
	oldString := "\tif true {\n\t\tdoSomething()\n\t}"
	newString := "\tif true {\n\t\tdoSomethingElse()\n\t\tlog(\"done\")\n\t}"

	result, _, ok := tool.flexibleMatch(fileContent, oldString, newString)
	require.True(t, ok)
	lines := strings.Split(result, "\n")
	assert.Equal(t, "    if true {", lines[1])
	assert.Equal(t, "        doSomethingElse()", lines[2])
	assert.Equal(t, "        log(\"done\")", lines[3])
	assert.Equal(t, "    }", lines[4])
}

func TestFlexibleMatch_AmbiguousMultipleMatches(t *testing.T) {
	// Multiple matches should return false
	tool := NewReplaceTool()

	fileContent := "line1\nfoo\nline3\nfoo\nline5"
	oldString := "foo"
	newString := "bar"

	_, _, ok := tool.flexibleMatch(fileContent, oldString, newString)
	assert.False(t, ok, "should fail on ambiguous multiple matches")
}

// --- Integration tests for regexMatch ---

func TestRegexMatch_NoDoubleComma(t *testing.T) {
	// The real-world bug: old_string ends with comma, regex tokenizer strips it,
	// match boundary doesn't include the comma, resulting in ",," after replacement.
	// With line-based replacement, the entire line is replaced — no boundary artifacts.
	tool := NewReplaceTool()

	fileContent := strings.Join([]string{
		"package main",
		"",
		"func buildStruct() MyStruct {",
		"    return MyStruct{",
		"        UUID: lo.Ternary(hasUUID, getUUID(), uuid.Nil),",
		"        Name: \"test\",",
		"    }",
		"}",
	}, "\n")

	oldString := "UUID: lo.Ternary(hasUUID, getUUID(), uuid.Nil),"
	newString := "UUID: lo.Ternary(hasUUID, generateNewUUID(), uuid.Nil),"

	result, lineNum, ok := tool.regexMatch(fileContent, oldString, newString)
	require.True(t, ok, "regexMatch should find a match")
	assert.Equal(t, 5, lineNum)

	// The critical check: no double comma
	assert.NotContains(t, result, ",,", "must not produce double comma")
	assert.Contains(t, result, "UUID: lo.Ternary(hasUUID, generateNewUUID(), uuid.Nil),")
}

func TestRegexMatch_LineBasedReplacement(t *testing.T) {
	// Verify that line-based replacement replaces full lines, not byte ranges
	tool := NewReplaceTool()

	fileContent := strings.Join([]string{
		"first line",
		"    target = value;  // comment",
		"third line",
	}, "\n")

	oldString := "target = value"
	newString := "target = newValue"

	result, lineNum, ok := tool.regexMatch(fileContent, oldString, newString)
	require.True(t, ok)
	assert.Equal(t, 2, lineNum)

	lines := strings.Split(result, "\n")
	assert.Equal(t, 3, len(lines), "should still have 3 lines")
	assert.Equal(t, "first line", lines[0])
	assert.Equal(t, "    target = newValue", lines[1])
	assert.Equal(t, "third line", lines[2])
}

func TestRegexMatch_IndentationFromFile(t *testing.T) {
	// Verify that regexMatch applies file's indentation style
	tool := NewReplaceTool()

	fileContent := strings.Join([]string{
		"func foo() {",
		"        value := compute(a, b)",
		"}",
	}, "\n")

	oldString := "value := compute(a, b)"
	newString := "value := compute(a, b, c)"

	result, _, ok := tool.regexMatch(fileContent, oldString, newString)
	require.True(t, ok)
	lines := strings.Split(result, "\n")
	assert.Equal(t, "        value := compute(a, b, c)", lines[1])
}

func TestRegexMatch_AmbiguousMultipleMatches(t *testing.T) {
	tool := NewReplaceTool()

	fileContent := "line1\nfoo := bar\nline3\nfoo := bar\nline5"
	oldString := "foo := bar"
	newString := "foo := baz"

	_, _, ok := tool.regexMatch(fileContent, oldString, newString)
	assert.False(t, ok, "should fail on ambiguous multiple matches")
}

// --- Integration tests using Execute (full cascade via real tool) ---

func TestReplace_ExactMatch_ViaExecute(t *testing.T) {
	dir := t.TempDir()
	tool := NewReplaceToolWithWorkspace(dir)

	filePath := createTempFile(t, dir, "test.go", "func foo() {\n    return 1\n}\n")

	input := map[string]any{
		"file_path":  filePath,
		"old_string": "    return 1",
		"new_string": "    return 2",
		"action":     "replace",
	}

	resp := tool.Execute(context.Background(), input)
	assert.Equal(t, "success", resp.Status)
	assert.Contains(t, resp.Observation, "strategy: exact")
	assert.Equal(t, "func foo() {\n    return 2\n}\n", readFile(t, filePath))
}

func TestReplace_FlexibleFallback_ViaExecute(t *testing.T) {
	dir := t.TempDir()
	tool := NewReplaceToolWithWorkspace(dir)

	// File uses spaces
	filePath := createTempFile(t, dir, "test.go", "func foo() {\n    return 1\n}\n")

	// old_string uses tab — exact match will fail, flexible should work
	input := map[string]any{
		"file_path":  filePath,
		"old_string": "\treturn 1",
		"new_string": "\treturn 2",
		"action":     "replace",
	}

	resp := tool.Execute(context.Background(), input)
	assert.Equal(t, "success", resp.Status)
	assert.Contains(t, resp.Observation, "strategy: flexible")
	// File should maintain its space-based indentation
	content := readFile(t, filePath)
	assert.Contains(t, content, "    return 2")
	assert.NotContains(t, content, "\t")
}

func TestReplace_RealWorldScenario_ViaExecute(t *testing.T) {
	// Reproduces the exact E2E failure scenario:
	// - File has 8-space indentation, single space after colon
	// - LLM sends 2-tab indentation, double space after colon
	// - flexibleMatch should now handle this (previously fell through to regex)
	dir := t.TempDir()
	tool := NewReplaceToolWithWorkspace(dir)

	fileContent := strings.Join([]string{
		"package main",
		"",
		"import (",
		"    \"github.com/google/uuid\"",
		"    \"github.com/samber/lo\"",
		")",
		"",
		"func createRecord(hasUUID bool) Record {",
		"    return Record{",
		"        UUID: lo.Ternary(hasUUID, getUUID(), uuid.Nil),",
		"        Name: \"test\",",
		"    }",
		"}",
	}, "\n")

	filePath := createTempFile(t, dir, "main.go", fileContent)

	// LLM sends tabs + double space (the problematic pattern)
	input := map[string]any{
		"file_path":  filePath,
		"old_string": "\t\tUUID:  lo.Ternary(hasUUID, getUUID(), uuid.Nil),",
		"new_string": "\t\tUUID:  lo.Ternary(hasUUID, generateNewUUID(), uuid.Nil),",
		"action":     "replace",
	}

	resp := tool.Execute(context.Background(), input)
	assert.Equal(t, "success", resp.Status)

	result := readFile(t, filePath)
	// Must use file's 8-space indentation, not tabs
	assert.Contains(t, result, "        UUID:")
	assert.Contains(t, result, "lo.Ternary(hasUUID, generateNewUUID(), uuid.Nil),")
	assert.NotContains(t, result, "\t\tUUID", "must not have tab indentation from old_string")
	// Must NOT have double comma
	assert.NotContains(t, result, ",,")
	// Other lines should be untouched
	assert.Contains(t, result, "        Name: \"test\",")
}

// ============================================================================
// SELF-HEALING TESTS (Anchor Preservation, Duplicate Detection, Bracket Balance)
// ============================================================================

func TestReplace_AnchorPreservation_WithPurpose(t *testing.T) {
	// Test: purpose indicates "add after" + old_string not in new_string → auto-correct
	// This is the PR 23575 scenario with purpose providing intent
	dir := t.TempDir()
	tool := NewReplaceToolWithWorkspace(dir)

	fileContent := strings.Join([]string{
		"FROM base-image:latest",
		"COPY --from=uv-base /opt/conda /opt/conda",
		"WORKDIR /app",
	}, "\n")

	filePath := createTempFile(t, dir, "Dockerfile", fileContent)

	// LLM sends old_string=COPY, new_string=RUN (missing anchor), but purpose says "add after"
	input := map[string]any{
		"file_path":  filePath,
		"old_string": "COPY --from=uv-base /opt/conda /opt/conda",
		"new_string": "RUN apt-get update && apt-get install -y openssl",
		"purpose":    "Add explicit package upgrade after conda copy",
		"action":     "replace",
	}

	resp := tool.Execute(context.Background(), input)
	assert.Equal(t, "success", resp.Status)
	assert.Contains(t, resp.Observation, "Auto-corrected to preserve anchor line")

	result := readFile(t, filePath)
	// Both COPY and RUN should be present
	assert.Contains(t, result, "COPY --from=uv-base /opt/conda /opt/conda")
	assert.Contains(t, result, "RUN apt-get update && apt-get install -y openssl")
}

func TestReplace_AnchorPreservation_NoPurpose(t *testing.T) {
	// Test: no purpose + old_string not in new_string + unrelated content → warning
	dir := t.TempDir()
	tool := NewReplaceToolWithWorkspace(dir)

	fileContent := strings.Join([]string{
		"FROM base-image:latest",
		"COPY --from=uv-base /opt/conda /opt/conda",
		"WORKDIR /app",
	}, "\n")

	filePath := createTempFile(t, dir, "Dockerfile", fileContent)

	// No purpose provided, completely unrelated replacement
	input := map[string]any{
		"file_path":  filePath,
		"old_string": "COPY --from=uv-base /opt/conda /opt/conda",
		"new_string": "RUN apt-get update && apt-get install -y openssl",
		"action":     "replace",
	}

	resp := tool.Execute(context.Background(), input)
	assert.Equal(t, "error", resp.Status)
	assert.Contains(t, resp.Error, "WARNING: This replacement completely removes the original code")
}

func TestReplace_LegitimateReplace_NoPurpose(t *testing.T) {
	// Test: legitimate replace (same first word) should proceed without warning
	dir := t.TempDir()
	tool := NewReplaceToolWithWorkspace(dir)

	fileContent := "func calculate() int {\n    return 1\n}\n"
	filePath := createTempFile(t, dir, "main.go", fileContent)

	input := map[string]any{
		"file_path":  filePath,
		"old_string": "return 1",
		"new_string": "return 2",
		"action":     "replace",
	}

	resp := tool.Execute(context.Background(), input)
	assert.Equal(t, "success", resp.Status)
	assert.NotContains(t, resp.Observation, "WARNING")
	assert.Contains(t, readFile(t, filePath), "return 2")
}

func TestReplace_DuplicateDetection(t *testing.T) {
	// Test: new_string sequence already exists near match location → warning
	// The duplicate detection looks for consecutive lines in new_string appearing elsewhere
	dir := t.TempDir()
	tool := NewReplaceToolWithWorkspace(dir)

	fileContent := strings.Join([]string{
		"func foo() {",
		"    validate()",
		"    process()",     // This sequence already exists
		"    cleanup()",     // at lines 3-4
		"    placeholder()", // We'll replace this
		"    finish()",
		"}",
	}, "\n")

	filePath := createTempFile(t, dir, "main.go", fileContent)

	// Try to insert process()\ncleanup() after placeholder() — but that sequence exists above!
	input := map[string]any{
		"file_path":  filePath,
		"old_string": "    placeholder()",
		"new_string": "    process()\n    cleanup()",
		"purpose":    "Replace placeholder with process and cleanup",
		"action":     "replace",
	}

	resp := tool.Execute(context.Background(), input)
	assert.Equal(t, "success", resp.Status)
	// Should have a warning about potential duplicate
	assert.Contains(t, resp.Observation, "WARNING")
	assert.Contains(t, resp.Observation, "duplicate")
}

func TestReplace_BracketBalanceWarning(t *testing.T) {
	// Test: significant bracket imbalance → warning
	dir := t.TempDir()
	tool := NewReplaceToolWithWorkspace(dir)

	fileContent := "func foo() {\n    bar()\n}\n"
	filePath := createTempFile(t, dir, "main.go", fileContent)

	// Replace with unbalanced brackets (adding 3 open braces, no close)
	input := map[string]any{
		"file_path":  filePath,
		"old_string": "bar()",
		"new_string": "if x { if y { if z { bar()",
		"purpose":    "Add nested conditions",
		"action":     "replace",
	}

	resp := tool.Execute(context.Background(), input)
	assert.Equal(t, "success", resp.Status)
	assert.Contains(t, resp.Observation, "WARNING")
	assert.Contains(t, resp.Observation, "Bracket balance")
}

func TestReplace_InsertAfter_DockerfileCOPY(t *testing.T) {
	// Real-world PR 23575 scenario: insert RUN after COPY in Dockerfile
	// With correct purpose, should auto-correct
	dir := t.TempDir()
	tool := NewReplaceToolWithWorkspace(dir)

	fileContent := strings.Join([]string{
		"FROM registry.nudgebee.com/nudgebee-ml-base:20250627-141845 AS builder",
		"",
		"COPY --from=uv-base /opt/conda /opt/conda",
		"",
		"RUN conda run -n myenv uv pip install --system --requirements pyproject.toml",
	}, "\n")

	filePath := createTempFile(t, dir, "Dockerfile", fileContent)

	// The exact pattern from PR 23575 — LLM forgot to include COPY in new_string
	input := map[string]any{
		"file_path":  filePath,
		"old_string": "COPY --from=uv-base /opt/conda /opt/conda",
		"new_string": "RUN apt-get update && apt-get install -y --no-install-recommends openssl-provider-legacy=3.5.4-1~deb13u2 && rm -rf /var/lib/apt/lists/*",
		"purpose":    "Add explicit package upgrade after conda environment copy",
		"action":     "replace",
	}

	resp := tool.Execute(context.Background(), input)
	assert.Equal(t, "success", resp.Status)
	assert.Contains(t, resp.Observation, "Auto-corrected to preserve anchor line")

	result := readFile(t, filePath)
	// CRITICAL: Both lines must be present
	assert.Contains(t, result, "COPY --from=uv-base /opt/conda /opt/conda", "COPY line must be preserved")
	assert.Contains(t, result, "RUN apt-get update", "RUN line must be added")
	// COPY should come before RUN
	copyIdx := strings.Index(result, "COPY --from=uv-base")
	runIdx := strings.Index(result, "RUN apt-get update")
	assert.True(t, copyIdx < runIdx, "COPY should come before RUN")
}
