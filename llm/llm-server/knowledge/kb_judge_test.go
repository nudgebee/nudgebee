package knowledge

import (
	"strings"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// The distinction these tests exist to protect: a judge that could not run and a
// judge that found nothing relevant mean opposite things. Confusing them either
// blanks the knowledge base on an outage or lets noise through on a deliberate
// suppression.

func kbResults(n int) RAGSearchResults {
	out := make(RAGSearchResults, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, RAGSearchResult{
			Document:        "parent document " + string(rune('A'+i)),
			Chunk:           "chunk " + string(rune('A'+i)),
			SimilarityScore: float32(n-i) / float32(n),
		})
	}
	return out
}

func withKBRerank(t *testing.T, enabled bool, minScore float64, fn func(
	rc *security.RequestContext, accountID, userID, conversationID, messageID, query string, chunks []string,
) (KBRerankVerdict, bool)) {
	t.Helper()
	prevEnabled, prevMin, prevFn := config.Config.KBRerankEnabled, config.Config.KBRerankMinScore, KBRerankFn
	config.Config.KBRerankEnabled, config.Config.KBRerankMinScore, KBRerankFn = enabled, minScore, fn
	t.Cleanup(func() {
		config.Config.KBRerankEnabled, config.Config.KBRerankMinScore, KBRerankFn = prevEnabled, prevMin, prevFn
	})
}

func rc() *security.RequestContext { return security.NewRequestContextForSuperAdmin() }

func TestJudgeKBResultsDisabledPassesEverythingThrough(t *testing.T) {
	withKBRerank(t, false, 0, func(*security.RequestContext, string, string, string, string, string, []string) (KBRerankVerdict, bool) {
		t.Fatal("judge must not be called when the flag is off")
		return KBRerankVerdict{}, false
	})
	in := kbResults(3)
	if got := JudgeKBResults(rc(), "a", "u", "c", "m", "why", in); len(got) != 3 {
		t.Fatalf("want 3 results untouched, got %d", len(got))
	}
}

func TestJudgeKBResultsKeepsOnlyTheChosenOne(t *testing.T) {
	withKBRerank(t, true, 0, func(*security.RequestContext, string, string, string, string, string, []string) (KBRerankVerdict, bool) {
		return KBRerankVerdict{Keep: 2, Score: 0.9}, true
	})
	got := JudgeKBResults(rc(), "a", "u", "c", "m", "why", kbResults(3))
	if len(got) != 1 {
		t.Fatalf("want exactly 1 result, got %d", len(got))
	}
	if got[0].Chunk != "chunk C" {
		t.Fatalf("want the chosen candidate (chunk C), got %q", got[0].Chunk)
	}
}

func TestJudgeKBResultsKeepNoneSuppressesEverything(t *testing.T) {
	// "None of these are relevant" is an answer, and the correct one for most
	// production questions — it must not be treated as a failure to decide.
	withKBRerank(t, true, 0, func(*security.RequestContext, string, string, string, string, string, []string) (KBRerankVerdict, bool) {
		return KBRerankVerdict{Keep: KBRerankNone}, true
	})
	if got := JudgeKBResults(rc(), "a", "u", "c", "m", "why", kbResults(3)); len(got) != 0 {
		t.Fatalf("want nothing kept, got %d", len(got))
	}
}

func TestJudgeKBResultsFailsOpenWhenTheJudgeCannotRun(t *testing.T) {
	// The opposite case: no verdict must leave the retrieval order alone, or a
	// judge outage silently empties the knowledge base.
	withKBRerank(t, true, 0, func(*security.RequestContext, string, string, string, string, string, []string) (KBRerankVerdict, bool) {
		return KBRerankVerdict{Keep: KBRerankNone}, false
	})
	if got := JudgeKBResults(rc(), "a", "u", "c", "m", "why", kbResults(3)); len(got) != 3 {
		t.Fatalf("want all 3 kept on a judge miss, got %d", len(got))
	}
}

func TestJudgeKBResultsAppliesTheCutoff(t *testing.T) {
	withKBRerank(t, true, 0.7, func(*security.RequestContext, string, string, string, string, string, []string) (KBRerankVerdict, bool) {
		return KBRerankVerdict{Keep: 1, Score: 0.55}, true
	})
	if got := JudgeKBResults(rc(), "a", "u", "c", "m", "why", kbResults(3)); len(got) != 0 {
		t.Fatalf("want nothing kept below the cutoff, got %d", len(got))
	}
}

func TestJudgeKBResultsJudgesChunksNotDocuments(t *testing.T) {
	// Whole documents run to tens of thousands of characters; the chunk is what
	// the retrieval score refers to and what the judge should read.
	var seen []string
	withKBRerank(t, true, 0, func(_ *security.RequestContext, _, _, _, _, _ string, chunks []string) (KBRerankVerdict, bool) {
		seen = chunks
		return KBRerankVerdict{Keep: 0, Score: 1}, true
	})
	JudgeKBResults(rc(), "a", "u", "c", "m", "why", kbResults(2))
	if len(seen) != 2 || seen[0] != "chunk A" {
		t.Fatalf("want the chunks passed to the judge, got %v", seen)
	}
}

func TestJudgeKBResultsFallsBackToDocumentWhenThereIsNoChunk(t *testing.T) {
	// Threshold mode, and collections indexed before chunk and document were
	// separate fields: the document is the only text available.
	var seen []string
	withKBRerank(t, true, 0, func(_ *security.RequestContext, _, _, _, _, _ string, chunks []string) (KBRerankVerdict, bool) {
		seen = chunks
		return KBRerankVerdict{Keep: 0, Score: 1}, true
	})
	JudgeKBResults(rc(), "a", "u", "c", "m", "why", RAGSearchResults{{Document: "only the document"}})
	if len(seen) != 1 || seen[0] != "only the document" {
		t.Fatalf("want the document used as the chunk, got %v", seen)
	}
}

func TestJudgeKBResultsIgnoresAnOutOfRangeChoice(t *testing.T) {
	// Defended in the parser as well; here it must degrade rather than panic.
	withKBRerank(t, true, 0, func(*security.RequestContext, string, string, string, string, string, []string) (KBRerankVerdict, bool) {
		return KBRerankVerdict{Keep: 99, Score: 1}, true
	})
	if got := JudgeKBResults(rc(), "a", "u", "c", "m", "why", kbResults(2)); len(got) != 2 {
		t.Fatalf("want fail-open on a bad index, got %d", len(got))
	}
}

func TestJudgeKBResultsHandlesNoCandidatesAndNoQuery(t *testing.T) {
	withKBRerank(t, true, 0, func(*security.RequestContext, string, string, string, string, string, []string) (KBRerankVerdict, bool) {
		t.Fatal("judge must not be called with nothing to judge")
		return KBRerankVerdict{}, false
	})
	if got := JudgeKBResults(rc(), "a", "u", "c", "m", "why", RAGSearchResults{}); len(got) != 0 {
		t.Fatalf("want empty in, empty out, got %d", len(got))
	}
	if got := JudgeKBResults(rc(), "a", "u", "c", "m", "   ", kbResults(2)); len(got) != 2 {
		t.Fatalf("want results untouched when there is no question, got %d", len(got))
	}
}

func TestJudgeKBResultsPassesTraceIDsWithoutRequiringThem(t *testing.T) {
	// The write-back is best-effort and several callers search without a trace
	// id; judging must behave identically either way, so a missing id can never
	// change which document reaches the prompt.
	withKBRerank(t, true, 0, func(*security.RequestContext, string, string, string, string, string, []string) (KBRerankVerdict, bool) {
		return KBRerankVerdict{Keep: 1, Score: 0.8}, true
	})
	withIDs := JudgeKBResults(rc(), "a", "u", "c", "m", "why", kbResults(3), "trace-1", "trace-2")
	withoutIDs := JudgeKBResults(rc(), "a", "u", "c", "m", "why", kbResults(3))
	if len(withIDs) != 1 || len(withoutIDs) != 1 {
		t.Fatalf("want one result either way, got %d and %d", len(withIDs), len(withoutIDs))
	}
	if withIDs[0].Chunk != withoutIDs[0].Chunk {
		t.Fatalf("trace ids must not change the choice: %q vs %q", withIDs[0].Chunk, withoutIDs[0].Chunk)
	}
}

func TestParentRefPrefersTheIngestParentID(t *testing.T) {
	// judge_kept_ref has to match the ref rag-server wrote on the candidate, or
	// the trace records a winner that matches nothing.
	withParent := RAGSearchResult{Chunk: "chunk", Metadata: map[string]any{"parent_id": "doc-42"}}
	if got := parentRef(withParent); got != "doc-42" {
		t.Fatalf("want the parent id, got %q", got)
	}
	// Legacy chunks carry no parent_id; rag-server falls back to 200 chars of
	// the text and so must this.
	long := RAGSearchResult{Chunk: strings.Repeat("x", 250)}
	if got := parentRef(long); len(got) != 200 {
		t.Fatalf("want a 200-char fallback, got %d chars", len(got))
	}
	// Threshold mode has no Chunk at all.
	if got := parentRef(RAGSearchResult{Document: "only doc"}); got != "only doc" {
		t.Fatalf("want the document used when there is no chunk, got %q", got)
	}
}
