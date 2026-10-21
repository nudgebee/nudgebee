package openai

import (
	"github.com/tmc/langchaingo/llms"
)

// Internal thinking-control keys llm-server sets on llms.CallOptions.Metadata via
// WithThinkingBudget / WithThinkingLevel. The native googleai adapter consumes these and
// translates them to genai.ThinkingConfig, so they never hit the wire there. Keep in sync with
// WithThinkingBudget/WithThinkingLevel in llm_common.go.
const (
	metaKeyThinkingBudget = "ThinkingBudget"
	metaKeyThinkingLevel  = "ThinkingLevel"
)

// The upstream langchaingo openai client only strips metadata keys prefixed "openai:", so
// ThinkingBudget/ThinkingLevel would serialize verbatim into the request's `metadata` field (as a
// raw int / string). The OpenAI spec defines metadata as string→string, so strict providers —
// notably Vertex AI's OpenAI-compatible endpoint reached through the NB AI Gateway — reject the
// whole request with "Expected a string key-value pair for 'metadata'". GenerateContent therefore
// always strips them.
//
// OpenAI has no numeric thinking-budget parameter (only qualitative reasoning_effort, which this
// langchaingo version exposes no public setter for), so there is nothing to translate these into
// on this path — we simply strip them. The thinking budget therefore does not apply over an
// OpenAI-compatible provider (same as the huggingface adapter, which never forwards Metadata),
// which is the honest behavior until a normalized budget is threaded through the gateway.

// stripThinkingMetadataOption runs AFTER the caller's options (which populate Metadata) and
// replaces the map with a copy that omits the internal keys, so they never reach the wire. It builds a fresh map rather than deleting in place: the caller's Metadata map may be
// shared/reused across calls (e.g. supplied via llms.WithMetadata), and an in-place delete would
// mutate that shared map — a side effect, and a fatal concurrent-map access if another goroutine
// touches it.
func stripThinkingMetadataOption(o *llms.CallOptions) {
	if o.Metadata == nil {
		return
	}
	_, hasBudget := o.Metadata[metaKeyThinkingBudget]
	_, hasLevel := o.Metadata[metaKeyThinkingLevel]
	if !hasBudget && !hasLevel {
		return // nothing to strip — skip the copy on the common path
	}
	numStripped := 0
	if hasBudget {
		numStripped++
	}
	if hasLevel {
		numStripped++
	}
	// If the internal keys were the only entries, drop the map — no allocation, and no empty
	// `metadata: {}` on the wire.
	if len(o.Metadata) == numStripped {
		o.Metadata = nil
		return
	}
	cleaned := make(map[string]any, len(o.Metadata)-numStripped)
	for k, v := range o.Metadata {
		if k == metaKeyThinkingBudget || k == metaKeyThinkingLevel {
			continue
		}
		cleaned[k] = v
	}
	o.Metadata = cleaned
}
