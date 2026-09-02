package core

import (
	"context"

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

// stripInternalThinkingMetadata wraps an OpenAI-compatible llms.Model to remove the internal
// thinking keys from CallOptions.Metadata before delegating.
//
// The upstream langchaingo openai client only strips metadata keys prefixed "openai:", so on the
// openai/custom path ThinkingBudget/ThinkingLevel would serialize verbatim into the request's
// `metadata` field (as a raw int / string). The OpenAI spec defines metadata as string→string,
// so strict providers — notably Vertex AI's OpenAI-compatible endpoint reached through the NB AI
// Gateway — reject the whole request with "Expected a string key-value pair for 'metadata'".
//
// OpenAI has no numeric thinking-budget parameter (only qualitative reasoning_effort, which this
// langchaingo version exposes no public setter for), so there is nothing to translate these into
// on this path — we simply strip them. The thinking budget therefore does not apply over an
// OpenAI-compatible provider (same as the huggingface adapter, which never forwards Metadata),
// which is the honest behavior until a normalized budget is threaded through the gateway.
type stripInternalThinkingMetadata struct {
	inner llms.Model
}

// wrapStripInternalThinkingMetadata decorates an OpenAI-compatible model; nil passes through.
func wrapStripInternalThinkingMetadata(model llms.Model) llms.Model {
	if model == nil {
		return model
	}
	return &stripInternalThinkingMetadata{inner: model}
}

func (w *stripInternalThinkingMetadata) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	// No options → no Metadata was set, so there is nothing to strip; skip the slice work.
	if len(options) == 0 {
		return w.inner.GenerateContent(ctx, messages)
	}
	// Copy before appending: append(options, …) can write into the caller's slice backing array
	// when it has spare capacity, clobbering a slice the caller reuses (and racing if shared).
	opts := make([]llms.CallOption, len(options), len(options)+1)
	copy(opts, options)
	opts = append(opts, stripThinkingMetadataOption)
	return w.inner.GenerateContent(ctx, messages, opts...)
}

func (w *stripInternalThinkingMetadata) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	// Route through GenerateContent so Call is instrumented identically (mirrors egressfilter).
	return llms.GenerateFromSinglePrompt(ctx, w, prompt, options...)
}

// stripThinkingMetadataOption runs AFTER the caller's options (which populate Metadata) and
// replaces the map with a copy that omits the internal keys, so they never reach the inner
// client. It builds a fresh map rather than deleting in place: the caller's Metadata map may be
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
