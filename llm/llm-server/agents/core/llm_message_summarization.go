package core

import (
	"context"
	"fmt"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/tmc/langchaingo/llms"
)

type summarizationCtxKeyType struct{}

var summarizationCtxKey = summarizationCtxKeyType{}

// CalculateTotalTokens calculates the total token count across all messages
// This gives us an accurate view of the entire conversation size
func CalculateTotalTokens(
	ctx *security.RequestContext,
	messages []llms.MessageContent,
	provider string,
	model string,
) (int, error) {
	totalTokens := 0
	tokensPerMessage := 4 // Overhead per message (role, formatting, etc.)

	for _, message := range messages {
		// Add per-message overhead
		totalTokens += tokensPerMessage

		// Count tokens in role
		roleTokens, err := CountTokens(provider, model, string(message.Role))
		if err != nil {
			ctx.GetLogger().Warn("Failed to count tokens for message role, using estimation",
				"role", message.Role,
				"error", err)
			roleTokens = len(string(message.Role)) / 4 // Rough estimation
		}
		totalTokens += roleTokens

		// Count tokens in each part
		for _, part := range message.Parts {
			if textPart, ok := part.(llms.TextContent); ok {
				contentTokens, err := CountTokens(provider, model, textPart.Text)
				if err != nil {
					ctx.GetLogger().Warn("Failed to count tokens for message content, using estimation",
						"contentLength", len(textPart.Text),
						"error", err)
					contentTokens = len(textPart.Text) / 4 // Rough estimation: ~4 chars per token
				}
				totalTokens += contentTokens
			}
		}
	}

	// Add final overhead for conversation structure
	totalTokens += 3

	ctx.GetLogger().Debug("Total token calculation",
		"totalTokens", totalTokens,
		"messageCount", len(messages),
		"provider", provider,
		"model", model)

	return totalTokens, nil
}

func SummarizeTextChunk(ctx *security.RequestContext, llm llms.Model, content string, accountId string, agentId string, conversationId string, messageId string, userId string) string {
	prompt := fmt.Sprintf("Please summarize the following content. The summary should be concise and capture the main points. Do not lose any important information, especially IDs, names, and commands.\n\nContent to summarize:\n<content>\n%s\n</content>", content)

	// Add flag to context to prevent recursive summarization
	summarizationCtx := context.WithValue(ctx.GetContext(), summarizationCtxKey, true)
	secCtx := security.NewRequestContext(
		summarizationCtx,
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	// Direct call to the LLM for summarization
	completion, err := GenerateAndTrackLLMContent(secCtx, userId, accountId, conversationId, messageId, agentId, false, []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, prompt),
	}, false, WithThinkingLevel(ThinkingLevelFastTask))

	if err != nil {
		ctx.GetLogger().Warn("messagesummary: Failed to summarize content chunk", "error", err)
		return "" // Return empty string on failure
	}

	if len(completion.Choices) > 0 {
		return completion.Choices[0].Content
	}

	return ""
}

func splitTextIntoChunks(text, provider, model string, chunkSize int) ([]string, error) {
	var chunks []string
	var currentChunk strings.Builder
	currentChunkTokens := 0

	lines := strings.Split(text, "\n")

	for _, line := range lines {
		lineTokens, err := CountTokens(provider, model, line+"\n")
		if err != nil {
			return nil, err
		}

		if lineTokens > chunkSize {
			if currentChunk.Len() > 0 {
				chunks = append(chunks, currentChunk.String())
				currentChunk.Reset()
				currentChunkTokens = 0
			}
			// Split the long line by words
			words := strings.Fields(line)
			var wordChunkBuilder strings.Builder
			var wordChunkTokens int
			for _, word := range words {
				wordWithSpace := word + " "
				wordTokens, _ := CountTokens(provider, model, wordWithSpace)
				if wordChunkTokens+wordTokens > chunkSize {
					chunks = append(chunks, wordChunkBuilder.String())
					wordChunkBuilder.Reset()
					wordChunkTokens = 0
				}
				wordChunkBuilder.WriteString(wordWithSpace)
				wordChunkTokens += wordTokens
			}
			if wordChunkBuilder.Len() > 0 {
				chunks = append(chunks, wordChunkBuilder.String())
			}
			continue
		}

		if currentChunkTokens+lineTokens > chunkSize {
			chunks = append(chunks, currentChunk.String())
			currentChunk.Reset()
			currentChunk.WriteString(line + "\n")
			currentChunkTokens = lineTokens
		} else {
			currentChunk.WriteString(line + "\n")
			currentChunkTokens += lineTokens
		}
	}

	if currentChunk.Len() > 0 {
		chunks = append(chunks, currentChunk.String())
	}

	return chunks, nil
}

// SummarizeContent creates a summary of a string content using a map-reduce approach if necessary.
func SummarizeContent(ctx *security.RequestContext, llm llms.Model, content string, accountId string, agentId string, conversationId string, messageId string, userId string) string {
	provider := GetLLMProvider(ctx, accountId, agentId, true, conversationId)
	modelName := GetLLMModelName(ctx, accountId, provider, agentId, true, conversationId)

	// Use the model's max token length as the upper bound.
	maxTokens := GetLlmMaxTokenLength(modelName)

	// Use a safe chunk size, leaving room for prompt and response.
	chunkSize := maxTokens / 2
	if chunkSize == 0 {
		chunkSize = 4000
	}

	tokenCount, err := CountTokens(provider, modelName, content)
	if err != nil {
		ctx.GetLogger().Warn("messagesummary: Failed to count tokens for content, attempting to summarize as a whole", "error", err)
		return SummarizeTextChunk(ctx, llm, content, accountId, agentId, conversationId, messageId, userId)
	}

	// Optimization: Skip summarization for very short messages
	minTokensForSummarizationStr := config.Config.GetString("llm_min_tokens_for_summarization", "50")
	minTokensForSummarization, err := strconv.Atoi(minTokensForSummarizationStr)
	if err != nil {
		minTokensForSummarization = 100
	}

	if tokenCount <= minTokensForSummarization {
		ctx.GetLogger().Debug("messagesummary: Skipping summarization for short content", "tokenCount", tokenCount, "minTokens", minTokensForSummarization)
		return content // Return original content as it's already short
	}

	if tokenCount <= chunkSize {
		return SummarizeTextChunk(ctx, llm, content, accountId, agentId, conversationId, messageId, userId)
	}

	ctx.GetLogger().Info("Content is too large, splitting into chunks for summarization.", "tokenCount", tokenCount, "chunkSize", chunkSize)

	chunks, err := splitTextIntoChunks(content, provider, modelName, chunkSize)
	if err != nil {
		ctx.GetLogger().Warn("messagesummary: Failed to split content into chunks, attempting to summarize as a whole", "error", err)
		return SummarizeTextChunk(ctx, llm, content, accountId, agentId, conversationId, messageId, userId)
	}

	ctx.GetLogger().Info("messagesummary: Summarizing content in chunks.", "numChunks", len(chunks))

	if config.Config.LlmSummarizationParallelEnabled {
		return SummarizeLargeMessageChunkedParallel(ctx, llm, content, provider, modelName, maxTokens, accountId, agentId, conversationId, messageId, userId)
	}

	// Fallback to sequential summarization if parallel is disabled
	var summaries []string
	for i, chunk := range chunks {
		ctx.GetLogger().Info("messagesummary: Summarizing chunk", "chunk", i+1, "totalChunks", len(chunks))
		summary := SummarizeTextChunk(ctx, llm, chunk, accountId, agentId, conversationId, messageId, userId)
		if summary != "" {
			summaries = append(summaries, summary)
		}
	}

	combinedSummary := strings.Join(summaries, "\n\n")

	// If we have multiple summaries, we summarize the result to get a final, coherent summary.
	if len(summaries) > 1 {
		ctx.GetLogger().Info("messagesummary: Combining and summarizing chunk summaries.", "numSummaries", len(summaries))
		return SummarizeContent(ctx, llm, combinedSummary, accountId, agentId, conversationId, messageId, userId)
	}

	// If there was only one summary, just return it.
	return combinedSummary
}

// calculateMessageTokens calculates token count for a single message
func calculateMessageTokens(
	ctx *security.RequestContext,
	message llms.MessageContent,
	provider string,
	model string,
) (int, error) {
	tokens := 4 // Message overhead

	// Count role tokens
	roleTokens, _ := CountTokens(provider, model, string(message.Role))
	tokens += roleTokens

	// Count content tokens
	for _, part := range message.Parts {
		if textPart, ok := part.(llms.TextContent); ok {
			contentTokens, err := CountTokens(provider, model, textPart.Text)
			if err != nil {
				return 0, err
			}
			tokens += contentTokens
		}
	}

	return tokens, nil
}

// estimateMessageTokens provides a rough token estimation when accurate counting fails
func estimateMessageTokens(message llms.MessageContent) int {
	totalChars := len(string(message.Role))
	for _, part := range message.Parts {
		if textPart, ok := part.(llms.TextContent); ok {
			totalChars += len(textPart.Text)
		}
	}
	// Rough approximation: 4 characters per token
	return (totalChars / 4) + 4
}

// preflightMaxMessageBytes is the hard cap per message applied before every LLM call.
// Messages exceeding this are truncated to prevent token-limit errors caused by upstream
// agents injecting large payloads (event logs, code files, etc.) without scratchpad limits.
// Default: 512 KB ≈ 128 000 tokens (well under Gemini's 1M-token context limit).
// Override via config.Config.LlmServerPreflightMaxMessageBytes (0 = use default, -1 = disabled).
const preflightMaxMessageBytesDefault = 512 * 1024 // 512 KB

// SmartTruncateToolOutput truncates a string by keeping the beginning and end,
// and inserting a truncation message in the middle. This is ideal for tool outputs
// like AWS logs or large JSONs where the most relevant info is often at the ends.
func SmartTruncateToolOutput(content string, maxBytes int) string {
	if len(content) <= maxBytes {
		return content
	}

	// For very small caps, just do a simple truncate
	if maxBytes < 1024 {
		suffix := "\n[...truncated...]"
		if maxBytes <= len(suffix) {
			return "" // Too small to even show truncation message
		}
		// Ensure we don't split UTF-8 runes
		idx := maxBytes - len(suffix)
		for idx > 0 && !utf8.RuneStart(content[idx]) {
			idx--
		}
		return content[:idx] + suffix
	}

	// Keep 50% at the beginning and 50% at the end (often errors/status are at the end)
	keepEach := (maxBytes - 200) / 2 // Leave room for the message
	if keepEach < 0 {
		return content[:maxBytes]
	}

	// Ensure we don't split UTF-8 runes
	beginIdx := keepEach
	for beginIdx > 0 && !utf8.RuneStart(content[beginIdx]) {
		beginIdx--
	}
	begin := content[:beginIdx]

	endIdx := len(content) - keepEach
	for endIdx < len(content) && !utf8.RuneStart(content[endIdx]) {
		endIdx++
	}
	end := content[endIdx:]

	return fmt.Sprintf("%s\n\n[... TRUNCATED %d bytes for LLM stability ... please use specific filters if more data is needed ...]\n\n%s",
		begin, len(content)-beginIdx-(len(content)-endIdx), end)
}

// applyPreflightMessageSizeCap hard-truncates any individual message that exceeds the
// configured per-message byte cap before making the first LLM call.  This is a
// defensive guard that handles all agents uniformly without requiring agent-level changes.
func applyPreflightMessageSizeCap(ctx *security.RequestContext, messages []llms.MessageContent, agentName string) []llms.MessageContent {
	maxBytes := config.Config.LlmServerPreflightMaxMessageBytes
	if maxBytes == 0 {
		maxBytes = preflightMaxMessageBytesDefault
	}
	if maxBytes < 0 {
		// Negative value → disabled.
		return messages
	}

	result := make([]llms.MessageContent, len(messages))

	for i, msg := range messages {
		// Start with a deep copy of Parts so mutations never affect the caller's slice.
		result[i] = msg
		if len(msg.Parts) == 0 {
			continue
		}
		newParts := make([]llms.ContentPart, len(msg.Parts))
		copy(newParts, msg.Parts)
		modified := false
		for j, part := range newParts {
			textPart, ok := part.(llms.TextContent)
			if !ok || len(textPart.Text) <= maxBytes {
				continue
			}

			// Use SmartTruncate for larger messages to preserve context at both ends
			truncated := SmartTruncateToolOutput(textPart.Text, maxBytes)

			ctx.GetLogger().Info("Pre-flight message size cap applied: part truncated before LLM call",
				"messageIndex", i,
				"partIndex", j,
				"messageRole", msg.Role,
				"originalBytes", len(textPart.Text),
				"capBytes", maxBytes,
				"agent", agentName,
			)
			newParts[j] = llms.TextContent{Text: truncated}
			modified = true
		}
		if modified {
			result[i] = llms.MessageContent{Role: msg.Role, Parts: newParts}
		}
	}
	return result
}

// truncateToTokenLimit truncates text to fit within token limit (emergency fallback)
func truncateToTokenLimit(text string, maxTokens int, provider string, model string) string {
	currentTokens, err := CountTokens(provider, model, text)
	if err != nil || currentTokens <= maxTokens {
		return text
	}

	// Calculate target character count (rough approximation)
	charsPerToken := float64(len(text)) / float64(currentTokens)
	targetChars := int(float64(maxTokens) * charsPerToken * 0.95) // 95% to be safe

	if targetChars >= len(text) {
		return text
	}

	if targetChars < 100 {
		targetChars = 100 // Minimum reasonable size
	}

	// Truncate and add indicator
	truncated := text[:targetChars] + "\n\n[... content truncated due to length constraints ...]"
	return truncated
}

// SummarizeLargeMessageChunked chunks a very large message and summarizes it with context preservation
// Each chunk is summarized, and subsequent chunks include previous summaries as context
// ALWAYS checks token limits before processing each chunk to prevent errors
func SummarizeLargeMessageChunked(
	ctx *security.RequestContext,
	llm llms.Model,
	content string,
	provider string,
	model string,
	maxTokens int,
	accountId string,
	agentId string,
	conversationId string,
	messageId string,
	userId string,
) string {
	ctx.GetLogger().Info("Starting chunked summarization for large message",
		"contentLength", len(content),
		"maxTokens", maxTokens)

	// Calculate safe chunk size (40% of max tokens per chunk)
	safeChunkSize := int(float64(maxTokens) * 0.4)
	// Reserve 30% for previous summaries that will be appended
	reserveForSummary := int(float64(maxTokens) * 0.3)

	ctx.GetLogger().Info("Chunk configuration",
		"safeChunkSize", safeChunkSize,
		"reserveForSummary", reserveForSummary)

	// Split content into chunks by tokens
	chunks, err := splitTextIntoChunks(content, provider, model, safeChunkSize)
	if err != nil {
		ctx.GetLogger().Error("Failed to split content into chunks", "error", err)
		// Fallback to simple summarization
		return SummarizeTextChunk(ctx, llm, content, accountId, agentId, conversationId, messageId, userId)
	}

	if len(chunks) <= 1 {
		ctx.GetLogger().Info("Content fits in single chunk, using simple summarization")
		return SummarizeTextChunk(ctx, llm, content, accountId, agentId, conversationId, messageId, userId)
	}

	ctx.GetLogger().Info("Content split into chunks for summarization",
		"numChunks", len(chunks),
		"chunkSize", safeChunkSize)

	// Track summaries from previous chunks
	previousSummaries := []string{}

	// Process each chunk
	for i, chunk := range chunks {
		ctx.GetLogger().Info("Processing chunk for summarization",
			"chunkNumber", i+1,
			"totalChunks", len(chunks),
			"previousSummaryCount", len(previousSummaries))

		// Build prompt with context from previous chunks
		var promptBuilder strings.Builder

		// Step 1: Check if we have previous summaries
		if len(previousSummaries) > 0 {
			combinedPrevSummaries := strings.Join(previousSummaries, "\n\n")

			// Step 2: Count tokens for previous summaries and current chunk
			prevSummaryTokens, _ := CountTokens(provider, model, combinedPrevSummaries)
			chunkTokens, _ := CountTokens(provider, model, chunk)

			ctx.GetLogger().Debug("Token check before processing chunk",
				"previousSummaryTokens", prevSummaryTokens,
				"chunkTokens", chunkTokens,
				"reserveForSummary", reserveForSummary,
				"total", prevSummaryTokens+chunkTokens)

			// Step 3: If previous summaries exceed reserve limit, compress them
			if prevSummaryTokens > reserveForSummary {
				ctx.GetLogger().Warn("Previous summaries exceed reserve, compressing",
					"prevSummaryTokens", prevSummaryTokens,
					"reserveForSummary", reserveForSummary)

				// Compress all previous summaries into one
				compressedSummary := CompressPreviousSummariesForMessage(
					ctx, llm, previousSummaries, reserveForSummary,
					provider, model, accountId, agentId,
					conversationId, messageId, userId,
				)
				previousSummaries = []string{compressedSummary}
				combinedPrevSummaries = compressedSummary

				// Re-check tokens after compression
				newSummaryTokens, _ := CountTokens(provider, model, compressedSummary)
				ctx.GetLogger().Info("Summaries compressed",
					"oldTokens", prevSummaryTokens,
					"newTokens", newSummaryTokens)
			}

			// Step 4: Append previous summaries to prompt
			promptBuilder.WriteString("Context from previous parts of this message:\n")
			promptBuilder.WriteString(combinedPrevSummaries)
			promptBuilder.WriteString("\n\n---\n\n")
		}

		// Step 5: Add instructions for this chunk
		promptBuilder.WriteString("Please summarize the following content")
		if len(previousSummaries) > 0 {
			promptBuilder.WriteString(" (continuing from the context above)")
		}
		promptBuilder.WriteString(". Be concise but preserve all important information, especially IDs, names, commands, and key facts.\n\n")
		promptBuilder.WriteString("Content to summarize:\n")
		promptBuilder.WriteString(chunk)

		// Step 6: Final token check before calling LLM
		finalPrompt := promptBuilder.String()
		promptTokens, _ := CountTokens(provider, model, finalPrompt)
		if promptTokens > maxTokens {
			ctx.GetLogger().Error("Prompt exceeds max tokens even after compression",
				"promptTokens", promptTokens,
				"maxTokens", maxTokens,
				"chunkNumber", i+1)

			// Emergency: skip this chunk or truncate
			ctx.GetLogger().Warn("Skipping chunk due to token limit", "chunkNumber", i+1)
			continue
		}

		ctx.GetLogger().Debug("Prompt ready for LLM",
			"promptTokens", promptTokens,
			"maxTokens", maxTokens,
			"chunkNumber", i+1)

		// Step 7: Summarize this chunk
		chunkSummary := SummarizeTextChunk(ctx, llm, finalPrompt, accountId, agentId, conversationId, messageId, userId)

		if chunkSummary == "" {
			ctx.GetLogger().Error("Got empty summary for chunk, using original chunk",
				"chunkNumber", i+1)
			chunkSummary = chunk // Fallback to original if summarization fails
		}

		// Step 8: Add this summary to our collection
		previousSummaries = append(previousSummaries, chunkSummary)

		ctx.GetLogger().Info("Chunk summarized successfully",
			"chunkNumber", i+1,
			"summaryLength", len(chunkSummary),
			"totalSummaries", len(previousSummaries))
	}

	// Step 9: Final step - combine all summaries
	if len(previousSummaries) == 0 {
		ctx.GetLogger().Warn("No summaries generated, returning original content")
		return content
	}

	if len(previousSummaries) == 1 {
		return previousSummaries[0]
	}

	// Step 10: If we have multiple summaries, combine and compress them into final summary
	ctx.GetLogger().Info("Combining all chunk summaries into final summary",
		"numSummaries", len(previousSummaries))

	finalSummary := CombineAndCompressSummariesForMessage(
		ctx, llm, previousSummaries, maxTokens,
		provider, model, accountId, agentId,
		conversationId, messageId, userId,
	)

	ctx.GetLogger().Info("Chunked summarization complete",
		"originalLength", len(content),
		"finalSummaryLength", len(finalSummary),
		"chunksProcessed", len(chunks))

	return finalSummary
}

// CompressPreviousSummariesForMessage compresses multiple previous summaries into one to fit within limit
func CompressPreviousSummariesForMessage(
	ctx *security.RequestContext,
	llm llms.Model,
	summaries []string,
	maxTokens int,
	provider string,
	model string,
	accountId string,
	agentId string,
	conversationId string,
	messageId string,
	userId string,
) string {
	combined := strings.Join(summaries, "\n\n")

	// Check current size
	currentTokens, _ := CountTokens(provider, model, combined)
	if currentTokens <= maxTokens {
		return combined // Already within limit
	}

	ctx.GetLogger().Info("Compressing previous summaries for message",
		"currentTokens", currentTokens,
		"maxTokens", maxTokens,
		"summaryCount", len(summaries))

	prompt := fmt.Sprintf(`The following are summaries from earlier parts of a large message.
Please create a single, concise summary that captures all key information.

Preserve: IDs, names, commands, decisions, and critical facts.

Summaries to compress:
%s

Provide a comprehensive but concise summary:`, combined)

	compressed := SummarizeTextChunk(ctx, llm, prompt, accountId, agentId, conversationId, messageId, userId)

	if compressed == "" {
		ctx.GetLogger().Warn("Compression failed, truncating instead")
		return truncateToTokenLimit(combined, maxTokens, provider, model)
	}

	return compressed
}

// CombineAndCompressSummariesForMessage combines all chunk summaries into a final coherent summary
func CombineAndCompressSummariesForMessage(
	ctx *security.RequestContext,
	llm llms.Model,
	summaries []string,
	maxTokens int,
	provider string,
	model string,
	accountId string,
	agentId string,
	conversationId string,
	messageId string,
	userId string,
) string {
	combined := strings.Join(summaries, "\n\n---\n\n")

	prompt := fmt.Sprintf(`The following are summaries from different parts of a single large message.
Please create ONE final summary that combines all the information coherently.

IMPORTANT:
- Preserve all IDs, names, commands, and key facts
- Make it read as a single cohesive summary, not separate parts
- Be thorough but concise

Part summaries:
%s

Final combined summary:`, combined)

	finalSummary := SummarizeTextChunk(ctx, llm, prompt, accountId, agentId, conversationId, messageId, userId)

	if finalSummary == "" {
		ctx.GetLogger().Warn("Final combination failed, using concatenated summaries")
		return combined
	}

	// Verify final summary isn't too long
	finalTokens, _ := CountTokens(provider, model, finalSummary)
	if finalTokens > maxTokens {
		ctx.GetLogger().Warn("Final summary exceeds limit, truncating",
			"finalTokens", finalTokens,
			"maxTokens", maxTokens)
		return truncateToTokenLimit(finalSummary, maxTokens, provider, model)
	}

	return finalSummary
}

// Constant for truncation limit of failed chunk summaries
const failedChunkSummaryTokenLimit = 1000

// SummarizeLargeMessageChunkedParallel implements a parallel map-reduce strategy for summarization.
func SummarizeLargeMessageChunkedParallel(
	ctx *security.RequestContext,
	llm llms.Model,
	content string,
	provider string,
	model string,
	maxTokens int,
	accountId string,
	agentId string,
	conversationId string,
	messageId string,
	userId string,
) string {
	ctx.GetLogger().Info("Starting parallel chunked summarization for large message",
		"contentLength", len(content),
		"maxTokens", maxTokens)

	// Use a safe chunk size, e.g., 70% of max tokens, to leave room for prompt overhead.
	safeChunkSize := int(float64(maxTokens) * 0.7)
	chunks, err := splitTextIntoChunks(content, provider, model, safeChunkSize)
	if err != nil {
		ctx.GetLogger().Error("Failed to split content into chunks for parallel summarization", "error", err)
		return truncateToTokenLimit(content, maxTokens, provider, model) // Fallback to simple truncation on split failure
	}

	if len(chunks) <= 1 {
		ctx.GetLogger().Info("Content fits in a single chunk, using simple summarization")
		return SummarizeTextChunk(ctx, llm, content, accountId, agentId, conversationId, messageId, userId)
	}

	ctx.GetLogger().Info("Content split into chunks, summarizing in parallel", "numChunks", len(chunks))

	// MAP PHASE: Summarize all chunks in parallel.
	var wg sync.WaitGroup
	summaries := make([]string, len(chunks))
	for i, chunk := range chunks {
		wg.Add(1)
		go func(i int, chunk string) {
			defer wg.Done()
			// Add recover block to prevent a panic in a goroutine from crashing the whole app
			defer func() {
				if r := recover(); r != nil {
					ctx.GetLogger().Error("Panic during parallel chunk summarization", "chunkNumber", i+1, "panic", r)
					// On panic, use a truncated piece of the original chunk as a poor-man's summary.
					summaries[i] = fmt.Sprintf("... (summary of chunk %d panicked, content snippet follows) ...\n%s", i+1, truncateToTokenLimit(chunk, failedChunkSummaryTokenLimit, provider, model))
				}
			}()

			ctx.GetLogger().Info("Summarizing chunk", "chunkNumber", i+1, "totalChunks", len(chunks))
			summary := SummarizeTextChunk(ctx, llm, chunk, accountId, agentId, conversationId, messageId, userId)
			if summary != "" {
				summaries[i] = summary
			} else {
				// If summarization of a chunk fails, use a truncated piece of the original chunk as a poor-man's summary.
				summaries[i] = fmt.Sprintf("... (summary of chunk %d failed, content snippet follows) ...\n%s", i+1, truncateToTokenLimit(chunk, failedChunkSummaryTokenLimit, provider, model))
			}
		}(i, chunk)
	}
	wg.Wait()

	// REDUCE PHASE: Combine all summaries into a final, coherent summary.
	ctx.GetLogger().Info("All chunks summarized, combining into final summary", "numSummaries", len(summaries))
	// The `CombineAndCompressSummariesForMessage` function can be reused for the reduce step.
	// We pass all summaries as a single item in a slice because the function expects a slice of summaries to combine.
	finalSummary := CombineAndCompressSummariesForMessage(
		ctx, llm, summaries, maxTokens,
		provider, model, accountId, agentId,
		conversationId, messageId, userId,
	)

	ctx.GetLogger().Info("Parallel chunked summarization complete",
		"originalLength", len(content),
		"finalSummaryLength", len(finalSummary),
		"chunksProcessed", len(chunks))

	return finalSummary
}
