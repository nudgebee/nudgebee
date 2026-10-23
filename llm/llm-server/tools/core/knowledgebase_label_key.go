package core

import "strings"

// LabelKey is the key a page's classifier LABEL row (llm_kb_capability_entries,
// entry_type 'kb_document') is stored under: DocumentKey for an integration
// collection, and the collection's own name for a manual knowledge base
// ("kb_<id>"), which is one document. A mark's key always contains "|" and is
// never made for "kb_*", so the two cannot be confused. "" when nothing can be
// keyed. Mirrored by rag-server's document_key.label_key; both are pinned to
// docs/fixtures/kb_document_keys.json.
func LabelKey(collection, sourceID string, docURL *string) string {
	collection = strings.TrimSpace(collection)
	if strings.HasPrefix(collection, "kb_") {
		return collection
	}
	return DocumentKey(collection, sourceID, docURL)
}
