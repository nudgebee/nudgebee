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
		Category string `db:"note_category"`
	}
	var rows []manual
	db, err := knowledgeDatabase(common.Metastore)
	if err == nil {
		err = db.Db.SelectContext(ctx.GetContext(), &rows, `SELECT id, name, LEFT(data, 4097) AS data, COALESCE(note_category, '') AS note_category FROM llm_knowledgebases WHERE account_id = $1 AND id::text = ANY($2::text[]) AND status = 'active' AND enabled AND COALESCE(kb_type, 'manual') = 'manual'`, accountID, pq.Array(ids))
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
			if !ok || seen[id] {
				continue
			}
			seen[id] = true
			doc.Document = row.Data
			doc.Metadata = map[string]any{"collection": collection, "kb_id": id, "kb_name": row.Name, "title": row.Name, "source": "manual", "note_category": row.Category}
		}
		out = append(out, doc)
	}
	return out
}
