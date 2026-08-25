package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"

	"github.com/lithammer/fuzzysearch/fuzzy"
	"github.com/tmc/langchaingo/llms"
)

// Memory-v2 seam. These package-level function pointers default to no-op /
// disabled implementations so this file compiles clean without importing
// nudgebee/llm/ee/memory. In prod (which ships the full source tree)
// agents/core/memory_v2.go's init() reassigns each pointer to a real impl
// that reaches into memory.Default(), memory.ComposeEnabledFor, and the
// typed stores. In OSS builds memory_v2.go is stripped by .oss-exclude and
// these no-op defaults stay in force — every memory-v2 code path becomes
// a legacy fall-through.
//
// Per-tenant gating is preserved: isMemoryV2ActiveFn calls through to
// memory.ComposeEnabledFor(tenantID) in EE, which reads the MEMORY_MODULE
// feature-flag row for the tenant. Tenants not enrolled in MEMORY_MODULE
// still take the legacy path even on EE binaries — unchanged from before
// the seam was introduced.
var (
	// isMemoryV2ActiveFn reports whether memory-v2 is enabled for this
	// tenant. OSS default: always false.
	isMemoryV2ActiveFn = func(tenantID string) bool { return false }

	// recordExtractedFactViaMemoryV2Fn writes a single extracted fact to
	// the memory-v2 typed stores. OSS default: returns an error so the
	// caller records stats.Errors and moves on.
	recordExtractedFactViaMemoryV2Fn = func(
		ctx *security.RequestContext,
		agent NBAgent,
		request NBAgentRequest,
		fact MemoryFact,
		memType MemoryType,
		confidence float64,
	) error {
		return errors.New("memory-v2 unavailable in this build")
	}

	// extractSessionWorkingMemoryFn runs the v2 session extractor after
	// each turn (writes a fresh per-session working-memory blob). OSS
	// default: no-op — session-working-memory feature simply doesn't run.
	extractSessionWorkingMemoryFn = func(
		ctx *security.RequestContext,
		request NBAgentRequest,
		response string,
		notebook string,
	) {
	}

	// composeMemoryV2BlockFn returns the rendered <memory> block the
	// executor injects into the agent prompt when memoryModuleActive is
	// true. OSS default: empty string — the executor's !memoryModuleActive
	// branch (legacy retrieveAndBuildMemoryNotebook path) is exclusively
	// taken in OSS, so this hook is never actually reached in an OSS
	// binary; the no-op is just what makes the file compile.
	composeMemoryV2BlockFn = func(
		ctx *security.RequestContext,
		request NBAgentRequest,
		agent NBAgent,
	) string {
		return ""
	}

	// loadTypedStoreDedupContextFn returns existing collective facts matching
	// this turn's query, as a bullet list, for the extractor's write-time dedup
	// hint (Layer 1). EE override (memory_v2.go) reads the collective store via
	// Postgres full-text search — current + authoritative, NOT the async RAG
	// index — because in module-active mode the legacy llm_conversation_memory
	// table is empty, which left the extractor blind and re-emitting known facts.
	// OSS default: empty (no memory module).
	loadTypedStoreDedupContextFn = func(
		ctx *security.RequestContext,
		request NBAgentRequest,
	) string {
		return ""
	}

	// dedupCanonicalSubjectFn is the Layer-2 pre-write dedup check: given a
	// freshly extracted fact, it returns the subject of an existing collective
	// fact that duplicates it under a DIFFERENT subject (Postgres FTS candidate +
	// body token-overlap over threshold), so the caller rewrites this fact's
	// subject to collide on the Upsert key instead of inserting a duplicate row.
	// Empty = no cross-subject duplicate. OSS default: no-op.
	dedupCanonicalSubjectFn = func(
		ctx *security.RequestContext,
		request NBAgentRequest,
		fact MemoryFact,
	) string {
		return ""
	}

	// RegisterMemoryMaintenanceJobsFn registers the memory-v2 scheduled
	// maintenance jobs (SQL-only bundle in memory/maintenance + LLM-driven
	// bundle in this package). Called from cmd/main.go AFTER config load
	// and scheduler bootstrap so config.Config.MemoryMaintenance* values
	// are populated and the scheduler singleton is ready. OSS default:
	// no-op.
	//
	// Split as a hook instead of a package `init()` on the EE-only file
	// because the old direct call in cmd/main.go was after config load —
	// moving it to package init() would have run it before config was
	// populated, breaking the config-driven schedule fields.
	RegisterMemoryMaintenanceJobsFn = func() {}

	// StartMemoryRagProjectorsFn spawns the memory-v2 RAG projector goroutines
	// (Patterns / Decisions / Collective) — write-side outbox workers that
	// drain rag_projected_at IS NULL rows into rag-server. Called from
	// cmd/main.go with the process syncCtx so a shutdown cancels the drain
	// loops. Gated internally on MemoryModuleEnabled && MemoryRagEnabled.
	// OSS default: no-op.
	StartMemoryRagProjectorsFn = func(_ context.Context) {}
)

// memoryDedupSimilarityThreshold is the minimum RAG cosine-similarity score at which a
// newly extracted memory is considered a duplicate of an existing one and discarded.
// 0.85 catches paraphrases ("ECR pulls need imagePullSecrets" ≈ "AWS ECR requires
// imagePullSecrets") while preserving genuinely new facts that happen to share topic.
const memoryDedupSimilarityThreshold = float32(0.85)

// Minimum character and word counts for an extracted fact to be considered
// worth persisting. These thresholds reject single-word "facts", stray
// template fragments, and markdown headers that occasionally leak from the
// extractor LLM.
const (
	minMemoryContentChars = 15
	minMemoryContentWords = 4
)

// ephemeralMemoryPattern matches snapshot-style content (pod ages, replica
// counts, StatefulSet indices) that is true now but misleads future
// investigations.
var ephemeralMemoryPattern = regexp.MustCompile(
	`(?i)\b(age[ds]?\s*(up\s+to\s+)?\d+[smhdw]|\d+[smhdw]\d+[smhdw]|\d+\s+replicas?|index\s+\d+|single\s+replica)\b`,
)

// imageRefPattern matches container image references pinned to a specific
// timestamp/sha tag — "true for this deploy, stale the next". These leak in as
// "architectural facts" ("X uses image reg/…:2026-06-09T…") but are not durable.
var imageRefPattern = regexp.MustCompile(`(?i)(:20\d\d-\d\d-\d\d[t_-]|@sha256:)`)

// bareCommandPattern matches content that is essentially a shell command
// (starts with a CLI verb + args) rather than a durable, tenant-specific
// insight. A real fact that *uses* a command starts with prose ("Namespaces
// stuck in Terminating can be deleted using kubectl patch…") and is kept; a
// bare "kubectl get pods -w" is rejected as noise. Deliberately case-sensitive:
// a command is typed lowercase, whereas sentence-case prose ("Docker images
// should…") capitalises the tool name and must not be filtered. The lowercase-
// prose edge ("git is the VCS we use") is caught by proseIndicatorWords.
var bareCommandPattern = regexp.MustCompile(`^\s*(kubectl|helm|docker|awk|sed|grep|curl|aws|gcloud|az|git|kubectx|kubens|psql|kubeconfig)(\s|$)`)

// proseIndicatorWords are common English function words. Content that opens
// with a tool name but contains ANY of these somewhere is prose *about* the
// tool ("docker container is configured…"), not a command invocation — a bare
// command practically never contains one. Scanning the whole content (rather
// than only the word right after the tool) avoids the endless follower-word
// whack-a-mole. Words that commonly appear as command flags/values ("in", "to",
// "it") are deliberately excluded to avoid keeping real commands.
var proseIndicatorWords = map[string]struct{}{
	"is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {}, "being": {},
	"has": {}, "have": {}, "had": {}, "does": {}, "did": {},
	"can": {}, "could": {}, "should": {}, "must": {}, "will": {}, "would": {}, "may": {}, "might": {},
	"the": {}, "an": {}, "this": {}, "that": {}, "these": {}, "those": {}, "its": {}, "their": {},
	"for": {}, "with": {}, "from": {}, "because": {}, "when": {}, "where": {}, "which": {}, "than": {},
	"uses": {}, "used": {}, "provides": {}, "needs": {}, "requires": {}, "allows": {},
	"supports": {}, "handles": {}, "manages": {}, "lets": {}, "configured": {}, "enabled": {},
	"should've": {}, "and": {}, "or": {},
}

// hasProseIndicator reports whether any whitespace-delimited token (stripped of
// surrounding punctuation) is an English function word — the signal that the
// text is prose rather than a bare command.
func hasProseIndicator(s string) bool {
	for _, f := range strings.Fields(s) {
		w := strings.Trim(strings.ToLower(f), ",.;:!?()'\"`")
		if _, ok := proseIndicatorWords[w]; ok {
			return true
		}
	}
	return false
}

// junkMemoryPrefixes rejects stray markdown headers, scratchpad tags, and
// template fragments that occasionally leak from the extractor LLM. The
// single "#" entry covers every markdown header level (`##`, `###`, ...).
var junkMemoryPrefixes = []string{
	"#", "**severity", "**impact", "**summary",
	"<scratchpad", "</scratchpad",
}

// junkMemoryExact rejects bare placeholder strings the LLM sometimes emits
// when it cannot produce a real fact. Production has been observing literal
// `null` and `none` text being persisted as a memory — those are the primary
// motivators for this list.
var junkMemoryExact = map[string]struct{}{
	"none": {}, "n/a": {}, "na": {}, "null": {}, "--": {}, "-": {},
	"[]": {}, "{}": {}, "nothing": {}, "nothing to extract": {},
}

// IsAcceptableMemoryContent runs the content-sanitization filters (junk,
// placeholder, markdown/template fragments, ephemeral state, pinned image
// tags, bare commands, length floors) on externally-supplied memory content.
// The conversation extractor applies these before Record; the memory-injection
// API calls this at its boundary so a finding pushed by a service can't bypass
// the sanitization (the `memory` package can't import this package, so the
// check has to run before Record is called). Pass memType="user_preference"
// with a non-empty subject to get the typed-preference relaxation (a pref
// value is naturally short).
func IsAcceptableMemoryContent(content, memType, subject string) (bool, string) {
	return isAcceptableMemoryFact(MemoryFact{Content: content, Type: memType, Subject: subject})
}

// isAcceptableMemoryFact decides whether an extracted fact is worth saving
// to the long-term memory store. The second return value is a short reason
// for observability and unit-test readability.
//
// For user_preference facts with a non-empty Subject (typed key), content is
// the preference VALUE — naturally short ("nudgebee", "loki", "kubectl top")
// and the (subject, value) pair is the unit of meaning. The length / word
// floors that filter free-form notes don't apply.
func isAcceptableMemoryFact(fact MemoryFact) (bool, string) {
	trimmed := strings.TrimSpace(fact.Content)
	if trimmed == "" {
		return false, "empty"
	}

	lower := strings.ToLower(trimmed)
	if _, junk := junkMemoryExact[lower]; junk {
		return false, "junk_placeholder"
	}

	for _, prefix := range junkMemoryPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return false, "markdown_or_template_fragment"
		}
	}

	typedPreference := strings.EqualFold(fact.Type, string(MemoryTypeUserPreference)) && fact.Subject != ""
	if !typedPreference {
		if len([]rune(trimmed)) < minMemoryContentChars {
			return false, "too_short"
		}
		if len(strings.Fields(trimmed)) < minMemoryContentWords {
			return false, "too_few_words"
		}
	}

	if ephemeralMemoryPattern.MatchString(trimmed) {
		return false, "ephemeral_state"
	}

	// A pinned image tag / sha is ephemeral — stale on the next deploy. Skip the
	// check for preferences (a user could legitimately pin a value).
	if !typedPreference && imageRefPattern.MatchString(trimmed) {
		return false, "ephemeral_image_ref"
	}

	// A bare command is not a durable insight. Preferences are exempt (a user's
	// preferred command, e.g. "kubectl top nodes", is a legitimate value).
	// Content that opens with a tool name is a command UNLESS it reads as prose
	// about the tool ("docker container is configured…") — detected by any
	// English function word anywhere in the text. Strip wrapping backticks so a
	// fenced `kubectl get pods` is still caught.
	trimmedCmd := strings.Trim(trimmed, "`")
	if !typedPreference && bareCommandPattern.MatchString(trimmedCmd) && !hasProseIndicator(trimmedCmd) {
		return false, "bare_command"
	}

	return true, ""
}

// filterAcceptableMemories applies isAcceptableMemoryFact at the read side of
// the RAG retrieval path. Memories cleared by the insert-time validator can
// still be present in the store from earlier code paths (legacy rows, direct
// writes that bypassed extractLongTermMemory), so we re-check on the way out
// to keep null/none/junk content out of the agent's context window.
//
// Filters in place: the input slice is freshly allocated by FindSimilarMemories
// per call and both call sites reassign to the same variable, so reusing the
// underlying array is safe and avoids an extra allocation per retrieval.
func filterAcceptableMemories(memories []LongTermMemory) []LongTermMemory {
	out := memories[:0]
	for _, m := range memories {
		if ok, _ := isAcceptableMemoryFact(MemoryFact{Content: m.Content, Type: string(m.MemoryType)}); ok {
			out = append(out, m)
		}
	}
	return out
}

// errMemoryRecordSkipped marks a memory-v2 Record outcome that the typed-store
// grounding gate intentionally refused (e.g. a Decisions fact with no
// user-agreement quote, or an empty subject). The EE bridge maps the store's
// sentinels onto this so the caller counts a benign skip as Skipped, not Errors,
// without this OSS-kept file importing ee/memory.
var errMemoryRecordSkipped = errors.New("memory-v2: record skipped by grounding gate")

// MemoryExtractionStats tracks outcomes of a single extraction run for
// observability and test verification.
type MemoryExtractionStats struct {
	Extracted    int // facts returned by the LLM
	Saved        int // new memories persisted
	Deduplicated int // skipped by content-based dedup
	Updated      int // existing memories updated (UPDATE facts)
	Skipped      int // filtered out (e.g. non-preference when preferenceOnly)
	Filtered     int // rejected by content validator (junk, ephemeral, too-short)
	Errors       int // save/dedup failures
}

func extractLongTermMemory(ctx *security.RequestContext, agent NBAgent, request NBAgentRequest, response string, notebook string, toolCallCount int) MemoryExtractionStats {
	var stats MemoryExtractionStats
	// When the Memory Module is enabled for this tenant, skip legacy extraction
	// entirely. Extracted facts go to the new typed stores via the bridge; the
	// legacy llm_conversation_memory table and its RAG index are not written.
	tenantID := ctx.GetSecurityContext().GetTenantId()
	memoryModuleActive := isMemoryV2ActiveFn(tenantID)

	// Layer-1 dedup: show the extractor the existing knowledge so it does not
	// re-emit facts already stored. This MUST read from where facts actually
	// live. In module-active mode facts are in the typed stores (collective) and
	// the legacy llm_conversation_memory table stays empty — reading it left the
	// extractor blind (the Q15 accumulation), so module-active reads the typed
	// store via Postgres full-text search (loadTypedStoreDedupContextFn), while
	// legacy mode keeps the llm_conversation_memory similarity read.
	existingMemoriesStr := "None"
	if memoryModuleActive {
		if dedup := strings.TrimSpace(loadTypedStoreDedupContextFn(ctx, request)); dedup != "" {
			existingMemoriesStr = dedup
		}
	} else {
		dedupQuery := request.Query
		if response != "" && len(response) < 500 {
			dedupQuery = request.Query + " " + response
		}
		if existingMemories, err := GetConversationDao().LoadLongTermMemories(request.AccountId, dedupQuery, 10); err == nil && len(existingMemories) > 0 {
			existingMemoriesStr = "- " + strings.Join(existingMemories, "\n- ")
		}
	}

	promptTemplate, promptErr := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptMemoryExtractor, request.AccountId)
	if promptErr != nil {
		// Extracting against an empty instruction would write junk into long-term
		// memory, which is worse than extracting nothing.
		ctx.GetLogger().Error("memory: extractor prompt failed to load, skipping extraction", "error", promptErr)
		return MemoryExtractionStats{}
	}

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
		context.WithValue(context.WithValue(ctx.GetContext(), ContextKeyModelTier, ModelTierSummary), ContextKeyCacheScope, CacheScopeGlobal),
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

			// Grounding gate: restrict extraction to user_preference facts (which are
			// user-STATED, not environment claims) when the turn lacks investigation
			// grounding — either no notebook context, OR zero tool calls executed.
			// A zero-tool answer's factual claims (metric/resource/label/config
			// identifiers) come from the model or injected memory/KB, not from fresh
			// verification against the live source; promoting them to durable facts
			// creates a hallucination feedback loop — an unverified guess becomes
			// "knowledge" and is re-injected on the next similar question. User
			// preferences are exempt: they assert intent, not environment truth.
			preferenceOnly := notebook == "" || toolCallCount == 0

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

				// Pre-save content validator — reject literal `null`/`none`
				// placeholders, markdown fragments, ephemeral cluster state
				// (ages/replicas/indices), and entries too short to carry
				// useful knowledge. Applied before the update / bridge /
				// legacy branches so low-quality content never reaches any
				// store. See isAcceptableMemoryFact for the full rule set.
				if ok, reason := isAcceptableMemoryFact(fact); !ok {
					ctx.GetLogger().Info("agentexecutor: rejecting low-quality memory fact",
						"reason", reason,
						"content", fact.Content,
						"type", memType,
					)
					stats.Filtered++
					continue
				}

				if memoryModuleActive {
					// Layer-2 pre-write dedup: if this fact duplicates an existing
					// one stored under a DIFFERENT subject, rewrite its subject to
					// the canonical one so the Upsert refreshes that row instead of
					// inserting a duplicate (the Q15 case: same metric, 3 subjects).
					// Same-subject re-assertions already collide on the Upsert key.
					if canon := dedupCanonicalSubjectFn(ctx, request, fact); canon != "" && !strings.EqualFold(canon, fact.Subject) {
						ctx.GetLogger().Info("agentexecutor: dedup merged fact onto existing subject",
							"extracted_subject", fact.Subject, "canonical_subject", canon)
						fact.Subject = canon
					}

					// Module-active: every fact (new OR update) routes to the typed
					// stores via the memory-v2 bridge. handleMemoryUpdate writes only
					// the legacy llm_conversation_memory table, which is unused here —
					// an IsUpdate fact routed there would vanish instead of refreshing
					// the typed-store row. The collective/patterns Upsert refreshes
					// body + bumps confidence on same-key re-assertion; a correction's
					// fresh confidence wins via the Correction flag. The bridge's
					// shape-conversion, super-admin user-id rescue, and agent-module
					// resolution live in memory_v2.go so this OSS-kept file never
					// references memory.ExtractedFact.
					if berr := recordExtractedFactViaMemoryV2Fn(ctx, agent, request, fact, memType, groundedConfidence(memType, toolCallCount)); berr != nil {
						if errors.Is(berr, errMemoryRecordSkipped) {
							// Intended grounding-gate skip (e.g. a Decisions fact
							// without a user-agreement quote), not a save failure.
							ctx.GetLogger().Debug("agentexecutor: memory-v2 record skipped", "reason", berr, "subject", fact.Subject, "type", memType)
							stats.Skipped++
						} else {
							ctx.GetLogger().Error("agentexecutor: memory-v2 record failed", "error", berr, "subject", fact.Subject, "type", memType)
							stats.Errors++
						}
					} else if fact.IsUpdate {
						stats.Updated++
					} else {
						stats.Saved++
					}
				} else if fact.IsUpdate {
					// Legacy UPDATE: old fact -> new fact in llm_conversation_memory.
					handleMemoryUpdate(ctx, request, fact)
					stats.Updated++
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
		"filtered", stats.Filtered,
		"errors", stats.Errors,
	)
	return stats
}

// extractSessionWorkingMemory runs after each turn to refresh the typed
// per-session working-memory blob the next turn's Compose will inject
// under <session_working_memory>. The real body lives in
// agents/core/memory_v2.go (EE-only) and is wired in via
// extractSessionWorkingMemoryFn's init override. In OSS builds the hook
// stays at the no-op default and this function returns without doing
// anything — the legacy path does not have an equivalent session-scoped
// working-memory feature, so there is nothing to fall back to.
func extractSessionWorkingMemory(ctx *security.RequestContext, request NBAgentRequest, response string, notebook string) {
	extractSessionWorkingMemoryFn(ctx, request, response, notebook)
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
// For ReAct planners on investigation/retrieval tasks the full memory set is returned.
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
	isSupportedPlanner := agent.GetPlannerType() == AgentPlannerTypeOrchestrating || isReActStylePlanner(agent.GetPlannerType())

	if isSupportedPlanner && (isInvestigationTask || isRetrievalTask) {
		// Use FindSimilarMemories (not RetrieveRelevantMemories) so use_count is only
		// incremented for memories that actually make it into the final notebook —
		// buildStructuredMemoryNotebook may filter some as redundant with convFacts.
		allMemories, memErr := GetConversationDao().FindSimilarMemories(request.AccountId, request.Query, 10)
		if memErr != nil {
			ctx.GetLogger().Warn("agentexecutor: failed to load long-term memories", "error", memErr)
			return ""
		}
		allMemories = filterAcceptableMemories(allMemories)
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
	allMemories = filterAcceptableMemories(allMemories)

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

// groundedConfidence scores a fact's STORED confidence from its type and how
// much live verification backed the turn. The collective composer reranks by
// this confidence (rerankCollectiveByConfidence), so a claim the agent never
// verified against a live source must rank below a tool-grounded one — that is
// what prevents an unverified guess (e.g. a hallucinated metric name emitted
// with zero tool calls) from outranking a metric-queried fact.
//
// User preferences are user-STATED intent, not environment truth, so grounding
// does not apply. Every other type is scaled by the number of tool calls the
// turn executed: 0 → heavy penalty (belt-and-suspenders with the extraction
// grounding gate), 1 → light penalty, ≥2 → full type confidence.
func groundedConfidence(mt MemoryType, toolCallCount int) float64 {
	base := memoryConfidence(mt)
	if mt == MemoryTypeUserPreference {
		return base
	}
	switch toolCallCount {
	case 0:
		return base * 0.4
	case 1:
		return base * 0.85
	default:
		return base
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
