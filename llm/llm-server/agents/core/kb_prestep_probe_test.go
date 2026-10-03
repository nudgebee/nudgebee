package core

import (
	"testing"

	toolcore "nudgebee/llm/tools/core"
)

func probeDoc(url, title, text string, score float32) toolcore.RAGSearchResult {
	return toolcore.RAGSearchResult{
		Document:        text,
		SimilarityScore: score,
		Metadata:        map[string]any{"url": url, "title": title, "source": "confluence"},
	}
}

// The probe must classify exactly as the pre-step does: a document attribution
// kept is injected and carries its owning KB; one attribution dropped is
// reported as dropped, NOT as "below a score cutoff" (there is no such cutoff).
func TestBuildProbeDocumentsClassifiesInjectedAndDropped(t *testing.T) {
	owned := probeDoc("https://kb/a", "Never auto-restart the auth service", "body a", 0.85)
	orphan := probeDoc("https://kb/b", "Change freeze during peak hours", "body b", 0.84)

	kept := toolcore.RAGSearchResults{owned, orphan}
	attributed := toolcore.RAGSearchResults{owned}
	refs := []AgentReference{{
		Type:        AgentReferenceTypeKB,
		ReferenceID: "kb-123:deadbeef",
		Metadata:    map[string]any{"name": "SRE Runbooks", "kb_id": "kb-123"},
	}}

	docs, matched := buildProbeDocuments(kept, refs, attributed)

	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}

	if !docs[0].Injected {
		t.Errorf("owned document should be injected")
	}
	if docs[0].KBName != "SRE Runbooks" {
		t.Errorf("expected owning KB name from ref metadata, got %q", docs[0].KBName)
	}
	if docs[0].KBId != "kb-123" {
		t.Errorf("kb id must come from metadata, not the composite ReferenceID; got %q", docs[0].KBId)
	}
	if docs[0].DropReason != "" {
		t.Errorf("injected document must carry no drop reason, got %q", docs[0].DropReason)
	}

	if docs[1].Injected {
		t.Errorf("unattributable document should not be injected")
	}
	if docs[1].DropReason != dropReasonUnattributable {
		t.Errorf("expected the fail-closed drop reason, got %q", docs[1].DropReason)
	}
	// A high score must NOT rescue an unattributable document — scores decide
	// nothing at this stage.
	if docs[1].Score != 0.84 {
		t.Errorf("expected the real score to be reported, got %v", docs[1].Score)
	}

	if _, ok := matched["kb-123"]; !ok {
		t.Errorf("kb-123 contributed a document and must be marked matched")
	}
	if len(matched) != 1 {
		t.Errorf("expected exactly one matched KB, got %d", len(matched))
	}
}

// Un-owned global/tenant collection documents are injected but belong to no KB
// row, so they must not invent a kb id or mark any KB as matched.
func TestBuildProbeDocumentsUnownedDocumentHasNoKBId(t *testing.T) {
	doc := probeDoc("https://docs/product", "Product docs page", "body", 0.9)
	refs := []AgentReference{{
		Type:        AgentReferenceTypeKB,
		ReferenceID: "global:confluence:abcd",
		Metadata:    map[string]any{"name": "Product documentation"},
	}}

	docs, matched := buildProbeDocuments(toolcore.RAGSearchResults{doc}, refs, toolcore.RAGSearchResults{doc})

	if !docs[0].Injected {
		t.Fatalf("un-owned document is still injected")
	}
	if docs[0].KBId != "" {
		t.Errorf("un-owned document must carry no kb id, got %q", docs[0].KBId)
	}
	if len(matched) != 0 {
		t.Errorf("un-owned document must not mark any KB as matched, got %d", len(matched))
	}
}

// Duplicate-collapsed and mismatched slices must not panic the probe.
func TestBuildProbeDocumentsSurvivesRefAttributedDrift(t *testing.T) {
	doc := probeDoc("https://kb/a", "A", "body a", 0.8)
	// attributed claims the doc, but refs is empty (defensive: production keeps
	// them in lockstep).
	docs, _ := buildProbeDocuments(toolcore.RAGSearchResults{doc}, nil, toolcore.RAGSearchResults{doc})
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	if docs[0].Injected {
		t.Errorf("without a ref the document cannot be reported as injected")
	}
}

// A knowledge base attribution can never credit must not be reported the same
// way as one that competed and lost. matchKB skips every non-active candidate,
// so archived KBs and failed loads left in "error" are ineligible regardless of
// the question. Shapes mirror a real dev account, where 11 of 29 candidates
// were non-active.
func TestSummarizeCandidatesMarksNonActiveIneligible(t *testing.T) {
	candidates := []toolcore.Knowledgebase{
		{Id: "kb-active-hit", Name: "crashloop_triage_runbook", KBType: "manual", Status: "active", Enabled: true},
		{Id: "kb-active-miss", Name: "office_meeting_rooms", KBType: "manual", Status: "active", Enabled: true},
		{Id: "kb-archived", Name: "dev-confluence", KBType: "integration", Status: "archived"},
		{Id: "kb-errored", Name: "incident_report", KBType: "manual", Status: "error"},
		// Indexed fine, but the user switched it off — attribution skips it, so
		// the panel must not imply it competed and lost.
		{Id: "kb-disabled", Name: "retired_runbook", KBType: "manual", Status: "active", Enabled: false},
	}
	matched := map[string]struct{}{"kb-active-hit": {}}

	got := summarizeCandidates(candidates, matched)
	if len(got) != 5 {
		t.Fatalf("expected every candidate reported, got %d", len(got))
	}

	byId := make(map[string]KBRetrievalProbeKB, len(got))
	for _, c := range got {
		byId[c.Id] = c
	}

	if !byId["kb-active-hit"].Eligible || !byId["kb-active-hit"].Matched {
		t.Errorf("an active KB that contributed must be eligible and matched")
	}
	if !byId["kb-active-miss"].Eligible || byId["kb-active-miss"].Matched {
		t.Errorf("an active KB that contributed nothing is eligible but unmatched")
	}
	for _, id := range []string{"kb-archived", "kb-errored", "kb-disabled"} {
		if byId[id].Eligible {
			t.Errorf("%s (status %q) can never attribute and must be ineligible", id, byId[id].Status)
		}
		if byId[id].Matched {
			t.Errorf("%s must never be reported as matched", id)
		}
	}
}

// A KB with no id cannot be attributed to either - guard mirrors matchKB.
func TestSummarizeCandidatesTreatsMissingIdAsIneligible(t *testing.T) {
	got := summarizeCandidates([]toolcore.Knowledgebase{{Id: "", Name: "orphan", Status: "active"}}, nil)
	if got[0].Eligible {
		t.Errorf("a candidate with no id must be ineligible")
	}
}

// kbCollectionName must stay the exact inverse of classifyCollection — the two
// spell the same naming scheme from opposite ends, and a drift would send the
// scoped probe at a collection that does not exist (silently: an empty result
// reads as "this KB has nothing relevant").
func TestKBCollectionNameRoundTripsClassifyCollection(t *testing.T) {
	integrationId := "9f1c2e3d-0000-4444-8888-aaaabbbbcccc"
	cases := []struct {
		name string
		kb   toolcore.Knowledgebase
		want string
	}{
		{"manual", toolcore.Knowledgebase{Id: "kb-uuid-1", KBType: "manual"}, "kb_kb-uuid-1"},
		{"integration", toolcore.Knowledgebase{Id: "kb-uuid-2", KBType: "integration", IntegrationId: &integrationId},
			integrationId + "_knowledge_base"},
	}
	for _, tc := range cases {
		got, ok := kbCollectionName(tc.kb)
		if !ok || got != tc.want {
			t.Fatalf("%s: got (%q, %v), want %q", tc.name, got, ok, tc.want)
		}
		gotKB, gotIntegration := classifyCollection(got)
		switch tc.kb.KBType {
		case "manual":
			if gotKB != tc.kb.Id {
				t.Errorf("%s: classifyCollection(%q) kb id = %q, want %q", tc.name, got, gotKB, tc.kb.Id)
			}
		default:
			if gotIntegration != integrationId {
				t.Errorf("%s: classifyCollection(%q) integration id = %q, want %q", tc.name, got, gotIntegration, integrationId)
			}
		}
	}
}

// An integration KB with no integration id owns no collection of its own.
func TestKBCollectionNameRejectsIntegrationWithoutId(t *testing.T) {
	if _, ok := kbCollectionName(toolcore.Knowledgebase{Id: "kb-1", KBType: "integration"}); ok {
		t.Errorf("an integration KB with no integration id has no collection")
	}
	empty := "  "
	if _, ok := kbCollectionName(toolcore.Knowledgebase{Id: "kb-1", KBType: "integration", IntegrationId: &empty}); ok {
		t.Errorf("a blank integration id has no collection")
	}
}

// The scoped probe must stay truthful against an older rag-server that treats
// collection_name as additive and answers from every collection.
func TestFilterDocsToCollectionDropsForeignCollections(t *testing.T) {
	mine := toolcore.RAGSearchResult{Document: "mine", Metadata: map[string]any{"collection": "kb_wanted"}}
	theirs := toolcore.RAGSearchResult{Document: "theirs", Metadata: map[string]any{"collection": "kb_other"}}
	untagged := toolcore.RAGSearchResult{Document: "untagged", Metadata: map[string]any{}}

	got := filterDocsToCollection(toolcore.RAGSearchResults{mine, theirs, untagged}, "kb_wanted")

	if len(got) != 2 {
		t.Fatalf("expected the scoped doc and the untagged one, got %d", len(got))
	}
	for _, doc := range got {
		if doc.Document == "theirs" {
			t.Errorf("a document from another collection must not survive a scoped probe")
		}
	}
	// Untagged documents are kept deliberately: the tag is added by rag-server's
	// search layer, so dropping them would discard content if it stops stamping.
	if got[1].Document != "untagged" {
		t.Errorf("untagged documents must be kept, got %q", got[1].Document)
	}
}
