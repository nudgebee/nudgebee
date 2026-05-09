package core

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/memory"
	"nudgebee/llm/security"

	"github.com/lithammer/fuzzysearch/fuzzy"
	"github.com/tmc/langchaingo/llms"
)

// memoryDedupSimilarityThreshold is the minimum RAG cosine-similarity score at which a
// newly extracted memory is considered a duplicate of an existing one and discarded.
// 0.85 catches paraphrases ("ECR pulls need imagePullSecrets" ≈ "AWS ECR requires
// imagePullSecrets") while preserving genuinely new facts that happen to share topic.
const memoryDedupSimilarityThreshold = float32(0.85)

// MemoryExtractionStats tracks outcomes of a single extraction run for
// observability and test verification.
type MemoryExtractionStats struct {
	Extracted    int // facts returned by the LLM
	Saved        int // new memories persisted
	Deduplicated int // skipped by content-based dedup
	Updated      int // existing memories updated (UPDATE facts)
	Skipped      int // filtered out (e.g. non-preference when preferenceOnly)
	Errors       int // save/dedup failures
}

func extractLongTermMemory(ctx *security.RequestContext, request NBAgentRequest, response string, notebook string) MemoryExtractionStats {
	var stats MemoryExtractionStats
	// When the Memory Module is enabled for this tenant, skip legacy extraction
	// entirely. Extracted facts go to the new typed stores via the bridge; the
	// legacy llm_conversation_memory table and its RAG index are not written.
	tenantID := ctx.GetSecurityContext().GetTenantId()
	memoryModuleActive := memory.ComposeEnabledFor(tenantID)
	// Load semantically similar existing memories to prevent duplicates.
	// Using the current query gives the LLM the most relevant existing knowledge for deduplication,
	// rather than an arbitrary window of the 25 most recently inserted memories.
	dedupQuery := request.Query
	if response != "" && len(response) < 500 {
		dedupQuery = request.Query + " " + response
	}
	existingMemories, err := GetConversationDao().LoadLongTermMemories(request.AccountId, dedupQuery, 10)
	existingMemoriesStr := "None"
	if err == nil && len(existingMemories) > 0 {
		existingMemoriesStr = "- " + strings.Join(existingMemories, "\n- ")
	}

	promptTemplate := prompts_repo.GetPrompt(prompts_repo.PromptMemoryExtractor)

	// Create the dynamic human message
	humanPrompt := fmt.Sprintf("Original Question: %s\nFinal Resolution: %s\nInvestigation Notes (Notebook): %s\n\nExisting Knowledge (Facts already learned):\n%s",
		request.Query, response, notebook, existingMemoriesStr)

	messages := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{llms.TextContent{Text: promptTemplate}},
		},
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: humanPrompt}},
		},
	}

	memoryCtx := security.NewRequestContext(
		context.WithValue(context.WithValue(ctx.GetContext(), ContextKeyUseLiteModel, true), ContextKeyCacheScope, CacheScopeGlobal),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	result, err := GenerateAndTrackLLMContent(memoryCtx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, "memory_extractor", false, messages, true, llms.WithTemperature(0.0), WithThinkingLevel(ThinkingLevelFastTask))
	if err != nil {
		ctx.GetLogger().Error("agentexecutor: failed to extract long-term memory", "error", err)
		return stats
	}

	ctx.GetLogger().Info("agentexecutor: LLM memory extraction response received", "choices_count", len(result.Choices))

	if len(result.Choices) > 0 {
		content := strings.TrimSpace(result.Choices[0].Content)
		ctx.GetLogger().Debug("agentexecutor: memory extractor raw response", "content", content)

		if content != "" && !strings.EqualFold(content, "NONE") && content != "[]" {

			// Parse the output - handle both single-line and multi-line facts
			parsedFacts := parseMemoryFacts(ctx, content)
			stats.Extracted = len(parsedFacts)
			ctx.GetLogger().Info("agentexecutor: long-term memory extraction complete", "facts_found", stats.Extracted)

			// When no investigation notebook exists (e.g., conversational/tool agents),
			// restrict extraction to user_preference facts only to avoid saving low-quality
			// observations that lack the full investigation context needed to validate them.
			preferenceOnly := notebook == ""

			for _, fact := range parsedFacts {
				memType := MemoryTypeInvestigationResult
				if fact.Type != "" {
					memType = MemoryType(fact.Type).Validate()
				} else if fact.IsPattern {
					memType = MemoryTypePattern
				} else if fact.IsWorkflow {
					memType = MemoryTypeWorkflow
				}

				if preferenceOnly && memType != MemoryTypeUserPreference {
					ctx.GetLogger().Debug("agentexecutor: skipping non-preference fact (no notebook context)", "type", memType)
					stats.Skipped++
					continue
				}

				if fact.IsUpdate {
					// Handle UPDATE: old fact -> new fact
					handleMemoryUpdate(ctx, request, fact)
					stats.Updated++
				} else if memoryModuleActive {
					// New path: route the fact through the classifier into the
					// typed stores. Legacy table + RAG index are not touched.
					if berr := memory.BridgeFromLegacy(memory.LegacyMemoryFact{
						TenantID:       tenantID,
						UserID:         request.UserId,
						ConversationID: request.ConversationId,
						MemoryType:     string(memType),
						Content:        fact.Content,
					}); berr != nil {
						ctx.GetLogger().Error("agentexecutor: typed-store bridge failed", "error", berr, "fact", fact.Content)
						stats.Errors++
					} else {
						stats.Saved++
					}
				} else {
					// Legacy path: similarity dedup + save to llm_conversation_memory
					// + AddMemoryToRAG. Runs only when the memory module is off
					// for this tenant.
					similar, serr := GetConversationDao().FindSimilarMemories(request.AccountId, fact.Content, 3)
					if serr != nil {
						ctx.GetLogger().Warn("agentexecutor: content-based dedup probe failed, proceeding with save", "error", serr)
					} else if len(similar) > 0 && similar[0].SimilarityScore >= memoryDedupSimilarityThreshold {
						ctx.GetLogger().Info("agentexecutor: skipping duplicate memory (content-based dedup)",
							"new_content", fact.Content,
							"existing_content", similar[0].Content,
							"similarity", similar[0].SimilarityScore,
						)
						stats.Deduplicated++
						continue
					}

					_, err := GetConversationDao().SaveLongTermMemory(
						request.AccountId,
						request.ConversationId,
						request.MessageId,
						fact.Content,
						memType,
					)
					if err != nil {
						ctx.GetLogger().Error("agentexecutor: failed to save individual long-term memory", "error", err, "fact", fact.Content)
						stats.Errors++
					} else {
						stats.Saved++
					}
				}
			}
		} else {
			ctx.GetLogger().Info("agentexecutor: memory extractor returned empty or NONE - no facts to save")
		}
	} else {
		ctx.GetLogger().Warn("agentexecutor: LTM choices is empty - no response from LLM")
	}

	ctx.GetLogger().Info("agentexecutor: memory extraction stats",
		"extracted", stats.Extracted,
		"saved", stats.Saved,
		"deduplicated", stats.Deduplicated,
		"updated", stats.Updated,
		"skipped", stats.Skipped,
		"errors", stats.Errors,
	)
	return stats
}

func parseMemoryFacts(ctx *security.RequestContext, content string) []MemoryFact {
	var facts []MemoryFact

	err := common.ExtractAndUnmarshalJSON([]byte(content), &facts)

	if err != nil {
		ctx.GetLogger().Warn("agentexecutor: failed to parse memory facts as JSON", "error", err)

	}
	return facts
}

// handleMemoryUpdate finds and updates existing memory by deleting old and inserting new.
// It uses semantic (RAG) search on OldContent to find the closest match, which is far more
// reliable than substring matching across an arbitrary window of recent memories.
func handleMemoryUpdate(ctx *security.RequestContext, request NBAgentRequest, fact MemoryFact) {
	// FindSimilarMemories (not RetrieveRelevantMemories) so this maintenance lookup does
	// not increment use_count as a side effect, and returns SimilarityScore for the guard
	// below. Also avoids a race between the async pool increment and CarryOverMemoryUseCount.
	existingMemories, err := GetConversationDao().FindSimilarMemories(request.AccountId, fact.OldContent, 5)
	if err != nil {
		ctx.GetLogger().Warn("agentexecutor: failed to load memories for update via semantic search", "error", err)
		return
	}

	// Only accept the top result when it is genuinely similar to OldContent.
	// Without a threshold RAG always returns the closest match even when unrelated,
	// which would silently update the wrong memory.
	var matchedMemory *LongTermMemory
	if len(existingMemories) > 0 && existingMemories[0].SimilarityScore >= memoryDedupSimilarityThreshold {
		matchedMemory = &existingMemories[0]
	}

	if matchedMemory != nil {
		memType := matchedMemory.MemoryType
		if memType == "" {
			memType = MemoryTypeInvestigationResult
		}

		// Insert the new version FIRST. Only delete the old one when the insert
		// succeeds, which closes the data-loss window of the previous delete-then-insert
		// ordering (where a transient DB error on insert would leave neither version).
		newID, saveErr := GetConversationDao().SaveLongTermMemory(
			request.AccountId,
			request.ConversationId,
			request.MessageId,
			fact.Content,
			memType,
		)
		if saveErr != nil {
			ctx.GetLogger().Error("agentexecutor: failed to save updated memory, keeping old version intact", "error", saveErr)
			return
		}

		// Carry over the old memory's use_count so frequently-referenced memories don't
		// lose their access history when their content is merely clarified or extended.
		GetConversationDao().CarryOverMemoryUseCount(matchedMemory.ID.String(), newID, request.AccountId)

		deleteErr := GetConversationDao().DeleteLongTermMemory(matchedMemory.ID.String(), request.AccountId)
		if deleteErr != nil {
			// New version is already saved; old version is a now-duplicate but not lost.
			ctx.GetLogger().Warn("agentexecutor: saved new memory but failed to delete old version (duplicate exists)", "error", deleteErr, "old_id", matchedMemory.ID)
		}
		return
	}

	// If old fact not found, save as new
	_, err = GetConversationDao().SaveLongTermMemory(
		request.AccountId,
		request.ConversationId,
		request.MessageId,
		fact.Content,
		MemoryTypeInvestigationResult,
	)
	if err != nil {
		ctx.GetLogger().Error("agentexecutor: failed to save new memory", "error", err)
	}
}

// retrieveAndBuildMemoryNotebook retrieves relevant memories and builds a structured notebook.
// For ReWoo/ReAct planners on investigation/retrieval tasks the full memory set is returned.
// For all other planners only user_preference memories are injected so agents can respect
// known user preferences (e.g. output format, namespace choices) without the overhead of
// a full knowledge-base lookup.
//
// convFacts contains the content strings of facts already stored in the per-conversation
// context (LlmUnifiedExtraction.MemoryFacts). Any LTM entry whose content closely matches
// one of these facts is suppressed to avoid surfacing the same information twice.
func retrieveAndBuildMemoryNotebook(ctx *security.RequestContext, request NBAgentRequest, agent NBAgent, convFacts []string) string {
	isRetrievalTask := IsDataRetrievalOrActionRequest(request.Query)
	isInvestigationTask := IsInvestigationRequestTask(request.Query) || request.ConversationSource == ConversationSourceInvestigation
	isSupportedPlanner := agent.GetPlannerType() == AgentPlannerTypeReWoo || isReActStylePlanner(agent.GetPlannerType())

	if isSupportedPlanner && (isInvestigationTask || isRetrievalTask) {
		// Use FindSimilarMemories (not RetrieveRelevantMemories) so use_count is only
		// incremented for memories that actually make it into the final notebook —
		// buildStructuredMemoryNotebook may filter some as redundant with convFacts.
		allMemories, memErr := GetConversationDao().FindSimilarMemories(request.AccountId, request.Query, 10)
		if memErr != nil {
			ctx.GetLogger().Warn("agentexecutor: failed to load long-term memories", "error", memErr)
			return ""
		}
		if len(allMemories) == 0 {
			return ""
		}

		patterns := []LongTermMemory{}
		workflows := []LongTermMemory{}
		facts := []LongTermMemory{}
		for _, m := range allMemories {
			switch m.MemoryType {
			case MemoryTypePattern:
				patterns = append(patterns, m)
			case MemoryTypeWorkflow:
				workflows = append(workflows, m)
			default:
				facts = append(facts, m)
			}
		}

		notebook := buildStructuredMemoryNotebook(patterns, workflows, facts, convFacts)
		if notebook != "" {
			// Determine which memories survived the convFacts redundancy filter and
			// increment use_count only for those — they are what the agent actually sees.
			var surfaced []LongTermMemory
			for _, m := range allMemories {
				if !isRedundantWithConversationContext(m.Content, convFacts) {
					surfaced = append(surfaced, m)
				}
			}
			GetConversationDao().IncrementMemoryUsage(request.AccountId, surfaced)
			saveMemoryReferences(ctx, request, surfaced)
		}
		return notebook
	}

	// For all other planner types (tool, conversational, classification, custom) inject
	// user_preference memories so the agent can honour known user instructions.
	return retrieveUserPreferenceNotebook(ctx, request, convFacts)
}

// retrieveUserPreferenceNotebook returns a short context block containing user preferences
// relevant to the current query. Returns "" when no preferences are stored.
//
// Uses FindSimilarMemories (no use_count increment) instead of RetrieveRelevantMemories
// so that non-preference memories returned by the RAG search don't get their use_count
// inflated — only the preferences actually surfaced to the agent are counted.
func retrieveUserPreferenceNotebook(ctx *security.RequestContext, request NBAgentRequest, convFacts []string) string {
	allMemories, err := GetConversationDao().FindSimilarMemories(request.AccountId, request.Query, 10)
	if err != nil {
		ctx.GetLogger().Warn("agentexecutor: failed to load user preference memories", "error", err)
		return ""
	}

	var prefs []LongTermMemory
	for _, m := range allMemories {
		if m.MemoryType == MemoryTypeUserPreference && !isRedundantWithConversationContext(m.Content, convFacts) {
			prefs = append(prefs, m)
		}
	}
	if len(prefs) == 0 {
		return ""
	}

	// Increment use_count only for the preferences that will actually be shown.
	GetConversationDao().IncrementMemoryUsage(request.AccountId, prefs)

	var sb strings.Builder
	sb.WriteString("**User Preferences (apply these to your response):**\n")
	for _, p := range prefs {
		fmt.Fprintf(&sb, "• %s\n", escapeTemplateSyntax(p.Content))
	}
	saveMemoryReferences(ctx, request, prefs)
	return sb.String()
}

// saveMemoryReferences asynchronously records which memories were surfaced for this agent call.
func saveMemoryReferences(ctx *security.RequestContext, request NBAgentRequest, memories []LongTermMemory) {
	if len(memories) == 0 {
		return
	}
	var refs []AgentReference
	for _, m := range memories {
		refs = append(refs, AgentReference{
			Type:        AgentReferenceTypeMemory,
			ReferenceID: m.ID.String(),
		})
	}
	trackFn := func() {
		if err := GetConversationDao().SaveAgentReferences(request.AccountId, request.ConversationId, request.MessageId, request.AgentId, refs); err != nil {
			ctx.GetLogger().Warn("agentexecutor: failed to save memory references", "error", err)
		}
	}
	if metricsPool := GetMetricsWorkerPool(); metricsPool != nil {
		if err := metricsPool.Submit(context.Background(), trackFn); err != nil {
			ctx.GetLogger().Error("agentexecutor: failed to submit memory reference task to pool, falling back to goroutine", "error", err)
			go trackFn()
		}
	} else {
		go trackFn()
	}
}

// memoryConfidence returns a quality score for a MemoryType that reflects how
// reliable and reusable the fact is expected to be. Used to sort memories within
// each section so the most trustworthy facts appear first.
//
//	0.9 — user_preference: explicit user instruction, highest trust
//	0.8 — pattern, workflow: validated by recurrence or multi-step procedure
//	0.7 — architectural_fact, configuration_insight, dependency_mapping, troubleshooting_guide
//	0.5 — investigation_result (default one-time finding)
func memoryConfidence(mt MemoryType) float64 {
	switch mt {
	case MemoryTypeUserPreference:
		return 0.9
	case MemoryTypePattern, MemoryTypeWorkflow:
		return 0.8
	case MemoryTypeArchitecturalFact, MemoryTypeConfigInsight, MemoryTypeDependencyMapping, MemoryTypeTroubleshooting:
		return 0.7
	default:
		return 0.5
	}
}

// isRedundantWithConversationContext returns true when content is already
// represented in the per-conversation context facts (fuzzy Levenshtein check
// at 85% similarity), used to suppress LTM duplicates at injection time.
func isRedundantWithConversationContext(content string, convFacts []string) bool {
	if len(convFacts) == 0 {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(content))
	for _, cf := range convFacts {
		cf = strings.ToLower(strings.TrimSpace(cf))
		dist := fuzzy.LevenshteinDistance(lower, cf)
		maxLen := len(lower)
		if len(cf) > maxLen {
			maxLen = len(cf)
		}
		if maxLen > 0 && float64(dist)/float64(maxLen) < 0.15 {
			return true
		}
	}
	return false
}

// buildStructuredMemoryNotebook formats memories into categorized sections.
// convFacts contains the fact contents already present in the per-conversation
// context; any LTM entry that duplicates one of these is suppressed to keep
// the injected context concise.
func buildStructuredMemoryNotebook(patterns, workflows, facts []LongTermMemory, convFacts []string) string {
	var sb strings.Builder

	sb.WriteString("**Knowledge Base (from previous investigations):**\n\n")

	// Sort each category by confidence DESC so the most trustworthy facts are shown first.
	sort.Slice(patterns, func(i, j int) bool {
		return memoryConfidence(patterns[i].MemoryType) > memoryConfidence(patterns[j].MemoryType)
	})
	sort.Slice(workflows, func(i, j int) bool {
		return memoryConfidence(workflows[i].MemoryType) > memoryConfidence(workflows[j].MemoryType)
	})
	sort.Slice(facts, func(i, j int) bool {
		return memoryConfidence(facts[i].MemoryType) > memoryConfidence(facts[j].MemoryType)
	})

	// Section 1: Known Patterns (most valuable for diagnosis)
	var writtenPatterns int
	if len(patterns) > 0 {
		sb.WriteString("**Known Failure Patterns:**\n")
		sb.WriteString("(These are recurring issues observed in the past)\n")
		for _, p := range patterns {
			if isRedundantWithConversationContext(p.Content, convFacts) {
				continue
			}
			content := strings.TrimPrefix(p.Content, "PATTERN:")
			content = strings.TrimSpace(content)
			fmt.Fprintf(&sb, "• %s\n", escapeTemplateSyntax(content))
			writtenPatterns++
		}
		if writtenPatterns > 0 {
			sb.WriteString("\n")
		}
	}

	// Section 2: Troubleshooting Workflows (actionable procedures)
	var writtenWorkflows int
	if len(workflows) > 0 {
		sb.WriteString("**Troubleshooting Procedures:**\n")
		sb.WriteString("(Use these step-by-step workflows when applicable)\n")
		for _, w := range workflows {
			if isRedundantWithConversationContext(w.Content, convFacts) {
				continue
			}
			fmt.Fprintf(&sb, "%s\n", escapeTemplateSyntax(w.Content))
			writtenWorkflows++
		}
		if writtenWorkflows > 0 {
			sb.WriteString("\n")
		}
	}

	// Section 3: Architecture & Configuration Facts — cap at top 8 by confidence.
	if len(facts) > 0 {
		sb.WriteString("**Architecture & Configuration:**\n")
		limit := 8
		written := 0
		redundant := 0
		for _, f := range facts {
			if written >= limit {
				break
			}
			if isRedundantWithConversationContext(f.Content, convFacts) {
				redundant++
				continue
			}
			fmt.Fprintf(&sb, "• %s\n", escapeTemplateSyntax(f.Content))
			written++
		}
		remaining := len(facts) - written - redundant
		if remaining > 0 {
			fmt.Fprintf(&sb, "(+ %d more facts available if needed)\n", remaining)
		}
		sb.WriteString("\n")
	}

	if writtenPatterns+writtenWorkflows == 0 && len(facts) == 0 {
		// Everything was filtered as redundant — return empty so caller can skip the section.
		return ""
	}

	sb.WriteString("**Instructions for using this knowledge:**\n")
	sb.WriteString("• Check known patterns first - they can save investigation time\n")
	sb.WriteString("• Follow established workflows when troubleshooting similar issues\n")
	sb.WriteString("• Use architecture facts to understand service relationships\n")
	sb.WriteString("• If a pattern matches current symptoms, mention it in your reasoning\n")

	return sb.String()
}

// StartMemoryTTLCleanup runs a periodic job that deletes stale long-term memories.
// Two independent criteria are applied:
//   - Never-used memories older than MemoryTTLNeverUsedDays (default 90d)
//   - Memories not accessed in MemoryTTLStaleDays (default 180d)
//
// Set MemoryTTLCleanupIntervalHours = 0 to disable. Respects context cancellation for
// clean shutdown.
func StartMemoryTTLCleanup(ctx context.Context) {
	intervalHours := config.Config.MemoryTTLCleanupIntervalHours
	if intervalHours <= 0 {
		slog.Info("memory-ttl: cleanup disabled (llm_memory_ttl_cleanup_interval_hours=0)")
		return
	}

	// purgeStaleMemories is on the concrete *ConversationDao, not the interface.
	// The underlying type is always *ConversationDao in production; fail fast if not.
	dao, ok := GetConversationDao().(*ConversationDao)
	if !ok {
		slog.Error("memory-ttl: DAO is not *ConversationDao, cleanup disabled")
		return
	}

	interval := time.Duration(intervalHours) * time.Hour
	slog.Info("memory-ttl: starting cleanup job",
		"interval_hours", intervalHours,
		"never_used_days", config.Config.MemoryTTLNeverUsedDays,
		"stale_days", config.Config.MemoryTTLStaleDays,
	)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run once at startup, then on each tick.
	dao.purgeStaleMemories()

	for {
		select {
		case <-ctx.Done():
			slog.Info("memory-ttl: cleanup job stopped")
			return
		case <-ticker.C:
			dao.purgeStaleMemories()
		}
	}
}
