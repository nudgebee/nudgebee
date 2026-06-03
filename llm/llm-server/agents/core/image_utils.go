package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/tmc/langchaingo/llms"
)

const (
	defaultImageMaxPerMessage = 4
	defaultImageMaxSizeMB     = 1.5
)

var allowedImageMIMETypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
}

// IsImageSupportEnabled returns whether image attachment support is enabled.
func IsImageSupportEnabled() bool {
	return config.Config.LlmServerImageSupportEnabled
}

// GetImageMaxPerMessage returns the maximum number of images allowed per message.
func GetImageMaxPerMessage() int {
	return config.Config.GetInt("llm_server_image_max_per_message", defaultImageMaxPerMessage)
}

// GetImageMaxSizeMB returns the maximum allowed image size in megabytes.
func GetImageMaxSizeMB() float64 {
	return config.Config.GetFloat64("llm_server_image_max_size_mb", defaultImageMaxSizeMB)
}

// GetAllowedImageMIMETypes returns the sorted list of accepted image MIME types.
// Exposed so callers (e.g. the ai_list_models capability response) can advertise
// the allowlist to clients without duplicating it.
func GetAllowedImageMIMETypes() []string {
	types := make([]string, 0, len(allowedImageMIMETypes))
	for t := range allowedImageMIMETypes {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

// ValidateImages checks that the given images conform to all constraints:
// count limit, mutual exclusivity of data/url, MIME allowlist, size limit, and SSRF safety.
// Returns nil if all images are valid, or an error describing the first violation.
//
// Side effect: ValidateImages mutates the input slice. For data-only inputs it sets
// MIMEType from sniffed bytes. For URL inputs it fetches the image, replaces URL with
// inline base64 data, and sets MIMEType from the response. Callers that need the
// original payload preserved must pass a copy.
//
// URL inputs are fetched concurrently with a bounded per-image timeout to limit
// the total wall-clock cost on the request hot path.
func ValidateImages(ctx context.Context, images []ImageAttachment) error {
	if len(images) == 0 {
		return nil
	}

	maxCount := GetImageMaxPerMessage()
	if maxCount > 0 && len(images) > maxCount {
		return fmt.Errorf("too many images: %d provided, maximum is %d", len(images), maxCount)
	}

	maxSizeBytes := int(GetImageMaxSizeMB() * 1024 * 1024)

	// First pass: validate data-only images and check exclusivity. Cheap, sequential.
	urlIndices := make([]int, 0)
	for i := range images {
		img := &images[i]
		hasData := img.Data != ""
		hasURL := img.URL != ""

		if hasData == hasURL {
			return fmt.Errorf("image[%d]: exactly one of data or url must be set", i)
		}

		if hasData {
			raw := stripDataURIPrefix(img.Data)
			decoded, err := decodeBase64(raw)
			if err != nil {
				return fmt.Errorf("image[%d]: invalid base64 data: %w", i, err)
			}
			if len(decoded) > maxSizeBytes {
				return fmt.Errorf("image[%d]: decoded size %d bytes exceeds maximum %d bytes", i, len(decoded), maxSizeBytes)
			}

			detected := detectMIMEType(decoded)
			if img.MIMEType == "" {
				img.MIMEType = detected
			} else if img.MIMEType != detected {
				return fmt.Errorf("image[%d]: declared mime_type %q does not match detected %q", i, img.MIMEType, detected)
			}

			if !allowedImageMIMETypes[img.MIMEType] {
				return fmt.Errorf("image[%d]: unsupported mime_type %q, allowed: image/png, image/jpeg", i, img.MIMEType)
			}
		} else {
			urlIndices = append(urlIndices, i)
		}
	}

	// Second pass: fetch URL images concurrently. Each fetch is bounded by
	// urlFetchTimeout so the request goroutine is never blocked beyond that
	// regardless of how many images are submitted.
	if len(urlIndices) > 0 {
		var wg sync.WaitGroup
		errs := make([]error, len(urlIndices))
		for k, idx := range urlIndices {
			wg.Add(1)
			go func(slot int, imgIdx int) {
				defer wg.Done()
				img := &images[imgIdx]
				mimeType, data, err := common.FetchImageSafely(ctx, img.URL, common.SafeFetchOptions{
					MaxSizeBytes:        int64(maxSizeBytes),
					Timeout:             urlFetchTimeout,
					AllowedMIMEPrefixes: []string{"image/"},
				})
				if err != nil {
					errs[slot] = fmt.Errorf("image[%d]: %w", imgIdx, err)
					return
				}
				if img.MIMEType != "" && img.MIMEType != mimeType {
					errs[slot] = fmt.Errorf("image[%d]: declared mime_type %q does not match downloaded %q", imgIdx, img.MIMEType, mimeType)
					return
				}
				if !allowedImageMIMETypes[mimeType] {
					errs[slot] = fmt.Errorf("image[%d]: unsupported mime_type %q, allowed: image/png, image/jpeg", imgIdx, mimeType)
					return
				}
				img.MIMEType = mimeType
				img.Data = base64.StdEncoding.EncodeToString(data)
				img.URL = ""
			}(k, idx)
		}
		wg.Wait()
		for _, e := range errs {
			if e != nil {
				return e
			}
		}
	}

	return nil
}

const urlFetchTimeout = 8 * time.Second

// detectMIMEType sniffs the actual MIME type from raw image bytes.
func detectMIMEType(data []byte) string {
	ct := http.DetectContentType(data)
	// DetectContentType may return params like "text/plain; charset=utf-8", strip them
	if idx := strings.Index(ct, ";"); idx != -1 {
		ct = ct[:idx]
	}
	return strings.TrimSpace(ct)
}

// decodeBase64 attempts to decode base64 data, trying standard encoding first,
// then URL-safe encoding, and finally with padding normalization.
// This handles input from frontends that may use URL-safe alphabet or omit padding.
func decodeBase64(data string) ([]byte, error) {
	// Try standard encoding first (most common)
	if decoded, err := base64.StdEncoding.DecodeString(data); err == nil {
		return decoded, nil
	}
	// Try URL-safe encoding (some frontends use - and _ instead of + and /)
	if decoded, err := base64.URLEncoding.DecodeString(data); err == nil {
		return decoded, nil
	}
	// Try without padding (common in web APIs)
	if decoded, err := base64.RawStdEncoding.DecodeString(data); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(data); err == nil {
		return decoded, nil
	}
	return nil, fmt.Errorf("data is not valid base64 in any supported encoding")
}

// stripDataURIPrefix removes a data URI prefix from base64 data.
// Handles: "data:image/png;base64,<b64>", "data:image/png;<b64>", or raw "<b64>".
func stripDataURIPrefix(data string) string {
	// data:image/png;base64,iVBOR...
	if idx := strings.Index(data, "base64,"); idx != -1 {
		return data[idx+7:]
	}
	// data:image/png;iVBOR...
	if strings.HasPrefix(data, "data:") {
		if idx := strings.Index(data, ";"); idx != -1 {
			return data[idx+1:]
		}
	}
	return data
}

// --- Phase 3: Vision Detection & Graceful Degradation ---

// defaultNonVisionPatterns lists regex patterns that match models known NOT to
// support vision/multimodal input. Patterns are anchored against word/segment
// boundaries so future variants that merely share a substring don't get
// silently downgraded — and so that we don't blanket-reject an entire vendor
// (e.g. "cohere") when only specific model families are non-vision.
//
// Default-allow: any model not matching these patterns is assumed vision-capable.
//
// Tradeoff: claude-2 is matched by prefix because every real claude-2.X model
// (claude-2.0, claude-2.1) is text-only. If Anthropic ever ships a vision
// variant in the claude-2 line, update this entry to be tighter.
var defaultNonVisionPatterns = []string{
	`(?i)(^|[^a-z0-9])gpt-3\.5([^a-z0-9]|$)`,
	`(?i)(^|[^a-z0-9])gpt-4-base([^a-z0-9]|$)`,
	`(?i)(^|[^a-z0-9])claude-2([^a-z]|$)`,
	`(?i)(^|[^a-z0-9])claude-instant([^a-z0-9]|$)`,
	`(?i)(^|[^a-z0-9])titan-text([^a-z0-9]|$)`,
	`(?i)(^|[.\-/])cohere\.command-(text|light)([^a-z0-9]|$)`,
}

type visionDenyCache struct {
	mu        sync.Mutex
	configCSV string
	regexes   []*regexp.Regexp
}

var visionDeny = &visionDenyCache{}

func compileVisionDenyPatterns(patterns []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		if strings.TrimSpace(p) == "" {
			continue
		}
		re, err := regexp.Compile(p)
		if err != nil {
			slog.Warn("image: invalid vision deny pattern, skipping", "pattern", p, "error", err)
			continue
		}
		out = append(out, re)
	}
	return out
}

// IsVisionCapableModel returns true if the given model supports multimodal (image) input.
// Uses anchored regex patterns to avoid substring-match footguns. Per-account
// overrides via config key "llm_server_non_vision_models" (comma-separated patterns).
func IsVisionCapableModel(provider, model string) bool {
	nonVisionCSV := config.Config.GetString("llm_server_non_vision_models", "")

	visionDeny.mu.Lock()
	if visionDeny.regexes == nil || nonVisionCSV != visionDeny.configCSV {
		patterns := defaultNonVisionPatterns
		if nonVisionCSV != "" {
			patterns = strings.Split(nonVisionCSV, ",")
		}
		visionDeny.regexes = compileVisionDenyPatterns(patterns)
		visionDeny.configCSV = nonVisionCSV
	}
	regexes := visionDeny.regexes
	visionDeny.mu.Unlock()

	for _, re := range regexes {
		if re.MatchString(model) {
			return false
		}
	}
	return true
}

// hasImageParts scans messages for any BinaryContent or ImageURLContent parts.
func hasImageParts(messages []llms.MessageContent) bool {
	for _, m := range messages {
		for _, part := range m.Parts {
			switch part.(type) {
			case llms.BinaryContent, llms.ImageURLContent:
				return true
			}
		}
	}
	return false
}

// stripImagePartsWithFallback removes BinaryContent and ImageURLContent parts from all messages,
// replacing them with a text note so the LLM knows images were present but cannot be processed.
func stripImagePartsWithFallback(messages []llms.MessageContent) []llms.MessageContent {
	result := make([]llms.MessageContent, 0, len(messages))
	for _, m := range messages {
		imageCount := 0
		filteredParts := make([]llms.ContentPart, 0, len(m.Parts))
		for _, part := range m.Parts {
			switch part.(type) {
			case llms.BinaryContent, llms.ImageURLContent:
				imageCount++
			default:
				filteredParts = append(filteredParts, part)
			}
		}
		if imageCount > 0 {
			fallback := fmt.Sprintf("[%d image(s) were attached but cannot be processed — the current model does not support vision input]", imageCount)
			filteredParts = append(filteredParts, llms.TextContent{Text: fallback})
		}
		result = append(result, llms.MessageContent{
			Role:  m.Role,
			Parts: filteredParts,
		})
	}
	return result
}

// --- Phase 4: Image → LLM Content Part Conversion ---

// ImageAttachmentToContentPart converts an ImageAttachment to a langchaingo ContentPart.
// For base64 data: decodes and returns BinaryContent.
// For URL: returns ImageURLContent.
// Returns a text fallback if conversion fails.
func ImageAttachmentToContentPart(img ImageAttachment) llms.ContentPart {
	if img.Data != "" {
		raw := stripDataURIPrefix(img.Data)
		decoded, err := decodeBase64(raw)
		if err != nil {
			slog.Warn("image: failed to decode base64 image data", "error", err)
			return llms.TextContent{Text: "[Image could not be decoded]"}
		}
		mimeType := img.MIMEType
		if mimeType == "" {
			mimeType = detectMIMEType(decoded)
		}
		return llms.BinaryContent{
			MIMEType: mimeType,
			Data:     decoded,
		}
	}
	if img.URL != "" {
		return llms.ImageURLContent{URL: img.URL}
	}
	return llms.TextContent{Text: "[Image could not be decoded]"}
}

// AppendImagesToLastHumanMessage appends image content parts to the last human message in the list.
// If no human message exists, a new one is added. This is the shared helper used by all planners.
func AppendImagesToLastHumanMessage(mcList []llms.MessageContent, images []ImageAttachment) []llms.MessageContent {
	if len(images) == 0 || !IsImageSupportEnabled() {
		return mcList
	}

	// Convert images to content parts
	imageParts := make([]llms.ContentPart, 0, len(images))
	for _, img := range images {
		imageParts = append(imageParts, ImageAttachmentToContentPart(img))
	}

	// Find the last human message and append image parts to it
	for i := len(mcList) - 1; i >= 0; i-- {
		if mcList[i].Role == llms.ChatMessageTypeHuman {
			mcList[i].Parts = append(mcList[i].Parts, imageParts...)
			return mcList
		}
	}

	// No human message found — create one with images
	mcList = append(mcList, llms.MessageContent{
		Role:  llms.ChatMessageTypeHuman,
		Parts: imageParts,
	})
	return mcList
}

// --- Image Context Extraction (Pre-Planner) ---

const imageContextExtractionPrompt = `You are a visual analysis assistant for a DevOps/SRE troubleshooting platform.
Extract ALL actionable technical details from the attached image(s) that would help an investigation agent.

Focus on:
- Service/application names, namespaces, pod names, container names
- Error messages, error codes, exception types, stack traces
- Metric values, thresholds, anomalies (CPU, memory, latency, error rates)
- Status indicators (healthy/unhealthy, running/crashed, open/resolved)
- Timestamps, durations, time ranges shown
- Dashboard names, alert names, severity levels
- Any other technical identifiers visible (IPs, ports, URLs, cluster names)

Output ONLY a concise factual summary of what is visible. No speculation. No recommendations.
If multiple items are shown (e.g. a list of alerts), enumerate each one.
Keep the output under 500 characters.`

// ExtractImageContext performs a synchronous vision LLM call to extract actionable text
// from attached images. The extracted context is appended to the user's query so the
// planner can generate a relevant investigation plan.
// Returns the enriched query, or the original query if extraction fails.
func ExtractImageContext(ctx *security.RequestContext, request NBAgentRequest) string {
	if len(request.Images) == 0 {
		return request.Query
	}

	provider := config.Config.LlmProvider

	// Determine effective model: prefer lite model if it supports vision
	model := config.Config.LlmModel
	useLite := false
	if liteModel := config.Config.LlmModelLite; liteModel != "" && IsVisionCapableModel(provider, liteModel) {
		model = liteModel
		useLite = true
	}

	if !IsVisionCapableModel(provider, model) {
		ctx.GetLogger().Debug("image: skipping context extraction, model not vision-capable",
			"provider", provider, "model", model)
		return request.Query
	}

	// Build multimodal message with all images + the user query
	parts := make([]llms.ContentPart, 0, len(request.Images)+1)
	for _, img := range request.Images {
		parts = append(parts, ImageAttachmentToContentPart(img))
	}
	parts = append(parts, llms.TextContent{Text: "Extract all technical details from the attached image(s)."})

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, imageContextExtractionPrompt),
		{Role: llms.ChatMessageTypeHuman, Parts: parts},
	}

	baseCtx := ctx.GetContext()
	if useLite {
		baseCtx = context.WithValue(baseCtx, ContextKeyModelTier, ModelTierSummary)
	}
	extractCtx := security.NewRequestContext(
		baseCtx,
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	completion, err := GenerateAndTrackLLMContent(
		extractCtx, request.UserId, request.AccountId, request.ConversationId, request.MessageId,
		"image_context_extraction", false, messages, true,
		WithThinkingLevel(ThinkingLevelFastTask),
	)
	if err != nil {
		ctx.GetLogger().Warn("image: context extraction failed, using original query", "error", err)
		return request.Query
	}

	if completion == nil || len(completion.Choices) == 0 || completion.Choices[0].Content == "" {
		return request.Query
	}

	extracted := strings.TrimSpace(completion.Choices[0].Content)
	if len(extracted) > 1000 {
		extracted = extracted[:997] + "..."
	}

	ctx.GetLogger().Info("image: extracted context from image(s)",
		"extracted_length", len(extracted))

	// Store the extracted context as attachment descriptions so the async
	// GenerateImageDescriptionsAsync path can skip the redundant LLM call.
	if dao := GetAttachmentDAO(); dao != nil {
		attachments, err := dao.LoadAttachments(request.MessageId, request.AccountId)
		if err == nil {
			desc := extracted
			if len(desc) > 500 {
				desc = desc[:497] + "..."
			}
			for _, att := range attachments {
				if att.Description == nil || *att.Description == "" {
					if updateErr := dao.UpdateAttachmentDescription(att.ID.String(), request.AccountId, desc); updateErr != nil {
						ctx.GetLogger().Warn("image: failed to save extracted context as description",
							"attachment_id", att.ID, "error", updateErr)
					}
				}
			}
		}
	}

	return request.Query + "\n\n[Attached image context: " + extracted + "]"
}

// --- Phase 6: Image Description Generation ---

const imageDescriptionPrompt = `You are an image description assistant for a DevOps/SRE troubleshooting platform.
Describe the attached image in 1-2 concise sentences focusing on:
- What type of image it is (screenshot, graph, diagram, log output, error message, dashboard, etc.)
- Key technical details visible (resource names, error codes, metric values, status indicators)
- Any anomalies or problems shown

Be factual and specific. Do not speculate beyond what is visible. Keep the description under 200 characters.`

// generateImageDescription uses a lightweight LLM call to describe a single image.
// Returns an empty string if the description cannot be generated or the model lacks vision.
func generateImageDescription(ctx *security.RequestContext, accountId, conversationId, messageId, userId string, img ImageAttachment) string {
	provider := config.Config.LlmProvider

	// Check that the effective model supports vision before sending images
	useLite := false
	if liteModel := config.Config.LlmModelLite; liteModel != "" && IsVisionCapableModel(provider, liteModel) {
		useLite = true
	} else if !IsVisionCapableModel(provider, config.Config.LlmModel) {
		ctx.GetLogger().Debug("image: skipping description generation, no vision-capable model available")
		return ""
	}

	parts := []llms.ContentPart{
		ImageAttachmentToContentPart(img),
		llms.TextContent{Text: "Describe this image concisely."},
	}

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, imageDescriptionPrompt),
		{Role: llms.ChatMessageTypeHuman, Parts: parts},
	}

	baseCtx := ctx.GetContext()
	if useLite {
		baseCtx = context.WithValue(baseCtx, ContextKeyModelTier, ModelTierSummary)
	}
	descCtx := security.NewRequestContext(
		baseCtx,
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	completion, err := GenerateAndTrackLLMContent(
		descCtx, userId, accountId, conversationId, messageId,
		"image_description", false, messages, true,
		WithThinkingLevel(ThinkingLevelFastTask),
	)
	if err != nil {
		ctx.GetLogger().Warn("image: failed to generate description", "error", err)
		return ""
	}

	if completion == nil || len(completion.Choices) == 0 || completion.Choices[0].Content == "" {
		return ""
	}

	desc := strings.TrimSpace(completion.Choices[0].Content)
	// Cap description length for DB storage
	if len(desc) > 500 {
		desc = desc[:497] + "..."
	}
	return desc
}

// GenerateImageDescriptionsAsync loads attachments for the current message and generates
// LLM descriptions for any that don't already have one. Runs via the conversation worker pool.
// Best-effort: failures are logged but do not affect the main response.
func GenerateImageDescriptionsAsync(ctx *security.RequestContext, request NBAgentRequest) {
	if len(request.Images) == 0 {
		return
	}

	dao := GetAttachmentDAO()
	if dao == nil {
		return
	}

	submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
	defer cancel()

	err := conversationAsyncTaskWorkerPool.Submit(submissionCtx, func() {
		// Use a bounded context for the LLM calls to prevent leaked goroutines
		taskCtx, taskCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer taskCancel()

		bgCtx := security.NewRequestContext(
			taskCtx,
			ctx.GetSecurityContext(),
			ctx.GetLogger(),
			ctx.GetTracer(),
			ctx.GetMeter(),
		)
		attachments, err := dao.LoadAttachments(request.MessageId, request.AccountId)
		if err != nil {
			bgCtx.GetLogger().Warn("image: failed to load attachments for description generation",
				"message_id", request.MessageId, "error", err)
			return
		}

		for _, att := range attachments {
			// Skip if description already exists
			if att.Description != nil && *att.Description != "" {
				continue
			}

			// Find the matching ImageAttachment from the request to pass to the LLM
			var matchedImg *ImageAttachment
			for i, img := range request.Images {
				if img.Data != "" {
					raw := stripDataURIPrefix(img.Data)
					hash := computeContentHash([]byte(raw))
					if hash == att.ContentHash {
						matchedImg = &request.Images[i]
						break
					}
				} else if img.URL != "" {
					hash := computeContentHash([]byte(img.URL))
					if hash == att.ContentHash {
						matchedImg = &request.Images[i]
						break
					}
				}
			}

			if matchedImg == nil {
				bgCtx.GetLogger().Debug("image: no matching request image for attachment",
					"attachment_id", att.ID)
				continue
			}

			desc := generateImageDescription(bgCtx, request.AccountId, request.ConversationId, request.MessageId, request.UserId, *matchedImg)
			if desc == "" {
				continue
			}

			if err := dao.UpdateAttachmentDescription(att.ID.String(), request.AccountId, desc); err != nil {
				bgCtx.GetLogger().Warn("image: failed to save description",
					"attachment_id", att.ID, "error", err)
			} else {
				bgCtx.GetLogger().Info("image: saved description",
					"attachment_id", att.ID, "description_length", len(desc))
			}
		}
	})

	if err != nil {
		ctx.GetLogger().Warn("image: failed to submit description generation task", "error", err)
	}
}
