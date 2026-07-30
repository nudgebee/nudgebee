package core

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// Minimal valid file headers for MIME detection
var (
	// PNG: 8-byte magic header
	pngHeader = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	// JPEG: starts with FF D8 FF
	jpegHeader = []byte{0xFF, 0xD8, 0xFF, 0xE0}
)

func pngBase64() string  { return base64.StdEncoding.EncodeToString(pngHeader) }
func jpegBase64() string { return base64.StdEncoding.EncodeToString(jpegHeader) }

func TestValidateImages_Empty(t *testing.T) {
	assert.NoError(t, ValidateImages(context.Background(), nil))
	assert.NoError(t, ValidateImages(context.Background(), []ImageAttachment{}))
}

func TestValidateImages_ValidInlineImage(t *testing.T) {
	imgs := []ImageAttachment{
		{Data: pngBase64(), MIMEType: "image/png"},
	}
	assert.NoError(t, ValidateImages(context.Background(), imgs))
}

// Note: TestValidateImages_ValidURLImage previously hit live DNS for example.com.
// Live-network tests are flaky and gated by environment; URL-fetch behavior is
// covered by TestValidateImageURLHost_* (pure validation) and a future
// httptest-based fetch test. We do not assert on real-world fetches here.

func TestValidateImages_TooManyImages(t *testing.T) {
	imgs := make([]ImageAttachment, defaultImageMaxPerMessage+1)
	for i := range imgs {
		imgs[i] = ImageAttachment{Data: pngBase64(), MIMEType: "image/png"}
	}
	err := ValidateImages(context.Background(), imgs)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too many images")
}

func TestValidateImages_MutualExclusivity_BothSet(t *testing.T) {
	imgs := []ImageAttachment{
		{Data: "abc", URL: "https://example.com/img.png", MIMEType: "image/png"},
	}
	err := ValidateImages(context.Background(), imgs)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one of data or url must be set")
}

func TestValidateImages_MutualExclusivity_NeitherSet(t *testing.T) {
	imgs := []ImageAttachment{
		{MIMEType: "image/png"},
	}
	err := ValidateImages(context.Background(), imgs)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one of data or url must be set")
}

func TestValidateImages_UnsupportedMIMEType(t *testing.T) {
	// Use PNG bytes but claim svg — should fail on MIME mismatch
	imgs := []ImageAttachment{
		{Data: pngBase64(), MIMEType: "image/svg+xml"},
	}
	err := ValidateImages(context.Background(), imgs)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not match detected")
}

func TestValidateImages_AllAllowedMIMETypes(t *testing.T) {
	headerMap := map[string]string{
		"image/png":  pngBase64(),
		"image/jpeg": jpegBase64(),
	}
	for mime, data := range headerMap {
		imgs := []ImageAttachment{
			{Data: data, MIMEType: mime},
		}
		assert.NoError(t, ValidateImages(context.Background(), imgs), "should accept %s", mime)
	}
}

func TestValidateImages_InvalidBase64(t *testing.T) {
	imgs := []ImageAttachment{
		{Data: "not-valid-base64!!!", MIMEType: "image/png"},
	}
	err := ValidateImages(context.Background(), imgs)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid base64")
}

func TestValidateImages_URLSafeBase64(t *testing.T) {
	// URL-safe encoding uses - and _ instead of + and /
	encoded := base64.URLEncoding.EncodeToString(pngHeader)
	imgs := []ImageAttachment{
		{Data: encoded, MIMEType: "image/png"},
	}
	assert.NoError(t, ValidateImages(context.Background(), imgs))
}

func TestValidateImages_NoPaddingBase64(t *testing.T) {
	encoded := base64.RawStdEncoding.EncodeToString(pngHeader)
	imgs := []ImageAttachment{
		{Data: encoded, MIMEType: "image/png"},
	}
	assert.NoError(t, ValidateImages(context.Background(), imgs))
}

func TestValidateImages_OversizedImage(t *testing.T) {
	// Create data larger than max size with PNG header
	bigData := make([]byte, int(defaultImageMaxSizeMB*1024*1024)+1)
	copy(bigData, pngHeader)
	encoded := base64.StdEncoding.EncodeToString(bigData)
	imgs := []ImageAttachment{
		{Data: encoded, MIMEType: "image/png"},
	}
	err := ValidateImages(context.Background(), imgs)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestValidateImages_URLBadScheme(t *testing.T) {
	imgs := []ImageAttachment{
		{URL: "ftp://example.com/img.png", MIMEType: "image/png"},
	}
	err := ValidateImages(context.Background(), imgs)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scheme")
}

func TestValidateImages_URLPrivateIP(t *testing.T) {
	imgs := []ImageAttachment{
		{URL: "http://10.0.0.1/img.png", MIMEType: "image/png"},
	}
	err := ValidateImages(context.Background(), imgs)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "private")
}

func parseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	assert.NoError(t, err)
	return u
}

func TestValidateImageURLHost_Localhost(t *testing.T) {
	err := common.ValidateImageURLHost(parseURL(t, "http://localhost/img.png"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "blocked")
}

func TestValidateImageURLHost_MetadataEndpoint(t *testing.T) {
	err := common.ValidateImageURLHost(parseURL(t, "http://169.254.169.254/latest/meta-data/"))
	assert.Error(t, err)
}

func TestValidateImageURLHost_GoogleMetadata(t *testing.T) {
	err := common.ValidateImageURLHost(parseURL(t, "http://metadata.google.internal/computeMetadata/v1/"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "blocked")
}

func TestValidateImageURLHost_BadScheme(t *testing.T) {
	err := common.ValidateImageURLHost(parseURL(t, "ftp://example.com/img.png"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scheme")
}

func TestValidateImageURLHost_LiteralPrivateIP(t *testing.T) {
	err := common.ValidateImageURLHost(parseURL(t, "http://10.0.0.1/img.png"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "private")
}

func TestIsBlockedImageHost(t *testing.T) {
	assert.True(t, common.IsBlockedImageHost("localhost"))
	assert.True(t, common.IsBlockedImageHost("LOCALHOST"))
	assert.True(t, common.IsBlockedImageHost("169.254.169.254"))
	assert.True(t, common.IsBlockedImageHost("metadata.google.internal"))
	assert.True(t, common.IsBlockedImageHost("metadata.azure.com"))
	assert.True(t, common.IsBlockedImageHost("100.100.100.200"))
	assert.False(t, common.IsBlockedImageHost("example.com"))
}

func TestIsPrivateOrLoopbackIP(t *testing.T) {
	tests := []struct {
		ip      string
		private bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"192.168.1.1", true},
		{"172.16.0.1", true},
		{"169.254.1.1", true},
		{"0.0.0.0", true},
		{"::1", true},
		{"fd00:ec2::254", true},
		{"fc00::1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false},
	}

	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		assert.Equal(t, tt.private, common.IsPrivateOrLoopbackIP(ip), "IP %s", tt.ip)
	}
}

func TestValidateImages_MultipleImages_SecondInvalid(t *testing.T) {
	imgs := []ImageAttachment{
		{Data: pngBase64(), MIMEType: "image/png"},
		{Data: pngBase64(), MIMEType: "image/bmp"}, // MIME mismatch
	}
	err := ValidateImages(context.Background(), imgs)
	assert.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "image[1]:"))
}

func TestStripDataURIPrefix(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"raw base64", "iVBORw0KGgoA", "iVBORw0KGgoA"},
		{"full data URI", "data:image/png;base64,iVBORw0KGgoA", "iVBORw0KGgoA"},
		{"data URI without base64 marker", "data:image/png;iVBORw0KGgoA", "iVBORw0KGgoA"},
		{"jpeg data URI", "data:image/jpeg;base64,/9j/4AAQ", "/9j/4AAQ"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, stripDataURIPrefix(tt.input))
		})
	}
}

func TestDecodeBase64_StandardEncoding(t *testing.T) {
	input := base64.StdEncoding.EncodeToString(pngHeader)
	decoded, err := decodeBase64(input)
	assert.NoError(t, err)
	assert.Equal(t, pngHeader, decoded)
}

func TestDecodeBase64_URLSafeEncoding(t *testing.T) {
	input := base64.URLEncoding.EncodeToString(pngHeader)
	decoded, err := decodeBase64(input)
	assert.NoError(t, err)
	assert.Equal(t, pngHeader, decoded)
}

func TestDecodeBase64_NoPadding(t *testing.T) {
	input := base64.RawStdEncoding.EncodeToString(pngHeader)
	decoded, err := decodeBase64(input)
	assert.NoError(t, err)
	assert.Equal(t, pngHeader, decoded)
}

func TestDecodeBase64_URLSafeNoPadding(t *testing.T) {
	input := base64.RawURLEncoding.EncodeToString(pngHeader)
	decoded, err := decodeBase64(input)
	assert.NoError(t, err)
	assert.Equal(t, pngHeader, decoded)
}

func TestDecodeBase64_Invalid(t *testing.T) {
	_, err := decodeBase64("not-valid-base64!!!")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not valid base64")
}

func TestValidateImages_DataURIPrefix(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString(pngHeader)
	dataURI := "data:image/png;base64," + raw
	imgs := []ImageAttachment{
		{Data: dataURI, MIMEType: "image/png"},
	}
	assert.NoError(t, ValidateImages(context.Background(), imgs))
}

func TestDetectMIMEType(t *testing.T) {
	assert.Equal(t, "image/png", detectMIMEType(pngHeader))
	assert.Equal(t, "image/jpeg", detectMIMEType(jpegHeader))

}

func TestValidateImages_AutoDetectMIME(t *testing.T) {
	// No mime_type provided — should auto-detect from bytes
	imgs := []ImageAttachment{
		{Data: pngBase64()},
	}
	assert.NoError(t, ValidateImages(context.Background(), imgs))
	assert.Equal(t, "image/png", imgs[0].MIMEType)
}

func TestValidateImages_MIMEMismatch(t *testing.T) {
	// PNG bytes but claim JPEG
	imgs := []ImageAttachment{
		{Data: pngBase64(), MIMEType: "image/jpeg"},
	}
	err := ValidateImages(context.Background(), imgs)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not match detected")
}

// --- Phase 3: Vision Detection Tests ---

func TestIsVisionCapableModel_DefaultDenyList(t *testing.T) {
	// Regex-denied models are never vision-capable, regardless of DB state.
	assert.False(t, IsVisionCapableModel("openai", "gpt-3.5-turbo"))
	assert.False(t, IsVisionCapableModel("bedrock", "claude-2.1"))
	assert.False(t, IsVisionCapableModel("bedrock", "claude-instant-v1"))
	assert.False(t, IsVisionCapableModel("bedrock", "amazon.titan-text-express-v1"))
	assert.False(t, IsVisionCapableModel("bedrock", "cohere.command-text-v14"))
	assert.False(t, IsVisionCapableModel("bedrock", "cohere.command-light-text-v14"))
}

// TestIsVisionCapableModel_UnconfirmedModelsDefaultDeny documents the
// default-deny posture: a model not on the deny-list is still NOT
// vision-capable absent an explicit llm_model_pricing.supports_image_input
// row. Absence from the deny-list is not evidence of vision support.
func TestIsVisionCapableModel_UnconfirmedModelsDefaultDeny(t *testing.T) {
	assert.False(t, IsVisionCapableModel("bedrock", "anthropic.claude-sonnet-4-20250514-v1:0"))
	assert.False(t, IsVisionCapableModel("openai", "gpt-4o"))
	assert.False(t, IsVisionCapableModel("googleai", "gemini-2.0-flash"))
}

func TestIsVisionCapableModel_CaseInsensitive(t *testing.T) {
	assert.False(t, IsVisionCapableModel("openai", "GPT-3.5-turbo"))
	assert.False(t, IsVisionCapableModel("bedrock", "Claude-2"))
}

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

// --- Phase 4: Content Part Conversion Tests ---

func TestImageAttachmentToContentPart_Base64(t *testing.T) {
	img := ImageAttachment{Data: pngBase64(), MIMEType: "image/png"}
	part := ImageAttachmentToContentPart(img)
	bc, ok := part.(llms.BinaryContent)
	assert.True(t, ok)
	assert.Equal(t, "image/png", bc.MIMEType)
	assert.Equal(t, pngHeader, bc.Data)
}

func TestImageAttachmentToContentPart_URL(t *testing.T) {
	img := ImageAttachment{URL: "https://example.com/img.png", MIMEType: "image/png"}
	part := ImageAttachmentToContentPart(img)
	iuc, ok := part.(llms.ImageURLContent)
	assert.True(t, ok)
	assert.Equal(t, "https://example.com/img.png", iuc.URL)
}

func TestImageAttachmentToContentPart_InvalidBase64(t *testing.T) {
	img := ImageAttachment{Data: "not-valid!!!", MIMEType: "image/png"}
	part := ImageAttachmentToContentPart(img)
	tc, ok := part.(llms.TextContent)
	assert.True(t, ok)
	assert.Contains(t, tc.Text, "could not be decoded")
}

func TestImageAttachmentToContentPart_Empty(t *testing.T) {
	img := ImageAttachment{}
	part := ImageAttachmentToContentPart(img)
	tc, ok := part.(llms.TextContent)
	assert.True(t, ok)
	assert.Contains(t, tc.Text, "could not be decoded")
}

func TestImageAttachmentToContentPart_DataURIPrefix(t *testing.T) {
	raw := pngBase64()
	img := ImageAttachment{Data: "data:image/png;base64," + raw, MIMEType: "image/png"}
	part := ImageAttachmentToContentPart(img)
	bc, ok := part.(llms.BinaryContent)
	assert.True(t, ok)
	assert.Equal(t, "image/png", bc.MIMEType)
	assert.Equal(t, pngHeader, bc.Data)
}

func TestImageAttachmentToContentPart_AutoDetectMIME(t *testing.T) {
	img := ImageAttachment{Data: pngBase64()} // No MIMEType set
	part := ImageAttachmentToContentPart(img)
	bc, ok := part.(llms.BinaryContent)
	assert.True(t, ok)
	assert.Equal(t, "image/png", bc.MIMEType)
}

func TestAppendImagesToLastHumanMessage_NoImages(t *testing.T) {
	mcList := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "hello"}}},
	}
	result := AppendImagesToLastHumanMessage(mcList, nil)
	assert.Len(t, result, 1)
	assert.Len(t, result[0].Parts, 1) // unchanged
}

func TestAppendImagesToLastHumanMessage_AppendsToLastHuman(t *testing.T) {
	mcList := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "system"}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "query"}}},
	}
	images := []ImageAttachment{
		{Data: pngBase64(), MIMEType: "image/png"},
	}
	result := AppendImagesToLastHumanMessage(mcList, images)

	assert.Len(t, result, 2)
	assert.Len(t, result[0].Parts, 1) // system unchanged
	assert.Len(t, result[1].Parts, 2) // text + image
	_, isBinary := result[1].Parts[1].(llms.BinaryContent)
	assert.True(t, isBinary)
}

func TestAppendImagesToLastHumanMessage_NoHumanMessage(t *testing.T) {
	mcList := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "system"}}},
	}
	images := []ImageAttachment{
		{URL: "https://example.com/img.png", MIMEType: "image/png"},
	}
	result := AppendImagesToLastHumanMessage(mcList, images)

	assert.Len(t, result, 2) // system + new human
	assert.Equal(t, llms.ChatMessageTypeHuman, result[1].Role)
}

func TestAppendImagesToLastHumanMessage_MultipleHumanMessages(t *testing.T) {
	mcList := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "first"}}},
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{llms.TextContent{Text: "response"}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "second"}}},
	}
	images := []ImageAttachment{
		{Data: pngBase64(), MIMEType: "image/png"},
	}
	result := AppendImagesToLastHumanMessage(mcList, images)

	// Should append to the LAST human message (index 2)
	assert.Len(t, result[0].Parts, 1) // first human unchanged
	assert.Len(t, result[2].Parts, 2) // last human has image appended
}

// --- Phase 6: Image Description Tests ---

func TestImageDescriptionPrompt_NotEmpty(t *testing.T) {
	assert.NotEmpty(t, imageDescriptionPrompt)
	assert.Contains(t, imageDescriptionPrompt, "image description")
}

func TestImageDescriptionPrompt_HasGuidance(t *testing.T) {
	// Prompt should guide the LLM to produce concise, technical descriptions
	assert.Contains(t, imageDescriptionPrompt, "concise")
	assert.Contains(t, imageDescriptionPrompt, "200 characters")
}

func TestContentHashMatching_Base64(t *testing.T) {
	// Verify that the hash matching logic in GenerateImageDescriptionsAsync
	// correctly pairs request images with stored attachments
	raw := pngBase64()
	hash := computeContentHash([]byte(raw))
	assert.NotEmpty(t, hash)

	// Same data should produce the same hash
	hash2 := computeContentHash([]byte(raw))
	assert.Equal(t, hash, hash2)

	// Different data should produce a different hash
	hash3 := computeContentHash([]byte(jpegBase64()))
	assert.NotEqual(t, hash, hash3)
}

func TestContentHashMatching_URL(t *testing.T) {
	url := "https://example.com/screenshot.png"
	hash := computeContentHash([]byte(url))
	assert.NotEmpty(t, hash)

	// Same URL should match
	hash2 := computeContentHash([]byte(url))
	assert.Equal(t, hash, hash2)
}

func TestContentHashMatching_DataURIStripped(t *testing.T) {
	// When SaveAttachments stores data, it strips the data URI prefix first.
	// The description generator must also strip the prefix before hashing.
	raw := pngBase64()
	dataURI := "data:image/png;base64," + raw
	stripped := stripDataURIPrefix(dataURI)

	hashFromStripped := computeContentHash([]byte(stripped))
	hashFromRaw := computeContentHash([]byte(raw))
	assert.Equal(t, hashFromStripped, hashFromRaw)
}

func TestGenerateImageDescriptionsAsync_NoImages(t *testing.T) {
	// Should be a no-op when there are no images
	request := NBAgentRequest{
		Images: nil,
	}
	// Should not panic
	GenerateImageDescriptionsAsync(nil, request)
}

func TestGenerateImageDescriptionsAsync_NoDAO(t *testing.T) {
	// Should be a no-op when DAO is nil
	old := attachmentDAO
	attachmentDAO = nil
	defer func() { attachmentDAO = old }()

	request := NBAgentRequest{
		Images: []ImageAttachment{{Data: pngBase64(), MIMEType: "image/png"}},
	}
	// Should not panic even with nil DAO
	GenerateImageDescriptionsAsync(nil, request)
}

// --- parseImageContextResponse ---

func TestParseImageContextResponse_TaggedFormat(t *testing.T) {
	content := "<summary>\nPod foo-bar is CrashLoopBackOff.\n</summary>\n" +
		"<descriptions>\n1. Screenshot of pod status showing CrashLoopBackOff.\n" +
		"2. Grafana graph showing a CPU spike at 14:02.\n</descriptions>"

	summary, descs := parseImageContextResponse(content)
	assert.Equal(t, "Pod foo-bar is CrashLoopBackOff.", summary)
	assert.Equal(t, []string{
		"Screenshot of pod status showing CrashLoopBackOff.",
		"Grafana graph showing a CPU spike at 14:02.",
	}, descs)
}

func TestParseImageContextResponse_NoTags_FallsBackToWholeContent(t *testing.T) {
	content := "  Just a plain untagged response.  "
	summary, descs := parseImageContextResponse(content)
	assert.Equal(t, "Just a plain untagged response.", summary)
	assert.Nil(t, descs)
}

func TestParseImageContextResponse_SummaryOnly_NoDescriptions(t *testing.T) {
	content := "<summary>Only a summary here.</summary>"
	summary, descs := parseImageContextResponse(content)
	assert.Equal(t, "Only a summary here.", summary)
	assert.Nil(t, descs)
}

func TestParseImageContextResponse_CaseInsensitiveTags(t *testing.T) {
	content := "<SUMMARY>Upper case tags.</SUMMARY>\n<Descriptions>\n1. one image.\n</Descriptions>"
	summary, descs := parseImageContextResponse(content)
	assert.Equal(t, "Upper case tags.", summary)
	assert.Equal(t, []string{"one image."}, descs)
}

// --- contentHashForImage ---

func TestContentHashForImage_MatchesComputeContentHash(t *testing.T) {
	raw := pngBase64()
	img := ImageAttachment{Data: raw}
	assert.Equal(t, computeContentHash([]byte(raw)), contentHashForImage(img))

	urlImg := ImageAttachment{URL: "https://example.com/img.png"}
	assert.Equal(t, computeContentHash([]byte("https://example.com/img.png")), contentHashForImage(urlImg))

	assert.Equal(t, "", contentHashForImage(ImageAttachment{}))
}

// --- saveImageDescriptions ---

// mockAttachmentDAO is a minimal hand-written IAttachmentDAO for testing
// saveImageDescriptions without a real database.
type mockAttachmentDAO struct {
	attachments []ConversationAttachment
	updated     map[string]string
	loadErr     error
	updateErr   error
}

func (m *mockAttachmentDAO) SaveAttachments(messageID, conversationID, accountID string, images []ImageAttachment) ([]uuid.UUID, error) {
	return nil, nil
}
func (m *mockAttachmentDAO) LoadAttachments(messageID, accountID string) ([]ConversationAttachment, error) {
	return m.attachments, m.loadErr
}
func (m *mockAttachmentDAO) LoadAttachmentDescriptions(messageIDs []string, accountID string) (map[string][]AttachmentDescription, error) {
	return nil, nil
}
func (m *mockAttachmentDAO) UpdateAttachmentDescription(attachmentID, accountID, description string) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	if m.updated == nil {
		m.updated = make(map[string]string)
	}
	m.updated[attachmentID] = description
	return nil
}
func (m *mockAttachmentDAO) PurgeExpiredAttachments(retentionDays int) (int64, error) {
	return 0, nil
}

func testRequestContext() *security.RequestContext {
	return security.NewRequestContext(context.Background(), nil, slog.Default(), nil, nil)
}

func TestSaveImageDescriptions_WritesMatchingAttachmentsByContentHash(t *testing.T) {
	old := attachmentDAO
	defer func() { attachmentDAO = old }()

	img1 := ImageAttachment{Data: pngBase64(), MIMEType: "image/png"}
	img2 := ImageAttachment{URL: "https://example.com/img.png", MIMEType: "image/png"}
	att1ID, att2ID := uuid.New(), uuid.New()

	mock := &mockAttachmentDAO{
		attachments: []ConversationAttachment{
			{ID: att1ID, ContentHash: contentHashForImage(img1)},
			{ID: att2ID, ContentHash: contentHashForImage(img2)},
		},
	}
	attachmentDAO = mock

	request := NBAgentRequest{MessageId: "msg-1", AccountId: "acc-1", Images: []ImageAttachment{img1, img2}}
	saveImageDescriptions(testRequestContext(), request, []string{"first image desc", "second image desc"})

	require.Len(t, mock.updated, 2)
	assert.Equal(t, "first image desc", mock.updated[att1ID.String()])
	assert.Equal(t, "second image desc", mock.updated[att2ID.String()])
}

func TestSaveImageDescriptions_CountMismatch_NoWrites(t *testing.T) {
	old := attachmentDAO
	defer func() { attachmentDAO = old }()

	img1 := ImageAttachment{Data: pngBase64(), MIMEType: "image/png"}
	mock := &mockAttachmentDAO{
		attachments: []ConversationAttachment{{ID: uuid.New(), ContentHash: contentHashForImage(img1)}},
	}
	attachmentDAO = mock

	request := NBAgentRequest{MessageId: "msg-1", AccountId: "acc-1", Images: []ImageAttachment{img1}}
	// Two descriptions for one image — mismatch, must defer to async fallback.
	saveImageDescriptions(testRequestContext(), request, []string{"a", "b"})

	assert.Empty(t, mock.updated)
}

func TestSaveImageDescriptions_SkipsAttachmentThatAlreadyHasDescription(t *testing.T) {
	old := attachmentDAO
	defer func() { attachmentDAO = old }()

	img1 := ImageAttachment{Data: pngBase64(), MIMEType: "image/png"}
	existing := "already described"
	mock := &mockAttachmentDAO{
		attachments: []ConversationAttachment{
			{ID: uuid.New(), ContentHash: contentHashForImage(img1), Description: &existing},
		},
	}
	attachmentDAO = mock

	request := NBAgentRequest{MessageId: "msg-1", AccountId: "acc-1", Images: []ImageAttachment{img1}}
	saveImageDescriptions(testRequestContext(), request, []string{"new description"})

	assert.Empty(t, mock.updated, "must not overwrite an existing description")
}
