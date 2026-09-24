package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lib/pq"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// Note categories a document can carry. Mirrors the CHECK constraint on
// llm_kb_document_categories.note_category.
const (
	KBNoteCategorySOP  = "sop"
	KBNoteCategoryFact = "fact"
)

// DocumentNoteCategoryKey is the metadata key carrying a category a user set on
// one document. Deliberately not "note_category": that key is written by the
// scrapers and by manual-KB resolution, so reusing it would let indexed content
// decide its own treatment. Only ResolveDocumentCategories writes this one, and
// only from the marks table.
const DocumentNoteCategoryKey = "document_note_category"

const errKBDocumentKeyRequired = "kb: document_key is required"
const errKBDocumentCategoryInvalid = "kb: note_category must be 'sop' or 'fact'"
const errKBDocumentNotMarkable = "kb: documents of a manual knowledge base cannot be marked individually; set the knowledge base's category instead"
const errKBDocumentKeyForeign = "kb: document_key does not belong to this knowledge base"

// documentSourceIDKeys are the metadata keys a scraper stamps its own document
// id on, in the order rag-server reads them (_SOURCE_ID_KEYS in
// rag/core/documents/collection.py): Confluence writes page_id, ServiceNow
// sys_id, and newer loaders source_id.
var documentSourceIDKeys = []string{"source_id", "page_id", "sys_id"}

// DocumentKey is the stable handle a Fact/SOP mark is stored against:
// "<collection>|<source id>", or "<collection>|<url>" when the scraper stamped
// no id. It is "" when the document cannot be marked.
//
// Not the document id the documents list returns: that is a Qdrant point id
// derived from content, so re-syncing a page replaces it and any mark keyed on
// it would silently detach. The scraper's id is preferred over the URL because
// Confluence rebuilds a page's URL from its title, so a rename or a space move
// would orphan a URL-keyed mark. The collection is part of the key because a
// source id is only unique within one integration: two Confluence sites in one
// account can both have a page 12345.
//
// There is deliberately no title fallback. A manual knowledge base's documents
// carry no URL and all share its name as their title, so a title key would let
// one mark cover every one of them. Manual knowledge bases are categorised on
// the knowledge base itself (llm_knowledgebases.note_category) instead.
func DocumentKey(collection, sourceID string, docURL *string) string {
	collection = strings.TrimSpace(collection)
	if !markableCollection(collection) {
		return ""
	}
	if id := strings.TrimSpace(sourceID); id != "" {
		return collection + "|" + id
	}
	if docURL != nil {
		if trimmed := strings.TrimSpace(*docURL); strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
			return collection + "|" + trimmed
		}
	}
	return ""
}

// markableCollection reports whether documents of a collection can carry a
// per-document mark. A knowledge base's own "kb_<id>" collection (see
// kbCollectionName) holds a manual note or an uploaded file, whose category is
// the knowledge base's note_category; only shared integration collections hold
// many independently markable pages.
func markableCollection(collection string) bool {
	return collection != "" && !strings.HasPrefix(collection, "kb_")
}

// documentSourceID reads the scraper's document id from point metadata.
// Confluence's page_id can arrive as a JSON number, so numbers are accepted and
// rendered the way rag-server's str() renders them.
func documentSourceID(metadata map[string]any) string {
	for _, key := range documentSourceIDKeys {
		switch v := metadata[key].(type) {
		case string:
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				return trimmed
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case json.Number:
			return v.String()
		case int:
			return strconv.Itoa(v)
		case int64:
			return strconv.FormatInt(v, 10)
		}
	}
	return ""
}

// documentKeyFromMetadata builds the same key from a retrieval hit's metadata.
func documentKeyFromMetadata(doc RAGSearchResult) string {
	collection, _ := doc.Metadata["collection"].(string)
	if !markableCollection(collection) {
		return ""
	}
	var docURL *string
	if u, ok := doc.Metadata["url"].(string); ok {
		docURL = &u
	}
	return DocumentKey(collection, documentSourceID(doc.Metadata), docURL)
}

// ValidKBNoteCategory reports whether category is one a document may be marked
// with. The empty string is valid and means "clear the mark".
func ValidKBNoteCategory(category string) bool {
	return category == "" || category == KBNoteCategorySOP || category == KBNoteCategoryFact
}

// kbDocumentCategories returns document_key -> note_category for the given
// document keys. Keys with no mark are simply absent from the map.
//
// Scoped by account, not by knowledge base, and deliberately so: one integration
// collection is shared by every knowledge base row of that integration, so a
// retrieval hit cannot name the row it came from. Reading this table any other
// way here than ResolveDocumentCategories reads it would let the Documents list
// and retrieval disagree about the same page.
func kbDocumentCategories(ctx context.Context, accountId string, keys []string) (map[string]string, error) {
	categories := map[string]string{}
	if accountId == "" || len(keys) == 0 {
		return categories, nil
	}
	dbms, err := knowledgeDatabase(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("kb: failed to get database manager: %w", err)
	}
	var rows []struct {
		DocumentKey  string `db:"document_key"`
		NoteCategory string `db:"note_category"`
	}
	err = dbms.Db.SelectContext(ctx, &rows,
		`SELECT document_key, note_category FROM llm_kb_document_categories
		 WHERE account_id = $1 AND document_key = ANY($2::text[])`,
		accountId, pq.Array(keys))
	if err != nil {
		return nil, fmt.Errorf("kb: failed to read document categories: %w", err)
	}
	for _, row := range rows {
		categories[row.DocumentKey] = row.NoteCategory
	}
	return categories, nil
}

// SetKBDocumentCategory marks one document as a procedure (sop) or reference
// material (fact). An empty category removes the mark, which returns the
// document to its knowledge base's default treatment.
//
// The mark applies to that document across the account. kbId says which
// knowledge base it was set from — it authorizes the write and gives the row
// something to cascade from — but the same page reached through a second
// knowledge base carries the same mark, because it is the same page.
func SetKBDocumentCategory(sc *security.RequestContext, accountId, kbId, documentKey, category string) error {
	if accountId == "" || kbId == "" {
		return errors.New(errKBAccountIDRequired)
	}
	if strings.TrimSpace(documentKey) == "" {
		return errors.New(errKBDocumentKeyRequired)
	}
	if !ValidKBNoteCategory(category) {
		return errors.New(errKBDocumentCategoryInvalid)
	}
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeUpdate) {
		return errors.New(errKBUnauthorized)
	}
	dbms, err := knowledgeDatabase(common.Metastore)
	if err != nil {
		return fmt.Errorf("kb: failed to get database manager: %w", err)
	}

	// The knowledge base is read back rather than trusted from the request: it
	// establishes that the KB belongs to this account, supplies the tenant the
	// mark is scoped to, and names the collection the document must be in.
	var kb struct {
		TenantID      string  `db:"tenant_id"`
		KBType        string  `db:"kb_type"`
		IntegrationID *string `db:"integration_id"`
	}
	err = dbms.Db.GetContext(sc.GetContext(), &kb,
		`SELECT tenant_id, kb_type, integration_id FROM llm_knowledgebases WHERE id = $1 AND account_id = $2`, kbId, accountId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New(errKBNotFound)
		}
		return fmt.Errorf("kb: failed to fetch knowledgebase: %w", err)
	}

	// The key must name a document of this knowledge base's own collection, and
	// that collection must be one whose documents are markable individually. A
	// manual knowledge base is categorised on its own row.
	documentKey = strings.TrimSpace(documentKey)
	collection := kbCollectionName(kb.KBType, kb.IntegrationID, kbId)
	if !markableCollection(collection) {
		return errors.New(errKBDocumentNotMarkable)
	}
	if !strings.HasPrefix(documentKey, collection+"|") {
		return errors.New(errKBDocumentKeyForeign)
	}
	if category == "" {
		_, err = dbms.Db.ExecContext(sc.GetContext(),
			`DELETE FROM llm_kb_document_categories WHERE account_id = $1 AND document_key = $2`,
			accountId, documentKey)
		if err != nil {
			return fmt.Errorf("kb: failed to clear document category: %w", err)
		}
		return nil
	}

	actor := sql.NullString{String: sc.GetSecurityContext().GetUserId(), Valid: sc.GetSecurityContext().GetUserId() != ""}
	_, err = dbms.Db.ExecContext(sc.GetContext(), `
		INSERT INTO llm_kb_document_categories
			(tenant_id, account_id, kb_id, document_key, note_category, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		ON CONFLICT (account_id, document_key)
		DO UPDATE SET kb_id = EXCLUDED.kb_id, note_category = EXCLUDED.note_category,
		              updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
		kb.TenantID, accountId, kbId, documentKey, category, actor)
	if err != nil {
		return fmt.Errorf("kb: failed to set document category: %w", err)
	}
	return nil
}

// ResolveDocumentCategories stamps each retrieval hit that a user has marked
// with its category, so KnowledgeDocumentPurpose can treat a marked document as
// a procedure regardless of which knowledge base it came from.
//
// One batched query per search, keyed by the same stable handle the marks are
// stored against. A failure leaves the hits unmarked — documents keep their
// knowledge base's default treatment rather than the search failing.
func ResolveDocumentCategories(sc *security.RequestContext, accountID string, docs RAGSearchResults) RAGSearchResults {
	if accountID == "" || len(docs) == 0 {
		return docs
	}
	keys := make([]string, 0, len(docs))
	seen := make(map[string]bool, len(docs))
	for _, doc := range docs {
		key := documentKeyFromMetadata(doc)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return docs
	}

	byKey, err := kbDocumentCategories(sc.GetContext(), accountID, keys)
	if err != nil {
		sc.GetLogger().Warn("knowledge: unable to resolve document categories", "error", err)
		return docs
	}
	if len(byKey) == 0 {
		return docs
	}

	out := make(RAGSearchResults, 0, len(docs))
	for _, doc := range docs {
		category, ok := byKey[documentKeyFromMetadata(doc)]
		if !ok {
			out = append(out, doc)
			continue
		}
		metadata := make(map[string]any, len(doc.Metadata)+1)
		for k, v := range doc.Metadata {
			metadata[k] = v
		}
		metadata[DocumentNoteCategoryKey] = category
		doc.Metadata = metadata
		out = append(out, doc)
	}
	return out
}
