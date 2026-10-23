package core

import (
	"context"
	"fmt"

	"github.com/lib/pq"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// kbDocumentLabel is what the classifier decided about one page: its kind
// ("sop"/"knowledge"), shape and the platform capabilities it references.
type kbDocumentLabel struct {
	Kind         string
	Shape        string
	Capabilities []string
}

// kbDocumentLabels returns label key -> the classifier's labels for those pages of one
// collection, read from Postgres (llm_kb_capability_entries, entry_type 'kb_document').
// Postgres owns these labels; the vector index is never asked. A page with no row is
// simply absent from the result. The keys are LabelKey values: the page's document_key
// for an integration collection, the collection's own name for a manual knowledge base.
//
// Callers pass only keys of a collection they have already authorized, so a row is
// never returned for a knowledge base the caller cannot see.
func kbDocumentLabels(ctx context.Context, collection string, keys []string) (map[string]kbDocumentLabel, error) {
	labels := map[string]kbDocumentLabel{}
	if collection == "" || len(keys) == 0 {
		return labels, nil
	}
	dbms, err := knowledgeDatabase(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("kb: failed to get database manager: %w", err)
	}
	var rows []struct {
		DocumentKey  string         `db:"document_key"`
		Kinds        pq.StringArray `db:"kinds"`
		Shapes       pq.StringArray `db:"shapes"`
		Capabilities pq.StringArray `db:"capabilities"`
	}
	err = dbms.Db.SelectContext(ctx, &rows,
		`SELECT document_key, kinds, shapes, capabilities FROM llm_kb_capability_entries
		 WHERE entry_type = 'kb_document' AND collection_name = $1 AND document_key = ANY($2::text[])`,
		collection, pq.Array(keys))
	if err != nil {
		return nil, fmt.Errorf("kb: failed to read document labels: %w", err)
	}
	for _, row := range rows {
		if len(row.Kinds) == 0 {
			continue
		}
		label := kbDocumentLabel{Kind: row.Kinds[0], Capabilities: []string(row.Capabilities)}
		if len(row.Shapes) > 0 {
			label.Shape = row.Shapes[0]
		}
		labels[row.DocumentKey] = label
	}
	return labels, nil
}

// applyKBDocumentLabels fills the classifier fields of a listing from Postgres. A failed
// read leaves the page unlabelled rather than failing the list: the documents are still
// worth seeing.
func applyKBDocumentLabels(sc *security.RequestContext, kbId, collection string, items []KBDocument) {
	keys := make([]string, 0, len(items))
	keyOf := make([]string, len(items))
	for i, item := range items {
		// A manual knowledge base is one document, labelled under its collection's name.
		key := item.DocumentKey
		if key == "" && item.Id == kbId {
			key = LabelKey(collection, "", nil)
		}
		keyOf[i] = key
		if key != "" {
			keys = append(keys, key)
		}
	}
	labels, err := kbDocumentLabels(sc.GetContext(), collection, keys)
	if err != nil {
		sc.GetLogger().Warn("kb: unable to read document labels", "error", err, "kb_id", kbId)
		return
	}
	for i := range items {
		label, ok := labels[keyOf[i]]
		if !ok || (label.Kind != "sop" && label.Kind != "knowledge") {
			continue
		}
		kind := label.Kind
		items[i].Kind = &kind
		if label.Shape != "" {
			shape := label.Shape
			items[i].Shape = &shape
		}
		items[i].Capabilities = label.Capabilities
	}
}
