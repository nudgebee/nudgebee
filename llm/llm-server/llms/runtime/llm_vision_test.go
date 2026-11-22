package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// PNG: 8-byte magic header
var pngHeader = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

// TestIsVisionCapableModel_AnchoredPatterns ensures the deny-list regexes do
// not accidentally match a model whose name shares a substring with an old
// non-vision model. The biggest historical footgun was the bare "cohere"
// prefix that blanket-rejected every Cohere model. Checked directly against
// the compiled patterns (not IsVisionCapableModel) since overall vision
// capability now also depends on DB state — this test isolates regex
// correctness from that (see TestIsVisionCapableModel_UnconfirmedModelsDefaultDeny).
func TestIsVisionCapableModel_AnchoredPatterns(t *testing.T) {
	regexes := compileVisionDenyPatterns(defaultNonVisionPatterns)
	deniedByRegex := func(model string) bool {
		for _, re := range regexes {
			if re.MatchString(model) {
				return true
			}
		}
		return false
	}
	// New Cohere models like command-r/r-plus must not be blanket-rejected
	assert.False(t, deniedByRegex("cohere.command-r-plus-v1"), "cohere command-r should not match the deny-list")
	assert.False(t, deniedByRegex("cohere.embed-english-v3"), "cohere embed should not match the deny-list")
	// gpt-4 (with vision) and gpt-4-turbo must not be caught by gpt-4-base pattern
	assert.False(t, deniedByRegex("gpt-4"), "gpt-4 should not match the deny-list")
	assert.False(t, deniedByRegex("gpt-4-turbo-2024-04-09"), "gpt-4-turbo should not match the deny-list")
	// claude-3 / claude-4 must not be caught by claude-2 pattern
	assert.False(t, deniedByRegex("anthropic.claude-3-opus-20240229-v1:0"), "claude-3 should not match the deny-list")
	assert.False(t, deniedByRegex("anthropic.claude-sonnet-4-5"), "claude-4 should not match the deny-list")
}

func TestHasImageParts_NoImages(t *testing.T) {
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "hello"}}},
	}
	assert.False(t, hasImageParts(msgs))
}

func TestHasImageParts_WithBinaryContent(t *testing.T) {
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.TextContent{Text: "describe this"},
			llms.BinaryContent{MIMEType: "image/png", Data: pngHeader},
		}},
	}
	assert.True(t, hasImageParts(msgs))
}

func TestHasImageParts_WithImageURL(t *testing.T) {
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.ImageURLContent{URL: "https://example.com/img.png"},
		}},
	}
	assert.True(t, hasImageParts(msgs))
}

func TestStripImagePartsWithFallback(t *testing.T) {
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "system prompt"}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.TextContent{Text: "describe this"},
			llms.BinaryContent{MIMEType: "image/png", Data: pngHeader},
			llms.ImageURLContent{URL: "https://example.com/img.png"},
		}},
	}
	result := stripImagePartsWithFallback(msgs)

	assert.Len(t, result, 2)
	// System message unchanged
	assert.Len(t, result[0].Parts, 1)
	// Human message: text + fallback note, images removed
	assert.Len(t, result[1].Parts, 2)
	assert.Equal(t, "describe this", result[1].Parts[0].(llms.TextContent).Text)
	assert.Contains(t, result[1].Parts[1].(llms.TextContent).Text, "2 image(s) were attached but cannot be processed")
}
