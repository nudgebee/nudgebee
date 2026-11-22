package knowledge

import (
	"strings"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// JudgeKBResults keeps the single best knowledge-base result, or none.
//
// It pairs with rag-server's ranked mode. That side stops filtering and returns
// its best documents ranked, because the score it ranks on is normalised per
// query and so cannot carry an absolute relevance decision — its top candidate
// scores highest whether or not anything relevant was found. This side makes
// that decision, with the question in front of it.
//
// Returning one result rather than a filtered list is deliberate: on 300
// production questions, 177 had no answer anywhere in the corpus, so the common
// correct outcome is nothing at all, and the second-best of a bad set is noise.
//
// Fail-open in every direction except a deliberate verdict: disabled, no
// candidates, or a judge that could not run all return the input untouched. A
// deliberate keep-none verdict returns nothing, because "none of these are
// relevant" is an answer.
// traceIDs name the llm_retrieval_trace rows the candidates came from, so the
// verdict can be recorded against them. Variadic because several callers make a
// single un-traced search; those still judge, they just record nothing.
func JudgeKBResults(
	rc *security.RequestContext,
	accountID, userID, conversationID, messageID, query string,
	results RAGSearchResults,
	traceIDs ...string,
) RAGSearchResults {
	if !config.Config.KBRerankEnabled || KBRerankFn == nil || len(results) == 0 {
		return results
	}
	if rc == nil || strings.TrimSpace(query) == "" {
		return results
	}

	// Judge the chunk that earned each document its place, not the document.
	// The chunk is what the retrieval score refers to, and whole documents run
	// to tens of thousands of characters — putting ten of them into a call whose
	// job is to pick one would cost more than the search it is filtering.
	chunks := make([]string, len(results))
	for i, r := range results {
		chunk := r.Chunk
		if chunk == "" {
			// Threshold mode, or a collection indexed before chunk and document
			// were separate fields. The document is the only text there is.
			chunk = r.Document
		}
		chunks[i] = chunk
	}

	started := time.Now()
	verdict, ok := KBRerankFn(rc, accountID, userID, conversationID, messageID, query, chunks)
	elapsed := time.Since(started)
	if !ok {
		rc.GetLogger().Debug("kb judge: no verdict, keeping retrieval order", "candidates", len(results))
		recordJudgeVerdict(rc, traceIDs, judgeVerdictUnavailable, "", 0, elapsed)
		return results
	}
	if verdict.Keep == KBRerankNone {
		rc.GetLogger().Info("kb judge: nothing relevant, dropping all candidates", "candidates", len(results))
		recordJudgeVerdict(rc, traceIDs, judgeVerdictNone, "", 0, elapsed)
		return RAGSearchResults{}
	}
	if verdict.Keep < 0 || verdict.Keep >= len(results) {
		// Defended in the parser too; a bad index here would panic rather than
		// degrade, so it fails open instead.
		return results
	}
	if verdict.Score < config.Config.KBRerankMinScore {
		rc.GetLogger().Info("kb judge: best candidate below cutoff, dropping all",
			"score", verdict.Score, "cutoff", config.Config.KBRerankMinScore, "candidates", len(results))
		// Below the cutoff is still a rejection, not an outage.
		recordJudgeVerdict(rc, traceIDs, judgeVerdictNone, "", 0, elapsed)
		return RAGSearchResults{}
	}
	kept := results[verdict.Keep]
	rc.GetLogger().Info("kb judge: kept one of the candidates",
		"score", verdict.Score, "index", verdict.Keep, "candidates", len(results))
	recordJudgeVerdict(rc, traceIDs, judgeVerdictKept, parentRef(kept), verdict.Score, elapsed)
	return RAGSearchResults{kept}
}

// parentRef is the identity rag-server records as a candidate's “ref“: the
// parent document id written by ingest, falling back to a prefix of the chunk
// for documents indexed before that existed. Kept in step with
// rag/core/llm/ranking.py::parent_key, so judge_kept_ref matches exactly one
// candidate in the trace.
func parentRef(r RAGSearchResult) string {
	if id, ok := r.Metadata["parent_id"].(string); ok && id != "" {
		return id
	}
	text := r.Chunk
	if text == "" {
		text = r.Document
	}
	if len(text) > 200 {
		return text[:200]
	}
	return text
}
