package knowledge

import (
	"nudgebee/llm/security"
)

// KBRerankNone is the Keep value meaning no candidate is worth using.
const KBRerankNone = -1

// KBRerankVerdict is the judge's decision about a set of knowledge-base
// candidates: which single one to keep, and how well it answers the question.
//
// Keep indexes the slice that was handed to the judge, or is KBRerankNone.
type KBRerankVerdict struct {
	Keep  int
	Score float64
}

// KBRerankFn judges which knowledge-base candidate answers the query, and is
// set to llmJudgeKBChunks (kb_rerank.go) in init. Tests replace it.
//
// Candidates are the matched chunks rather than whole documents: the chunk is
// what the retrieval score refers to, and scoring whole documents would put tens
// of thousands of characters into a call whose only job is to pick one of them.
//
// Returns (verdict, true) when the judge decided, including a Keep of
// KBRerankNone, which is a deliberate "none of these are relevant". (_, false)
// means the judge could not run, and the caller must fall back to the order it
// already had rather than treating the absence of a verdict as a verdict.
var KBRerankFn func(
	rc *security.RequestContext,
	accountID, userID, conversationID, messageID, query string,
	chunks []string,
) (KBRerankVerdict, bool)
