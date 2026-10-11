package core

import (
	"context"
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

	// What Content is when llm-server sets it itself: the whole stored document.
	// rag-server supplies "indexed" and "fragment" for what it serves.
	contentSourceOriginal = "source"
	// A typed note is written as prose with markdown headings and lists.
	contentTypeMarkdown = "text/markdown"
)

// KBDocument is one stored document of a knowledge base. Content is only
// populated when a single document is fetched.
type KBDocument struct {
	Id      string  `json:"id"`
	Title   string  `json:"title"`
	Url     *string `json:"url"`
	Content *string `json:"content,omitempty"`
	// Where Content came from: "source" (the document as fetched), "indexed"
	// (the whole document as stored on the point), or "fragment" (one chunk,
	// because no source was kept for it). Empty on a listing.
	ContentSource *string `json:"content_source,omitempty"`
	// What Content is, so the reader gets a rendered page rather than its markup.
	// A stored source keeps the form the site returned it in, usually text/html;
	// anything served from the index was converted on the way in.
	ContentType *string `json:"content_type,omitempty"`
	// DocumentKey is the handle a Fact/SOP mark is stored against (see
	// DocumentKey). Empty when the document cannot be marked individually.
	DocumentKey string `json:"document_key"`
	// NoteCategory is the mark a user put on this document: "sop", "fact", or
	// "" when it carries none and keeps its knowledge base's treatment.
	NoteCategory string `json:"note_category"`
	// AccountMarks counts the accounts whose own mark overrides the tenant's on
	// this document. Set only when listing an integration's organisation
	// knowledge base (#40605), so a tenant admin sees where they differ.
	AccountMarks int `json:"account_marks,omitempty"`
}

// KBDocumentsPage is one page of a knowledge base's documents. NextOffset is
// an opaque cursor; nil means there are no more documents.
type KBDocumentsPage struct {
	Items      []KBDocument `json:"items"`
	NextOffset *string      `json:"next_offset"`
	// Whether the search index actually holds anything for this knowledge base.
	// nil when rag-server could not be reached: an outage must not be reported as
	// "nothing is indexed", which is a different and alarming claim.
	Indexed *bool `json:"indexed,omitempty"`
}

type ragKBDocument struct {
	Id            string  `json:"id"`
	Title         *string `json:"title"`
	Url           *string `json:"url"`
	Content       *string `json:"content"`
	ContentSource *string `json:"content_source"`
	ContentType   *string `json:"content_type"`
	SourceId      *string `json:"source_id"`
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
	dbms, err := knowledgeDatabase(common.Metastore)
	if err != nil {
		return "", "", fmt.Errorf("kb: failed to get database manager: %w", err)
	}
	var kbMeta struct {
		Name          string  `db:"name"`
		KBType        string  `db:"kb_type"`
		IntegrationID *string `db:"integration_id"`
	}
	scope, scopeArgs := KBAccountScope("", 2, 3, VisibleOrgKnowledgebaseIDs(sc.GetContext(), accountId))
	err = dbms.Db.GetContext(sc.GetContext(), &kbMeta, "SELECT name, kb_type, integration_id FROM llm_knowledgebases WHERE id = $1 AND "+scope, append([]any{kbId, accountId}, scopeArgs...)...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", errors.New(errKBNotFound)
		}
		return "", "", fmt.Errorf("kb: failed to fetch knowledgebase: %w", err)
	}
	return kbMeta.Name, kbCollectionName(kbMeta.KBType, kbMeta.IntegrationID, kbId), nil
}

// kbManualKB returns a knowledge base someone created by hand: its title, its
// stored content and what that content is.
//
// Everything a person typed or uploaded is held here by llm-server, which stored it
// before rag-server indexed anything. Reading it back from the database means the
// document view survives the index being re-chunked, rebuilt or dropped — and for an
// uploaded file it is the only copy, since only the text pulled out of it is indexed.
//
// ok is false for an integration knowledge base, whose content genuinely lives in
// rag-server, and for a manual one with nothing stored.
// withContent selects the stored file itself. The listing wants only a title and
// whether anything is stored, and an upload runs to megabytes of base64 — reading
// the blob out of Postgres to discard it would cost that on every modal open.
func kbManualKB(ctx context.Context, accountId, kbId string, withContent bool) (title, content, contentType string, ok bool, err error) {
	dbms, err := knowledgeDatabase(common.Metastore)
	if err != nil {
		return "", "", "", false, fmt.Errorf("kb: failed to get database manager: %w", err)
	}
	var row struct {
		Name     string  `db:"name"`
		Data     *string `db:"data"`
		HasData  bool    `db:"has_data"`
		Format   string  `db:"data_format"`
		Filename string  `db:"data_filename"`
	}
	dataColumn := `NULL AS data`
	if withContent {
		dataColumn = `data`
	}
	scope, scopeArgs := KBAccountScope("", 2, 3, VisibleOrgKnowledgebaseIDs(ctx, accountId))
	err = dbms.Db.GetContext(
		ctx,
		&row,
		fmt.Sprintf(`SELECT name, %s, (llm_knowledgebases.data IS NOT NULL AND llm_knowledgebases.data <> '') AS has_data,
		        COALESCE(data_format, 'text') AS data_format,
		        COALESCE(data_filename, '') AS data_filename
		   FROM llm_knowledgebases
		  WHERE id = $1 AND %s AND kb_type = 'manual'`, dataColumn, scope),
		append([]any{kbId, accountId}, scopeArgs...)...,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", "", false, nil
		}
		return "", "", "", false, fmt.Errorf("kb: failed to fetch knowledgebase content: %w", err)
	}
	if !row.HasData {
		// Nothing stored to serve; fall through to rag-server rather than show a blank.
		return "", "", "", false, nil
	}
	title = row.Name
	switch row.Format {
	case "pdf":
		// Held as base64 of the file itself, so the reader gets the document rather
		// than the text someone pulled out of it.
		contentType = "application/pdf"
		if row.Filename != "" {
			title = row.Filename
		}
	case "json":
		contentType = "application/json"
	default:
		// A typed note is written as prose, with markdown headings and lists.
		contentType = contentTypeMarkdown
	}
	if row.Data != nil {
		content = *row.Data
	}
	return title, content, contentType, true, nil
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
// into out. It reports found=false on 404. ctx is the caller's request context,
// so an abandoned request stops waiting on rag-server.
func getRAG(ctx context.Context, path string, out any) (found bool, err error) {
	ragServerURL := config.Config.RAGServerUrl
	if ragServerURL == "" {
		return false, errors.New("kb: search index service not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ragServerURL+path, nil)
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

// integrationOrgKBTenant returns the tenant of kbId when it is an integration's
// organisation knowledge base (#40605), and "" otherwise or on a failed read.
func integrationOrgKBTenant(ctx context.Context, kbId string) string {
	dbms, err := knowledgeDatabase(common.Metastore)
	if err != nil {
		return ""
	}
	var tenantId string
	if err := dbms.Db.GetContext(ctx, &tenantId, `SELECT tenant_id::text FROM llm_knowledgebases
		WHERE id = $1 AND account_id IS NULL AND kb_type = 'integration'`, kbId); err != nil {
		return ""
	}
	return tenantId
}

// ListKBDocuments returns one page of a knowledge base's stored documents
// (title and link only). A KB whose collection was never built returns an
// empty page.
func ListKBDocuments(sc *security.RequestContext, accountId, kbId string, limit int, offset string) (*KBDocumentsPage, error) {
	kbName, collection, err := kbDocumentsCollection(sc, accountId, kbId)
	if err != nil {
		return nil, err
	}
	if title, _, _, ok, kbErr := kbManualKB(sc.GetContext(), accountId, kbId, false); kbErr != nil {
		return nil, kbErr
	} else if ok {
		// One knowledge base someone made by hand is one document — whatever the index
		// split it into, and whether or not the index still exists. There is never a
		// second page, so an offset means the caller has already been handed it; the
		// one document would otherwise repeat for every offset asked for.
		items := []KBDocument{{Id: kbId, Title: title}}
		if offset != "" {
			items = []KBDocument{}
		}
		return &KBDocumentsPage{
			Items:   items,
			Indexed: kbIndexHasDocuments(sc.GetContext(), collection),
		}, nil
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
	found, err := getRAG(sc.GetContext(), fmt.Sprintf("/collections/%s/documents?%s", url.PathEscape(collection), query.Encode()), &ragPage)
	if err != nil {
		return nil, err
	}
	page := &KBDocumentsPage{Items: []KBDocument{}}
	if !found {
		// No collection at all: the index is gone, whatever the row still claims.
		noIndex := false
		page.Indexed = &noIndex
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
	// A failed read shows the page unmarked rather than failing the list: the
	// documents are still worth seeing, and the marks still apply at retrieval.
	categories, err := kbDocumentCategories(sc.GetContext(), accountId, keys)
	if err != nil {
		sc.GetLogger().Warn("kb: unable to read document categories", "error", err, "kb_id", kbId)
		categories = map[string]string{}
	}
	for i := range page.Items {
		page.Items[i].NoteCategory = categories[page.Items[i].DocumentKey]
	}
	if tenantId := integrationOrgKBTenant(sc.GetContext(), kbId); tenantId != "" {
		counts, err := kbDocumentAccountMarkCounts(sc.GetContext(), tenantId, keys)
		if err != nil {
			sc.GetLogger().Warn("kb: unable to count account document marks", "error", err, "kb_id", kbId)
		}
		for i := range page.Items {
			page.Items[i].AccountMarks = counts[page.Items[i].DocumentKey]
		}
	}
	page.NextOffset = ragPage.NextOffset
	// An offset means an earlier page was served, so the index holds documents even
	// when this last page came back empty — reading that as "nothing is indexed"
	// would put the rebuild banner over a healthy knowledge base.
	hasDocuments := len(page.Items) > 0 || offset != ""
	page.Indexed = &hasDocuments
	return page, nil
}

// kbIndexHasDocuments reports whether rag-server holds anything for a collection.
//
// The knowledge base row keeps a document_count from its last load, and nothing
// clears it if the index is later dropped — so a knowledge base can claim documents
// it no longer has. This asks the index itself. nil means the question could not be
// answered, which is not the same as no.
func kbIndexHasDocuments(ctx context.Context, collection string) *bool {
	var page struct {
		Items []ragKBDocument `json:"items"`
	}
	found, err := getRAG(ctx, fmt.Sprintf("/collections/%s/documents?limit=1", url.PathEscape(collection)), &page)
	if err != nil {
		return nil
	}
	has := found && len(page.Items) > 0
	return &has
}

// KBDocumentSource is a document as it was fetched, before conversion and chunking.
type KBDocumentSource struct {
	SourceId    string `json:"source_id"`
	Title       string `json:"title"`
	Url         string `json:"url"`
	Content     string `json:"content"`
	ContentType string `json:"content_type"`
	ContentHash string `json:"content_hash"`
	FetchedAt   string `json:"fetched_at"`
}

// KBRunDocument is one document a load captured.
type KBRunDocument struct {
	SourceId    string `json:"source_id"`
	Title       string `json:"title"`
	Url         string `json:"url"`
	ContentHash string `json:"content_hash"`
	FetchedAt   string `json:"fetched_at"`
}

// KBRunDocumentsPage is one page of what a load captured.
type KBRunDocumentsPage struct {
	Items      []KBRunDocument `json:"items"`
	NextOffset *string         `json:"next_offset"`
}

// GetKBDocumentSource returns a document's source exactly as it was fetched.
//
// Addressed by content hash rather than by document, so a caller can ask for the version a
// particular load indexed. Old versions are pruned, so a hash from an older run legitimately
// returns not-found — callers render "source unavailable", not a broken link.
func GetKBDocumentSource(sc *security.RequestContext, accountId, kbId, contentHash string) (*KBDocumentSource, error) {
	// An uploaded document is its own source, and llm-server already holds it: it was
	// stored here before rag-server ever saw it. Serving the stored file is both the
	// truthful answer and the one that needs no second copy — unlike a scraped page,
	// which exists nowhere else once it has been converted.
	if uploaded, err := uploadedKBSource(sc, accountId, kbId); err != nil || uploaded != nil {
		return uploaded, err
	}
	if contentHash == "" {
		return nil, errors.New("kb: content_hash is required")
	}
	_, collection, err := kbDocumentsCollection(sc, accountId, kbId)
	if err != nil {
		return nil, err
	}
	var source KBDocumentSource
	found, err := getRAG(sc.GetContext(),
		fmt.Sprintf("/collections/%s/source/%s", url.PathEscape(collection), url.PathEscape(contentHash)), &source)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New(errKBDocumentNotFound)
	}
	return &source, nil
}

// uploadedKBSource returns the stored file for a knowledge base that was uploaded as a
// document, or nil when the knowledge base has none — a scraped integration, whose source
// is served from rag-server instead.
//
// Content carries the bytes as llm_knowledgebases.data holds them: base64 for a PDF, the
// text itself otherwise. ContentType is what tells the two apart.
func uploadedKBSource(sc *security.RequestContext, accountId, kbId string) (*KBDocumentSource, error) {
	dbms, err := knowledgeDatabase(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("kb: failed to get database manager: %w", err)
	}
	var row struct {
		Name      string `db:"name"`
		Filename  string `db:"data_filename"`
		Format    string `db:"data_format"`
		Data      string `db:"data"`
		UpdatedAt string `db:"updated_at"`
	}
	scope, scopeArgs := KBAccountScope("", 2, 3, VisibleOrgKnowledgebaseIDs(sc.GetContext(), accountId))
	err = dbms.Db.GetContext(sc.GetContext(), &row, `SELECT name, COALESCE(data_filename, '') AS data_filename,
	                                COALESCE(data_format, '') AS data_format, COALESCE(data, '') AS data,
	                                COALESCE(updated_at::text, '') AS updated_at
	                         FROM llm_knowledgebases
	                         WHERE id = $1 AND `+scope+` AND enabled`, append([]any{kbId, accountId}, scopeArgs...)...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New(errKBDocumentNotFound)
		}
		return nil, fmt.Errorf("kb: failed to read knowledge base: %w", err)
	}
	if row.Data == "" {
		return nil, nil
	}
	contentType := "text/plain"
	if row.Format == string(KBFormatPDF) {
		contentType = "application/pdf"
	}
	title := row.Filename
	if title == "" {
		title = row.Name
	}
	return &KBDocumentSource{
		SourceId:    kbId,
		Title:       title,
		Content:     row.Data,
		ContentType: contentType,
		ContentHash: "",
		FetchedAt:   row.UpdatedAt,
	}, nil
}

// ListKBRunDocuments returns what one load captured.
//
// Failures are not here: a document that failed to load produced no record. Those are on
// the load-history entry for the same run.
func ListKBRunDocuments(
	sc *security.RequestContext, accountId, kbId, runId string, limit int, offset string,
) (*KBRunDocumentsPage, error) {
	if runId == "" {
		return nil, errors.New("kb: run_id is required")
	}
	_, collection, err := kbDocumentsCollection(sc, accountId, kbId)
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
		Documents  []KBRunDocument `json:"documents"`
		NextOffset *string         `json:"next_offset"`
	}
	found, err := getRAG(sc.GetContext(), fmt.Sprintf("/collections/%s/runs/%s/documents?%s",
		url.PathEscape(collection), url.PathEscape(runId), query.Encode()), &ragPage)
	if err != nil {
		return nil, err
	}
	page := &KBRunDocumentsPage{Items: []KBRunDocument{}}
	if !found {
		return page, nil
	}
	page.Items = append(page.Items, ragPage.Documents...)
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
	if title, content, contentType, ok, kbErr := kbManualKB(sc.GetContext(), accountId, kbId, true); kbErr != nil {
		return nil, kbErr
	} else if ok {
		whole := contentSourceOriginal
		return &KBDocument{Id: kbId, Title: title, Content: &content, ContentSource: &whole, ContentType: &contentType}, nil
	}
	var doc ragKBDocument
	found, err := getRAG(sc.GetContext(), fmt.Sprintf("/collections/%s/documents/%s", url.PathEscape(collection), url.PathEscape(documentId)), &doc)
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
		Id:            doc.Id,
		Title:         documentTitle(doc, kbName),
		Url:           doc.Url,
		Content:       doc.Content,
		ContentSource: doc.ContentSource,
		ContentType:   doc.ContentType,
		DocumentKey:   key,
		NoteCategory:  category,
	}, nil
}

// ragDocumentKey is DocumentKey for a document rag-server listed or served.
func ragDocumentKey(collection string, doc ragKBDocument) string {
	var sourceID string
	if doc.SourceId != nil {
		sourceID = *doc.SourceId
	}
	return DocumentKey(collection, sourceID, doc.Url)
}
