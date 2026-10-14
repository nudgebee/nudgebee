package core

import (
	"context"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	nbprompts "nudgebee/llm/prompts"
	"nudgebee/llm/security"

	"github.com/tmc/langchaingo/llms"
)

const (
	memoryPrestepMaxPhrases   = 6
	memoryPrestepMaxPhraseLen = 80
)

var memoryPrestepSeparators = strings.NewReplacer("_", " ", "-", " ")

// memoryPrestepFn decides, before the planner runs, what stored memory the
// question needs. A variable so tests can stub the LLM call.
var memoryPrestepFn = memoryPrestepSearches

// memoryPrestepSearches asks a small model for the search phrases the memory
// lookups should run ("default namespace", "checkout"). ok=false means the
// call failed, timed out or never produced usable JSON; the caller then falls
// back to the stated preferences instead of guessing. An empty list with
// ok=true is a real verdict: the question is self-contained.
func memoryPrestepSearches(ctx *security.RequestContext, req NBAgentRequest) ([]string, bool) {
	system, err := nbprompts.GetPromptStrict(ctx.GetContext(), nbprompts.PromptMemoryPrestep, req.AccountId)
	if err != nil {
		ctx.GetLogger().Warn("memory prestep: prompt unavailable", "error", err)
		return nil, false
	}
	human := "Question:\n" + strings.TrimSpace(req.Query)
	if h := recentHistoryTail(req.RecentHistory, memoryPrestepMaxHistory); h != "" {
		human += "\n\nRecent turns (oldest first):\n" + h
	}
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, system),
		llms.TextParts(llms.ChatMessageTypeHuman, human),
	}

	tier := classificationTier(ctx, ClassificationCall{AccountId: req.AccountId, AgentId: req.AgentId, ConversationId: req.ConversationId})
	timeout := time.Duration(config.Config.MemoryPrestepTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second // a zero budget would fall back on every turn
	}
	base, cancel := context.WithTimeout(ctx.GetContext(), timeout)
	defer cancel()
	callCtx := security.NewRequestContext(
		context.WithValue(context.WithValue(base, ContextKeyModelTier, tier), ContextKeyCacheScope, CacheScopeGlobal),
		ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter(),
	)
	userID := ctx.GetSecurityContext().GetUserId()
	start := time.Now()

	// The LLM client keeps trying fallback models past the context deadline,
	// so the budget is enforced here: after the timeout the turn proceeds
	// with the stated preferences and the deferred cancel abandons the call.
	type outcome struct {
		searches []string
		ok       bool
	}
	done := make(chan outcome, 1)
	go func() {
		// One retry on unusable output only; a transport error is final.
		for attempt := 0; attempt < 2; attempt++ {
			res, err := GenerateAndTrackLLMContent(callCtx, userID, req.AccountId, req.ConversationId, req.MessageId,
				"memory_prestep", false, messages, true,
				llms.WithTemperature(0.0), WithThinkingLevel(ThinkingLevelFastTask))
			if err != nil || res == nil || len(res.Choices) == 0 {
				if base.Err() == nil { // past the budget the timeout path has already logged
					ctx.GetLogger().Warn("memory prestep: call failed", "error", err, "attempt", attempt+1,
						"duration_ms", time.Since(start).Milliseconds(), "tier", tier)
				}
				done <- outcome{}
				return
			}
			if searches, ok := parseMemoryPrestep(res.Choices[0].Content); ok {
				done <- outcome{searches, true}
				return
			}
			ctx.GetLogger().Warn("memory prestep: unusable output", "attempt", attempt+1, "content", truncateForLog(res.Choices[0].Content))
		}
		done <- outcome{}
	}()

	select {
	case out := <-done:
		if out.ok {
			ctx.GetLogger().Info("memory prestep: done", "conversation", req.ConversationId,
				"duration_ms", time.Since(start).Milliseconds(), "tier", tier, "phrases", len(out.searches))
		}
		return out.searches, out.ok
	case <-base.Done():
		if ctx.GetContext().Err() != nil { // the request itself was cancelled, not our budget
			return nil, false
		}
		ctx.GetLogger().Warn("memory prestep: timed out, using stated preferences", "conversation", req.ConversationId,
			"timeout_ms", timeout.Milliseconds(), "tier", tier)
		return nil, false
	}
}

// parseMemoryPrestep reads the {"search": [...]} object from the reply, with
// or without a code fence. A reply without a "search" key is unusable, not
// "nothing needed".
func parseMemoryPrestep(content string) ([]string, bool) {
	var out struct {
		Search *[]string `json:"search"`
	}
	if err := common.ExtractAndUnmarshalJSON([]byte(content), &out); err != nil || out.Search == nil {
		return nil, false
	}
	seen := make(map[string]struct{}, len(*out.Search))
	searches := make([]string, 0, len(*out.Search))
	for _, s := range *out.Search {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if r := []rune(s); len(r) > memoryPrestepMaxPhraseLen {
			s = string(r[:memoryPrestepMaxPhraseLen])
		}
		// Dedupe the way the lookups fold: "default_namespace" is "default namespace".
		key := strings.Join(strings.Fields(strings.ToLower(memoryPrestepSeparators.Replace(s))), " ")
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		searches = append(searches, s)
		if len(searches) == memoryPrestepMaxPhrases {
			break
		}
	}
	return searches, true
}
