package core

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestKnowledgePurposeCompatibility(t *testing.T) {
	for _, tc := range []struct {
		kind, category string
		want           KnowledgeContentPurpose
	}{
		{"manual", "sop", KnowledgePurposeProcedure}, {"manual", "fact", KnowledgePurposeReference},
		{"manual", "", KnowledgePurposeReference}, {"manual", "rule", KnowledgePurposeReference},
		{"manual", "policy", KnowledgePurposeReference}, {"integration", "sop", KnowledgePurposeReference},
	} {
		t.Run(tc.kind+"/"+tc.category, func(t *testing.T) {
			require.Equal(t, tc.want, KnowledgePurpose(tc.kind, tc.category))
		})
	}
	// External metadata cannot promote an integration article to a procedure.
	doc := RAGSearchResult{Metadata: map[string]any{"collection": "confluence_knowledge_base", "kb_id": "fake", "note_category": "sop"}}
	var c KnowledgeCandidate
	SetKnowledgeDocumentHandle(&c, doc)
	require.Equal(t, KnowledgePurposeReference, c.Purpose)
	require.Contains(t, KnowledgePurposeGuidance(KnowledgePurposeProcedure), "does not authorize actions")
}

func TestMappedSkillPurpose(t *testing.T) {
	for _, kind := range []string{"manual", "integration"} {
		block, _ := renderSkillsBlock([]agentSkillRow{{Name: "example", Data: "body", KBType: kind, NoteCategory: "sop"}})
		require.Contains(t, block, "<guidance>"+KnowledgePurposeGuidance(KnowledgePurpose(kind, "sop"))+"</guidance>")
	}
}
