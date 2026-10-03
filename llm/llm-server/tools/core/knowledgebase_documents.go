package core

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"strconv"
)

const (
	kbDocumentsDefaultLimit = 50
	kbDocumentsMaxLimit     = 200
	errKBDocumentNotFound   = "kb: document not found, the knowledge base may have been re-synced"
)

// KBDocument is one stored document of a knowledge base. Content is only
// populated when a single document is fetched.
type KBDocument struct {
	Id    string  `json:"id"`
	Title string  `json:"title"`
	Url   *string `json:"url"`
	// DocumentKey is the handle a Fact/SOP mark is stored against (see
	// DocumentKey). Empty when the document cannot be marked individually.
	DocumentKey string `json:"document_key"`
	// NoteCategory is the mark a user put on this document: "sop", "fact", or
	// empty when it carries none.
	NoteCategory string  `json:"note_category"`
	Content      *string `json:"content,omitempty"`
}

// KBDocumentsPage is one page of a knowledge base's documents. NextOffset is
// an opaque cursor; nil means there are no more documents.
type KBDocumentsPage struct {
	Items      []KBDocument `json:"items"`
	NextOffset *string      `json:"next_offset"`
}

type ragKBDocument struct {
	Id       string  `json:"id"`
	Title    *string `json:"title"`
	Url      *string `json:"url"`
	Content  *string `json:"content"`
	SourceId *string `json:"source_id"`
}

// ragDocumentKey is DocumentKey for a document rag-server listed or served.
func ragDocumentKey(collection string, doc ragKBDocument) string {
	var sourceID string
	if doc.SourceId != nil {
		sourceID = *doc.SourceId
	}
	return DocumentKey(collection, sourceID, doc.Url)
}

// kbDocumentsCollection authorizes read access and resolves the KB's name and
// rag-server collection.
func kbDocumentsCollection(sc *security.RequestContext, accountId, kbId string) (name, collection string, err error) {
	if accountId == "" || kbId == "" {
		return "", "", errors.New(errKBAccountIDRequired)
	}
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return "", "", errors.New(errKBUnauthorized)
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", "", fmt.Errorf("kb: failed to get database manager: %w", err)
	}
	var kbMeta struct {
		Name          string  `db:"name"`
		KBType        string  `db:"kb_type"`
		IntegrationID *string `db:"integration_id"`
	}
	err = dbms.Db.Get(&kbMeta, "SELECT name, kb_type, integration_id FROM llm_knowledgebases WHERE id = $1 AND account_id = $2", kbId, accountId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", errors.New(errKBNotFound)
		}
		return "", "", fmt.Errorf("kb: failed to fetch knowledgebase: %w", err)
	}
	return kbMeta.Name, kbCollectionName(kbMeta.KBType, kbMeta.IntegrationID, kbId), nil
}

// documentTitle falls back to the KB name for stored documents that carry no
// title (manual notes and uploaded files).
func documentTitle(doc ragKBDocument, kbName string) string {
	if doc.Title != nil && *doc.Title != "" {
		return *doc.Title
	}
	return kbName
}

// getRAG issues an authenticated GET to rag-server and decodes {"data": ...}
// into out. It reports found=false on 404.
func getRAG(path string, out any) (found bool, err error) {
	ragServerURL := config.Config.RAGServerUrl
	if ragServerURL == "" {
		return false, errors.New("kb: search index service not configured")
	}
	req, err := http.NewRequest(http.MethodGet, ragServerURL+path, nil)
	if err != nil {
		return false, fmt.Errorf("kb: failed to prepare search index request: %w", err)
	}
	addRAGAuth(req)
	resp, err := ragClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("kb: failed to reach search index: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("kb: failed to close response body", "error", closeErr)
		}
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return false, fmt.Errorf("kb: failed to read search index response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("kb: search index request failed (status %d): %s", resp.StatusCode, string(body))
	}
	envelope := struct {
		Data any `json:"data"`
	}{Data: out}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false, fmt.Errorf("kb: failed to parse search index response: %w", err)
	}
	return true, nil
}

// ListKBDocuments returns one page of a knowledge base's stored documents
// (title and link only). A KB whose collection was never built returns an
// empty page.
func ListKBDocuments(sc *security.RequestContext, accountId, kbId string, limit int, offset string) (*KBDocumentsPage, error) {
	kbName, collection, err := kbDocumentsCollection(sc, accountId, kbId)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = kbDocumentsDefaultLimit
	}
	limit = min(limit, kbDocumentsMaxLimit)

	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if offset != "" {
		query.Set("offset", offset)
	}
	var ragPage struct {
		Items      []ragKBDocument `json:"items"`
		NextOffset *string         `json:"next_offset"`
	}
	found, err := getRAG(fmt.Sprintf("/collections/%s/documents?%s", url.PathEscape(collection), query.Encode()), &ragPage)
	if err != nil {
		return nil, err
	}
	page := &KBDocumentsPage{Items: []KBDocument{}}
	if !found {
		return page, nil
	}
	keys := make([]string, 0, len(ragPage.Items))
	for _, doc := range ragPage.Items {
		key := ragDocumentKey(collection, doc)
		page.Items = append(page.Items, KBDocument{Id: doc.Id, Title: documentTitle(doc, kbName), Url: doc.Url, DocumentKey: key})
		if key != "" {
			keys = append(keys, key)
		}
	}
	// One lookup for the page, not one per document. A failure costs the marks,
	// not the listing: the documents are still worth showing without them.
	categories, err := kbDocumentCategories(sc.GetContext(), accountId, keys)
	if err != nil {
		sc.GetLogger().Warn("kb: unable to read document categories", "error", err, "kb_id", kbId)
		categories = map[string]string{}
	}
	for i := range page.Items {
		page.Items[i].NoteCategory = categories[page.Items[i].DocumentKey]
	}
	page.NextOffset = ragPage.NextOffset
	return page, nil
}

// GetKBDocument returns one stored document of a knowledge base with its content.
func GetKBDocument(sc *security.RequestContext, accountId, kbId, documentId string) (*KBDocument, error) {
	if documentId == "" {
		return nil, errors.New("kb: document_id is required")
	}
	kbName, collection, err := kbDocumentsCollection(sc, accountId, kbId)
	if err != nil {
		return nil, err
	}
	var doc ragKBDocument
	found, err := getRAG(fmt.Sprintf("/collections/%s/documents/%s", url.PathEscape(collection), url.PathEscape(documentId)), &doc)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New(errKBDocumentNotFound)
	}
	key := ragDocumentKey(collection, doc)
	var category string
	if key != "" {
		categories, err := kbDocumentCategories(sc.GetContext(), accountId, []string{key})
		if err != nil {
			sc.GetLogger().Warn("kb: unable to read document categories", "error", err, "kb_id", kbId)
		}
		category = categories[key]
	}
	return &KBDocument{
		Id:           doc.Id,
		Title:        documentTitle(doc, kbName),
		Url:          doc.Url,
		DocumentKey:  key,
		NoteCategory: category,
		Content:      doc.Content,
	}, nil
}
