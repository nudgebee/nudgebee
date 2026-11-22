package knowledge

import "testing"

func candidateStage(t *testing.T, search KnowledgeSearch, ref string) KnowledgeSearchDocument {
	t.Helper()
	for _, doc := range search.Documents {
		if doc.Snippet == ref {
			return doc
		}
	}
	t.Fatalf("no candidate with snippet %q", ref)
	return KnowledgeSearchDocument{}
}

func TestBuildKnowledgeSearchStages(t *testing.T) {
	// Ranking drops are decided by rag-server before anything judges relevance,
	// so they must not inherit wording from the verdict.
	t.Run("ranking drops never borrow the verdict's wording", func(t *testing.T) {
		search := buildKnowledgeSearch("q", 3, judgeVerdictKept, "winner", "", []traceCandidate{
			{Ref: "winner", Snippet: "winner", Status: stageReturned},
			{Ref: "sibling", Snippet: "sibling", Status: stageDroppedSibling},
			{Ref: "outranked", Snippet: "outranked", Status: stageDroppedRank},
		})

		if got := candidateStage(t, search, "sibling"); got.Stage != stageDroppedSibling || got.Why != whySamePage {
			t.Errorf("sibling: got stage %q why %q", got.Stage, got.Why)
		}
		if got := candidateStage(t, search, "outranked"); got.Stage != stageDroppedRank || got.Why != whyOutranked {
			t.Errorf("outranked: got stage %q why %q", got.Stage, got.Why)
		}
		if got := candidateStage(t, search, "winner"); got.Stage != stageAdded || got.Why != whyPicked {
			t.Errorf("winner: got stage %q why %q", got.Stage, got.Why)
		}
		if search.Added != 1 || search.Returned != 1 || search.Scored != 3 {
			t.Errorf("funnel: scored=%d returned=%d added=%d", search.Scored, search.Returned, search.Added)
		}
	})

	t.Run("judge kept another search's document", func(t *testing.T) {
		search := buildKnowledgeSearch("q", 1, judgeVerdictKept, "elsewhere", "", []traceCandidate{
			{Ref: "mine", Snippet: "mine", Status: stageReturned},
		})
		got := candidateStage(t, search, "mine")
		if got.Stage != stageReturned || got.Why != whyOtherPicked {
			t.Errorf("got stage %q why %q", got.Stage, got.Why)
		}
		if search.Outcome != outcomeElsewhere {
			t.Errorf("outcome = %q", search.Outcome)
		}
	})

	t.Run("judge found nothing relevant", func(t *testing.T) {
		search := buildKnowledgeSearch("q", 1, judgeVerdictNone, "", "", []traceCandidate{
			{Ref: "a", Snippet: "a", Status: stageReturned},
		})
		if got := candidateStage(t, search, "a"); got.Stage != stageReturned || got.Why != whyNotAnswer {
			t.Errorf("got stage %q why %q", got.Stage, got.Why)
		}
	})

	// Fail-open: the whole shortlist reaches the agent, and the reader is told
	// it was never checked rather than being shown an invented winner.
	t.Run("no judge means everything returned was added, unchecked", func(t *testing.T) {
		for _, verdict := range []string{"", judgeVerdictUnavailable} {
			search := buildKnowledgeSearch("q", 2, verdict, "", "", []traceCandidate{
				{Ref: "a", Snippet: "a", Status: stageReturned},
				{Ref: "b", Snippet: "b", Status: stageDroppedRank},
			})
			got := candidateStage(t, search, "a")
			if got.Stage != stageAdded || got.Why != whyUnchecked {
				t.Errorf("verdict %q: got stage %q why %q", verdict, got.Stage, got.Why)
			}
			if search.Added != 1 {
				t.Errorf("verdict %q: added = %d", verdict, search.Added)
			}
		}
	})

	t.Run("no knowledge base in scope", func(t *testing.T) {
		search := buildKnowledgeSearch("q", 0, "", "", "no_collections", nil)
		if search.Outcome != outcomeNothing || search.Summary != summaryNoKBs {
			t.Errorf("outcome %q summary %q", search.Outcome, search.Summary)
		}
	})
}

func TestWithActivityTotalsCountsDistinctDocuments(t *testing.T) {
	// The pre-step fans out over several collections, so one page comes back
	// from more than one search; counting per search reported it repeatedly.
	activity := KnowledgeActivity{Searches: []KnowledgeSearch{
		buildKnowledgeSearch("q1", 1, judgeVerdictKept, "same", "", []traceCandidate{
			{Ref: "same", Url: "http://doc", Snippet: "same", Status: stageReturned},
		}),
		buildKnowledgeSearch("q2", 2, judgeVerdictKept, "other", "", []traceCandidate{
			{Ref: "same", Url: "http://doc", Snippet: "same", Status: stageReturned},
			{Ref: "extra", Url: "http://extra", Snippet: "extra", Status: stageDroppedRank},
		}),
	}}

	got := withActivityTotals(activity)
	if got.Scored != 2 {
		t.Errorf("scored = %d, want 2 distinct documents", got.Scored)
	}
	// Added anywhere means added: the second search reported the same document
	// as merely returned, which must not demote it.
	if got.Added != 1 {
		t.Errorf("added = %d, want 1", got.Added)
	}
	if got.Returned != 1 {
		t.Errorf("returned = %d, want 1", got.Returned)
	}
}
