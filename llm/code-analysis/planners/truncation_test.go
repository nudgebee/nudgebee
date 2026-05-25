package planners

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tmc/langchaingo/llms"
)

func TestProportionalTruncate_UnderLimit(t *testing.T) {
	content := "short content"
	result := proportionalTruncate(content, 100)
	if result != content {
		t.Errorf("expected unchanged content, got %q", result)
	}
}

func TestProportionalTruncate_OverLimit(t *testing.T) {
	content := strings.Repeat("x", 1000)
	result := proportionalTruncate(content, 200)

	if len(result) > 260 { // 200 + marker overhead
		t.Errorf("expected truncated result under 260 chars, got %d", len(result))
	}
	if !strings.Contains(result, "chars omitted") {
		t.Error("expected omission marker")
	}
}

func TestLineBasedTruncate_UnderLimit(t *testing.T) {
	content := "line1\nline2\nline3"
	result := lineBasedTruncate(content, 10)
	if result != content {
		t.Errorf("expected unchanged content, got %q", result)
	}
}

func TestLineBasedTruncate_OverLimit(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "line"
	}
	content := strings.Join(lines, "\n")

	result := lineBasedTruncate(content, 20)
	if !strings.Contains(result, "lines omitted") {
		t.Error("expected line omission marker")
	}
	// First line should be preserved
	firstLine := strings.SplitN(result, "\n", 2)[0]
	if firstLine != "line" {
		t.Errorf("expected first line preserved, got %q", firstLine)
	}
	// Last line should be preserved
	lastLine := result[strings.LastIndex(result, "\n")+1:]
	if lastLine != "line" {
		t.Errorf("expected last line preserved, got %q", lastLine)
	}
}

func TestCompactJSONArgs_ShortUnchanged(t *testing.T) {
	args := `{"pattern":"func main","type":"go"}`
	result := compactJSONArgs(args, 200)
	// Should pass through since it's under maxLen
	if result != args {
		t.Errorf("expected unchanged short args, got %q", result)
	}
}

func TestCompactJSONArgs_NestedDropped(t *testing.T) {
	// Use maxLen smaller than input to force compaction
	args := `{"name":"rg","nested":{"key":"value"},"items":[1,2,3],"count":42}`
	result := compactJSONArgs(args, 40)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v, result: %s", err, result)
	}
	// Scalars should be kept
	if _, ok := parsed["name"]; !ok {
		t.Error("expected 'name' to be preserved")
	}
	if _, ok := parsed["count"]; !ok {
		t.Error("expected 'count' to be preserved")
	}
	// Nested and arrays should be dropped
	if _, ok := parsed["nested"]; ok {
		t.Error("expected 'nested' to be dropped")
	}
	if _, ok := parsed["items"]; ok {
		t.Error("expected 'items' to be dropped")
	}
}

func TestCompactJSONArgs_InvalidJSON(t *testing.T) {
	// maxLen must be smaller than input to bypass the short-circuit
	result := compactJSONArgs("not json at all", 5)
	if result != "{}" {
		t.Errorf("expected {} for invalid JSON, got %q", result)
	}
}

func TestCompactJSONArgs_OverMaxLen(t *testing.T) {
	// Create args that after compaction still exceed maxLen
	input := map[string]any{}
	for i := 0; i < 20; i++ {
		input[strings.Repeat("k", 10)+string(rune('a'+i))] = strings.Repeat("v", 90)
	}
	argsJSON, _ := json.Marshal(input)

	result := compactJSONArgs(string(argsJSON), 50)
	if result != "{}" {
		t.Errorf("expected {} when compacted result exceeds maxLen, got %q", result)
	}
}

func TestEstimateMessageTokens(t *testing.T) {
	messages := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: strings.Repeat("a", 400)}},
		},
	}
	tokens := estimateMessageTokens(messages)
	if tokens != 100 {
		t.Errorf("expected 100 tokens, got %d", tokens)
	}
}
