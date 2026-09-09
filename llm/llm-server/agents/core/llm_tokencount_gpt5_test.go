package core

import "testing"

// gpt-5.x matched no case in the context map and fell to the 32,000 global
// default. With the catalog's 65,536 output reserve exceeding that window,
// summarizationChunkSize halved it and the pre-flight trimmer cut the human turn
// — question, scratchpad and tool output — to the 256-token floor. Session
// f1a7ddd5 answered a question it could no longer see.
func TestGPT5ContextWindows(t *testing.T) {
	for model, want := range map[string]int{
		"gpt-5.6-sol":   1_050_000,
		"gpt-5.6-luna":  1_050_000,
		"gpt-5.6-terra": 1_050_000,
		"gpt-5.5":       1_050_000,
		"gpt-5.5-pro":   1_050_000,
		"gpt-5.4":       1_050_000,
		"gpt-5.4-mini":  1_050_000,
		// The 5.4 boundary: plain GPT-5 keeps its own smaller window, and a
		// future point release must land above it rather than below.
		"gpt-5":      400_000,
		"gpt-5-mini": 400_000,
		"gpt-5-nano": 400_000,
		"gpt-5.7":    1_050_000,
		"gpt-5.12":   1_050_000,
	} {
		if got := GetLlmMaxTokenLength(model); got != want {
			t.Errorf("GetLlmMaxTokenLength(%q) = %d, want %d", model, got, want)
		}
	}
}

// TestCodeMapMatchesCatalogSeed pins the values in this map to the CASE in
// V885. The two layers resolve the same models — catalog first, this map when
// no row exists — so a disagreement means a fresh install sizes prompts
// differently from a seeded one. Verified against the migration for all 35
// models below; gpt-4o previously matched nothing here and got the 32,000
// global default while the catalog said 128,000.
func TestCodeMapMatchesCatalogSeed(t *testing.T) {
	for model, want := range map[string]int{
		// Claude 4.6 generation onward
		"claude-fable-5": 1_000_000, "claude-opus-5": 1_000_000, "claude-sonnet-5": 1_000_000,
		"claude-opus-4.8": 1_000_000, "claude-opus-4.7": 1_000_000,
		"claude-opus-4.6": 1_000_000, "claude-sonnet-4.6": 1_000_000,
		// every earlier Claude generation
		"claude-opus-4.5": 200_000, "claude-sonnet-4.5": 200_000, "claude-haiku-4.5": 200_000,
		"claude-haiku-4-5-20251001": 200_000, "claude-opus-4": 200_000, "claude-sonnet-4": 200_000,
		"claude-sonnet-3.7": 200_000, "claude-haiku-3": 200_000, "claude-opus-3": 200_000,
		// Two-digit minor versions must land in the 1M band...
		"claude-opus-4.10": 1_000_000, "claude-sonnet-4.12": 1_000_000, "claude-opus-4.100": 1_000_000,
		// ...but a DATED id must not: "claude-opus-4-20250514" has a "20" right
		// where a minor version would be, and an unbounded digit match reads it
		// as one. That regressed once during review; these pin it.
		"claude-opus-4-20250514": 200_000, "claude-sonnet-4.5-20250929": 200_000,
		// OpenAI
		"gpt-4o": 128_000, "gpt-4o-mini": 128_000,
		"o3": 200_000, "o4-mini": 200_000,
	} {
		if got := GetLlmMaxTokenLength(model); got != want {
			t.Errorf("GetLlmMaxTokenLength(%q) = %d, want %d (V885 CASE)", model, got, want)
		}
	}
}
