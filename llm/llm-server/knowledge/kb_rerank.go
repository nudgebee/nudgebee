package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"

	llmruntime "nudgebee/llm/llms/runtime"

	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/tmc/langchaingo/llms"
)

// Wire the knowledge-base judge into the retrieval paths through KBRerankFn.
func init() {
	KBRerankFn = llmJudgeKBChunks
}

// kbJudgeSystemPrompt is the STATIC (cacheable) instruction prefix, so it must
// stay byte-stable across calls for CacheScopeGlobal to serve it from cache.
//
// The reply is one number and one score, and candidates are numbered from 1 so
// that 0 is free to mean "none of these". That shape was chosen over asking for
// an empty JSON array: "none" is the correct answer to most production questions
// (177 of 300 measured had no answer anywhere in the corpus), so it is the
// answer the contract has to make easiest to emit, and every model can emit a
// digit. An empty container is not reliably produced — a model that has decided
// nothing is relevant tends to say so in prose, which is indistinguishable from
// having failed to answer, and the two mean opposite things here.
//
// "Being about the same topic is not enough" earns its place: without it, the
// retrieved runbook for "get me recent log errors in namespace X" scores highly,
// since the passage genuinely is about log errors. What makes it unhelpful is
// that the question wanted live data, which no document can supply.
const kbJudgeSystemPrompt = `You decide which passage from an internal knowledge base answers the user's question.
You are given the question and a list of candidate passages, numbered from 1.

Reply with one line of JSON and nothing else:
{"best": <number>, "score": <number between 0 and 1>}

"best" is the number of the single passage that best answers the question.
"best" is 0 when no passage answers it. Answer 0 whenever you are unsure — most questions have no answer in the knowledge base, so 0 is the expected reply.
Being about the same topic is NOT enough to pick a passage. A passage that is merely related is a 0.
If the question asks for the current state of a running system (live pod counts, today's logs, current spend), no stored passage can answer it. Answer 0.
"score" is how well the chosen passage answers the question: 1 means it contains the answer or the exact procedure needed, 0 means no help. With "best": 0, send "score": 0.

Examples of valid replies:
{"best": 3, "score": 0.9}
{"best": 0, "score": 0}`

// kbJudgeObjectRe matches innermost JSON objects, so the verdict can be found in
// a fenced block or after prose without the pattern spanning unrelated braces.
var kbJudgeObjectRe = regexp.MustCompile(`\{[^{}]*\}`)

// llmJudgeKBChunks picks at most one candidate with a fast retrieval-tier call.
//
// One call judges every candidate. Per-candidate calls measured better at
// separating answers from noise (AUC 0.79 against 0.68 on the same 300 questions)
// but cost one call each and roughly twice the wall time; this starts from the
// cheaper shape, and the hook's signature is unchanged if that trade is revisited.
func llmJudgeKBChunks(
	rc *security.RequestContext,
	accountID, userID, conversationID, messageID, query string,
	chunks []string,
) (KBRerankVerdict, bool) {
	none := KBRerankVerdict{Keep: KBRerankNone}
	if !config.Config.KBRerankEnabled || rc == nil || len(chunks) == 0 {
		return none, false
	}
	if strings.TrimSpace(query) == "" {
		// Nothing to judge against. Judging an empty question would return an
		// arbitrary survivor with a confident-looking score.
		return none, false
	}

	var human strings.Builder
	fmt.Fprintf(&human, "Question: %s\n\nCandidates:\n", strings.TrimSpace(query))
	for i, chunk := range chunks {
		// Numbered from 1, matching the prompt's contract.
		fmt.Fprintf(&human, "[%d] %s\n", i+1, strings.TrimSpace(chunk))
	}

	messages := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: kbJudgeSystemPrompt}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: human.String()}}},
	}

	// Retrieval tier + fast-task thinking + global cache scope, matching the
	// memory reranker: this is a ranking decision on a latency-sensitive path,
	// and the static system prompt above is what the global scope caches.
	judgeCtx := security.NewRequestContext(
		context.WithValue(
			context.WithValue(rc.GetContext(), llmruntime.ContextKeyModelTier, llmruntime.ModelTierRetrieval),
			llmruntime.ContextKeyCacheScope, llmruntime.CacheScopeGlobal,
		),
		rc.GetSecurityContext(),
		rc.GetLogger(),
		rc.GetTracer(),
		rc.GetMeter(),
	)

	result, err := llmruntime.GenerateAndTrackLLMContent(
		judgeCtx, userID, accountID, conversationID, messageID,
		"kb_rerank", false, messages, true,
		llms.WithTemperature(0.0), llmruntime.WithThinkingLevel(llmruntime.ThinkingLevelFastTask),
	)
	if err != nil || result == nil || len(result.Choices) == 0 {
		// Fail open: the caller keeps the order rag-server gave it. A judge
		// outage must not look like "nothing was relevant".
		rc.GetLogger().Warn("kb judge: scoring unavailable, keeping retrieval order", "error", err)
		return none, false
	}
	return parseKBJudgeVerdict(result.Choices[0].Content, len(chunks))
}

// parseKBJudgeVerdict extracts the verdict from the model's reply.
//
// A "best" of 0 is a real keep-none and must be distinguished from unparseable
// output, because the two mean opposite things to the caller: one is "nothing
// here is relevant", the other is "I have no verdict, keep what you had". The
// last usable object wins, so a model that reasons in JSON before concluding is
// read by its conclusion.
func parseKBJudgeVerdict(content string, n int) (KBRerankVerdict, bool) {
	none := KBRerankVerdict{Keep: KBRerankNone}
	matches := kbJudgeObjectRe.FindAllString(content, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		var raw struct {
			// Pointer, so a missing "best" is detectable rather than defaulting
			// to 0 — which is a meaningful value here, not a zero value.
			// json.Number so a quoted number or "3.0" still parses.
			Best  *json.Number `json:"best"`
			Score *json.Number `json:"score"`
		}
		if err := json.Unmarshal([]byte(matches[i]), &raw); err != nil || raw.Best == nil {
			continue
		}
		best, ok := kbJudgeInt(*raw.Best)
		if !ok {
			continue
		}
		if best == 0 {
			return none, true
		}
		if best < 1 || best > n {
			// More likely a hallucinated number than a deliberate verdict, and
			// dropping every candidate on it would be the expensive mistake.
			continue
		}
		var score float64
		if raw.Score != nil {
			if f, err := raw.Score.Float64(); err == nil {
				score = math.Max(0, math.Min(1, f))
			}
		}
		return KBRerankVerdict{Keep: best - 1, Score: score}, true
	}
	return none, false
}

// kbJudgeInt reads a candidate number that may arrive as 3, "3" or 3.0.
func kbJudgeInt(num json.Number) (int, bool) {
	f, err := num.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
		return 0, false
	}
	return int(f), true
}
