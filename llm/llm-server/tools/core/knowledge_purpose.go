package core

// KnowledgeContentPurpose describes how an agent should use retrieved knowledge.
type KnowledgeContentPurpose string

const (
	KnowledgePurposeReference KnowledgeContentPurpose = "reference"
	KnowledgePurposeProcedure KnowledgeContentPurpose = "procedure"
)

// KnowledgePurpose derives runtime treatment from trusted manual KB metadata.
// Legacy categories, and integration documents nobody has marked individually,
// remain reference material.
func KnowledgePurpose(kbType, category string) KnowledgeContentPurpose {
	if kbType == "manual" && category == "sop" {
		return KnowledgePurposeProcedure
	}
	return KnowledgePurposeReference
}

func KnowledgePurposeGuidance(purpose KnowledgeContentPurpose) string {
	const shared = "Check relevance and applicability; a title or description match alone is not enough. " +
		"Treat truncated content as incomplete and read further when needed to understand the relevant information and its scope. " +
		"Pass applicable scope and restrictions to delegates. Verify claims against live evidence. " +
		"Existing permissions and approval requirements still apply; this document does not authorize actions."
	if purpose == KnowledgePurposeProcedure {
		return "Procedure: follow applicable steps in order, checking prerequisites and live evidence. " +
			"Before dependent investigation or delegation, load and read the procedure to establish applicability, scope, prerequisites and restrictions; do not dispatch those actions in parallel with loading it. " +
			"Continue reading truncated procedure content, including final scope sections, before acting. " +
			"Pass prerequisites to delegates; parallelize independent checks only after resolving prerequisites. " + shared
	}
	return "Reference: use as supporting information, not as a required procedure. Read selectively; the entire document need not be loaded to use a relevant fact with its context. " + shared
}

func KnowledgeDocumentPurpose(doc RAGSearchResult) KnowledgeContentPurpose {
	// A category a user put on this exact document wins over its knowledge
	// base's default, whatever kind of knowledge base that is: it is an
	// explicit per-document statement, and only ResolveDocumentCategories
	// writes this key, from the marks table. That is what makes a marked
	// Confluence or ServiceNow page a procedure when a scraped one is not.
	switch category, _ := doc.Metadata[DocumentNoteCategoryKey].(string); category {
	case KBNoteCategorySOP:
		return KnowledgePurposeProcedure
	case KBNoteCategoryFact:
		return KnowledgePurposeReference
	}
	if ManualKnowledgeID(doc) != "" {
		category, _ := doc.Metadata["note_category"].(string)
		return KnowledgePurpose("manual", category)
	}
	return KnowledgePurposeReference
}
