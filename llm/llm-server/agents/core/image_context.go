package core

import (
	"context"
	"regexp"
	"strings"
	"time"

	"nudgebee/llm/attachments"
	"nudgebee/llm/config"
	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/security"

	"github.com/tmc/langchaingo/llms"
)

// --- Phase 3: Vision Detection & Graceful Degradation ---

// --- Phase 4: Image → LLM Content Part Conversion ---

// resolveVisionCapableTier decides whether attached images can be sent to an
// LLM at all, and if so, whether the lite/summary tier should handle it.
// Prefers the lite model (cheaper/faster) when it's vision-capable; falls
// back to the main model. ok is false if neither model supports vision —
// callers should skip the vision call entirely in that case.
func resolveVisionCapableTier(accountId, provider, model string) (useLite bool, ok bool) {
	if liteModel := config.Config.LlmModelLite; liteModel != "" && llmruntime.IsVisionCapableModel(accountId, provider, liteModel) {
		return true, true
	}
	return false, llmruntime.IsVisionCapableModel(accountId, provider, model)
}

// resolveEffectiveVisionProvider returns the provider/model that will actually
// generate this request's response — honoring any per-request or
// conversation-level override via ResolveLLMConfig — falling back to the raw
// global default only if resolution errors. The vision-capability gate must
// check this, not config.Config.LlmProvider/LlmModel directly: a conversation
// can be pinned to an explicitly-chosen vision-capable model while the
// process-wide default is a different (non-vision) fallback model, and
// checking the global default alone wrongly blocks image attachments in that
// case even though the model that will actually handle the request supports
// them.
func resolveEffectiveVisionProvider(ctx *security.RequestContext, accountId, agentName, conversationId string) (provider, model string) {
	if res, err := llmruntime.ResolveLLMConfig(ctx, accountId, agentName, conversationId); err == nil {
		return res.Provider, res.Model
	}
	return config.Config.LlmProvider, config.Config.LlmModel
}

// --- Image Context Extraction (Pre-Planner) ---

const imageContextExtractionPrompt = `You are a visual analysis assistant. Describe exactly what is shown in the
attached image(s) — this is the ONLY description of these images the rest of the system will ever see, so it
must stand on its own. Never respond with just a category label (e.g. "a non-technical image") without also
saying what the image actually contains.

Always transcribe verbatim any text visible in the image (titles, questions, labels, captions, error messages) —
do not paraphrase or summarize away specific wording, numbers, or names.

If the image is relevant to DevOps/SRE troubleshooting, also extract technical details:
- Service/application names, namespaces, pod names, container names
- Error messages, error codes, exception types, stack traces
- Metric values, thresholds, anomalies (CPU, memory, latency, error rates)
- Status indicators (healthy/unhealthy, running/crashed, open/resolved)
- Timestamps, durations, time ranges shown
- Dashboard names, alert names, severity levels
- Any other technical identifiers visible (IPs, ports, URLs, cluster names)

If the image is NOT technical (e.g. a riddle, diagram, photo, or other non-DevOps content), describe its actual
content faithfully instead — what it shows, and any text it contains — rather than only noting that it is
non-technical.

Be factual. No speculation. No recommendations. If multiple items are shown within one image (e.g. a list of
alerts, or several questions), enumerate each one.

Respond in exactly this format, with no other text before or after:

<summary>
{a factual summary of everything shown across every image — technical details and/or verbatim text/content}
</summary>
<descriptions>
1. {a 1-2 sentence description of image 1: what it shows, including any visible text verbatim}
2. {a 1-2 sentence description of image 2, if present}
</descriptions>

Include exactly one numbered line per attached image, in the order the images were attached.`

// ExtractImageContext performs a synchronous vision LLM call to extract actionable text
// from attached images. The extracted context is appended to the user's query so the
// planner can generate a relevant investigation plan.
// Returns the enriched query, or the original query if extraction fails.
func ExtractImageContext(ctx *security.RequestContext, request NBAgentRequest) string {
	if len(request.Images) == 0 {
		return request.Query
	}

	provider, model := resolveEffectiveVisionProvider(ctx, request.AccountId, "", request.ConversationId)

	useLite, ok := resolveVisionCapableTier(request.AccountId, provider, model)
	if !ok {
		ctx.GetLogger().Debug("image: skipping context extraction, model not vision-capable",
			"provider", provider, "model", model)
		return request.Query
	}

	// Build multimodal message with all images + the user query
	parts := make([]llms.ContentPart, 0, len(request.Images)+1)
	for _, img := range request.Images {
		parts = append(parts, attachments.ImageAttachmentToContentPart(img))
	}
	parts = append(parts, llms.TextContent{Text: "Extract all technical details from the attached image(s)."})

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, imageContextExtractionPrompt),
		{Role: llms.ChatMessageTypeHuman, Parts: parts},
	}

	baseCtx := ctx.GetContext()
	if useLite {
		baseCtx = context.WithValue(baseCtx, llmruntime.ContextKeyModelTier, llmruntime.ModelTierSummary)
	}
	extractCtx := security.NewRequestContext(
		baseCtx,
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	completion, err := llmruntime.GenerateAndTrackLLMContent(
		extractCtx, request.UserId, request.AccountId, request.ConversationId, request.MessageId,
		"image_context_extraction", false, messages, true,
		llmruntime.WithThinkingLevel(llmruntime.ThinkingLevelFastTask),
	)
	if err != nil {
		ctx.GetLogger().Warn("image: context extraction failed, using original query", "error", err)
		return request.Query
	}

	if completion == nil || len(completion.Choices) == 0 || completion.Choices[0].Content == "" {
		return request.Query
	}

	summary, perImageDescriptions := parseImageContextResponse(completion.Choices[0].Content)

	extracted := summary
	if len(extracted) > 1000 {
		extracted = TruncateHead(extracted, 997) + "..."
	}

	ctx.GetLogger().Info("image: extracted context from image(s)",
		"extracted_length", len(extracted))

	// Write per-image descriptions directly from this same call, one LLM
	// call covering both query enrichment and per-attachment descriptions.
	// GenerateImageDescriptionsAsync (per-image, separate LLM calls) remains
	// as the fallback for attachments left without a description here — e.g.
	// this call failed, or the model didn't follow the numbered format.
	saveImageDescriptions(ctx, request, perImageDescriptions)

	return request.Query + "\n\n[Attached image context: " + extracted + "]"
}

// summaryTagRe / descriptionsTagRe extract the two sections of the structured
// response requested by imageContextExtractionPrompt. numberedLineRe splits
// the descriptions section into one entry per numbered line.
var (
	summaryTagRe      = regexp.MustCompile(`(?is)<summary>\s*(.*?)\s*</summary>`)
	descriptionsTagRe = regexp.MustCompile(`(?is)<descriptions>\s*(.*?)\s*</descriptions>`)
	numberedLineRe    = regexp.MustCompile(`(?m)^\s*\d+\.\s*(.+?)\s*$`)
)

// parseImageContextResponse splits a vision completion into the combined
// summary (for query enrichment) and the per-image descriptions (for
// attachment records), per the format requested by imageContextExtractionPrompt.
// Falls back to treating the whole response as the summary if the model
// didn't follow the tagged format — callers must tolerate a nil/short
// perImageDescriptions slice (the async description path picks up the rest).
func parseImageContextResponse(content string) (summary string, perImageDescriptions []string) {
	content = strings.TrimSpace(content)
	summary = content
	if m := summaryTagRe.FindStringSubmatch(content); m != nil {
		summary = strings.TrimSpace(m[1])
	}

	if m := descriptionsTagRe.FindStringSubmatch(content); m != nil {
		for _, line := range numberedLineRe.FindAllStringSubmatch(m[1], -1) {
			perImageDescriptions = append(perImageDescriptions, strings.TrimSpace(line[1]))
		}
	}
	return summary, perImageDescriptions
}

// saveImageDescriptions writes one description per request image to its
// matching attachment record, when the count lines up 1:1 with request.Images
// (same order they were sent to the LLM in). Attachments that already have a
// description, or that have no corresponding parsed description, are left
// alone — GenerateImageDescriptionsAsync fills those in later.
func saveImageDescriptions(ctx *security.RequestContext, request NBAgentRequest, descriptions []string) {
	if len(descriptions) != len(request.Images) {
		if len(descriptions) > 0 {
			ctx.GetLogger().Warn("image: per-image description count mismatch, deferring to async fallback",
				"images", len(request.Images), "descriptions", len(descriptions))
		}
		return
	}

	dao := attachments.GetAttachmentDAO()
	if dao == nil {
		return
	}
	saved, err := dao.LoadAttachments(request.MessageId, request.AccountId)
	if err != nil {
		ctx.GetLogger().Warn("image: failed to load attachments for description write", "error", err)
		return
	}

	for i, img := range request.Images {
		hash := attachments.ContentHashForImage(img)
		if hash == "" {
			continue
		}
		desc := descriptions[i]
		if len(desc) > 500 {
			desc = TruncateHead(desc, 497) + "..."
		}
		for _, att := range saved {
			if att.ContentHash != hash || (att.Description != nil && *att.Description != "") {
				continue
			}
			if updateErr := dao.UpdateAttachmentDescription(att.ID.String(), request.AccountId, desc); updateErr != nil {
				ctx.GetLogger().Warn("image: failed to save description", "attachment_id", att.ID, "error", updateErr)
			}
			break
		}
	}
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
func generateImageDescription(ctx *security.RequestContext, accountId, conversationId, messageId, userId string, img attachments.ImageAttachment) string {
	provider, model := resolveEffectiveVisionProvider(ctx, accountId, "", conversationId)

	useLite, ok := resolveVisionCapableTier(accountId, provider, model)
	if !ok {
		ctx.GetLogger().Debug("image: skipping description generation, no vision-capable model available")
		return ""
	}

	parts := []llms.ContentPart{
		attachments.ImageAttachmentToContentPart(img),
		llms.TextContent{Text: "Describe this image concisely."},
	}

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, imageDescriptionPrompt),
		{Role: llms.ChatMessageTypeHuman, Parts: parts},
	}

	baseCtx := ctx.GetContext()
	if useLite {
		baseCtx = context.WithValue(baseCtx, llmruntime.ContextKeyModelTier, llmruntime.ModelTierSummary)
	}
	descCtx := security.NewRequestContext(
		baseCtx,
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	completion, err := llmruntime.GenerateAndTrackLLMContent(
		descCtx, userId, accountId, conversationId, messageId,
		"image_description", false, messages, true,
		llmruntime.WithThinkingLevel(llmruntime.ThinkingLevelFastTask),
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
		desc = TruncateHead(desc, 497) + "..."
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

	dao := attachments.GetAttachmentDAO()
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
		saved, err := dao.LoadAttachments(request.MessageId, request.AccountId)
		if err != nil {
			bgCtx.GetLogger().Warn("image: failed to load attachments for description generation",
				"message_id", request.MessageId, "error", err)
			return
		}

		for _, att := range saved {
			// Skip if description already exists
			if att.Description != nil && *att.Description != "" {
				continue
			}

			// Find the matching ImageAttachment from the request to pass to the LLM
			var matchedImg *attachments.ImageAttachment
			for i, img := range request.Images {
				if hash := attachments.ContentHashForImage(img); hash != "" && hash == att.ContentHash {
					matchedImg = &request.Images[i]
					break
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
