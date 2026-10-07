package core

import (
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"strings"
	"testing"
)

func TestResolveManualKnowledgeCanonicalIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldDatabase := knowledgeDatabase
	knowledgeDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	}
	t.Cleanup(func() { knowledgeDatabase = oldDatabase })

	mock.ExpectQuery(`SELECT id, name, .*LEFT.* FROM llm_knowledgebases WHERE account_id = \$1.*status = 'active' AND enabled`).WithArgs("account-a", sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data", "note_category"}).AddRow("manual-a", "Runbook", "FULL-CONTENT-TAIL", "sop"))
	docs := RAGSearchResults{
		{Document: "partial", Metadata: map[string]any{"collection": "kb_manual-a", "source": "/tmp/file.json"}},
		{Document: "other chunk", Metadata: map[string]any{"collection": "kb_manual-a"}},
		{Document: "foreign or disabled", Metadata: map[string]any{"collection": "kb_denied"}},
		{Document: "external article", Metadata: map[string]any{"collection": "integration_knowledge_base", "url": "https://example.test"}},
	}
	got := ResolveManualKnowledge(security.NewRequestContextForSuperAdmin(), "account-a", docs)
	require.Len(t, got, 2)
	assert.Equal(t, "FULL-CONTENT-TAIL", got[0].Document)
	assert.Equal(t, "manual-a", got[0].Metadata["kb_id"])
	assert.Equal(t, "Runbook", got[0].Metadata["title"])
	assert.Equal(t, "manual", got[0].Metadata["source"])
	assert.Equal(t, KnowledgePurposeProcedure, KnowledgeDocumentPurpose(got[0]))
	assert.Equal(t, docs[3], got[1])
	require.NoError(t, mock.ExpectationsWereMet())
}

// A manual-KB hit's classification metadata (kind/capabilities, #38623) must
// survive the canonical-body swap: KnowledgeDocumentPurpose reads it straight
// off this function's output.
func TestResolveManualKnowledgeKeepsClassificationMetadata(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldDatabase := knowledgeDatabase
	knowledgeDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	}
	t.Cleanup(func() { knowledgeDatabase = oldDatabase })

	mock.ExpectQuery(`SELECT id, name, .*LEFT.* FROM llm_knowledgebases WHERE account_id = \$1.*status = 'active' AND enabled`).WithArgs("account-a", sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data", "note_category"}).AddRow("manual-a", "Runbook", "FULL-CONTENT-TAIL", ""))
	docs := RAGSearchResults{
		{Document: "chunk", Metadata: map[string]any{"collection": "kb_manual-a", "kind": "sop", "capabilities": []any{"k8s.workload_restart"}}},
	}
	got := ResolveManualKnowledge(security.NewRequestContextForSuperAdmin(), "account-a", docs)
	require.Len(t, got, 1)
	assert.Equal(t, "sop", got[0].Metadata["kind"])
	assert.Equal(t, []any{"k8s.workload_restart"}, got[0].Metadata["capabilities"])
	// The canonical fields this function exists to set must still win over
	// whatever the Qdrant payload carried.
	assert.Equal(t, "manual-a", got[0].Metadata["kb_id"])
	assert.Equal(t, "Runbook", got[0].Metadata["title"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResolveManualKnowledgeKeepsPDFChunks(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldDatabase := knowledgeDatabase
	knowledgeDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	}
	t.Cleanup(func() { knowledgeDatabase = oldDatabase })

	// The query must not return a PDF's base64 data column as the body.
	mock.ExpectQuery(`CASE WHEN data_format = 'pdf' THEN '' ELSE LEFT\(data, 4097\) END`).
		WithArgs("account-a", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data", "data_format", "note_category"}).AddRow("pdf-a", "Guide.pdf", "", "pdf", ""))
	docs := RAGSearchResults{
		{Document: "Guide.pdf — page 3 of 9\n\nRestart the ingester", Metadata: map[string]any{"collection": "kb_pdf-a", "retrieval_id": "11", "content_sha256": "abc"}},
		{Document: "Guide.pdf — page 4, figure 1\nFigure: queue topology", Metadata: map[string]any{"collection": "kb_pdf-a", "retrieval_id": "12"}},
	}
	got := ResolveManualKnowledge(security.NewRequestContextForSuperAdmin(), "account-a", docs)
	require.Len(t, got, 2)
	assert.Equal(t, docs[0].Document, got[0].Document)
	assert.Equal(t, docs[1].Document, got[1].Document)
	assert.Equal(t, "Guide.pdf", got[0].Metadata["title"])
	assert.Equal(t, "11", got[0].Metadata["retrieval_id"])
	// Without kb_id the hit stays an indexed document and is read by its handle.
	assert.Empty(t, ManualKnowledgeID(got[0]))
	require.NoError(t, mock.ExpectationsWereMet())
}

// A PDF chunk carries no kb_id, so its note type must still reach the purpose:
// a PDF saved as an SOP is a procedure, not optional reference material.
func TestResolveManualKnowledgePDFKeepsNoteTypePurpose(t *testing.T) {
	for _, tc := range []struct {
		category string
		want     KnowledgeContentPurpose
	}{
		{"sop", KnowledgePurposeProcedure},
		{"fact", KnowledgePurposeReference},
	} {
		t.Run(tc.category, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			oldDatabase := knowledgeDatabase
			knowledgeDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
				return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
			}
			t.Cleanup(func() { knowledgeDatabase = oldDatabase })

			mock.ExpectQuery(`FROM llm_knowledgebases`).
				WithArgs("account-a", sqlmock.AnyArg()).
				WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data", "data_format", "note_category"}).AddRow("pdf-a", "Guide.pdf", "", "pdf", tc.category))
			docs := RAGSearchResults{{Document: "Guide.pdf — page 1 of 5\n\nCrashLoopBackOff", Metadata: map[string]any{"collection": "kb_pdf-a", "retrieval_id": "11"}}}

			got := ResolveManualKnowledge(security.NewRequestContextForSuperAdmin(), "account-a", docs)
			require.Len(t, got, 1)
			assert.Empty(t, ManualKnowledgeID(got[0]), "a PDF chunk must not route into the manual data-column path")
			assert.Equal(t, tc.want, KnowledgeDocumentPurpose(got[0]))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// Only a chunk resolved from the KB's own collection may carry the PDF marker's
// purpose; the same key on a foreign document is ignored.
func TestKnowledgeDocumentPurposeIgnoresForeignPDFMarker(t *testing.T) {
	doc := RAGSearchResult{Metadata: map[string]any{"collection": "integration_knowledge_base", pdfKnowledgeIDKey: "pdf-a", "note_category": "sop"}}
	assert.Equal(t, KnowledgePurposeReference, KnowledgeDocumentPurpose(doc))
}

func TestRenderSkillsBlockNeverEmitsPDFData(t *testing.T) {
	block, _ := renderSkillsBlock([]agentSkillRow{{Name: "guide", Data: "JVBERi0xLjQK", DataFormat: string(KBFormatPDF)}})
	assert.NotContains(t, block, "JVBERi0xLjQK")
	assert.Contains(t, block, PDFKnowledgePointer)
}

func TestKnowledgeCandidateRejectsOversizedEntry(t *testing.T) {
	candidate := KnowledgeCandidate{ID: "knowledge:oversized", Content: strings.Repeat("x", MaxKnowledgeCandidateBytes)}
	require.ErrorContains(t, StoreKnowledgeCandidate(t.Name(), "conv", "msg", candidate), "exceeds")
	_, ok := LoadKnowledgeCandidate(t.Name(), "conv", "msg", candidate.ID)
	assert.False(t, ok)
}
