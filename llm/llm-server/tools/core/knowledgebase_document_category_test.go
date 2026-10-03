package core

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

const confluenceCollection = "integration-a_knowledge_base"

// The scraper's id survives a Confluence rename or space move, which rebuilds
// the URL; the URL is only the fallback for loaders that stamp no id.
func TestDocumentKeyPrefersSourceIDOverURL(t *testing.T) {
	url := "https://wiki.test/pages/1/Reset+VPN"
	assert.Equal(t, confluenceCollection+"|1", DocumentKey(confluenceCollection, "1", &url))
	assert.Equal(t, confluenceCollection+"|"+url, DocumentKey(confluenceCollection, " ", &url))

	notHTTP := "javascript:alert(1)"
	assert.Equal(t, "", DocumentKey(confluenceCollection, "", &notHTTP))
	assert.Equal(t, "", DocumentKey(confluenceCollection, "", nil))
	assert.Equal(t, "", DocumentKey("", "1", &url))
}

// A manual knowledge base's documents share its name as their title and carry
// no URL, so any per-document key would collapse them onto one mark. They are
// not markable at all; the knowledge base's own category covers them.
func TestDocumentKeyRefusesManualKnowledgeBaseCollections(t *testing.T) {
	url := "https://wiki.test/a"
	assert.Equal(t, "", DocumentKey("kb_manual-a", "1", &url))
	assert.Equal(t, "", documentKeyFromMetadata(RAGSearchResult{Metadata: map[string]any{
		"collection": "kb_manual-a", "kb_id": "manual-a", "title": "Runbooks",
	}}))
}

// Confluence's page_id reaches llm-server as a JSON number; it must render the
// way rag-server's str() renders it for the listing, or the two keys differ.
func TestDocumentKeyFromMetadataAcceptsNumericSourceID(t *testing.T) {
	doc := RAGSearchResult{Metadata: map[string]any{"collection": confluenceCollection, "page_id": float64(590112), "url": "https://wiki.test/x"}}
	assert.Equal(t, confluenceCollection+"|590112", documentKeyFromMetadata(doc))

	servicenow := RAGSearchResult{Metadata: map[string]any{"collection": "sn_knowledge_base", "sys_id": "abc123"}}
	assert.Equal(t, "sn_knowledge_base|abc123", documentKeyFromMetadata(servicenow))
}

func TestValidKBNoteCategory(t *testing.T) {
	for _, category := range []string{"", KBNoteCategorySOP, KBNoteCategoryFact} {
		assert.True(t, ValidKBNoteCategory(category), category)
	}
	for _, category := range []string{"policy", "SOP", "procedure"} {
		assert.False(t, ValidKBNoteCategory(category), category)
	}
}

// A mark is what makes an integration document a procedure: without one, a
// scraped Confluence page stays reference material however it is worded.
func TestKnowledgeDocumentPurposeHonoursDocumentMark(t *testing.T) {
	scraped := RAGSearchResult{Metadata: map[string]any{"collection": "confluence_knowledge_base", "url": "https://wiki.test/a"}}
	assert.Equal(t, KnowledgePurposeReference, KnowledgeDocumentPurpose(scraped))

	marked := RAGSearchResult{Metadata: map[string]any{
		"collection": "confluence_knowledge_base", "url": "https://wiki.test/a", DocumentNoteCategoryKey: KBNoteCategorySOP,
	}}
	assert.Equal(t, KnowledgePurposeProcedure, KnowledgeDocumentPurpose(marked))

	// An unrecognised mark is not a licence to skip the manual-KB rules.
	junk := RAGSearchResult{Metadata: map[string]any{
		"collection": "kb_manual-a", "kb_id": "manual-a", "note_category": KBNoteCategorySOP, DocumentNoteCategoryKey: "policy",
	}}
	assert.Equal(t, KnowledgePurposeProcedure, KnowledgeDocumentPurpose(junk))
}

func TestResolveDocumentCategoriesStampsMarkedDocuments(t *testing.T) {
	mock := mockKBDatabase(t)
	mock.ExpectQuery(`SELECT document_key, note_category FROM llm_kb_document_categories`).
		WithArgs("account-a", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"document_key", "note_category"}).
			AddRow(confluenceCollection+"|101", "sop"))

	docs := RAGSearchResults{
		{Document: "steps", Metadata: map[string]any{"collection": confluenceCollection, "page_id": "101", "url": "https://wiki.test/runbook"}},
		{Document: "other", Metadata: map[string]any{"collection": confluenceCollection, "page_id": "102", "url": "https://wiki.test/other"}},
	}
	got := ResolveDocumentCategories(security.NewRequestContextForSuperAdmin(), "account-a", docs)

	require.Len(t, got, 2)
	assert.Equal(t, KBNoteCategorySOP, got[0].Metadata[DocumentNoteCategoryKey])
	assert.Equal(t, KnowledgePurposeProcedure, KnowledgeDocumentPurpose(got[0]))
	assert.NotContains(t, got[1].Metadata, DocumentNoteCategoryKey)
	assert.Equal(t, KnowledgePurposeReference, KnowledgeDocumentPurpose(got[1]))
	// The input must not be mutated: callers hold the pre-stamp slice too.
	assert.NotContains(t, docs[0].Metadata, DocumentNoteCategoryKey)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A renamed page keeps its page_id, so the mark made under its old URL still
// applies — the case a URL key orphaned.
func TestResolveDocumentCategoriesSurvivesRename(t *testing.T) {
	mock := mockKBDatabase(t)
	mock.ExpectQuery(`SELECT document_key, note_category FROM llm_kb_document_categories`).
		WithArgs("account-a", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"document_key", "note_category"}).AddRow(confluenceCollection+"|101", "sop"))

	renamed := RAGSearchResults{{Metadata: map[string]any{
		"collection": confluenceCollection, "page_id": "101", "url": "https://wiki.test/pages/101/New+Title",
	}}}
	got := ResolveDocumentCategories(security.NewRequestContextForSuperAdmin(), "account-a", renamed)
	assert.Equal(t, KBNoteCategorySOP, got[0].Metadata[DocumentNoteCategoryKey])
	require.NoError(t, mock.ExpectationsWereMet())
}

// Retrieval is not worth failing over a marks lookup: the hits are still
// answerable, they just carry their knowledge base's default treatment.
func TestResolveDocumentCategoriesSurvivesQueryFailure(t *testing.T) {
	mock := mockKBDatabase(t)
	mock.ExpectQuery(`SELECT document_key, note_category FROM llm_kb_document_categories`).
		WillReturnError(assert.AnError)

	docs := RAGSearchResults{{Document: "steps", Metadata: map[string]any{"collection": confluenceCollection, "page_id": "101"}}}
	got := ResolveDocumentCategories(security.NewRequestContextForSuperAdmin(), "account-a", docs)
	assert.Equal(t, docs, got)
}

// Manual chunks and documents with neither a source id nor a URL cannot be
// marked, so no query is issued.
func TestResolveDocumentCategoriesSkipsUnkeyableDocuments(t *testing.T) {
	mock := mockKBDatabase(t)
	docs := RAGSearchResults{
		{Document: "chunk", Metadata: map[string]any{"collection": "kb_manual-a", "title": "Runbooks"}},
		{Document: "bare", Metadata: map[string]any{"collection": confluenceCollection, "title": "No id"}},
	}
	assert.Equal(t, docs, ResolveDocumentCategories(security.NewRequestContextForSuperAdmin(), "account-a", docs))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetKBDocumentCategoryValidatesInput(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	assert.EqualError(t, SetKBDocumentCategory(sc, "", "kb-1", "key", "sop"), errKBAccountIDRequired)
	assert.EqualError(t, SetKBDocumentCategory(sc, "account-a", "", "key", "sop"), errKBAccountIDRequired)
	assert.EqualError(t, SetKBDocumentCategory(sc, "account-a", "kb-1", "  ", "sop"), errKBDocumentKeyRequired)
	assert.EqualError(t, SetKBDocumentCategory(sc, "account-a", "kb-1", "key", "policy"), errKBDocumentCategoryInvalid)
}

func markableKBRow() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"tenant_id", "kb_type", "integration_id"}).AddRow("tenant-1", KBTypeIntegration, "integration-a")
}

func TestSetKBDocumentCategoryUpsertsAndClears(t *testing.T) {
	mock := mockKBDatabase(t)
	sc := security.NewRequestContextForSuperAdmin()
	key := confluenceCollection + "|101"

	mock.ExpectQuery(`SELECT tenant_id, kb_type, integration_id FROM llm_knowledgebases`).WithArgs("kb-1", "account-a").
		WillReturnRows(markableKBRow())
	mock.ExpectExec(`INSERT INTO llm_kb_document_categories`).
		WithArgs("tenant-1", "account-a", "kb-1", key, "sop", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, SetKBDocumentCategory(sc, "account-a", "kb-1", " "+key+" ", "sop"))

	mock.ExpectQuery(`SELECT tenant_id, kb_type, integration_id FROM llm_knowledgebases`).WithArgs("kb-1", "account-a").
		WillReturnRows(markableKBRow())
	mock.ExpectExec(`DELETE FROM llm_kb_document_categories`).
		WithArgs("account-a", key).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, SetKBDocumentCategory(sc, "account-a", "kb-1", key, ""))

	require.NoError(t, mock.ExpectationsWereMet())
}

// The key is checked against the knowledge base's own collection: a caller
// cannot mark a document of another integration through a KB it can write to,
// and cannot mark a manual knowledge base document by document.
func TestSetKBDocumentCategoryRejectsKeysOutsideTheKnowledgeBase(t *testing.T) {
	mock := mockKBDatabase(t)
	sc := security.NewRequestContextForSuperAdmin()

	mock.ExpectQuery(`SELECT tenant_id, kb_type, integration_id FROM llm_knowledgebases`).WithArgs("kb-1", "account-a").
		WillReturnRows(markableKBRow())
	assert.EqualError(t, SetKBDocumentCategory(sc, "account-a", "kb-1", "integration-b_knowledge_base|101", "sop"), errKBDocumentKeyForeign)

	mock.ExpectQuery(`SELECT tenant_id, kb_type, integration_id FROM llm_knowledgebases`).WithArgs("kb-m", "account-a").
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id", "kb_type", "integration_id"}).AddRow("tenant-1", KBTypeManual, nil))
	assert.EqualError(t, SetKBDocumentCategory(sc, "account-a", "kb-m", "kb_kb-m|Runbooks", "sop"), errKBDocumentNotMarkable)

	require.NoError(t, mock.ExpectationsWereMet())
}

// The Documents list and retrieval must read the marks table the same way. One
// integration collection is shared by several knowledge base rows, so a mark is
// identified by (account, document_key): a page marked while viewing one
// knowledge base is marked when it is reached through another. Scoping either
// read by kb_id would make the two disagree — and retrieval hits from a shared
// integration collection carry no kb_id to scope by in the first place.
func TestDocumentMarksAreAccountWideOnBothReadPaths(t *testing.T) {
	mock := mockKBDatabase(t)
	key := confluenceCollection + "|101"

	mock.ExpectQuery(`SELECT document_key, note_category FROM llm_kb_document_categories\s+WHERE account_id = \$1 AND document_key = ANY`).
		WithArgs("account-a", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"document_key", "note_category"}).AddRow(key, "sop"))

	// The hit carries no kb_id at all — which is what a shared integration collection
	// looks like. It still resolves to a procedure.
	docs := RAGSearchResults{{Metadata: map[string]any{"collection": confluenceCollection, "page_id": "101"}}}
	got := ResolveDocumentCategories(security.NewRequestContextForSuperAdmin(), "account-a", docs)
	assert.Equal(t, KnowledgePurposeProcedure, KnowledgeDocumentPurpose(got[0]))

	mock.ExpectQuery(`SELECT document_key, note_category FROM llm_kb_document_categories\s+WHERE account_id = \$1 AND document_key = ANY`).
		WithArgs("account-a", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"document_key", "note_category"}).AddRow(key, "sop"))

	listed, err := kbDocumentCategories(security.NewRequestContextForSuperAdmin().GetContext(), "account-a", []string{key})
	require.NoError(t, err)
	assert.Equal(t, KBNoteCategorySOP, listed[key], "the list must show the same mark retrieval applies")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKBDocumentCategoriesSkipsQueryWithoutKeys(t *testing.T) {
	mock := mockKBDatabase(t)
	got, err := kbDocumentCategories(security.NewRequestContextForSuperAdmin().GetContext(), "account-a", nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A knowledge base in another account must not be markable through a guessed id.
func TestSetKBDocumentCategoryRejectsForeignKnowledgeBase(t *testing.T) {
	mock := mockKBDatabase(t)
	mock.ExpectQuery(`SELECT tenant_id, kb_type, integration_id FROM llm_knowledgebases`).WithArgs("kb-other", "account-a").
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id", "kb_type", "integration_id"}))

	err := SetKBDocumentCategory(security.NewRequestContextForSuperAdmin(), "account-a", "kb-other", "key", "sop")
	assert.EqualError(t, err, errKBNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The list hands the UI the same key retrieval computes from a search hit, so
// a mark set from the list is the mark retrieval finds.
func TestListingKeyMatchesRetrievalKey(t *testing.T) {
	sourceID, url := "101", "https://wiki.test/runbook"
	listed := ragDocumentKey(confluenceCollection, ragKBDocument{Id: "p1", SourceId: &sourceID, Url: &url})
	hit := documentKeyFromMetadata(RAGSearchResult{Metadata: map[string]any{
		"collection": confluenceCollection, "page_id": float64(101), "url": url,
	}})
	assert.Equal(t, confluenceCollection+"|101", listed)
	assert.Equal(t, listed, hit)
	assert.Equal(t, "", ragDocumentKey(confluenceCollection, ragKBDocument{Id: "p2"}), "no id and no URL: not markable")
}

// mockKBDatabase points the knowledge queries at sqlmock and returns the mock.
func mockKBDatabase(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	old := knowledgeDatabase
	knowledgeDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	}
	t.Cleanup(func() { knowledgeDatabase = old })
	return mock
}
