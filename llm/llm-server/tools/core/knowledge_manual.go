package core

import (
	"strings"

	"github.com/lib/pq"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

var knowledgeDatabase = common.GetDatabaseManager

// ResolveManualKnowledge replaces vector chunks with the canonical active manual
// KB body. Collection IDs, never text or source filenames, establish ownership.
// Missing/disabled/foreign manual rows fail closed; external articles are kept.
func ResolveManualKnowledge(ctx *security.RequestContext, accountID string, docs RAGSearchResults) RAGSearchResults {
	var ids []string
	for _, doc := range docs {
		collection, _ := doc.Metadata["collection"].(string)
		if strings.HasPrefix(collection, "kb_") {
			ids = append(ids, strings.TrimPrefix(collection, "kb_"))
		}
	}
	if len(ids) == 0 {
		return docs
	}
	type manual struct {
		ID       string `db:"id"`
		Name     string `db:"name"`
		Data     string `db:"data"`
		Format   string `db:"data_format"`
		Category string `db:"note_category"`
	}
	var rows []manual
	db, err := knowledgeDatabase(common.Metastore)
	if err == nil {
		scope, scopeArgs := KBAccountScope("", 1, 3, VisibleOrgKnowledgebaseIDs(ctx.GetContext(), accountID))
		err = db.Db.SelectContext(ctx.GetContext(), &rows, `SELECT id, name, CASE WHEN data_format = 'pdf' THEN '' ELSE LEFT(data, 4097) END AS data, COALESCE(data_format, '') AS data_format, COALESCE(note_category, '') AS note_category FROM llm_knowledgebases WHERE `+scope+` AND id::text = ANY($2::text[]) AND status = 'active' AND enabled AND COALESCE(kb_type, 'manual') = 'manual'`, append([]any{accountID, pq.Array(ids)}, scopeArgs...)...)
	}
	if err != nil {
		ctx.GetLogger().Warn("knowledge: unable to resolve manual identities", "error", err)
	}
	byID := make(map[string]manual, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	seen := make(map[string]bool)
	out := make(RAGSearchResults, 0, len(docs))
	for _, doc := range docs {
		collection, _ := doc.Metadata["collection"].(string)
		if strings.HasPrefix(collection, "kb_") {
			id := strings.TrimPrefix(collection, "kb_")
			row, ok := byID[id]
			if !ok {
				continue
			}
			if row.Format == string(KBFormatPDF) {
				// A PDF's data column is base64 of the document, not a body. Its
				// hits are page and figure chunks, so each one is kept as the
				// indexed document it is and read back through its exact handle.
				// kb_id is deliberately not set: that would route the hit into
				// the manual path, which serves the data column. The PDF marker
				// carries the identity instead, so the note type still decides
				// whether the chunk is presented as a procedure.
				if strings.TrimSpace(doc.Document) == "" {
					doc.Document = PDFKnowledgePointer
				}
				metadata := make(map[string]any, len(doc.Metadata)+5)
				for k, v := range doc.Metadata {
					metadata[k] = v
				}
				delete(metadata, "kb_id")
				metadata[pdfKnowledgeIDKey] = id
				metadata["kb_name"], metadata["title"] = row.Name, row.Name
				metadata["source"], metadata["note_category"] = "manual", row.Category
				doc.Metadata = metadata
				out = append(out, doc)
				continue
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			doc.Document = row.Data
			// Overlay onto the existing metadata rather than replacing it outright:
			// Overlay, not replace: the Qdrant payload's kind/capabilities must
			// survive for consumers such as KnowledgeDocumentPurpose.
			metadata := make(map[string]any, len(doc.Metadata)+6)
			for k, v := range doc.Metadata {
				metadata[k] = v
			}
			metadata["collection"] = collection
			metadata["kb_id"] = id
			metadata["kb_name"] = row.Name
			metadata["title"] = row.Name
			metadata["source"] = "manual"
			metadata["note_category"] = row.Category
			doc.Metadata = metadata
		}
		out = append(out, doc)
	}
	return out
}
