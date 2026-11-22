package runtime

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/config"
	"regexp"
	"strings"
	"sync"

	"github.com/tmc/langchaingo/llms"
)

// defaultNonVisionPatterns lists regex patterns that match models known NOT to
// support vision/multimodal input. Patterns are anchored against word/segment
// boundaries so future variants that merely share a substring don't get
// silently downgraded — and so that we don't blanket-reject an entire vendor
// (e.g. "cohere") when only specific model families are non-vision.
//
// A model matching none of these is NOT assumed vision-capable — see
// IsVisionCapableModel for the default-deny fallback.
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
// Resolution order:
//  1. "llm_server_non_vision_models" config override (comma-separated regex
//     patterns, deny-only) — falls back to the built-in defaultNonVisionPatterns
//     deny-list when unset. Anchored regexes avoid substring-match footguns.
//     Checked first so it can deny a model even if the DB says otherwise
//     (e.g. an emergency override without touching data).
//  2. An explicit verdict from llm_model_pricing.supports_image_input, when
//     recorded — this is the authoritative, DB-backed source of truth. The
//     account's own tenant row is consulted first, then the built-in row.
//  3. Default-deny. Without a positive DB record, a model is NOT treated as
//     vision-capable — absence of evidence in the deny-list is not evidence
//     of vision support. Callers that want a model to drive image-based
//     investigation must record supports_image_input = true for it.
func IsVisionCapableModel(accountId, provider, model string) bool {
	return isVisionCapable(TenantForPricing(accountId), provider, model)
}

// isVisionCapable is IsVisionCapableModel with the tenant already resolved.
func isVisionCapable(tenantId, provider, model string) bool {
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

	if tenantId != "" {
		if supported, known := imageSupportForTenant(tenantId).lookup(provider, model); known {
			return supported
		}
	}
	if supported, known := imageSupport.lookup(provider, model); known {
		return supported
	}

	return false
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
