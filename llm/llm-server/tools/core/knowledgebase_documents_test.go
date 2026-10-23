package core

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withRAGServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	previous := config.Config.RAGServerUrl
	config.Config.RAGServerUrl = server.URL
	t.Cleanup(func() { config.Config.RAGServerUrl = previous })
}

func TestGetRAGDecodesDataEnvelope(t *testing.T) {
	withRAGServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/collections/int-1_knowledge_base/documents", r.URL.Path)
		assert.Equal(t, "cursor/1", r.URL.Query().Get("offset"))
		_, _ = w.Write([]byte(`{"data":{"items":[{"id":"a","title":null,"url":"https://wiki/a"}],"next_offset":"b"}}`))
	})

	var page struct {
		Items      []ragKBDocument `json:"items"`
		NextOffset *string         `json:"next_offset"`
	}
	found, err := getRAG(context.Background(), "/collections/int-1_knowledge_base/documents?offset=cursor%2F1", &page)
	require.NoError(t, err)
	assert.True(t, found)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "Runbooks", documentTitle(page.Items[0], "Runbooks"))
	require.NotNil(t, page.NextOffset)
	assert.Equal(t, "b", *page.NextOffset)
}

func TestGetRAGNotFoundAndServerError(t *testing.T) {
	status := http.StatusNotFound
	withRAGServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"detail":"nope"}`))
	})

	var out map[string]any
	found, err := getRAG(context.Background(), "/collections/kb_1/documents", &out)
	require.NoError(t, err)
	assert.False(t, found)

	status = http.StatusInternalServerError
	_, err = getRAG(context.Background(), "/collections/kb_1/documents", &out)
	assert.Error(t, err)
}

// A caller that gave up must not keep waiting on rag-server.
func TestGetRAGStopsWhenTheCallerCancels(t *testing.T) {
	calls := 0
	withRAGServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"data":{}}`))
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out map[string]any
	_, err := getRAG(ctx, "/collections/kb_1/documents", &out)
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, calls, "a cancelled request never reaches the index")
}

func TestDocumentTitlePrefersStoredTitle(t *testing.T) {
	title := "Reset VPN"
	assert.Equal(t, "Reset VPN", documentTitle(ragKBDocument{Title: &title}, "ServiceNow KB"))
	empty := ""
	assert.Equal(t, "ServiceNow KB", documentTitle(ragKBDocument{Title: &empty}, "ServiceNow KB"))
}

func TestGetKBDocumentSourceServesTheUploadedFile(t *testing.T) {
	// An uploaded document is its own source, and llm-server already holds it — asking
	// rag-server for it would mean a second copy of a file we stored ourselves.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldDatabase := knowledgeDatabase
	knowledgeDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	}
	t.Cleanup(func() { knowledgeDatabase = oldDatabase })

	mock.ExpectQuery(`SELECT name, .*FROM llm_knowledgebases`).
		WithArgs("kb-1", "account-a").
		WillReturnRows(sqlmock.NewRows([]string{"name", "data_filename", "data_format", "data", "updated_at"}).
			AddRow("Disk runbook", "disk.pdf", "pdf", "JVBERi0xLjcK", "2026-09-19 10:00:00"))

	// No content hash: an uploaded document has one source, not versions.
	got, err := GetKBDocumentSource(security.NewRequestContextForSuperAdmin(), "account-a", "kb-1", "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "disk.pdf", got.Title)
	assert.Equal(t, "application/pdf", got.ContentType)
	assert.Equal(t, "JVBERi0xLjcK", got.Content)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetKBDocumentSourceFallsThroughForAScrapedPage(t *testing.T) {
	// A scraped page exists nowhere else once converted, so its source comes from
	// rag-server — and identifying which version needs the content hash.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldDatabase := knowledgeDatabase
	knowledgeDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	}
	t.Cleanup(func() { knowledgeDatabase = oldDatabase })

	mock.ExpectQuery(`SELECT name, .*FROM llm_knowledgebases`).
		WithArgs("kb-2", "account-a").
		WillReturnRows(sqlmock.NewRows([]string{"name", "data_filename", "data_format", "data", "updated_at"}).
			AddRow("Confluence", "", "text", "", "2026-09-19 10:00:00"))

	_, err = GetKBDocumentSource(security.NewRequestContextForSuperAdmin(), "account-a", "kb-2", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "content_hash is required")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetKBDocumentSourceServesAnUploadedTextFile(t *testing.T) {
	// rag-server keeps the source of scraped pages only, so a text upload has to be
	// served from here too — falling through would 404 a file we are holding.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldDatabase := knowledgeDatabase
	knowledgeDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	}
	t.Cleanup(func() { knowledgeDatabase = oldDatabase })

	mock.ExpectQuery(`SELECT name, .*FROM llm_knowledgebases`).
		WithArgs("kb-3", "account-a").
		WillReturnRows(sqlmock.NewRows([]string{"name", "data_filename", "data_format", "data", "updated_at"}).
			AddRow("Escalation policy", "escalation.txt", "text", "Page the on-call SRE.", "2026-09-19 10:00:00"))

	got, err := GetKBDocumentSource(security.NewRequestContextForSuperAdmin(), "account-a", "kb-3", "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "escalation.txt", got.Title)
	assert.Equal(t, "text/plain", got.ContentType)
	assert.Equal(t, "Page the on-call SRE.", got.Content)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKBManualKBServesTheWholeNoteFromTheDatabase(t *testing.T) {
	// Chunking leaves the index holding slices, but a manual KB's whole text was
	// never lost — llm-server stored it before rag-server indexed anything.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldDatabase := knowledgeDatabase
	knowledgeDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	}
	t.Cleanup(func() { knowledgeDatabase = oldDatabase })

	whole := "Step 1: drain the node.\n\nStep 2: cordon it.\n\nStep 3: page the on-call."
	mock.ExpectQuery(`SELECT name, data`).
		WithArgs("kb-1", "account-a").
		WillReturnRows(sqlmock.NewRows([]string{"name", "data", "has_data", "data_format", "data_filename"}).AddRow("Runbook", whole, true, "text", ""))

	title, content, contentType, ok, err := kbManualKB(context.Background(), "account-a", "kb-1", true)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "Runbook", title)
	assert.Equal(t, whole, content)
	assert.Equal(t, "text/markdown", contentType)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKBManualKBDeclinesForAnIntegrationKB(t *testing.T) {
	// The query filters on kb_type/data_format, so an integration KB or an
	// uploaded file matches no row — and "no row" must read as "nothing to
	// serve", not as an error that would fail the whole document view.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldDatabase := knowledgeDatabase
	knowledgeDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	}
	t.Cleanup(func() { knowledgeDatabase = oldDatabase })

	mock.ExpectQuery(`SELECT name, data`).
		WithArgs("kb-2", "account-a").
		WillReturnError(sql.ErrNoRows)

	_, _, _, ok, err := kbManualKB(context.Background(), "account-a", "kb-2", true)
	require.NoError(t, err)
	assert.False(t, ok, "an integration KB or an upload must fall through to rag-server")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKBManualKBTreatsEmptyAndNullAsNothingToServe(t *testing.T) {
	// A manual KB can exist with no text in it — one such row is live on dev, and
	// the column is nullable. Either way there is nothing better than what the
	// index already returned, so the caller must keep that rather than blank the
	// document out.
	for _, tc := range []struct {
		name string
		row  *sqlmock.Rows
	}{
		{"empty string", sqlmock.NewRows([]string{"name", "data", "has_data", "data_format", "data_filename"}).AddRow("Runbook", "", false, "text", "")},
		{"null", sqlmock.NewRows([]string{"name", "data", "has_data", "data_format", "data_filename"}).AddRow("Runbook", nil, false, "text", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			oldDatabase := knowledgeDatabase
			knowledgeDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
				return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
			}
			t.Cleanup(func() { knowledgeDatabase = oldDatabase })

			mock.ExpectQuery(`SELECT name, data`).
				WithArgs("kb-3", "account-a").
				WillReturnRows(tc.row)

			_, _, _, ok, err := kbManualKB(context.Background(), "account-a", "kb-3", true)
			require.NoError(t, err)
			assert.False(t, ok, "nothing stored means fall through, not a blank document")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
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

func expectManualNote(mock sqlmock.Sqlmock, kbID, accountID, name, text string) {
	mock.ExpectQuery(`SELECT name, kb_type, integration_id FROM llm_knowledgebases`).
		WithArgs(kbID, accountID).
		WillReturnRows(sqlmock.NewRows([]string{"name", "kb_type", "integration_id"}).AddRow(name, "manual", nil))
	// The listing asks for has_data without the blob; the get path asks for both. The
	// same stub answers either, since sqlmock matches on the leading columns.
	mock.ExpectQuery(`SELECT name, `).
		WithArgs(kbID, accountID).
		WillReturnRows(sqlmock.NewRows([]string{"name", "data", "has_data", "data_format", "data_filename"}).
			AddRow(name, text, true, "text", ""))
}

func TestAManualNoteListsAsOneDocument(t *testing.T) {
	// rag-server indexes a typed note one line per point, so asking it would list a
	// 54-line runbook as 54 documents. These tests also prove rag is never called:
	// no rag-server URL is configured here, so any call would error.
	mock := mockKBDatabase(t)
	expectManualNote(mock, "kb-1", "account-a", "Payment runbook", "## Steps\n- drain the node\n- cordon it")

	page, err := ListKBDocuments(security.NewRequestContextForSuperAdmin(), "account-a", "kb-1", 50, "")
	require.NoError(t, err)
	require.Len(t, page.Items, 1, "a typed note is one document, whatever the index split it into")
	assert.Equal(t, "kb-1", page.Items[0].Id)
	assert.Equal(t, "Payment runbook", page.Items[0].Title)
	assert.Nil(t, page.NextOffset)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAManualNoteHasNoSecondPage(t *testing.T) {
	// The one document is returned for the first page only. Handing it back for every
	// offset would have a paging caller append the same document again and again.
	mock := mockKBDatabase(t)
	expectManualNote(mock, "kb-1", "account-a", "Payment runbook", "## Steps")

	page, err := ListKBDocuments(security.NewRequestContextForSuperAdmin(), "account-a", "kb-1", 50, "some-offset")
	require.NoError(t, err)
	assert.Empty(t, page.Items)
}

func TestAManualNoteIsServedWholeFromTheDatabase(t *testing.T) {
	whole := "## Steps\n- drain the node\n- cordon it\n- page the on-call"
	mock := mockKBDatabase(t)
	expectManualNote(mock, "kb-1", "account-a", "Payment runbook", whole)

	doc, err := GetKBDocument(security.NewRequestContextForSuperAdmin(), "account-a", "kb-1", "kb-1")
	require.NoError(t, err)
	require.NotNil(t, doc.Content)
	assert.Equal(t, whole, *doc.Content, "the note is served entire, not one indexed line of it")
	require.NotNil(t, doc.ContentSource)
	assert.Equal(t, "source", *doc.ContentSource)
	assert.Equal(t, "Payment runbook", doc.Title)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKBManualKBServesAnUploadedPDFAsTheFile(t *testing.T) {
	// The text of a PDF only ever lives in the index; the file itself is the only
	// copy llm-server holds. Serving it means the document survives the index being
	// dropped — which is exactly what happened to sop_k8s_01_crashloopbackoff.
	mock := mockKBDatabase(t)
	mock.ExpectQuery(`SELECT name, data`).
		WithArgs("kb-pdf", "account-a").
		WillReturnRows(sqlmock.NewRows([]string{"name", "data", "has_data", "data_format", "data_filename"}).
			AddRow("sop_k8s_01_crashloopbackoff", "JVBERi0xLjQK", true, "pdf", "sop-k8s-01-crashloopbackoff.pdf"))

	title, content, contentType, ok, err := kbManualKB(context.Background(), "account-a", "kb-pdf", true)
	require.NoError(t, err)
	assert.True(t, ok, "an uploaded PDF must not fall through to an index that may be gone")
	assert.Equal(t, "application/pdf", contentType)
	assert.Equal(t, "JVBERi0xLjQK", content, "the stored file is served as-is, base64 and all")
	assert.Equal(t, "sop-k8s-01-crashloopbackoff.pdf", title, "the filename names the document")
}

func TestKBManualKBServesJSONAsJSON(t *testing.T) {
	mock := mockKBDatabase(t)
	mock.ExpectQuery(`SELECT name, data`).
		WithArgs("kb-json", "account-a").
		WillReturnRows(sqlmock.NewRows([]string{"name", "data", "has_data", "data_format", "data_filename"}).
			AddRow("incidents", `[{"id":1}]`, true, "json", ""))

	title, content, contentType, ok, err := kbManualKB(context.Background(), "account-a", "kb-json", true)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "application/json", contentType)
	assert.Equal(t, `[{"id":1}]`, content)
	assert.Equal(t, "incidents", title)
}

func labelRows(rows ...[]driver.Value) *sqlmock.Rows {
	out := sqlmock.NewRows([]string{"document_key", "kinds", "shapes", "capabilities"})
	for _, row := range rows {
		out.AddRow(row...)
	}
	return out
}

// The labels come from Postgres, one query for the page, bound to the authorized collection.
func TestKBDocumentLabelsAreReadFromPostgres(t *testing.T) {
	mock := mockKBDatabase(t)
	mock.ExpectQuery(`SELECT document_key, kinds, shapes, capabilities FROM llm_kb_capability_entries`).
		WithArgs("int-1_knowledge_base", sqlmock.AnyArg()).
		WillReturnRows(labelRows(
			[]driver.Value{"int-1_knowledge_base|1", "{sop}", "{deterministic_procedure}", "{kubectl_execute,logs}"},
			[]driver.Value{"int-1_knowledge_base|2", "{knowledge}", "{}", "{}"},
		))

	got, err := kbDocumentLabels(context.Background(), "int-1_knowledge_base", []string{"int-1_knowledge_base|1", "int-1_knowledge_base|2", "int-1_knowledge_base|3"})
	require.NoError(t, err)
	assert.Equal(t, kbDocumentLabel{Kind: "sop", Shape: "deterministic_procedure", Capabilities: []string{"kubectl_execute", "logs"}}, got["int-1_knowledge_base|1"])
	assert.Equal(t, "knowledge", got["int-1_knowledge_base|2"].Kind)
	assert.Empty(t, got["int-1_knowledge_base|2"].Shape)
	assert.NotContains(t, got, "int-1_knowledge_base|3", "a page with no row is not classified")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKBDocumentLabelsAskNothingWithoutKeys(t *testing.T) {
	mock := mockKBDatabase(t)
	got, err := kbDocumentLabels(context.Background(), "int-1_knowledge_base", nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	require.NoError(t, mock.ExpectationsWereMet(), "no keys means no query")
}

func TestApplyKBDocumentLabelsFillsTheListingAndSkipsUnlabelledPages(t *testing.T) {
	mock := mockKBDatabase(t)
	mock.ExpectQuery(`SELECT document_key, kinds, shapes, capabilities FROM llm_kb_capability_entries`).
		WillReturnRows(labelRows(
			[]driver.Value{"c|1", "{sop}", "{decision_tree}", "{a}"},
			[]driver.Value{"c|9", "{embedded}", "{decision_tree}", "{x}"}, // a kind the classifier never writes
		))
	items := []KBDocument{{Id: "p1", DocumentKey: "c|1"}, {Id: "p2", DocumentKey: "c|2"}, {Id: "p9", DocumentKey: "c|9"}}

	applyKBDocumentLabels(security.NewRequestContextForSuperAdmin(), "kb-1", "c", items)

	require.NotNil(t, items[0].Kind)
	assert.Equal(t, "sop", *items[0].Kind)
	assert.Equal(t, "decision_tree", *items[0].Shape)
	assert.Equal(t, []string{"a"}, items[0].Capabilities)
	assert.Nil(t, items[1].Kind)
	assert.Nil(t, items[2].Kind, "a kind outside sop/knowledge is not a label")
	assert.Nil(t, items[2].Shape)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A manual knowledge base is one document, labelled under its collection's name.
func TestApplyKBDocumentLabelsLabelsAManualKnowledgeBaseByItsCollection(t *testing.T) {
	mock := mockKBDatabase(t)
	mock.ExpectQuery(`SELECT document_key, kinds, shapes, capabilities FROM llm_kb_capability_entries`).
		WithArgs("kb_kb-1", sqlmock.AnyArg()).
		WillReturnRows(labelRows([]driver.Value{"kb_kb-1", "{sop}", "{deterministic_procedure}", "{}"}))
	items := []KBDocument{{Id: "kb-1", Title: "Payment runbook"}}

	applyKBDocumentLabels(security.NewRequestContextForSuperAdmin(), "kb-1", "kb_kb-1", items)

	require.NotNil(t, items[0].Kind)
	assert.Equal(t, "sop", *items[0].Kind)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyKBDocumentLabelsSurvivesAFailedRead(t *testing.T) {
	mock := mockKBDatabase(t)
	mock.ExpectQuery(`SELECT document_key, kinds, shapes, capabilities FROM llm_kb_capability_entries`).
		WillReturnError(assert.AnError)
	items := []KBDocument{{Id: "p1", DocumentKey: "c|1"}}

	applyKBDocumentLabels(security.NewRequestContextForSuperAdmin(), "kb-1", "c", items)

	assert.Nil(t, items[0].Kind, "the page is listed unlabelled, not failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKBDocumentJSONExposesTheLabelsForTheClient(t *testing.T) {
	kind := "knowledge"
	out, err := json.Marshal(KBDocument{Id: "4", Kind: &kind, Capabilities: []string{"events"}})
	require.NoError(t, err)
	assert.Contains(t, string(out), `"kind":"knowledge"`)
	assert.Contains(t, string(out), `"capabilities":["events"]`)
	assert.Contains(t, string(out), `"shape":null`)
}
